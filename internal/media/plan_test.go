package media_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/media"
	"github.com/guigui42/filetwist/internal/profiles"
)

func TestBuildPlan(t *testing.T) {
	videoProbe := readProbeFixture(t, "iphone-spatial.json")
	audioProbe := readProbeFixture(t, "common-audio.json")

	tests := []struct {
		name              string
		operation         corpus.Operation
		probe             media.Probe
		wantContainer     string
		wantVideoCodec    string
		wantAudioCodec    string
		wantAudioPresence media.AudioPresence
		wantArgs          []string
	}{
		{
			name:              "compatible video",
			operation:         corpus.OperationCompatibleVideo,
			probe:             videoProbe,
			wantContainer:     "mp4",
			wantVideoCodec:    "h264",
			wantAudioCodec:    "aac",
			wantAudioPresence: media.AudioRequired,
			wantArgs: []string{
				"-hide_banner", "-loglevel", "warning", "-nostdin", "-y",
				"-progress", "pipe:1", "-nostats",
				"-protocol_whitelist", "file,pipe", "-i", "input.mov",
				"-map", "0:0", "-map", "0:1", "-map_metadata", "-1", "-map_metadata:s", "-1", "-map_chapters", "-1", "-sn", "-dn",
				"-c:v", "libx264", "-preset", "medium", "-crf", "20",
				"-vf", "scale=1080:1920", "-pix_fmt", "yuv420p",
				"-c:a", "aac", "-b:a", "192k", "-ac", "2", "-ar", "48000",
				"-movflags", "+faststart", "-f", "mp4", "output.mp4",
			},
		},
		{
			name:              "smaller video",
			operation:         corpus.OperationSmallerVideo,
			probe:             videoProbe,
			wantContainer:     "mp4",
			wantVideoCodec:    "h264",
			wantAudioCodec:    "aac",
			wantAudioPresence: media.AudioRequired,
		},
		{
			name:              "extract audio",
			operation:         corpus.OperationExtractAudio,
			probe:             videoProbe,
			wantContainer:     "mp4",
			wantAudioCodec:    "aac",
			wantAudioPresence: media.AudioRequired,
		},
		{
			name:              "compatible audio",
			operation:         corpus.OperationCompatibleAudio,
			probe:             audioProbe,
			wantContainer:     "mp3",
			wantAudioCodec:    "mp3",
			wantAudioPresence: media.AudioRequired,
		},
		{
			name:              "lossless audio",
			operation:         corpus.OperationLosslessAudio,
			probe:             audioProbe,
			wantContainer:     "flac",
			wantAudioCodec:    "flac",
			wantAudioPresence: media.AudioRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := media.BuildPlan(media.PlanRequest{
				Operation:  tt.operation,
				InputPath:  "input.mov",
				OutputPath: outputName(tt.wantContainer),
				Input:      tt.probe,
				Timeout:    2 * time.Minute,
			})
			if err != nil {
				t.Fatalf("BuildPlan() error = %v", err)
			}

			if plan.Expected.Container != tt.wantContainer {
				t.Errorf("container = %q; want %q", plan.Expected.Container, tt.wantContainer)
			}
			if plan.Expected.VideoCodec != tt.wantVideoCodec {
				t.Errorf("video codec = %q; want %q", plan.Expected.VideoCodec, tt.wantVideoCodec)
			}
			if plan.Expected.AudioCodec != tt.wantAudioCodec {
				t.Errorf("audio codec = %q; want %q", plan.Expected.AudioCodec, tt.wantAudioCodec)
			}
			if plan.Expected.AudioPresence != tt.wantAudioPresence {
				t.Errorf("audio presence = %q; want %q", plan.Expected.AudioPresence, tt.wantAudioPresence)
			}
			if tt.operation == corpus.OperationSmallerVideo {
				if plan.Expected.Width != 406 || plan.Expected.Height != 720 {
					t.Errorf(
						"smaller dimensions = %dx%d; want 406x720",
						plan.Expected.Width,
						plan.Expected.Height,
					)
				}
				if !containsPair(plan.Args, "-vf", "scale=406:720") {
					t.Errorf("smaller args = %v; want literal scale=406:720", plan.Args)
				}
			}
			if tt.wantArgs != nil && !reflect.DeepEqual(plan.Args, tt.wantArgs) {
				t.Errorf("args:\n got: %#v\nwant: %#v", plan.Args, tt.wantArgs)
			}

			command := plan.Command("/usr/local/bin/ffmpeg")
			if command.Path != "/usr/local/bin/ffmpeg" {
				t.Errorf("command path = %q; want resolved ffmpeg path", command.Path)
			}
			if !reflect.DeepEqual(command.Args, plan.Args) {
				t.Error("command args do not match plan args")
			}
			if command.Timeout != 2*time.Minute {
				t.Errorf("command timeout = %s; want 2m", command.Timeout)
			}
		})
	}
}

