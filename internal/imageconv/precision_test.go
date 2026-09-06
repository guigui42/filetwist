package imageconv_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/imageconv"
)

func TestBuildPlanICCPrecision(t *testing.T) {
	inputs := []struct {
		name           string
		bands          int
		bandFormat     string
		interpretation string
	}{
		{name: "CMYK8", bands: 4, bandFormat: "uchar", interpretation: "cmyk"},
		{name: "CMYK16", bands: 4, bandFormat: "ushort", interpretation: "cmyk"},
		{name: "gray8", bands: 1, bandFormat: "uchar", interpretation: "b-w"},
		{name: "gray16", bands: 1, bandFormat: "ushort", interpretation: "grey16"},
		{name: "ushort b-w", bands: 1, bandFormat: "ushort", interpretation: "b-w"},
	}
	operations := []corpus.Operation{
		corpus.OperationLosslessImage,
		corpus.OperationCompatiblePhoto,
		corpus.OperationSmallerPhoto,
	}
	for _, input := range inputs {
		for _, operation := range operations {
			t.Run(input.name+"/"+string(operation), func(t *testing.T) {
				plan, err := imageconv.BuildPlan(imageconv.PlanRequest{
					InputPath: "/input/photo.tif",
					OutputDir: "/out",
					WorkDir:   "/work",
					Operation: operation,
					Input: imageconv.Info{
						Format:         imageconv.FormatTIFF,
						Width:          3,
						Height:         2,
						Bands:          input.bands,
						BandFormat:     input.bandFormat,
						Interpretation: input.interpretation,
						Orientation:    1,
						Pages:          1,
						Metadata:       imageconv.Metadata{ICC: true},
					},
					VipsPath: "/tools/vips",
				})
				if err != nil {
					t.Fatalf("BuildPlan() error = %v", err)
				}
				if len(plan.Commands) != 3 {
					t.Fatalf("commands = %d; want autorot, ICC transform, and save", len(plan.Commands))
				}
				depth := "8"
				if operation == corpus.OperationLosslessImage && input.bandFormat == "ushort" {
					depth = "16"
				}
				want := []string{
					"icc_transform", filepath.Join("/work", "oriented.v"), filepath.Join("/work", "colour.v"),
					"srgb", "--embedded", "--depth", depth,
				}
				if !reflect.DeepEqual(plan.Commands[1].Args, want) {
					t.Errorf("ICC transform = %v; want %v", plan.Commands[1].Args, want)
				}
				if !plan.Expect.Metadata.ICC {
					t.Error("normalized output must retain its sRGB ICC profile")
				}
				wantBandFormat := ""
				if operation == corpus.OperationLosslessImage {
					wantBandFormat = input.bandFormat
				}
				if plan.Expect.BandFormat != wantBandFormat {
					t.Errorf("expected band format = %q; want %q", plan.Expect.BandFormat, wantBandFormat)
				}
			})
		}
	}
}

