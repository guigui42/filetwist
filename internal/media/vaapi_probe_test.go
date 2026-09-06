package media_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/media"
	"github.com/guigui42/filetwist/internal/runner"
)

func TestProbeVAAPIRequiresEncodeAndValidatedReprobe(t *testing.T) {
	call := 0
	run := func(_ context.Context, command runner.Command) (runner.Result, error) {
		call++
		switch call {
		case 1:
			if !containsPair(command.Args, "-c:v", "h264_vaapi") {
				t.Errorf("encode args = %v; want h264_vaapi", command.Args)
			}
			if err := os.WriteFile(
				command.Args[len(command.Args)-1],
				append(mp4Box("ftyp"), append(mp4Box("moov"), mp4Box("mdat")...)...),
				0o600,
			); err != nil {
				t.Fatalf("write probe output: %v", err)
			}
			return runner.Result{ExitCode: 0}, nil
		case 2:
			if !containsPair(command.Args, "-protocol_whitelist", "file,pipe") {
				t.Errorf("ffprobe args = %v; want local protocol allowlist", command.Args)
			}
			return runner.Result{
				ExitCode: 0,
				Stdout: runner.Output{Bytes: []byte(`{
					"streams": [{
						"index": 0,
						"codec_name": "h264",
						"codec_type": "video",
						"width": 320,
						"height": 180,
						"pix_fmt": "yuv420p"
					}],
					"format": {
						"format_name": "mov,mp4,m4a,3gp,3g2,mj2",
						"duration": "0.400000"
					}
				}`)},
			}, nil
		case 3:
			if !containsPair(command.Args, "-hwaccel", "vaapi") {
				t.Errorf("decode args = %v; want VA-API hardware decode", command.Args)
			}
			return runner.Result{ExitCode: 0}, nil
		default:
			return runner.Result{}, errors.New("unexpected command")
		}
	}

	result, err := media.ProbeVAAPI(
		context.Background(),
		run,
		"/tools/ffmpeg",
		"/tools/ffprobe",
		media.AccelerationConfig{},
		time.Second,
		media.WithVAAPIDeviceCheck(func(path string) error {
			if path != media.DefaultVAAPIDevice {
				t.Errorf("device = %q; want default", path)
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("ProbeVAAPI() error = %v", err)
	}
	if call != 3 {
		t.Errorf("command count = %d; want 3", call)
	}
	if !result.Readiness.Ready || !result.Readiness.FunctionalProbeComplete {
		t.Errorf("readiness = %+v; want successful functional probe", result.Readiness)
	}
	if !result.Readiness.SupportsHardwareDecode("h264") {
		t.Error("H.264 hardware decode was not reported")
	}
}

func TestProbeVAAPIFailureReasons(t *testing.T) {
	tests := []struct {
		name        string
		checkDevice func(string) error
		run         media.RunFunc
		wantReason  media.FallbackReason
	}{
		{
			name: "device inaccessible",
			checkDevice: func(string) error {
				return errors.New("permission denied")
			},
			run: func(context.Context, runner.Command) (runner.Result, error) {
				return runner.Result{}, errors.New("unexpected command")
			},
			wantReason: media.FallbackVAAPIDevice,
		},
		{
			name:        "encoder unavailable",
			checkDevice: func(string) error { return nil },
			run: func(context.Context, runner.Command) (runner.Result, error) {
				result := runner.Result{
					ExitCode: 1,
					Stderr: runner.Output{
						Bytes: []byte("No usable encoding entrypoint found for profile"),
					},
				}
				return result, &runner.ExitError{
					Executable: "ffmpeg",
					ExitCode:   1,
					Result:     result,
				}
			},
			wantReason: media.FallbackVAAPIEncoder,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := media.ProbeVAAPI(
				context.Background(),
				tt.run,
				"/tools/ffmpeg",
				"/tools/ffprobe",
				media.AccelerationConfig{},
				time.Second,
				media.WithVAAPIDeviceCheck(tt.checkDevice),
			)
			var probeErr *media.VAAPIProbeError
			if !errors.As(err, &probeErr) {
				t.Fatalf("ProbeVAAPI() error = %T %v; want *media.VAAPIProbeError", err, err)
			}
			if probeErr.Reason != tt.wantReason {
				t.Errorf("error reason = %q; want %q", probeErr.Reason, tt.wantReason)
			}
			if result.Readiness.UnavailableReason != tt.wantReason {
				t.Errorf("readiness reason = %q; want %q", result.Readiness.UnavailableReason, tt.wantReason)
			}
			if strings.Contains(err.Error(), "permission denied") {
				t.Errorf("error %q exposes device details", err)
			}
		})
	}
}
