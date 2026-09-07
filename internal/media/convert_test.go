package media_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/media"
	"github.com/guigui42/filetwist/internal/runner"
)

func TestConvertReprobesAndValidatesOutput(t *testing.T) {
	input := readProbeFixture(t, "common-audio.json")
	outputPath := filepath.Join(t.TempDir(), "output.mp3")
	plan, err := media.BuildPlan(media.PlanRequest{
		Operation:  corpus.OperationCompatibleAudio,
		InputPath:  "input.flac",
		OutputPath: outputPath,
		Input:      input,
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}

	call := 0
	run := func(_ context.Context, command runner.Command) (runner.Result, error) {
		call++
		switch call {
		case 1:
			if command.Path != "/tools/ffmpeg" {
				t.Errorf("encode path = %q; want ffmpeg", command.Path)
			}
			return runner.Result{
				ExitCode: 0,
				Stdout: runner.Output{
					Bytes: []byte("out_time_us=3500000\nprogress=end\n"),
				},
			}, nil
		case 2:
			if command.Path != "/tools/ffprobe" {
				t.Errorf("probe path = %q; want ffprobe", command.Path)
			}
			if !containsPair(command.Args, "-protocol_whitelist", "file,pipe") {
				t.Error("ffprobe command lacks local protocol allowlist")
			}
			if command.Args[len(command.Args)-1] == outputPath {
				t.Error("ffprobe inspected final path before validation")
			}
			return runner.Result{
				ExitCode: 0,
				Stdout: runner.Output{Bytes: []byte(`{
					"streams": [{
						"index": 0,
						"codec_name": "mp3",
						"codec_type": "audio",
						"sample_rate": "48000",
						"channels": 2,
						"channel_layout": "stereo"
					}],
					"format": {
						"format_name": "mp3",
						"duration": "3.500000"
					}
				}`)},
			}, nil
		default:
			return runner.Result{}, fmt.Errorf("unexpected command %d", call)
		}
	}

	result, err := media.Convert(context.Background(), run, "/tools/ffmpeg", "/tools/ffprobe", plan)
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if call != 2 {
		t.Errorf("command calls = %d; want 2", call)
	}
	if len(result.Progress) != 1 || !result.Progress[0].Done {
		t.Errorf("progress = %+v; want one completed update", result.Progress)
	}
	if result.Output.Format.FormatName != "mp3" {
		t.Errorf("output container = %q; want mp3", result.Output.Format.FormatName)
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Errorf("validated output was not published: %v", err)
	}
}

func TestConversionErrorDoesNotExposeCapturedOutput(t *testing.T) {
	const privateOutput = "private media metadata"
	input := readProbeFixture(t, "common-audio.json")
	outputPath := filepath.Join(t.TempDir(), "output.mp3")
	plan, err := media.BuildPlan(media.PlanRequest{
		Operation:  corpus.OperationCompatibleAudio,
		InputPath:  "input.flac",
		OutputPath: outputPath,
		Input:      input,
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}

	_, err = media.Convert(
		context.Background(),
		func(context.Context, runner.Command) (runner.Result, error) {
			return runner.Result{
				Stderr: runner.Output{Bytes: []byte(privateOutput)},
			}, fmt.Errorf("runner failure: %s", privateOutput)
		},
		"/tools/ffmpeg",
		"/tools/ffprobe",
		plan,
	)
	if err == nil {
		t.Fatal("Convert() error = nil; want encode failure")
	}
	if strings.Contains(err.Error(), privateOutput) {
		t.Errorf("error %q exposes captured output", err)
	}
	if _, statErr := os.Stat(outputPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("failed conversion left output behind: %v", statErr)
	}
}

func TestConvertRejectsMissingRequiredFFmpegFilter(t *testing.T) {
	plan := media.Plan{
		RequiredFilters: []string{"zscale", "tonemap"},
	}
	calls := 0
	run := func(_ context.Context, command runner.Command) (runner.Result, error) {
		calls++
		if !containsPair(command.Args, "-hide_banner", "-filters") {
			t.Errorf("filter probe args = %v", command.Args)
		}
		return runner.Result{
			ExitCode: 0,
			Stdout: runner.Output{
				Bytes: []byte(" .S tonemap V->V Conversion to/from different dynamic ranges.\n"),
			},
		}, nil
	}

	_, err := media.Convert(
		context.Background(),
		run,
		"/tools/ffmpeg",
		"/tools/ffprobe",
		plan,
	)
	var planErr *media.PlanError
	if !errors.As(err, &planErr) || planErr.Code != media.ErrorUnsupportedHDR {
		t.Fatalf("Convert() error = %v; want unsupported HDR", err)
	}
	if calls != 1 {
		t.Errorf("command calls = %d; want one capability probe", calls)
	}
}

func TestConvertPropagatesFilterProbeCancellation(t *testing.T) {
	plan := media.Plan{
		RequiredFilters: []string{"zscale", "tonemap"},
	}
	_, err := media.Convert(
		context.Background(),
		func(context.Context, runner.Command) (runner.Result, error) {
			return runner.Result{}, context.Canceled
		},
		"/tools/ffmpeg",
		"/tools/ffprobe",
		plan,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Convert() error = %v; want context canceled", err)
	}
}

func TestConvertDoesNotClassifyFilterProbeExecutionFailureAsUnsupportedHDR(t *testing.T) {
	plan := media.Plan{
		RequiredFilters: []string{"zscale", "tonemap"},
	}

	_, err := media.Convert(
		context.Background(),
		func(context.Context, runner.Command) (runner.Result, error) {
			return runner.Result{}, errors.New("filters probe failed")
		},
		"/tools/ffmpeg",
		"/tools/ffprobe",
		plan,
	)
	var conversionErr *media.ConversionError
	if !errors.As(err, &conversionErr) || conversionErr.Stage != media.ConversionStageCapability {
		t.Fatalf("Convert() error = %v; want capability-stage conversion failure", err)
	}
	var planErr *media.PlanError
	if errors.As(err, &planErr) {
		t.Fatalf("Convert() error = %v; filter probe execution failure must not be unsupported HDR", err)
	}
}

func TestConvertDoesNotOverwriteExistingOutput(t *testing.T) {
	input := readProbeFixture(t, "common-audio.json")
	outputPath := filepath.Join(t.TempDir(), "output.mp3")
	if err := os.WriteFile(outputPath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("write existing output: %v", err)
	}
	plan, err := media.BuildPlan(media.PlanRequest{
		Operation:  corpus.OperationCompatibleAudio,
		InputPath:  "input.flac",
		OutputPath: outputPath,
		Input:      input,
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}

	call := 0
	run := func(_ context.Context, command runner.Command) (runner.Result, error) {
		call++
		switch call {
		case 1:
			if err := os.WriteFile(command.Args[len(command.Args)-1], []byte("converted"), 0o600); err != nil {
				t.Fatalf("write temporary output: %v", err)
			}
			return runner.Result{
				ExitCode: 0,
				Stdout:   runner.Output{Bytes: []byte("out_time_us=3500000\nprogress=end\n")},
			}, nil
		case 2:
			return runner.Result{
				ExitCode: 0,
				Stdout: runner.Output{Bytes: []byte(`{
					"streams": [{
						"index": 0,
						"codec_name": "mp3",
						"codec_type": "audio",
						"sample_rate": "48000",
						"channels": 2,
						"channel_layout": "stereo"
					}],
					"format": {
						"format_name": "mp3",
						"duration": "3.500000"
					}
				}`)},
			}, nil
		default:
			return runner.Result{}, fmt.Errorf("unexpected command %d", call)
		}
	}

	_, err = media.Convert(context.Background(), run, "/tools/ffmpeg", "/tools/ffprobe", plan)
	if !errors.Is(err, media.ErrOutputExists) {
		t.Fatalf("Convert() error = %v; want ErrOutputExists", err)
	}
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read existing output: %v", err)
	}
	if string(content) != "existing" {
		t.Errorf("existing output = %q; want unchanged", content)
	}
}

func TestConvertRetriesRecognizedVAAPIFailureOnceOnCPU(t *testing.T) {
	input := media.Probe{
		Streams: []media.Stream{{
			Index:       0,
			CodecName:   "h264",
			CodecType:   "video",
			Width:       320,
			Height:      180,
			PixelFormat: "yuv420p",
		}},
		Format: media.Format{DurationText: "1.000000"},
	}
	outputPath := filepath.Join(t.TempDir(), "output.mp4")
	plan, err := media.BuildPlan(media.PlanRequest{
		Operation:  corpus.OperationCompatibleVideo,
		InputPath:  "input.mp4",
		OutputPath: outputPath,
		Input:      input,
		Acceleration: media.AccelerationConfig{
			Mode:   media.AccelerationVAAPI,
			Device: media.DefaultVAAPIDevice,
		},
		VAAPI: &media.VAAPIReadiness{
			Device:                  media.DefaultVAAPIDevice,
			Ready:                   true,
			FunctionalProbeComplete: true,
			HardwareDecodeCodecs:    map[string]bool{"h264": true},
		},
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}

	call := 0
	run := func(_ context.Context, command runner.Command) (runner.Result, error) {
		call++
		switch call {
		case 1:
			if !containsPair(command.Args, "-c:v", "h264_vaapi") {
				t.Errorf("first attempt args = %v; want h264_vaapi", command.Args)
			}
			result := runner.Result{
				ExitCode: 1,
				Stderr: runner.Output{
					Bytes: []byte("Failed to initialise VAAPI encoder"),
				},
			}
			return result, &runner.ExitError{
				Executable: "ffmpeg",
				ExitCode:   1,
				Result:     result,
			}
		case 2:
			if !containsPair(command.Args, "-c:v", "libx264") {
				t.Errorf("second attempt args = %v; want libx264", command.Args)
			}
			if err := os.WriteFile(
				command.Args[len(command.Args)-1],
				append(mp4Box("ftyp"), append(mp4Box("moov"), mp4Box("mdat")...)...),
				0o600,
			); err != nil {
				t.Fatalf("write CPU output: %v", err)
			}
			return runner.Result{
				ExitCode: 0,
				Stdout: runner.Output{
					Bytes: []byte("out_time_us=1000000\nprogress=end\n"),
				},
			}, nil
		case 3:
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
							"duration": "1.000000"
						}
					}`)},
			}, nil
		default:
			return runner.Result{}, fmt.Errorf("unexpected command %d", call)
		}
	}

	result, err := media.Convert(context.Background(), run, "/tools/ffmpeg", "/tools/ffprobe", plan)
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if call != 3 {
		t.Errorf("command count = %d; want VA-API, CPU, and ffprobe", call)
	}
	if result.Execution.InitialPath != media.ExecutionPathVAAPIHardwareDecode ||
		result.Execution.FinalPath != media.ExecutionPathCPU {
		t.Errorf("execution metadata = %+v; want VA-API to CPU", result.Execution)
	}
	if result.Execution.FallbackReason != media.FallbackVAAPIEncoder {
		t.Errorf("fallback reason = %q; want encoder", result.Execution.FallbackReason)
	}
	if result.Execution.AttemptCount != 2 || len(result.Attempts) != 2 {
		t.Errorf("attempt metadata = %+v; want two attempts", result.Execution)
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Errorf("validated CPU fallback output was not published: %v", err)
	}
}

func TestClassifyVAAPIFailure(t *testing.T) {
	exitFailure := func(stage media.ConversionStage) error {
		return &media.ConversionError{
			Stage: stage,
			Cause: &runner.ExitError{
				Executable: "ffmpeg",
				ExitCode:   1,
			},
		}
	}
	tests := []struct {
		name   string
		err    error
		stderr string
		want   media.FallbackReason
	}{
		{
			name:   "device",
			err:    exitFailure(media.ConversionStageEncode),
			stderr: "No VA display found for device /dev/dri/renderD128",
			want:   media.FallbackVAAPIDevice,
		},
		{
			name:   "initialization",
			err:    exitFailure(media.ConversionStageEncode),
			stderr: "Failed to initialise VAAPI connection",
			want:   media.FallbackVAAPIInitialization,
		},
		{
			name:   "upload",
			err:    exitFailure(media.ConversionStageEncode),
			stderr: "A hardware device reference is required to upload frames to",
			want:   media.FallbackVAAPIUpload,
		},
		{
			name:   "encoder",
			err:    exitFailure(media.ConversionStageEncode),
			stderr: "No usable encoding entrypoint found",
			want:   media.FallbackVAAPIEncoder,
		},
		{
			name:   "encoder takes precedence over echoed upload filter",
			err:    exitFailure(media.ConversionStageEncode),
			stderr: "Error while opening encoder after filter hwupload",
			want:   media.FallbackVAAPIEncoder,
		},
		{
			name:   "generic encode failure",
			err:    exitFailure(media.ConversionStageEncode),
			stderr: "Invalid data found when processing input",
		},
		{
			name: "timeout",
			err: &media.ConversionError{
				Stage: media.ConversionStageEncode,
				Cause: &runner.ContextError{Cause: context.DeadlineExceeded},
			},
			stderr: "Failed to initialise VAAPI connection",
		},
		{
			name:   "validation failure",
			err:    exitFailure(media.ConversionStageValidate),
			stderr: "Failed to initialise VAAPI connection",
		},
		{
			name: "disk failure",
			err: &media.ConversionError{
				Stage: media.ConversionStageEncode,
				Cause: os.ErrPermission,
			},
			stderr: "Failed to initialise VAAPI connection",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := media.ClassifyVAAPIFailure(tt.err, []byte(tt.stderr)); got != tt.want {
				t.Errorf("ClassifyVAAPIFailure() = %q; want %q", got, tt.want)
			}
		})
	}
}