func TestBuildPlanLosslessPrecisionWithoutNormalization(t *testing.T) {
	tests := []struct {
		name           string
		bands          int
		bandFormat     string
		interpretation string
		alpha          bool
	}{
		{name: "RGB8", bands: 3, bandFormat: "uchar", interpretation: "srgb"},
		{name: "RGBA8", bands: 4, bandFormat: "uchar", interpretation: "srgb", alpha: true},
		{name: "RGB16", bands: 3, bandFormat: "ushort", interpretation: "rgb16"},
		{name: "RGBA16", bands: 4, bandFormat: "ushort", interpretation: "rgb16", alpha: true},
		{name: "gray16", bands: 1, bandFormat: "ushort", interpretation: "grey16"},
		{name: "gray alpha16", bands: 2, bandFormat: "ushort", interpretation: "grey16", alpha: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := imageconv.BuildPlan(imageconv.PlanRequest{
				InputPath: "/input/photo.png",
				OutputDir: "/out",
				WorkDir:   "/work",
				Operation: corpus.OperationLosslessImage,
				Input: imageconv.Info{
					Format:         imageconv.FormatPNG,
					Width:          3,
					Height:         2,
					Bands:          tt.bands,
					BandFormat:     tt.bandFormat,
					Interpretation: tt.interpretation,
					Orientation:    1,
					HasAlpha:       tt.alpha,
					Pages:          1,
				},
				VipsPath: "/tools/vips",
			})
			if err != nil {
				t.Fatalf("BuildPlan() error = %v", err)
			}
			if plan.Expect.BandFormat != tt.bandFormat {
				t.Errorf("expected band format = %q; want %q", plan.Expect.BandFormat, tt.bandFormat)
			}
			if plan.Expect.HasAlpha != tt.alpha {
				t.Errorf("expected alpha = %t; want %t", plan.Expect.HasAlpha, tt.alpha)
			}
			if len(plan.Commands) != 2 {
				t.Fatalf("commands = %d; want only autorot and pngsave", len(plan.Commands))
			}
			wantSave := []string{
				"pngsave", filepath.Join("/work", "oriented.v"), filepath.Join("/work", "output.png"),
				"--compression", "9", "--keep", "none",
			}
			if !reflect.DeepEqual(plan.Commands[1].Args, wantSave) {
				t.Errorf("save = %v; want %v", plan.Commands[1].Args, wantSave)
			}
		})
	}
}

func TestValidateOutputPrecision(t *testing.T) {
	tests := []struct {
		name     string
		expected string
		observed string
		wantCode string
	}{
		{name: "preserves 16 bits", expected: "ushort", observed: "ushort"},
		{name: "preserves 8 bits", expected: "uchar", observed: "uchar"},
		{name: "rejects reduction", expected: "ushort", observed: "uchar", wantCode: imageconv.CodeOutputPrecision},
		{name: "rejects missing precision", expected: "ushort", wantCode: imageconv.CodeOutputPrecision},
		{name: "rejects unsupported precision", expected: "ushort", observed: "float", wantCode: imageconv.CodeOutputPrecision},
		{name: "rejects unexpected precision", expected: "uchar", observed: "ushort", wantCode: imageconv.CodeOutputPrecision},
		{name: "no precision requirement", observed: "uchar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := imageconv.ValidateOutput(imageconv.Expectation{
				Format:      imageconv.FormatPNG,
				MIMEType:    "image/png",
				Width:       3,
				Height:      2,
				Orientation: 1,
				BandFormat:  tt.expected,
			}, imageconv.Info{
				Format:      imageconv.FormatPNG,
				MIMEType:    "image/png",
				Width:       3,
				Height:      2,
				Orientation: 1,
				Pages:       1,
				BandFormat:  tt.observed,
			})
			assertErrorCode(t, err, tt.wantCode)
		})
	}
}

func TestBuildPlanRejectsProfiled16BitAlphaPreservation(t *testing.T) {
	inputs := []struct {
		interpretation string
		bands          int
	}{
		{interpretation: "cmyk", bands: 5},
		{interpretation: "grey16", bands: 2},
		{interpretation: "b-w", bands: 2},
	}
	for _, input := range inputs {
		for _, operation := range []corpus.Operation{corpus.OperationLosslessImage, corpus.OperationSmallerPhoto} {
			t.Run(input.interpretation+"/"+string(operation), func(t *testing.T) {
				_, err := imageconv.BuildPlan(imageconv.PlanRequest{
					InputPath: "/input/photo.tif",
					OutputDir: "/out",
					WorkDir:   "/work",
					Operation: operation,
					Input: imageconv.Info{
						Format:         imageconv.FormatTIFF,
						Width:          3,
						Height:         2,
						Bands:          input.bands,
						BandFormat:     "ushort",
						Interpretation: input.interpretation,
						Orientation:    1,
						HasAlpha:       true,
						Pages:          1,
						Metadata:       imageconv.Metadata{ICC: true},
					},
					VipsPath: "/tools/vips",
				})
				assertErrorCode(t, err, imageconv.CodeAlphaUnsupported)
			})
		}
	}
}
