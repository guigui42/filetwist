package media_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/media"
	"github.com/guigui42/filetwist/internal/runner"
)

func TestFFmpegProfilesIntegration(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe unavailable")
	}

	r, err := runner.New(runner.Config{
		Timeout:     2 * time.Minute,
		StdoutLimit: 256 * 1024,
		StderrLimit: 256 * 1024,
	})
	if err != nil {
		t.Fatalf("runner.New() error = %v", err)
	}

	temp := t.TempDir()
	oddInput := filepath.Join(temp, "odd.mkv")
	videoInput := filepath.Join(temp, "rotated.mkv")
	largeVideoInput := filepath.Join(temp, "portrait.mp4")
	audioInput := filepath.Join(temp, "input.wav")
	runCommand(t, r, ffmpeg, []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc=size=321x241:rate=24:duration=1",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100:duration=1",
		"-map", "0:v:0", "-map", "1:a:0",
		"-c:v", "ffv1", "-c:a", "pcm_s16le",
		oddInput,
	})
	runCommand(t, r, ffmpeg, []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-display_rotation:v:0", "90",
		"-i", oddInput,
		"-map", "0",
		"-c", "copy",
		videoInput,
	})
	runCommand(t, r, ffmpeg, []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=1080x1920:rate=1:duration=1",
		"-f", "lavfi", "-i", "sine=frequency=220:sample_rate=48000:duration=1",
		"-map", "0:v:0", "-map", "1:a:0",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac",
		largeVideoInput,
	})
	runCommand(t, r, ffmpeg, []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "sine=frequency=880:sample_rate=44100:duration=1",
		"-c:a", "pcm_s16le", audioInput,
	})

	rotatedProbe, _, err := media.ProbeFile(context.Background(), r.Run, ffprobe, videoInput, 30*time.Second)
	if err != nil {
		t.Fatalf("ProbeFile(rotated fixture) error = %v", err)
	}
	rotatedSelection := media.SelectStreams(rotatedProbe)
	if rotatedSelection.Video == nil ||
		rotatedSelection.Video.Width != 321 ||
		rotatedSelection.Video.Height != 241 ||
		rotatedSelection.Video.Rotation() != 90 {
		t.Fatalf("rotated fixture video = %+v; want 321x241 rotation 90", rotatedSelection.Video)
	}
	largeProbe, _, err := media.ProbeFile(context.Background(), r.Run, ffprobe, largeVideoInput, 30*time.Second)
	if err != nil {
		t.Fatalf("ProbeFile(large fixture) error = %v", err)
	}
	largeSelection := media.SelectStreams(largeProbe)
	if largeSelection.Video == nil ||
		largeSelection.Video.Width != 1080 ||
		largeSelection.Video.Height != 1920 {
		t.Fatalf("large fixture video = %+v; want 1080x1920", largeSelection.Video)
	}

	tests := []struct {
		name      string
		operation corpus.Operation
		input     string
		output    string
	}{
		{name: "compatible video", operation: corpus.OperationCompatibleVideo, input: videoInput, output: filepath.Join(temp, "compatible.mp4")},
		{name: "smaller video", operation: corpus.OperationSmallerVideo, input: largeVideoInput, output: filepath.Join(temp, "smaller.mp4")},
		{name: "extract audio", operation: corpus.OperationExtractAudio, input: videoInput, output: filepath.Join(temp, "extracted.m4a")},
		{name: "compatible audio", operation: corpus.OperationCompatibleAudio, input: audioInput, output: filepath.Join(temp, "compatible.mp3")},
		{name: "lossless audio", operation: corpus.OperationLosslessAudio, input: audioInput, output: filepath.Join(temp, "lossless.flac")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputProbe, _, err := media.ProbeFile(context.Background(), r.Run, ffprobe, tt.input, 30*time.Second)
			if err != nil {
				t.Fatalf("ProbeFile(input) error = %v", err)
			}
			plan, err := media.BuildPlan(media.PlanRequest{
				Operation:  tt.operation,
				InputPath:  tt.input,
				OutputPath: tt.output,
				Input:      inputProbe,
				Timeout:    2 * time.Minute,
			})
			if err != nil {
				t.Fatalf("BuildPlan() error = %v", err)
			}
			result, err := media.Convert(context.Background(), r.Run, ffmpeg, ffprobe, plan)
			if err != nil {
				t.Fatalf("Convert() error = %v; stderr = %s", err, result.Command.Stderr.Bytes)
			}
			if _, err := os.Stat(tt.output); err != nil {
				t.Fatalf("output stat error = %v", err)
			}
			if len(result.Progress) == 0 || !result.Progress[len(result.Progress)-1].Done {
				t.Fatalf("progress = %+v; want completed update", result.Progress)
			}
		})
	}
}

