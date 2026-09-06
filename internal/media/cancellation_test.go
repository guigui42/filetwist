package media_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/media"
	"github.com/guigui42/filetwist/internal/runner"
)

func TestConvertDoesNotPublishAfterProbeCancellation(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "output.mp3")
	plan, err := media.BuildPlan(media.PlanRequest{
		Operation: corpus.OperationCompatibleAudio, InputPath: "input.flac",
		OutputPath: output, Input: readProbeFixture(t, "common-audio.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := func(_ context.Context, command runner.Command) (runner.Result, error) {
		if command.Path == "ffmpeg" {
			if err := os.WriteFile(command.Args[len(command.Args)-1], []byte("converted"), 0o600); err != nil {
				t.Fatal(err)
			}
			return runner.Result{}, nil
		}
		cancel()
		return runner.Result{Stdout: runner.Output{Bytes: []byte(`{
			"streams":[{"codec_type":"audio","codec_name":"mp3","channels":2,
				"channel_layout":"stereo","sample_rate":"48000"}],
			"format":{"format_name":"mp3","duration":"3.5"}
		}`)}}, nil
	}
	if _, err := media.Convert(ctx, run, "ffmpeg", "ffprobe", plan); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v; want cancellation", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("canceled conversion left files: %v", entries)
	}
}
