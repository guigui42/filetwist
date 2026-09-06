package imageconv_test

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/imageconv"
	"github.com/guigui42/filetwist/internal/probe"
	"github.com/guigui42/filetwist/internal/runner"
)

func TestLibvipsPrecisionIntegration(t *testing.T) {
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
	config := imageconv.Config{
		VipsExecutable:   vipsPath,
		HeaderExecutable: headerPath,
	}
	converter, err := imageconv.New(processRunner.Run, prober, config)
	if err != nil {
		t.Fatalf("imageconv.New() error = %v", err)
	}
	requireOperation := func(t *testing.T, operation string) {
		t.Helper()
		if _, err := processRunner.Run(context.Background(), runner.Command{
			Path: vipsPath, Args: []string{"-l", operation},
		}); err != nil {
			t.Skipf("libvips integration skipped: %s is unavailable: %v", operation, err)
		}
	}
	runVips := func(t *testing.T, args ...string) {
		t.Helper()
		result, err := processRunner.Run(context.Background(), runner.Command{Path: vipsPath, Args: args})
		if err != nil {
			t.Fatalf("vips %v: %v: %s", args, err, result.Stderr.Bytes)
		}
	}

	tests := []struct {
		name       string
		bandFormat string
		alpha      bool
		icc        bool
	}{
		{name: "RGB16", bandFormat: "ushort"},
		{name: "RGBA16", bandFormat: "ushort", alpha: true},
		{name: "gray16", bandFormat: "ushort"},
		{name: "CMYK8", bandFormat: "uchar", icc: true},
		{name: "CMYK16", bandFormat: "ushort", icc: true},
	}
	operations := []struct {
		operation corpus.Operation
		saver     string
		format    imageconv.Format
	}{
		{operation: corpus.OperationLosslessImage, saver: "pngsave", format: imageconv.FormatPNG},
		{operation: corpus.OperationCompatiblePhoto, saver: "jpegsave", format: imageconv.FormatJPEG},
		{operation: corpus.OperationSmallerPhoto, saver: "webpsave", format: imageconv.FormatWebP},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "source.png")
			if tt.icc {
				requireOperation(t, "icc_transform")
				requireOperation(t, "tiffsave")
				if _, err := processRunner.Run(context.Background(), runner.Command{
					Path: vipsPath, Args: []string{"profile_load", "cmyk"},
				}); err != nil {
					t.Skipf("libvips integration skipped: built-in CMYK profile is unavailable: %v", err)
				}
				depth := "8"
				if tt.bandFormat == "ushort" {
					depth = "16"
				}
				source := filepath.Join("..", "..", "fixtures", "generated", "opaque-3x2.jpg")
				if _, err := imageconv.ProbeFile(context.Background(), processRunner.Run, headerPath, source); err != nil {
					t.Skipf("libvips integration skipped: JPEG fixture loader is unavailable: %v", err)
				}
				cmyk := filepath.Join(dir, "cmyk.v")
				runVips(t, "icc_transform", source, cmyk, "cmyk", "--input-profile", "srgb", "--depth", depth)
				input = filepath.Join(dir, "source.tif")
				runVips(t, "tiffsave", cmyk, input, "--keep", "icc")
			} else {
				writePrecisionPNG(t, input, tt.name == "gray16", tt.alpha)
			}
			info, err := imageconv.ProbeFile(context.Background(), processRunner.Run, headerPath, input)
			if err != nil {
				t.Fatalf("ProbeFile(input) error = %v", err)
			}
			if info.BandFormat != tt.bandFormat || info.Metadata.ICC != tt.icc || info.HasAlpha != tt.alpha {
				t.Fatalf("unexpected precision fixture properties: %+v", info)
			}
			if tt.icc && info.Interpretation != "cmyk" {
				t.Fatalf("fixture interpretation = %q; want cmyk", info.Interpretation)
			}

			for _, operation := range operations {
				t.Run(string(operation.operation), func(t *testing.T) {
					requireOperation(t, operation.saver)
					result, err := converter.Convert(context.Background(), imageconv.Request{
						InputPath: input,
						OutputDir: t.TempDir(),
						Operation: operation.operation,
					})
					if err != nil {
						t.Fatalf("Convert() error = %v", err)
					}
					wantBandFormat := "uchar"
					if operation.operation == corpus.OperationLosslessImage {
						wantBandFormat = tt.bandFormat
					}
					if result.Observed.BandFormat != wantBandFormat {
						t.Errorf("output band format = %q; want %q", result.Observed.BandFormat, wantBandFormat)
					}
					if result.Observed.Format != operation.format {
						t.Errorf("output format = %q; want %q", result.Observed.Format, operation.format)
					}
					if result.Observed.Metadata.ICC != tt.icc {
						t.Errorf("output ICC profile = %t; want %t", result.Observed.Metadata.ICC, tt.icc)
					}
					wantAlpha := tt.alpha && operation.operation != corpus.OperationCompatiblePhoto
					if result.Observed.HasAlpha != wantAlpha {
						t.Errorf("output alpha = %t; want %t", result.Observed.HasAlpha, wantAlpha)
					}
					if result.Observed.Width != info.Width || result.Observed.Height != info.Height {
						t.Errorf("output dimensions = %dx%d; want %dx%d",
							result.Observed.Width, result.Observed.Height, info.Width, info.Height)
					}
					if operation.operation == corpus.OperationLosslessImage {
						output := readPrecisionPNG(t, result.Output.Path)
						if !tt.icc {
							assertImagePixelsEqual(t, readPrecisionPNG(t, input), output)
						} else if tt.bandFormat == "ushort" {
							if result.Observed.Interpretation != "rgb16" {
								t.Errorf("normalized interpretation = %q; want rgb16", result.Observed.Interpretation)
							}
							assertSub8BitPrecision(t, output)
						}
					}
				})
			}
			if tt.bandFormat == "ushort" {
				t.Run("reject reduced PNG precision", func(t *testing.T) {
					requireOperation(t, "pngsave")
					reduced := false
					run := func(ctx context.Context, command runner.Command) (runner.Result, error) {
						if command.Path == vipsPath && len(command.Args) > 0 && command.Args[0] == "pngsave" {
							command.Args = append(append([]string(nil), command.Args...), "--bitdepth", "8")
							reduced = true
						}
						return processRunner.Run(ctx, command)
					}
					reducingConverter, err := imageconv.New(run, prober, config)
					if err != nil {
						t.Fatalf("imageconv.New() error = %v", err)
					}
					outputDir := t.TempDir()
					result, err := reducingConverter.Convert(context.Background(), imageconv.Request{
						InputPath: input,
						OutputDir: outputDir,
						Operation: corpus.OperationLosslessImage,
					})
					assertErrorCode(t, err, imageconv.CodeOutputPrecision)
					if !reduced || result.Observed.BandFormat != "uchar" {
						t.Fatalf("expected a real reduced-precision PNG, reduced=%t observed=%+v", reduced, result.Observed)
					}
					entries, err := os.ReadDir(outputDir)
					if err != nil {
						t.Fatal(err)
					}
					if len(entries) != 0 {
						t.Errorf("rejected conversion published output or left work files: %v", entries)
					}
				})
			}
		})
	}
}

