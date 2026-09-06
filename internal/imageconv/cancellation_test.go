package imageconv_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/imageconv"
	"github.com/guigui42/filetwist/internal/probe"
	"github.com/guigui42/filetwist/internal/runner"
)

func TestConvertDoesNotPublishAfterProbeCancellation(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := func(_ context.Context, command runner.Command) (runner.Result, error) {
		if filepath.Base(command.Path) == "vipsheader" {
			loader := "pngload"
			if command.Args[len(command.Args)-1] != "input.png" {
				loader = "jpegload"
				cancel()
			}
			return runner.Result{Stdout: runner.Output{Bytes: []byte(
				"width: 2\nheight: 2\nbands: 3\nformat: uchar\ninterpretation: srgb\nvips-loader: " + loader,
			)}}, nil
		}
		if command.Args[0] == "jpegsave" {
			if err := os.WriteFile(command.Args[2], []byte("converted"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return runner.Result{}, nil
	}
	prober, err := probe.New(run, probe.WithLookPath(func(name string) (string, error) {
		return name, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	converter, err := imageconv.New(run, prober, imageconv.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := converter.Convert(ctx, imageconv.Request{
		InputPath: "input.png", OutputDir: dir, Operation: corpus.OperationCompatiblePhoto,
	}); !errors.Is(err, context.Canceled) {
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
