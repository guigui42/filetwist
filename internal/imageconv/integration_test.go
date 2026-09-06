package imageconv_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/imageconv"
	"github.com/guigui42/filetwist/internal/probe"
	"github.com/guigui42/filetwist/internal/runner"
)

func TestLibvipsIntegration(t *testing.T) {
	vipsPath, err := exec.LookPath("vips")
	if err != nil {
		t.Skip("libvips integration skipped: vips executable is unavailable")
	}
	headerPath, err := exec.LookPath("vipsheader")
	if err != nil {
		t.Skip("libvips integration skipped: vipsheader executable is unavailable")
	}

	processRunner, err := runner.New(runner.Config{Timeout: 15 * time.Second})
	if err != nil {
		t.Fatalf("runner.New() error = %v", err)
	}
	prober, err := probe.New(processRunner.Run)
	if err != nil {
		t.Fatalf("probe.New() error = %v", err)
	}
	converter, err := imageconv.New(processRunner.Run, prober, imageconv.Config{
		VipsExecutable:    vipsPath,
		HeaderExecutable:  headerPath,
		Limits:            imageconv.DefaultLimits(),
		CaptureDatePolicy: imageconv.CaptureDateStrip,
		Capabilities:      imageconv.Capabilities{},
	})
	if err != nil {
		t.Fatalf("imageconv.New() error = %v", err)
	}

	root := filepath.Clean(filepath.Join("..", ".."))
	tests := []struct {
		name             string
		input            string
		operation        corpus.Operation
		saver            string
		format           imageconv.Format
		alpha            bool
		width            int
		height           int
		inputGPS         bool
		inputOrientation int
	}{
		{
			name:      "compatible JPEG",
			input:     filepath.Join(root, "fixtures", "generated", "opaque-3x2.jpg"),
			operation: corpus.OperationCompatiblePhoto,
			saver:     "jpegsave",
			format:    imageconv.FormatJPEG,
			width:     3,
			height:    2,
		},
		{
			name:             "compatible oriented GPS JPEG",
			input:            filepath.Join(root, "fixtures", "generated", "oriented-gps-3x2.jpg"),
			operation:        corpus.OperationCompatiblePhoto,
			saver:            "jpegsave",
			format:           imageconv.FormatJPEG,
			width:            2,
			height:           3,
			inputGPS:         true,
			inputOrientation: 6,
		},
		{
			name:      "smaller transparent WebP",
			input:     filepath.Join(root, "fixtures", "generated", "rgba-2x2.png"),
			operation: corpus.OperationSmallerPhoto,
			saver:     "webpsave",
			format:    imageconv.FormatWebP,
			alpha:     true,
			width:     2,
			height:    2,
		},
		{
			name:      "lossless transparent PNG",
			input:     filepath.Join(root, "fixtures", "generated", "rgba-2x2.png"),
			operation: corpus.OperationLosslessImage,
			saver:     "pngsave",
			format:    imageconv.FormatPNG,
			alpha:     true,
			width:     2,
			height:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := imageconv.ProbeFile(context.Background(), processRunner.Run, headerPath, tt.input); err != nil {
				t.Skipf("libvips integration skipped: required input loader is unavailable: %v", err)
			}
			if _, err := processRunner.Run(context.Background(), runner.Command{
				Path: vipsPath,
				Args: []string{"-l", tt.saver},
			}); err != nil {
				t.Skipf("libvips integration skipped: %s saver is unavailable: %v", tt.saver, err)
			}

			result, err := converter.Convert(context.Background(), imageconv.Request{
				InputPath: tt.input,
				OutputDir: t.TempDir(),
				Operation: tt.operation,
			})
			if err != nil {
				t.Fatalf("Convert() error = %v", err)
			}
			if result.Observed.Format != tt.format {
				t.Errorf("format = %q; want %q", result.Observed.Format, tt.format)
			}
			if result.Observed.HasAlpha != tt.alpha {
				t.Errorf("alpha = %t; want %t", result.Observed.HasAlpha, tt.alpha)
			}
			if result.Observed.Width != tt.width || result.Observed.Height != tt.height {
				t.Errorf(
					"dimensions = %dx%d; want %dx%d",
					result.Observed.Width,
					result.Observed.Height,
					tt.width,
					tt.height,
				)
			}
			if tt.inputGPS && !result.Input.Metadata.GPS {
				t.Error("input fixture is missing synthetic GPS metadata")
			}
			if tt.inputOrientation != 0 && result.Input.Orientation != tt.inputOrientation {
				t.Errorf("input orientation = %d; want %d", result.Input.Orientation, tt.inputOrientation)
			}
			if result.Observed.Metadata.GPS {
				t.Error("output retained GPS metadata")
			}
			if result.Observed.Orientation != 1 {
				t.Errorf("orientation = %d; want normalized orientation 1", result.Observed.Orientation)
			}
		})
	}
}
