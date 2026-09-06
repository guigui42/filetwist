package media_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/media"
	"github.com/guigui42/filetwist/internal/runner"
)

func TestFFmpegProfilesStripKnownSensitiveMetadataIntegration(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe unavailable")
	}
	r, err := runner.New(runner.Config{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "tagged.mkv")
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=32x18:rate=10:duration=0.4",
		"-f", "lavfi", "-i", "sine=sample_rate=48000:duration=0.4",
		"-map", "0:v", "-map", "1:a", "-c:v", "libx264", "-c:a", "flac",
	}
	for _, scope := range []string{"-metadata", "-metadata:s:v:0", "-metadata:s:a:0"} {
		for _, key := range []string{"LOCATION", "EXIF", "XMP", "GAIN_MAP"} {
			args = append(args, scope, key+"=harmless-test-metadata")
		}
	}
	runCommand(t, r, ffmpeg, append(args, inputPath))
	input, _, err := media.ProbeFile(context.Background(), r.Run, ffprobe, inputPath, time.Second*10)
	if err != nil {
		t.Fatal(err)
	}
	wantMetadata := media.SensitiveMetadata{GPS: true, EXIF: true, XMP: true, GainMap: true}
	if got := media.DetectSensitiveMetadata(media.Probe{Format: input.Format}); got != wantMetadata {
		t.Fatalf("fixture container metadata = %+v; want %+v", got, wantMetadata)
	}
	for _, stream := range input.Streams {
		if got := media.DetectSensitiveMetadata(media.Probe{Streams: []media.Stream{stream}}); got != wantMetadata {
			t.Fatalf("fixture stream %d metadata = %+v; want %+v", stream.Index, got, wantMetadata)
		}
	}

	for _, profile := range []struct {
		operation corpus.Operation
		extension string
	}{
		{corpus.OperationCompatibleVideo, ".mp4"},
		{corpus.OperationSmallerVideo, ".mp4"},
		{corpus.OperationExtractAudio, ".m4a"},
		{corpus.OperationCompatibleAudio, ".mp3"},
		{corpus.OperationLosslessAudio, ".flac"},
	} {
		t.Run(string(profile.operation), func(t *testing.T) {
			plan, err := media.BuildPlan(media.PlanRequest{
				Operation: profile.operation, InputPath: inputPath, Input: input,
				OutputPath:   filepath.Join(dir, string(profile.operation)+profile.extension),
				Acceleration: media.AccelerationConfig{Mode: media.AccelerationCPU},
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := media.Convert(context.Background(), r.Run, ffmpeg, ffprobe, plan)
			if err != nil {
				t.Fatalf("Convert() error = %v; stderr = %s", err, result.Command.Stderr.Bytes)
			}
			if metadata := media.DetectSensitiveMetadata(result.Output); metadata != (media.SensitiveMetadata{}) {
				t.Errorf("output retained known sensitive metadata: %+v", metadata)
			}
		})
	}
}