func TestRegistryMediaProfilesBuildCompletePlans(t *testing.T) {
	videoProbe := readProbeFixture(t, "iphone-spatial.json")
	audioProbe := readProbeFixture(t, "common-audio.json")

	for _, spec := range profiles.All() {
		if spec.Engine != profiles.EngineMedia {
			continue
		}
		t.Run(string(spec.Operation), func(t *testing.T) {
			input := audioProbe
			if spec.OutputKind == profiles.MediaVideo {
				input = videoProbe
			}
			output, err := profiles.OutputName("input.bin", spec.Operation)
			if err != nil {
				t.Fatalf("OutputName() error = %v", err)
			}
			plan, err := media.BuildPlan(media.PlanRequest{
				Operation:  spec.Operation,
				InputPath:  "input.bin",
				OutputPath: output,
				Input:      input,
			})
			if err != nil {
				t.Fatalf("BuildPlan() error = %v", err)
			}
			if len(plan.Args) == 0 || plan.Args[len(plan.Args)-1] != output {
				t.Errorf("plan args do not publish the registry output: %v", plan.Args)
			}
			commandContainer := outputContainer(plan.Args)
			if commandContainer == "" {
				t.Errorf("plan has no output container argument: %v", plan.Args)
			}
			if commandContainer != spec.Output.Container {
				t.Errorf("command container = %q; registry = %q", commandContainer, spec.Output.Container)
			}
			if plan.Expected.Container != commandContainer {
				t.Errorf("expected container = %q; command = %q", plan.Expected.Container, commandContainer)
			}
			if plan.Expected.AudioPresence == "" {
				t.Errorf("audio expectation is incomplete: %+v", plan.Expected)
			}
			if spec.OutputKind == profiles.MediaVideo {
				if plan.Expected.VideoCodec == "" ||
					plan.Expected.PixelFormat == "" ||
					plan.Expected.Width <= 0 ||
					plan.Expected.Height <= 0 {
					t.Errorf("video expectation is incomplete: %+v", plan.Expected)
				}
			} else if plan.Expected.AudioCodec == "" ||
				plan.Expected.Channels <= 0 ||
				plan.Expected.SampleRate <= 0 {
				t.Errorf("audio expectation is incomplete: %+v", plan.Expected)
			}
		})
	}
}

func TestBuildPlanRejectsProtocolPaths(t *testing.T) {
	probe := readProbeFixture(t, "common-audio.json")
	tests := []struct {
		name       string
		inputPath  string
		outputPath string
	}{
		{name: "network input", inputPath: "https://example.com/input.flac", outputPath: "output.mp3"},
		{name: "network output", inputPath: "input.flac", outputPath: "rtmp://example.com/output"},
		{name: "pipe output", inputPath: "input.flac", outputPath: "pipe:1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := media.BuildPlan(media.PlanRequest{
				Operation:  corpus.OperationCompatibleAudio,
				InputPath:  tt.inputPath,
				OutputPath: tt.outputPath,
				Input:      probe,
			})
			var planErr *media.PlanError
			if !errors.As(err, &planErr) || planErr.Code != media.ErrorInvalidRequest {
				t.Fatalf("BuildPlan() error = %v; want invalid request", err)
			}
		})
	}
}