func writePrecisionPNG(t *testing.T, path string, grayscale, alpha bool) {
	t.Helper()
	bounds := image.Rect(0, 0, 3, 2)
	rgb := image.NewNRGBA64(bounds)
	gray := image.NewGray16(bounds)
	for y := range bounds.Dy() {
		for x := range bounds.Dx() {
			value := uint16(0x1234 + (x+y*bounds.Dx())*0x2345)
			a := uint16(0xffff)
			if alpha && x == 0 {
				a = 0x4321
			}
			rgb.SetNRGBA64(x, y, color.NRGBA64{R: value, G: 0xabcd, B: 0x2345, A: a})
			gray.SetGray16(x, y, color.Gray16{Y: value})
		}
	}
	var pixels image.Image = rgb
	if grayscale {
		pixels = gray
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encodeErr := png.Encode(file, pixels)
	closeErr := file.Close()
	if encodeErr != nil || closeErr != nil {
		t.Fatalf("write precision PNG: encode=%v close=%v", encodeErr, closeErr)
	}
}

func readPrecisionPNG(t *testing.T, path string) image.Image {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	pixels, err := png.Decode(file)
	if err != nil {
		t.Fatalf("decode PNG: %v", err)
	}
	return pixels
}

func assertImagePixelsEqual(t *testing.T, want, got image.Image) {
	t.Helper()
	if got.Bounds() != want.Bounds() {
		t.Fatalf("image bounds = %v; want %v", got.Bounds(), want.Bounds())
	}
	for y := range want.Bounds().Dy() {
		for x := range want.Bounds().Dx() {
			wantColor := color.NRGBA64Model.Convert(want.At(x, y))
			gotColor := color.NRGBA64Model.Convert(got.At(x, y))
			if gotColor != wantColor {
				t.Errorf("pixel (%d,%d) = %v; want %v", x, y, gotColor, wantColor)
			}
		}
	}
}

func assertSub8BitPrecision(t *testing.T, pixels image.Image) {
	t.Helper()
	for y := range pixels.Bounds().Dy() {
		for x := range pixels.Bounds().Dx() {
			r, g, b, _ := pixels.At(x, y).RGBA()
			if r%257 != 0 || g%257 != 0 || b%257 != 0 {
				return
			}
		}
	}
	t.Error("all normalized samples are representable at 8 bits; want real 16-bit precision")
}
