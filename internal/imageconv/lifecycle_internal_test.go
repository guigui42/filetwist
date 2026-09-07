package imageconv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/probe"
	"github.com/guigui42/filetwist/internal/runner"
)

func TestProbeHEIFIgnoresCleanupFailureAfterSuccess(t *testing.T) {
	prober := mustFunctionalProber(t, func(context.Context, runner.Command) (runner.Result, error) {
		return runner.Result{ExitCode: 0}, nil
	})

	capabilities, err := probeDecodeWithFS(
		context.Background(),
		prober,
		"vips",
		"input.heic",
		t.TempDir(),
		FormatHEIF,
		os.MkdirTemp,
		func(string) error { return errors.New("cleanup failed") },
	)
	if err != nil {
		t.Fatalf("ProbeHEIF() error = %v", err)
	}
	if !capabilities.HEIFDecode {
		t.Fatal("ProbeHEIF() did not report HEIF decode support")
	}
}

func TestProbeHEIFPreservesCleanupFailureOnProbeError(t *testing.T) {
	prober := mustFunctionalProber(t, func(context.Context, runner.Command) (runner.Result, error) {
		return runner.Result{}, errors.New("decode unavailable")
	})

	_, err := probeDecodeWithFS(
		context.Background(),
		prober,
		"vips",
		"input.heic",
		t.TempDir(),
		FormatHEIF,
		os.MkdirTemp,
		func(string) error { return errors.New("cleanup failed") },
	)
	if err == nil {
		t.Fatal("ProbeHEIF() error = nil; want probe and cleanup failure")
	}
	var probeErr *probe.Error
	if !errors.As(err, &probeErr) || probeErr.Stage != probe.StageFunctional {
		t.Fatalf("ProbeHEIF() error = %v; want functional probe failure", err)
	}
	var cleanupErr *Error
	if !errors.As(err, &cleanupErr) || cleanupErr.Code != CodeCleanupFailed {
		t.Fatalf("ProbeHEIF() error = %v; want cleanup failure preserved", err)
	}
}

func TestConvertIgnoresCleanupFailureAfterSuccess(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.png")
	if err := os.WriteFile(input, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(_ context.Context, command runner.Command) (runner.Result, error) {
		switch filepath.Base(command.Path) {
		case "vipsheader":
			loader := "pngload"
			if command.Args[len(command.Args)-1] != input {
				loader = "jpegload"
			}
			return runner.Result{Stdout: runner.Output{Bytes: []byte(
				"width: 2\nheight: 2\nbands: 3\nformat: uchar\ninterpretation: srgb\nvips-loader: " + loader,
			)}}, nil
		case "vips":
			if len(command.Args) > 0 && command.Args[0] == "jpegsave" {
				if err := os.WriteFile(command.Args[2], []byte("converted"), 0o600); err != nil {
					t.Fatal(err)
				}
				return runner.Result{}, nil
			}
		}
		return runner.Result{}, nil
	}

	converter, err := New(run, mustFunctionalProber(t, run), Config{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := converter.convertWithFS(context.Background(), Request{
		InputPath: input,
		OutputDir: dir,
		Operation: corpus.OperationCompatiblePhoto,
	}, imageFilesystem{
		mkdirTemp: os.MkdirTemp,
		removeAll: func(string) error { return errors.New("cleanup failed") },
		link:      os.Link,
		remove:    os.Remove,
	})
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if _, err := os.Stat(result.Output.Path); err != nil {
		t.Fatalf("published output missing: %v", err)
	}
}

func TestPublishNoReplaceTreatsLinkAsCommit(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "staged.jpg")
	target := filepath.Join(dir, "final.jpg")
	if err := os.WriteFile(source, []byte("converted"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := publishNoReplaceWith(source, target, os.Link, func(string) error { return errors.New("unlink failed") }); err != nil {
		t.Fatalf("publishNoReplace() error = %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(content) != "converted" {
		t.Fatalf("target content = %q; want converted", content)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("staged output should remain when unlink fails: %v", err)
	}
}

func mustFunctionalProber(t *testing.T, run probe.RunFunc) *probe.Prober {
	t.Helper()

	prober, err := probe.New(run, probe.WithLookPath(func(name string) (string, error) {
		return name, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	return prober
}