func TestBuildPlanRejectsUnsafeQualityConversions(t *testing.T) {
	tests := []struct {
		name      string
		operation corpus.Operation
		probe     media.Probe
		wantCode  media.ErrorCode
	}{
		{
			name:      "HDR video with unsupported transfer signaling",
			operation: corpus.OperationCompatibleVideo,
			probe: media.Probe{
				Streams: []media.Stream{{
					Index:          0,
					CodecName:      "hevc",
					CodecType:      "video",
					Width:          1920,
					Height:         1080,
					ColorTransfer:  "gamma28",
					ColorPrimaries: "bt2020",
				}},
				Format: media.Format{DurationText: "1.000000"},
			},
			wantCode: media.ErrorUnsupportedHDR,
		},
		{
			name:      "HLG video without complete color signaling",
			operation: corpus.OperationCompatibleVideo,
			probe: media.Probe{
				Streams: []media.Stream{{
					Index:          0,
					CodecName:      "hevc",
					CodecType:      "video",
					Width:          1920,
					Height:         1080,
					ColorTransfer:  "arib-std-b67",
					ColorPrimaries: "bt2020",
				}},
				Format: media.Format{DurationText: "1.000000"},
			},
			wantCode: media.ErrorUnsupportedHDR,
		},
		{
			name:      "floating point PCM to FLAC",
			operation: corpus.OperationLosslessAudio,
			probe: media.Probe{
				Streams: []media.Stream{{
					Index:        0,
					CodecName:    "pcm_f64le",
					CodecType:    "audio",
					SampleFormat: "dbl",
					SampleRate:   "48000",
					Channels:     2,
				}},
				Format: media.Format{DurationText: "1.000000"},
			},
			wantCode: media.ErrorUnsupportedLosslessAudio,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := media.BuildPlan(media.PlanRequest{
				Operation:  tt.operation,
				InputPath:  "input.bin",
				OutputPath: "output.bin",
				Input:      tt.probe,
			})
			var planErr *media.PlanError
			if !errors.As(err, &planErr) || planErr.Code != tt.wantCode {
				t.Fatalf("BuildPlan() error = %v; want code %q", err, tt.wantCode)
			}
		})
	}
}