func TestVAAPIIntegration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("VA-API integration requires Linux")
	}
	config, err := media.AccelerationConfigFromEnv()
	if err != nil {
		t.Fatalf("AccelerationConfigFromEnv() error = %v", err)
	}
	config.Mode = media.AccelerationVAAPI
	device := config.Device
	info, err := os.Stat(device)
	if err != nil {
		t.Skipf("VA-API integration requires %s: %v", device, err)
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		t.Skipf("VA-API integration requires a character device at %s", device)
	}

	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("VA-API integration requires ffmpeg")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("VA-API integration requires ffprobe")
	}
	r, err := runner.New(runner.Config{
		Timeout:     2 * time.Minute,
		StdoutLimit: 256 * 1024,
		StderrLimit: 256 * 1024,
	})
	if err != nil {
		t.Fatalf("runner.New() error = %v", err)
	}
	probeResult, err := media.ProbeVAAPI(
		context.Background(),
		r.Run,
		ffmpeg,
		ffprobe,
		config,
		30*time.Second,
	)
	if err != nil {
		t.Skipf(
			"VA-API integration requires usable h264_vaapi on %s (%s)",
			device,
			probeResult.Readiness.UnavailableReason,
		)
	}

	temp := t.TempDir()
	inputPath := filepath.Join(temp, "input.mp4")
	outputPath := filepath.Join(temp, "output.mp4")
	runCommand(t, r, ffmpeg, []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-init_hw_device", "vaapi=filetwist:" + device,
		"-filter_hw_device", "filetwist",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24",
		"-t", "1",
		"-an",
		"-vf", "format=nv12,hwupload",
		"-c:v", "h264_vaapi",
		"-qp", "24",
		"-movflags", "+faststart",
		inputPath,
	})
	inputProbe, _, err := media.ProbeFile(
		context.Background(),
		r.Run,
		ffprobe,
		inputPath,
		30*time.Second,
	)
	if err != nil {
		t.Fatalf("ProbeFile(input) error = %v", err)
	}
	plan, err := media.BuildPlan(media.PlanRequest{
		Operation:    corpus.OperationCompatibleVideo,
		InputPath:    inputPath,
		OutputPath:   outputPath,
		Input:        inputProbe,
		Timeout:      2 * time.Minute,
		Acceleration: config,
		VAAPI:        &probeResult.Readiness,
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	result, err := media.Convert(context.Background(), r.Run, ffmpeg, ffprobe, plan)
	if err != nil {
		t.Fatalf("Convert() error = %v; stderr = %s", err, result.Command.Stderr.Bytes)
	}
	if result.Execution.FinalPath == media.ExecutionPathCPU {
		t.Fatalf("VA-API integration unexpectedly fell back: %+v", result.Execution)
	}
}

func runCommand(t *testing.T, r *runner.Runner, path string, args []string) {
	t.Helper()
	result, err := r.Run(context.Background(), runner.Command{
		Path:    path,
		Args:    args,
		Timeout: 2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("%s error = %v; stderr = %s", filepath.Base(path), err, result.Stderr.Bytes)
	}
}