func TestBuildPlanToneMapsHDRVideo(t *testing.T) {
	probe := media.Probe{
		Streams: []media.Stream{{
			Index:          0,
			CodecName:      "hevc",
			CodecType:      "video",
			Width:          1920,
			Height:         1080,
			PixelFormat:    "yuv420p10le",
			ColorRange:     "tv",
			ColorSpace:     "bt2020nc",
			ColorTransfer:  "arib-std-b67",
			ColorPrimaries: "bt2020",
		}},
		Format: media.Format{DurationText: "1.000000"},
	}

	plan, err := media.BuildPlan(media.PlanRequest{
		Operation:  corpus.OperationCompatibleVideo,
		InputPath:  "input.mov",
		OutputPath: "output.mp4",
		Input:      probe,
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	wantFilter := "zscale=t=linear:npl=100,format=gbrpf32le," +
		"tonemap=hable:desat=0," +
		"zscale=p=bt709:t=bt709:m=bt709:r=tv," +
		"scale=1920:1080,format=yuv420p"
	if !containsPair(plan.Args, "-vf", wantFilter) {
		t.Errorf("args = %v; want HDR filter %q", plan.Args, wantFilter)
	}
	if !reflect.DeepEqual(plan.RequiredFilters, []string{"zscale", "tonemap"}) {
		t.Errorf("required filters = %v; want zscale and tonemap", plan.RequiredFilters)
	}
	if plan.Expected.ColorRange != "tv" ||
		plan.Expected.ColorSpace != "bt709" ||
		plan.Expected.ColorTransfer != "bt709" ||
		plan.Expected.ColorPrimaries != "bt709" {
		t.Errorf("expected color profile = %+v; want BT.709 limited-range SDR", plan.Expected)
	}
	for option, value := range map[string]string{
		"-color_primaries": "bt709",
		"-color_trc":       "bt709",
		"-colorspace":      "bt709",
		"-color_range":     "tv",
	} {
		if !containsPair(plan.Args, option, value) {
			t.Errorf("args = %v; want %s %s", plan.Args, option, value)
		}
	}
}

func TestBuildPlanUsesMappedStreamDuration(t *testing.T) {
	probe := media.Probe{
		Streams: []media.Stream{
			{
				Index:        0,
				CodecName:    "h264",
				CodecType:    "video",
				Width:        1280,
				Height:       720,
				DurationText: "10.000000",
			},
			{
				Index:         1,
				CodecName:     "aac",
				CodecType:     "audio",
				SampleRate:    "48000",
				Channels:      2,
				DurationText:  "8.000000",
				ChannelLayout: "stereo",
			},
			{
				Index:        2,
				CodecName:    "apac",
				CodecType:    "audio",
				DurationText: "20.000000",
			},
		},
		Format: media.Format{DurationText: "20.000000"},
	}

	extract, err := media.BuildPlan(media.PlanRequest{
		Operation:  corpus.OperationExtractAudio,
		InputPath:  "input.mov",
		OutputPath: "output.m4a",
		Input:      probe,
	})
	if err != nil {
		t.Fatalf("BuildPlan(extract) error = %v", err)
	}
	if extract.Expected.Duration != 8*time.Second {
		t.Errorf("extract duration = %s; want 8s selected audio duration", extract.Expected.Duration)
	}

	video, err := media.BuildPlan(media.PlanRequest{
		Operation:  corpus.OperationCompatibleVideo,
		InputPath:  "input.mov",
		OutputPath: "output.mp4",
		Input:      probe,
	})
	if err != nil {
		t.Fatalf("BuildPlan(video) error = %v", err)
	}
	if video.Expected.Duration != 10*time.Second {
		t.Errorf("video duration = %s; want 10s longest mapped stream", video.Expected.Duration)
	}
}

func TestBuildPlanAllowsSilentVideoAndRejectsAudioWithoutAllowlistedStream(t *testing.T) {
	probe := readProbeFixture(t, "video-no-supported-audio.json")

	videoPlan, err := media.BuildPlan(media.PlanRequest{
		Operation:  corpus.OperationCompatibleVideo,
		InputPath:  "input.mkv",
		OutputPath: "output.mp4",
		Input:      probe,
	})
	if err != nil {
		t.Fatalf("BuildPlan(video) error = %v", err)
	}
	if videoPlan.Expected.AudioPresence != media.AudioForbidden {
		t.Errorf("audio presence = %q; want forbidden", videoPlan.Expected.AudioPresence)
	}
	if containsPair(videoPlan.Args, "-map", "0:6") {
		t.Error("unsupported audio stream was mapped")
	}

	_, err = media.BuildPlan(media.PlanRequest{
		Operation:  corpus.OperationExtractAudio,
		InputPath:  "input.mkv",
		OutputPath: "output.m4a",
		Input:      probe,
	})
	var planErr *media.PlanError
	if !errors.As(err, &planErr) {
		t.Fatalf("BuildPlan(audio) error = %T %v; want *media.PlanError", err, err)
	}
	if planErr.Code != media.ErrorNoSupportedAudio {
		t.Errorf("error code = %q; want %q", planErr.Code, media.ErrorNoSupportedAudio)
	}
}

func outputName(container string) string {
	switch container {
	case "mp4":
		return "output.mp4"
	case "mp3":
		return "output.mp3"
	default:
		return "output.flac"
	}
}

func containsPair(args []string, first, second string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == first && args[index+1] == second {
			return true
		}
	}
	return false
}

func outputContainer(args []string) string {
	for index := len(args) - 2; index >= 0; index-- {
		if args[index] == "-f" {
			return args[index+1]
		}
	}
	return ""
}
