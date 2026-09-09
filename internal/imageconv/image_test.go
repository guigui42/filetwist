package imageconv_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/imageconv"
	"github.com/guigui42/filetwist/internal/probe"
	"github.com/guigui42/filetwist/internal/profiles"
	"github.com/guigui42/filetwist/internal/runner"
)

func TestParseHeader(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		want    imageconv.Info
		wantErr bool
	}{
		{
			name: "HEIF with orientation alpha and metadata",
			header: strings.Join([]string{
				"phone.bin: 3024x4032 uchar, 4 bands, srgb, heifload",
				"width: 3024",
				"height: 4032",
				"bands: 4",
				"format: uchar",
				"interpretation: srgb",
				"vips-loader: heifload",
				"n-pages: 1",
				"orientation: 6",
				"icc-profile-data: 560 bytes of binary data",
				"exif-data: 1234 bytes of binary data",
				"exif-ifd2-DateTimeOriginal: 2025:01:02 03:04:05",
				"exif-ifd3-GPSLatitude: 48/1 51/1 0/1",
			}, "\n"),
			want: imageconv.Info{
				Format:         imageconv.FormatHEIF,
				MIMEType:       "image/heif",
				Loader:         "heifload",
				Width:          3024,
				Height:         4032,
				Bands:          4,
				BandFormat:     "uchar",
				Interpretation: "srgb",
				Orientation:    6,
				HasAlpha:       true,
				Pages:          1,
				Metadata: imageconv.Metadata{
					GPS:         true,
					ICC:         true,
					EXIF:        true,
					CaptureDate: true,
				},
			},
		},
		{
			name: "UltraHDR uses JPEG compatibility profile",
			header: strings.Join([]string{
				"input.jpg: 100x50 uchar, 3 bands, srgb, uhdrload",
				"width: 100",
				"height: 50",
				"bands: 3",
				"format: uchar",
				"interpretation: srgb",
				"vips-loader: uhdrload",
				"gainmap-data: 200 bytes of binary data",
			}, "\n"),
			want: imageconv.Info{
				Format:         imageconv.FormatJPEG,
				MIMEType:       "image/jpeg",
				Loader:         "uhdrload",
				Width:          100,
				Height:         50,
				Bands:          3,
				BandFormat:     "uchar",
				Interpretation: "srgb",
				Orientation:    1,
				Pages:          1,
				Metadata: imageconv.Metadata{
					GainMap: true,
				},
			},
		},
		{
			name: "AVIF is identified from HEIF compression",
			header: strings.Join([]string{
				"input.dat: 100x50 uchar, 3 bands, srgb, heifload",
				"width: 100",
				"height: 50",
				"bands: 3",
				"interpretation: srgb",
				"vips-loader: heifload",
				"heif-compression: av1",
				"cicp-colour-primaries: 9",
				"cicp-transfer-characteristics: 16",
			}, "\n"),
			want: imageconv.Info{
				Format:         imageconv.FormatAVIF,
				MIMEType:       "image/avif",
				Loader:         "heifload",
				Width:          100,
				Height:         50,
				Bands:          3,
				Interpretation: "srgb",
				Orientation:    1,
				Pages:          1,
				HDR:            true,
				CICP: imageconv.CICP{
					Present:   true,
					Primaries: 9,
					Transfer:  16,
					Matrix:    -1,
					FullRange: -1,
				},
			},
		},
		{
			name: "CMYK does not mistake fourth band for alpha",
			header: strings.Join([]string{
				"input.bin: 40x30 uchar, 4 bands, cmyk, jpegload",
				"width: 40",
				"height: 30",
				"bands: 4",
				"interpretation: cmyk",
				"vips-loader: jpegload",
			}, "\n"),
			want: imageconv.Info{
				Format:         imageconv.FormatJPEG,
				MIMEType:       "image/jpeg",
				Loader:         "jpegload",
				Width:          40,
				Height:         30,
				Bands:          4,
				Interpretation: "cmyk",
				Orientation:    1,
				Pages:          1,
			},
		},
		{
			name: "BMP through ImageMagick loader",
			header: strings.Join([]string{
				"input.unknown: 8x9 uchar, 3 bands, srgb, magickload",
				"width: 8",
				"height: 9",
				"bands: 3",
				"interpretation: srgb",
				"vips-loader: magickload",
				"magick-format: BMP",
			}, "\n"),
			want: imageconv.Info{
				Format:         imageconv.FormatBMP,
				MIMEType:       "image/bmp",
				Loader:         "magickload",
				Width:          8,
				Height:         9,
				Bands:          3,
				Interpretation: "srgb",
				Orientation:    1,
				Pages:          1,
			},
		},
		{
			name: "unknown loader",
			header: strings.Join([]string{
				"input.bin: 1x1 uchar, 3 bands, srgb, pdfload",
				"width: 1",
				"height: 1",
				"bands: 3",
				"interpretation: srgb",
				"vips-loader: pdfload",
			}, "\n"),
			wantErr: true,
		},
		{
			name: "missing dimensions",
			header: strings.Join([]string{
				"vips-loader: pngload",
				"bands: 4",
				"interpretation: srgb",
			}, "\n"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := imageconv.ParseHeader([]byte(tt.header))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseHeader() error = %v; wantErr %t", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseHeader() = %#v; want %#v", got, tt.want)
			}
		})
	}
}

func TestParseHeaderHDRTransferCharacteristics(t *testing.T) {
	tests := []struct {
		name string
		code string
		want bool
	}{
		{name: "PQ", code: "16", want: true},
		{name: "HLG", code: "18", want: true},
		{name: "sRGB", code: "13", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := imageconv.ParseHeader([]byte(strings.Join([]string{
				"width: 20",
				"height: 10",
				"bands: 3",
				"format: ushort",
				"interpretation: rgb16",
				"vips-loader: heifload",
				"heif-compression: hevc",
				"cicp-transfer-characteristics: " + tt.code,
			}, "\n")))
			if err != nil {
				t.Fatalf("ParseHeader() error = %v", err)
			}
			if info.HDR != tt.want {
				t.Errorf("HDR = %t; want %t", info.HDR, tt.want)
			}
		})
	}
}

func TestParseHeaderDoesNotTreatLabQPackingAsAlpha(t *testing.T) {
	info, err := imageconv.ParseHeader([]byte(strings.Join([]string{
		"width: 20",
		"height: 10",
		"bands: 4",
		"format: uchar",
		"coding: labq",
		"interpretation: lab",
		"vips-loader: tiffload",
	}, "\n")))
	if err != nil {
		t.Fatalf("ParseHeader() error = %v", err)
	}
	if info.HasAlpha {
		t.Error("LabQ packing band was incorrectly treated as alpha")
	}
	if info.Coding != "labq" {
		t.Errorf("Coding = %q; want labq", info.Coding)
	}
}

func TestValidateContentAndCapabilities(t *testing.T) {
	tests := []struct {
		name         string
		info         imageconv.Info
		capabilities imageconv.CapabilitySet
		limits       imageconv.Limits
		wantCode     string
	}{
		{
			name: "accepted JPEG",
			info: imageconv.Info{
				Format: imageconv.FormatJPEG,
				Width:  4032,
				Height: 3024,
				Pages:  1,
			},
			limits: imageconv.DefaultLimits(),
		},
		{
			name: "HEIF requires functional capability",
			info: imageconv.Info{
				Format: imageconv.FormatHEIF,
				Width:  4032,
				Height: 3024,
				Pages:  1,
			},
			limits:   imageconv.DefaultLimits(),
			wantCode: imageconv.CodeHEIFUnavailable,
		},
		{
			name: "HEIF accepted after functional probe",
			info: imageconv.Info{
				Format: imageconv.FormatHEIF,
				Width:  4032,
				Height: 3024,
				Pages:  1,
			},
			capabilities: imageconv.NewCapabilitySet(imageconv.CapabilityDecodeHEIF),
			limits:       imageconv.DefaultLimits(),
		},
		{
			name: "AVIF requires independent functional capability",
			info: imageconv.Info{
				Format: imageconv.FormatAVIF,
				Width:  4032,
				Height: 3024,
				Pages:  1,
			},
			capabilities: imageconv.NewCapabilitySet(imageconv.CapabilityDecodeHEIF),
			limits:       imageconv.DefaultLimits(),
			wantCode:     imageconv.CodeAVIFUnavailable,
		},
		{
			name: "AVIF accepted after functional probe",
			info: imageconv.Info{
				Format: imageconv.FormatAVIF,
				Width:  4032,
				Height: 3024,
				Pages:  1,
			},
			capabilities: imageconv.NewCapabilitySet(imageconv.CapabilityDecodeAVIF),
			limits:       imageconv.DefaultLimits(),
		},
		{
			name: "HDR input is clearly rejected",
			info: imageconv.Info{
				Format: imageconv.FormatHEIF,
				Width:  4032,
				Height: 3024,
				Pages:  1,
				HDR:    true,
			},
			capabilities: imageconv.NewCapabilitySet(imageconv.CapabilityDecodeHEIF),
			limits:       imageconv.DefaultLimits(),
			wantCode:     imageconv.CodeHDRUnsupported,
		},
		{
			name: "non-sRGB CICP input is clearly rejected",
			info: imageconv.Info{
				Format: imageconv.FormatHEIF,
				Width:  4032,
				Height: 3024,
				Pages:  1,
				CICP: imageconv.CICP{
					Present:   true,
					Primaries: 9,
					Transfer:  13,
					Matrix:    6,
					FullRange: 0,
				},
			},
			capabilities: imageconv.NewCapabilitySet(imageconv.CapabilityDecodeHEIF),
			limits:       imageconv.DefaultLimits(),
			wantCode:     imageconv.CodeColorUnsupported,
		},
		{
			name: "LabQ coding is clearly rejected",
			info: imageconv.Info{
				Format:         imageconv.FormatTIFF,
				Width:          100,
				Height:         100,
				Bands:          4,
				BandFormat:     "uchar",
				Coding:         "labq",
				Interpretation: "lab",
				Pages:          1,
			},
			limits:   imageconv.DefaultLimits(),
			wantCode: imageconv.CodeColorUnsupported,
		},
		{
			name: "LabS interpretation is clearly rejected",
			info: imageconv.Info{
				Format:         imageconv.FormatTIFF,
				Width:          100,
				Height:         100,
				Bands:          3,
				BandFormat:     "short",
				Interpretation: "labs",
				Pages:          1,
			},
			limits:   imageconv.DefaultLimits(),
			wantCode: imageconv.CodeColorUnsupported,
		},
		{
			name: "floating grayscale is clearly rejected",
			info: imageconv.Info{
				Format:         imageconv.FormatTIFF,
				Width:          100,
				Height:         100,
				Bands:          1,
				BandFormat:     "float",
				Interpretation: "b-w",
				Pages:          1,
			},
			limits:   imageconv.DefaultLimits(),
			wantCode: imageconv.CodeColorUnsupported,
		},
		{
			name: "extended-range scRGB is clearly rejected",
			info: imageconv.Info{
				Format:         imageconv.FormatTIFF,
				Width:          100,
				Height:         100,
				Bands:          3,
				BandFormat:     "float",
				Interpretation: "scrgb",
				Pages:          1,
			},
			limits:   imageconv.DefaultLimits(),
			wantCode: imageconv.CodeColorUnsupported,
		},
		{
			name: "multiple extra bands are clearly rejected",
			info: imageconv.Info{
				Format:         imageconv.FormatTIFF,
				Width:          100,
				Height:         100,
				Bands:          5,
				BandFormat:     "uchar",
				Interpretation: "srgb",
				Pages:          1,
			},
			limits:   imageconv.DefaultLimits(),
			wantCode: imageconv.CodeAlphaUnsupported,
		},
		{
			name: "unprofiled CMYK is clearly rejected",
			info: imageconv.Info{
				Format:         imageconv.FormatTIFF,
				Width:          100,
				Height:         100,
				Bands:          4,
				BandFormat:     "uchar",
				Interpretation: "cmyk",
				Pages:          1,
			},
			limits:   imageconv.DefaultLimits(),
			wantCode: imageconv.CodeColorUnsupported,
		},
		{
			name: "high-bit-depth HEIF without color signaling is clearly rejected",
			info: imageconv.Info{
				Format:         imageconv.FormatHEIF,
				Width:          100,
				Height:         100,
				Bands:          3,
				BandFormat:     "ushort",
				Interpretation: "rgb16",
				Pages:          1,
			},
			capabilities: imageconv.NewCapabilitySet(imageconv.CapabilityDecodeHEIF),
			limits:       imageconv.DefaultLimits(),
			wantCode:     imageconv.CodeColorUnsupported,
		},
		{
			name: "Lab alpha is clearly rejected",
			info: imageconv.Info{
				Format:         imageconv.FormatTIFF,
				Width:          100,
				Height:         100,
				Bands:          4,
				BandFormat:     "uchar",
				Interpretation: "lab",
				HasAlpha:       true,
				Pages:          1,
			},
			limits:   imageconv.DefaultLimits(),
			wantCode: imageconv.CodeAlphaUnsupported,
		},
		{
			name: "animated image rejected",
			info: imageconv.Info{
				Format: imageconv.FormatGIF,
				Width:  2,
				Height: 4,
				Pages:  2,
			},
			limits:   imageconv.DefaultLimits(),
			wantCode: imageconv.CodeAnimatedUnsupported,
		},
		{
			name: "width limit",
			info: imageconv.Info{
				Format: imageconv.FormatPNG,
				Width:  1001,
				Height: 20,
				Pages:  1,
			},
			limits:   imageconv.Limits{MaxWidth: 1000, MaxHeight: 1000, MaxPixels: 1_000_000},
			wantCode: imageconv.CodeDimensionsExceeded,
		},
		{
			name: "decoded pixel limit",
			info: imageconv.Info{
				Format: imageconv.FormatPNG,
				Width:  1000,
				Height: 1000,
				Pages:  1,
			},
			limits:   imageconv.Limits{MaxWidth: 2000, MaxHeight: 2000, MaxPixels: 999_999},
			wantCode: imageconv.CodeDimensionsExceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := tt.info
			if info.Format != "" && info.Bands == 0 {
				info.Bands = 3
				info.BandFormat = "uchar"
				info.Interpretation = "srgb"
			}
			err := imageconv.ValidateContent(info, tt.limits)
			if err == nil {
				err = imageconv.ValidateCapabilities(info, tt.capabilities)
			}
			assertErrorCode(t, err, tt.wantCode)
		})
	}
}

func TestCapabilitySet(t *testing.T) {
	var zero imageconv.CapabilitySet
	if zero.Has(imageconv.CapabilityDecodeHEIF) || zero.Has(imageconv.CapabilityDecodeAVIF) {
		t.Fatal("zero-value capability set contains optional decode support")
	}

	set := imageconv.NewCapabilitySet(imageconv.CapabilityDecodeHEIF)
	if !set.Has(imageconv.CapabilityDecodeHEIF) || set.Has(imageconv.CapabilityDecodeAVIF) {
		t.Fatalf("capability set = %064b; want only HEIF decode", set)
	}
	if imageconv.NewCapabilitySet(imageconv.Capability(0)) != 0 {
		t.Fatal("zero capability ID must not grant optional support")
	}
	if set.Has(imageconv.Capability(64)) {
		t.Fatal("capability set reported an out-of-range capability")
	}
}

func TestValidateCapabilities(t *testing.T) {
	tests := []struct {
		name     string
		format   imageconv.Format
		set      imageconv.CapabilitySet
		wantCode string
	}{
		{name: "JPEG needs no optional capability", format: imageconv.FormatJPEG},
		{name: "zero-value rejects HEIF", format: imageconv.FormatHEIF, wantCode: imageconv.CodeHEIFUnavailable},
		{name: "zero-value rejects AVIF", format: imageconv.FormatAVIF, wantCode: imageconv.CodeAVIFUnavailable},
		{
			name:   "HEIF accepts matching capability",
			format: imageconv.FormatHEIF,
			set:    imageconv.NewCapabilitySet(imageconv.CapabilityDecodeHEIF),
		},
		{
			name:     "HEIF rejects independent AVIF capability",
			format:   imageconv.FormatHEIF,
			set:      imageconv.NewCapabilitySet(imageconv.CapabilityDecodeAVIF),
			wantCode: imageconv.CodeHEIFUnavailable,
		},
		{
			name:   "AVIF accepts matching capability",
			format: imageconv.FormatAVIF,
			set:    imageconv.NewCapabilitySet(imageconv.CapabilityDecodeAVIF),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertErrorCode(t, imageconv.ValidateCapabilities(imageconv.Info{Format: tt.format}, tt.set), tt.wantCode)
		})
	}
}

func TestInfoCorpusProperties(t *testing.T) {
	info := imageconv.Info{
		Format:      imageconv.FormatHEIF,
		Width:       3024,
		Height:      4032,
		Orientation: 5,
		HasAlpha:    true,
		Metadata: imageconv.Metadata{
			GPS:  true,
			ICC:  true,
			EXIF: true,
		},
	}

	got := info.CorpusProperties()
	if got.Container != "heif" || len(got.Streams) != 1 || got.Streams[0].Codec != "hevc" {
		t.Errorf("content properties = %#v; want HEIF image stream", got)
	}
	if got.Dimensions == nil || got.Dimensions.Width != 3024 || got.Dimensions.Height != 4032 {
		t.Errorf("dimensions = %#v; want 3024x4032", got.Dimensions)
	}
	if got.Orientation == nil ||
		got.Orientation.RotationDegrees != 90 ||
		!got.Orientation.Mirrored ||
		got.Orientation.PixelNormalized {
		t.Errorf("orientation = %#v; want mirrored 90-degree display orientation", got.Orientation)
	}
	if got.Alpha != corpus.PresenceRequired ||
		got.Metadata.GPS != corpus.PresenceRequired ||
		got.Metadata.ColorProfile != corpus.PresenceRequired {
		t.Errorf("presence properties = %#v; want detected fields required", got)
	}
}

func TestBuildPlan(t *testing.T) {
	baseInput := imageconv.Info{
		Format:          imageconv.FormatPNG,
		MIMEType:        "image/png",
		Loader:          "pngload",
		Width:           1200,
		Height:          800,
		Bands:           4,
		BandFormat:      "uchar",
		Interpretation:  "srgb",
		Orientation:     6,
		HasAlpha:        true,
		HasTransparency: true,
		Pages:           1,
		Metadata: imageconv.Metadata{
			GPS:         true,
			ICC:         true,
			EXIF:        true,
			CaptureDate: true,
		},
	}

	tests := []struct {
		name        string
		operation   corpus.Operation
		input       imageconv.Info
		captureDate imageconv.CaptureDatePolicy
		wantOutput  imageconv.Output
		wantArgs    [][]string
		wantAlpha   bool
		wantCapture bool
	}{
		{
			name:      "compatible photo flattens alpha and strips GPS",
			operation: corpus.OperationCompatiblePhoto,
			input:     baseInput,
			wantOutput: imageconv.Output{
				Path:      filepath.Join("/out", "my-photo-compatible.jpg"),
				Extension: ".jpg",
				MIMEType:  "image/jpeg",
			},
			wantArgs: [][]string{
				{"autorot", "/input/My Photo.weird", filepath.Join("/work", "oriented.v")},
				{"flatten", filepath.Join("/work", "oriented.v"), filepath.Join("/work", "pixels.v"), "--background", "255"},
				{"jpegsave", filepath.Join("/work", "pixels.v"), filepath.Join("/work", "output.jpg"), "--Q", "90", "--optimize-coding", "--interlace", "--keep", "icc"},
			},
		},
		{
			name:      "smaller photo preserves alpha",
			operation: corpus.OperationSmallerPhoto,
			input:     withoutGPS(baseInput),
			wantOutput: imageconv.Output{
				Path:      filepath.Join("/out", "my-photo-smaller.webp"),
				Extension: ".webp",
				MIMEType:  "image/webp",
			},
			wantArgs: [][]string{
				{"autorot", "/input/My Photo.weird", filepath.Join("/work", "oriented.v")},
				{"webpsave", filepath.Join("/work", "oriented.v"), filepath.Join("/work", "output.webp"), "--Q", "80", "--effort", "6", "--alpha-q", "100", "--exact", "--keep", "icc,exif"},
			},
			wantAlpha:   true,
			wantCapture: true,
			captureDate: imageconv.CaptureDatePreserveWhenSafe,
		},
		{
			name:      "lossless image preserves alpha and strips capture date by policy",
			operation: corpus.OperationLosslessImage,
			input:     withoutGPS(baseInput),
			wantOutput: imageconv.Output{
				Path:      filepath.Join("/out", "my-photo-lossless.png"),
				Extension: ".png",
				MIMEType:  "image/png",
			},
			wantArgs: [][]string{
				{"autorot", "/input/My Photo.weird", filepath.Join("/work", "oriented.v")},
				{"pngsave", filepath.Join("/work", "oriented.v"), filepath.Join("/work", "output.png"), "--compression", "9", "--keep", "icc"},
			},
			wantAlpha: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := imageconv.BuildPlan(imageconv.PlanRequest{
				InputPath:         "/input/My Photo.weird",
				OutputDir:         "/out",
				WorkDir:           "/work",
				Operation:         tt.operation,
				Input:             tt.input,
				CaptureDatePolicy: tt.captureDate,
				VipsPath:          "/usr/bin/vips",
			})
			if err != nil {
				t.Fatalf("BuildPlan() error = %v", err)
			}
			if plan.Output != tt.wantOutput {
				t.Errorf("Output = %#v; want %#v", plan.Output, tt.wantOutput)
			}
			if plan.Expect.Width != tt.input.Height || plan.Expect.Height != tt.input.Width {
				t.Errorf("oriented dimensions = %dx%d; want %dx%d", plan.Expect.Width, plan.Expect.Height, tt.input.Height, tt.input.Width)
			}
			if plan.Expect.HasAlpha != tt.wantAlpha {
				t.Errorf("expected alpha = %t; want %t", plan.Expect.HasAlpha, tt.wantAlpha)
			}
			if plan.Expect.Metadata.GPS {
				t.Error("expected GPS metadata to be stripped")
			}
			if plan.Expect.Metadata.CaptureDate != tt.wantCapture {
				t.Errorf("expected capture date = %t; want %t", plan.Expect.Metadata.CaptureDate, tt.wantCapture)
			}
			if len(plan.Commands) != len(tt.wantArgs) {
				t.Fatalf("commands = %d; want %d", len(plan.Commands), len(tt.wantArgs))
			}
			for index, command := range plan.Commands {
				if command.Path != "/usr/bin/vips" {
					t.Errorf("command[%d].Path = %q; want /usr/bin/vips", index, command.Path)
				}
				if !reflect.DeepEqual(command.Args, tt.wantArgs[index]) {
					t.Errorf("command[%d].Args = %#v; want %#v", index, command.Args, tt.wantArgs[index])
				}
			}
		})
	}
}

func TestRegistryImageProfilesBuildCompletePlans(t *testing.T) {
	input := imageconv.Info{
		Format:         imageconv.FormatPNG,
		MIMEType:       "image/png",
		Width:          1200,
		Height:         800,
		Bands:          4,
		BandFormat:     "uchar",
		Interpretation: "srgb",
		Orientation:    1,
		HasAlpha:       true,
		Pages:          1,
	}

	for _, spec := range profiles.All() {
		if spec.Engine != profiles.EngineImage {
			continue
		}
		t.Run(string(spec.Operation), func(t *testing.T) {
			plan, err := imageconv.BuildPlan(imageconv.PlanRequest{
				InputPath:         "/input/photo.png",
				OutputDir:         "/out",
				WorkDir:           "/work",
				Operation:         spec.Operation,
				Input:             input,
				CaptureDatePolicy: imageconv.CaptureDateStrip,
				VipsPath:          "/usr/bin/vips",
			})
			if err != nil {
				t.Fatalf("BuildPlan() error = %v", err)
			}
			if len(plan.Commands) == 0 || plan.TemporaryOutput == "" {
				t.Fatalf("plan has no executable output: %+v", plan)
			}
			if plan.Output.Path == "" {
				t.Error("output path is empty")
			}

			save := plan.Commands[len(plan.Commands)-1]
			if len(save.Args) == 0 {
				t.Fatalf("save command has no arguments: %+v", save)
			}
			var wantFormat imageconv.Format
			var wantMIMEType string
			var wantExtension string
			switch save.Args[0] {
			case "jpegsave":
				wantFormat = imageconv.FormatJPEG
				wantMIMEType = "image/jpeg"
				wantExtension = ".jpg"
			case "webpsave":
				wantFormat = imageconv.FormatWebP
				wantMIMEType = "image/webp"
				wantExtension = ".webp"
			case "pngsave":
				wantFormat = imageconv.FormatPNG
				wantMIMEType = "image/png"
				wantExtension = ".png"
			default:
				t.Fatalf("unsupported save command %q", save.Args[0])
			}
			if plan.Expect.Format != wantFormat ||
				plan.Expect.MIMEType != wantMIMEType ||
				plan.Output.MIMEType != wantMIMEType ||
				plan.Output.Extension != wantExtension ||
				filepath.Ext(plan.TemporaryOutput) != wantExtension {
				t.Errorf(
					"save contract = output %+v, temporary %q, expectation %+v; command %q",
					plan.Output,
					plan.TemporaryOutput,
					plan.Expect,
					save.Args[0],
				)
			}
			if plan.Expect.Format == "" ||
				plan.Expect.MIMEType == "" ||
				plan.Expect.Width <= 0 ||
				plan.Expect.Height <= 0 ||
				plan.Expect.Orientation != 1 {
				t.Errorf("expectation is incomplete: %+v", plan.Expect)
			}
		})
	}
}

func TestBuildPlanUsesFormatCorrectWhiteBackground(t *testing.T) {
	tests := []struct {
		name           string
		bands          int
		bandFormat     string
		interpretation string
		wantCommands   [][]string
	}{
		{
			name:           "16-bit RGB",
			bands:          4,
			bandFormat:     "ushort",
			interpretation: "rgb16",
			wantCommands: [][]string{
				{"autorot", "/input/photo.bin", filepath.Join("/work", "oriented.v")},
				{"flatten", filepath.Join("/work", "oriented.v"), filepath.Join("/work", "pixels.v"), "--background", "65535"},
				{"jpegsave", filepath.Join("/work", "pixels.v"), filepath.Join("/work", "output.jpg"), "--Q", "90", "--optimize-coding", "--interlace", "--keep", "none"},
			},
		},
		{
			name:           "16-bit CMYK",
			bands:          5,
			bandFormat:     "ushort",
			interpretation: "cmyk",
			wantCommands: [][]string{
				{"autorot", "/input/photo.bin", filepath.Join("/work", "oriented.v")},
				{"flatten", filepath.Join("/work", "oriented.v"), filepath.Join("/work", "opaque.v"), "--background", "0,0,0,0", "--max-alpha", "65535"},
				{"icc_transform", filepath.Join("/work", "opaque.v"), filepath.Join("/work", "colour.v"), "srgb", "--embedded", "--depth", "8"},
				{"jpegsave", filepath.Join("/work", "colour.v"), filepath.Join("/work", "output.jpg"), "--Q", "90", "--optimize-coding", "--interlace", "--keep", "icc"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := imageconv.BuildPlan(imageconv.PlanRequest{
				InputPath: "/input/photo.bin",
				OutputDir: "/out",
				WorkDir:   "/work",
				Operation: corpus.OperationCompatiblePhoto,
				Input: imageconv.Info{
					Format:         imageconv.FormatTIFF,
					Width:          10,
					Height:         20,
					Bands:          tt.bands,
					BandFormat:     tt.bandFormat,
					Interpretation: tt.interpretation,
					Orientation:    1,
					HasAlpha:       true,
					Pages:          1,
					Metadata:       imageconv.Metadata{ICC: tt.interpretation == "cmyk"},
				},
				VipsPath: "/tools/vips",
			})
			if err != nil {
				t.Fatalf("BuildPlan() error = %v", err)
			}
			if len(plan.Commands) != len(tt.wantCommands) {
				t.Fatalf("commands = %d; want %d", len(plan.Commands), len(tt.wantCommands))
			}
			for index, command := range plan.Commands {
				if !reflect.DeepEqual(command.Args, tt.wantCommands[index]) {
					t.Errorf("command[%d] args = %#v; want %#v", index, command.Args, tt.wantCommands[index])
				}
			}
		})
	}
}

func TestBuildPlanRejectsUnsupportedCMYKAlphaPreservation(t *testing.T) {
	_, err := imageconv.BuildPlan(imageconv.PlanRequest{
		InputPath: "/input/photo.tif",
		OutputDir: "/out",
		WorkDir:   "/work",
		Operation: corpus.OperationLosslessImage,
		Input: imageconv.Info{
			Format:         imageconv.FormatTIFF,
			Width:          10,
			Height:         20,
			Bands:          5,
			BandFormat:     "ushort",
			Interpretation: "cmyk",
			Orientation:    1,
			HasAlpha:       true,
			Pages:          1,
			Metadata:       imageconv.Metadata{ICC: true},
		},
		VipsPath: "/tools/vips",
	})
	assertErrorCode(t, err, imageconv.CodeAlphaUnsupported)
}

func TestBuildPlanRejectsUnsupportedCMYKAlphaFlattening(t *testing.T) {
	_, err := imageconv.BuildPlan(imageconv.PlanRequest{
		InputPath: "/input/photo.tif",
		OutputDir: "/out",
		WorkDir:   "/work",
		Operation: corpus.OperationCompatiblePhoto,
		Input: imageconv.Info{
			Format:         imageconv.FormatTIFF,
			Width:          10,
			Height:         20,
			Bands:          5,
			BandFormat:     "float",
			Interpretation: "cmyk",
			Orientation:    1,
			HasAlpha:       true,
			Pages:          1,
			Metadata:       imageconv.Metadata{ICC: true},
		},
		VipsPath: "/tools/vips",
	})
	assertErrorCode(t, err, imageconv.CodeAlphaUnsupported)
}

func TestBuildPlanRejectsOversizedWebP(t *testing.T) {
	_, err := imageconv.BuildPlan(imageconv.PlanRequest{
		InputPath: "/input/panorama.jpg",
		OutputDir: "/out",
		WorkDir:   "/work",
		Operation: corpus.OperationSmallerPhoto,
		Input: imageconv.Info{
			Format:         imageconv.FormatJPEG,
			Width:          16_384,
			Height:         100,
			Bands:          3,
			BandFormat:     "uchar",
			Interpretation: "srgb",
			Orientation:    1,
			Pages:          1,
		},
		VipsPath: "/tools/vips",
	})
	assertErrorCode(t, err, imageconv.CodeDimensionsExceeded)
}

func TestBuildPlanNormalizesProfiledGrayscaleForWebP(t *testing.T) {
	plan, err := imageconv.BuildPlan(imageconv.PlanRequest{
		InputPath: "/input/photo.png",
		OutputDir: "/out",
		WorkDir:   "/work",
		Operation: corpus.OperationSmallerPhoto,
		Input: imageconv.Info{
			Format:         imageconv.FormatPNG,
			Width:          10,
			Height:         20,
			Bands:          1,
			BandFormat:     "uchar",
			Interpretation: "b-w",
			Orientation:    1,
			Pages:          1,
			Metadata:       imageconv.Metadata{ICC: true},
		},
		VipsPath: "/tools/vips",
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	want := []string{
		"icc_transform", filepath.Join("/work", "oriented.v"), filepath.Join("/work", "colour.v"),
		"srgb", "--embedded", "--depth", "8",
	}
	if !reflect.DeepEqual(plan.Commands[1].Args, want) {
		t.Errorf("grayscale normalization = %#v; want %#v", plan.Commands[1].Args, want)
	}
	if !plan.Expect.Metadata.ICC {
		t.Error("normalized grayscale output should require an sRGB profile")
	}
}

func TestTransparencyProbe(t *testing.T) {
	plan, err := imageconv.BuildTransparencyProbe("/tools/vips", "/input/photo.png", "/work", imageconv.Info{
		Bands:      4,
		BandFormat: "ushort",
		HasAlpha:   true,
	})
	if err != nil {
		t.Fatalf("BuildTransparencyProbe() error = %v", err)
	}
	wantCommands := [][]string{
		{"extract_band", "/input/photo.png", filepath.Join("/work", "alpha.v"), "3"},
		{"cast", filepath.Join("/work", "alpha.v"), filepath.Join("/work", "alpha-8.v"), "uchar", "--shift"},
		{"min", filepath.Join("/work", "alpha-8.v")},
	}
	for index, command := range plan.Commands {
		if !reflect.DeepEqual(command.Args, wantCommands[index]) {
			t.Errorf("command[%d] = %#v; want %#v", index, command.Args, wantCommands[index])
		}
	}
	if plan.MaxAlpha != 255 {
		t.Errorf("MaxAlpha = %v; want 255", plan.MaxAlpha)
	}

	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{name: "transparent", output: "128\n", want: true},
		{name: "opaque", output: "255\n", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, parseErr := imageconv.ParseTransparency([]byte(tt.output), plan.MaxAlpha)
			if parseErr != nil {
				t.Fatalf("ParseTransparency() error = %v", parseErr)
			}
			if got != tt.want {
				t.Errorf("ParseTransparency() = %t; want %t", got, tt.want)
			}
		})
	}
}

func TestValidateOutput(t *testing.T) {
	base := imageconv.Expectation{
		Format:      imageconv.FormatWebP,
		MIMEType:    "image/webp",
		Width:       800,
		Height:      1200,
		Orientation: 1,
		HasAlpha:    true,
		Metadata: imageconv.Metadata{
			ICC:         true,
			EXIF:        true,
			CaptureDate: true,
		},
	}
	valid := imageconv.Info{
		Format:         imageconv.FormatWebP,
		MIMEType:       "image/webp",
		Loader:         "webpload",
		Width:          800,
		Height:         1200,
		Bands:          4,
		Interpretation: "srgb",
		Orientation:    1,
		HasAlpha:       true,
		Pages:          1,
		Metadata: imageconv.Metadata{
			ICC:         true,
			EXIF:        true,
			CaptureDate: true,
		},
	}
	tests := []struct {
		name     string
		mutate   func(*imageconv.Info)
		wantCode string
	}{
		{name: "valid"},
		{
			name: "HDR output",
			mutate: func(info *imageconv.Info) {
				info.HDR = true
			},
			wantCode: imageconv.CodeOutputColor,
		},
		{
			name: "non-sRGB CICP output",
			mutate: func(info *imageconv.Info) {
				info.CICP = imageconv.CICP{
					Present:   true,
					Primaries: 9,
					Transfer:  13,
				}
			},
			wantCode: imageconv.CodeOutputColor,
		},
		{
			name: "wrong content format",
			mutate: func(info *imageconv.Info) {
				info.Format = imageconv.FormatJPEG
				info.MIMEType = "image/jpeg"
			},
			wantCode: imageconv.CodeOutputFormat,
		},
		{
			name: "wrong dimensions",
			mutate: func(info *imageconv.Info) {
				info.Width++
			},
			wantCode: imageconv.CodeOutputDimensions,
		},
		{
			name: "orientation not normalized",
			mutate: func(info *imageconv.Info) {
				info.Orientation = 6
			},
			wantCode: imageconv.CodeOutputOrientation,
		},
		{
			name: "alpha lost",
			mutate: func(info *imageconv.Info) {
				info.HasAlpha = false
			},
			wantCode: imageconv.CodeOutputAlpha,
		},
		{
			name: "GPS leaked",
			mutate: func(info *imageconv.Info) {
				info.Metadata.GPS = true
			},
			wantCode: imageconv.CodeOutputMetadata,
		},
		{
			name: "XMP leaked",
			mutate: func(info *imageconv.Info) {
				info.Metadata.XMP = true
			},
			wantCode: imageconv.CodeOutputMetadata,
		},
		{
			name: "gain map leaked",
			mutate: func(info *imageconv.Info) {
				info.Metadata.GainMap = true
			},
			wantCode: imageconv.CodeOutputMetadata,
		},
		{
			name: "ICC lost",
			mutate: func(info *imageconv.Info) {
				info.Metadata.ICC = false
			},
			wantCode: imageconv.CodeOutputMetadata,
		},
		{
			name: "capture date lost",
			mutate: func(info *imageconv.Info) {
				info.Metadata.CaptureDate = false
			},
			wantCode: imageconv.CodeOutputMetadata,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := valid
			if tt.mutate != nil {
				tt.mutate(&got)
			}
			err := imageconv.ValidateOutput(base, got)
			assertErrorCode(t, err, tt.wantCode)
		})
	}
}

func TestValidateOutputRejectsUnexpectedEXIF(t *testing.T) {
	expect := imageconv.Expectation{
		Format:      imageconv.FormatPNG,
		MIMEType:    "image/png",
		Width:       2,
		Height:      2,
		Orientation: 1,
	}
	observed := imageconv.Info{
		Format:      imageconv.FormatPNG,
		MIMEType:    "image/png",
		Width:       2,
		Height:      2,
		Orientation: 1,
		Pages:       1,
		Metadata:    imageconv.Metadata{EXIF: true},
	}
	assertErrorCode(t, imageconv.ValidateOutput(expect, observed), imageconv.CodeOutputMetadata)
}

func TestHEIFProbeSpec(t *testing.T) {
	spec, err := imageconv.HEIFProbeSpec("/custom/vips", "/fixtures/phone.heic", "/tmp/probe.v")
	if err != nil {
		t.Fatalf("HEIFProbeSpec() error = %v", err)
	}
	if spec.Name != "libvips HEIF decode" {
		t.Errorf("Name = %q; want libvips HEIF decode", spec.Name)
	}
	if spec.Executable != "/custom/vips" {
		t.Errorf("Executable = %q; want /custom/vips", spec.Executable)
	}
	wantArgs := []string{"copy", "/fixtures/phone.heic", "/tmp/probe.v"}
	if !reflect.DeepEqual(spec.Args, wantArgs) {
		t.Errorf("Args = %#v; want %#v", spec.Args, wantArgs)
	}
}

func TestProbeHEIF(t *testing.T) {
	tests := []struct {
		name    string
		runErr  error
		want    bool
		wantErr bool
	}{
		{name: "functional decode", want: true},
		{name: "decode unavailable", runErr: errors.New("decode failed"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := func(_ context.Context, command runner.Command) (runner.Result, error) {
				if command.Path != "/tools/vips" {
					t.Errorf("Path = %q; want /tools/vips", command.Path)
				}
				return runner.Result{ExitCode: 0}, tt.runErr
			}
			prober, err := probe.New(run, probe.WithLookPath(func(string) (string, error) {
				return "/tools/vips", nil
			}))
			if err != nil {
				t.Fatalf("probe.New() error = %v", err)
			}

			tempDir := t.TempDir()
			got, err := imageconv.ProbeHEIF(context.Background(), prober, "/tools/vips", "/fixtures/phone.heic", tempDir)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ProbeHEIF() error = %v; wantErr %t", err, tt.wantErr)
			}
			if got.Has(imageconv.CapabilityDecodeHEIF) != tt.want {
				t.Errorf("HEIF capability = %t; want %t", got.Has(imageconv.CapabilityDecodeHEIF), tt.want)
			}
		})
	}
}

func TestProbeAVIFSetsIndependentCapability(t *testing.T) {
	run := func(_ context.Context, command runner.Command) (runner.Result, error) {
		if command.Path != "/tools/vips-avif" {
			t.Errorf("Path = %q; want exact probed executable", command.Path)
		}
		return runner.Result{ExitCode: 0}, nil
	}
	prober, err := probe.New(run, probe.WithLookPath(func(name string) (string, error) {
		return name, nil
	}))
	if err != nil {
		t.Fatalf("probe.New() error = %v", err)
	}

	got, err := imageconv.ProbeAVIF(context.Background(), prober, "/tools/vips-avif", "/fixtures/photo.avif", t.TempDir())
	if err != nil {
		t.Fatalf("ProbeAVIF() error = %v", err)
	}
	if !got.Has(imageconv.CapabilityDecodeAVIF) || got.Has(imageconv.CapabilityDecodeHEIF) {
		t.Errorf("capabilities = %#v; want only AVIF decode", got)
	}
}

func TestConvertRejectsMediaProfileBeforeOutputCheck(t *testing.T) {
	var commands []runner.Command
	run := func(_ context.Context, command runner.Command) (runner.Result, error) {
		commands = append(commands, command)
		return runner.Result{
			ExitCode: 0,
			Stdout: runner.Output{Bytes: []byte(strings.Join([]string{
				"width: 3",
				"height: 2",
				"bands: 3",
				"format: uchar",
				"interpretation: srgb",
				"vips-loader: pngload",
			}, "\n"))},
		}, nil
	}
	prober, err := probe.New(run, probe.WithLookPath(func(name string) (string, error) {
		return filepath.Join("/tools", name), nil
	}))
	if err != nil {
		t.Fatalf("probe.New() error = %v", err)
	}
	converter, err := imageconv.New(run, prober, imageconv.Config{})
	if err != nil {
		t.Fatalf("imageconv.New() error = %v", err)
	}

	input := filepath.Join(t.TempDir(), "source.png")
	outputDir := t.TempDir()
	existing := filepath.Join(outputDir, "source-compatible.mp4")
	if err := os.WriteFile(existing, []byte("existing media"), 0o600); err != nil {
		t.Fatalf("write existing output: %v", err)
	}

	_, err = converter.Convert(context.Background(), imageconv.Request{
		InputPath: input,
		OutputDir: outputDir,
		Operation: profiles.OperationCompatibleVideo,
		Input: imageconv.Info{
			Format: imageconv.FormatPNG, MIMEType: "image/png",
			Width: 3, Height: 2, Bands: 3, BandFormat: "uchar",
			Interpretation: "srgb", Orientation: 1, Pages: 1,
		},
	})
	assertErrorCode(t, err, imageconv.CodeInvalidRequest)
	if len(commands) != 0 {
		t.Errorf("commands = %d; want profile rejection before subprocesses", len(commands))
	}
}

func TestConvertRunsPlanAndValidatesOutput(t *testing.T) {
	var commands []runner.Command
	run := func(_ context.Context, command runner.Command) (runner.Result, error) {
		commands = append(commands, command)
		if filepath.Base(command.Path) == "vipsheader" {
			format := "jpegload"
			if len(commands) == 1 {
				format = "pngload"
			}
			return runner.Result{
				ExitCode: 0,
				Stdout: runner.Output{Bytes: []byte(strings.Join([]string{
					"width: 3",
					"height: 2",
					"bands: 3",
					"format: uchar",
					"interpretation: srgb",
					"vips-loader: " + format,
				}, "\n"))},
			}, nil
		}
		return runner.Result{ExitCode: 0}, nil
	}
	prober, err := probe.New(run, probe.WithLookPath(func(name string) (string, error) {
		return filepath.Join("/tools", name), nil
	}))
	if err != nil {
		t.Fatalf("probe.New() error = %v", err)
	}
	converter, err := imageconv.New(run, prober, imageconv.Config{
		Limits: imageconv.DefaultLimits(),
	})
	if err != nil {
		t.Fatalf("imageconv.New() error = %v", err)
	}

	input := filepath.Join(t.TempDir(), "source.bin")
	outputDir := t.TempDir()
	result, err := converter.Convert(context.Background(), imageconv.Request{
		InputPath: input,
		OutputDir: outputDir,
		Operation: corpus.OperationCompatiblePhoto,
		Input: imageconv.Info{
			Format: imageconv.FormatPNG, MIMEType: "image/png",
			Width: 3, Height: 2, Bands: 3, BandFormat: "uchar",
			Interpretation: "srgb", Orientation: 1, Pages: 1,
		},
	})
	if err == nil {
		t.Fatal("Convert() error = nil; mock did not create output and should fail publication")
	}
	if result.Output.MIMEType != "image/jpeg" || result.Output.Extension != ".jpg" {
		t.Errorf("declared output = %#v; want JPEG", result.Output)
	}
	if len(commands) != 3 {
		t.Fatalf("commands = %d; want exactly autorot, save, and output probe", len(commands))
	}
	for _, command := range commands {
		switch filepath.Base(command.Path) {
		case "sh", "bash", "zsh":
			t.Errorf("command path %q invokes a shell", command.Path)
		case "vipsheader":
			if command.Args[len(command.Args)-1] == input {
				t.Error("converter redundantly probed the supplied input")
			}
		}
	}
}

func TestConvertRejectsMissingInputInfoWithoutRunningCommands(t *testing.T) {
	runCalls := 0
	run := func(context.Context, runner.Command) (runner.Result, error) {
		runCalls++
		return runner.Result{}, errors.New("command must not run")
	}
	prober, err := probe.New(run, probe.WithLookPath(func(name string) (string, error) {
		return filepath.Join("/tools", name), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	converter, err := imageconv.New(run, prober, imageconv.Config{})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		info imageconv.Info
	}{
		{name: "empty"},
		{
			name: "incomplete dimensions",
			info: imageconv.Info{
				Format: imageconv.FormatPNG, Width: 2, Bands: 3,
				BandFormat: "uchar", Interpretation: "srgb", Pages: 1,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := converter.Convert(context.Background(), imageconv.Request{
				InputPath: "input.png",
				OutputDir: t.TempDir(),
				Operation: corpus.OperationCompatiblePhoto,
				Input:     tt.info,
			})
			assertErrorCode(t, err, imageconv.CodeInvalidRequest)
		})
	}
	if runCalls != 0 {
		t.Errorf("commands = %d; want zero for invalid supplied Info", runCalls)
	}
}

func TestConvertProbesTransparencyBeforeWebPSave(t *testing.T) {
	input := filepath.Join(t.TempDir(), "source.bin")
	if err := os.WriteFile(input, []byte("mock image"), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	outputDir := t.TempDir()

	var commands []runner.Command
	run := func(_ context.Context, command runner.Command) (runner.Result, error) {
		commands = append(commands, command)
		switch filepath.Base(command.Path) {
		case "vipsheader":
			loader := "pngload"
			if command.Args[len(command.Args)-1] != input {
				loader = "webpload"
			}
			return runner.Result{
				ExitCode: 0,
				Stdout: runner.Output{Bytes: []byte(strings.Join([]string{
					"width: 3",
					"height: 2",
					"bands: 4",
					"format: uchar",
					"interpretation: srgb",
					"has-alpha: true",
					"vips-loader: " + loader,
				}, "\n"))},
			}, nil
		case "vips":
			switch command.Args[0] {
			case "min":
				return runner.Result{ExitCode: 0, Stdout: runner.Output{Bytes: []byte("128\n")}}, nil
			case "webpsave":
				if err := os.WriteFile(command.Args[2], []byte("mock webp"), 0o600); err != nil {
					return runner.Result{}, err
				}
			}
			return runner.Result{ExitCode: 0}, nil
		default:
			return runner.Result{}, errors.New("unexpected executable")
		}
	}
	prober, err := probe.New(run, probe.WithLookPath(func(name string) (string, error) {
		return filepath.Join("/tools", name), nil
	}))
	if err != nil {
		t.Fatalf("probe.New() error = %v", err)
	}
	converter, err := imageconv.New(run, prober, imageconv.Config{})
	if err != nil {
		t.Fatalf("imageconv.New() error = %v", err)
	}

	result, err := converter.Convert(context.Background(), imageconv.Request{
		InputPath: input,
		OutputDir: outputDir,
		Operation: corpus.OperationSmallerPhoto,
		Input: imageconv.Info{
			Format: imageconv.FormatPNG, MIMEType: "image/png",
			Width: 3, Height: 2, Bands: 4, BandFormat: "uchar",
			Interpretation: "srgb", Orientation: 1, HasAlpha: true, Pages: 1,
		},
	})
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if !result.Input.HasTransparency {
		t.Error("input transparency was not recorded")
	}
	var sawExtract, sawMinimum bool
	for _, command := range commands {
		if filepath.Base(command.Path) != "vips" || len(command.Args) == 0 {
			continue
		}
		sawExtract = sawExtract || command.Args[0] == "extract_band"
		sawMinimum = sawMinimum || command.Args[0] == "min"
	}
	if !sawExtract || !sawMinimum {
		t.Errorf("transparency commands missing: extract=%t min=%t", sawExtract, sawMinimum)
	}
}

func TestConvertPublishesValidatedOutput(t *testing.T) {
	input := filepath.Join(t.TempDir(), "source.untrusted")
	if err := os.WriteFile(input, []byte("mock image"), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	outputDir := t.TempDir()

	var commands []runner.Command
	run := func(_ context.Context, command runner.Command) (runner.Result, error) {
		commands = append(commands, command)
		switch filepath.Base(command.Path) {
		case "vipsheader":
			loader := "pngload"
			if command.Args[len(command.Args)-1] != input {
				loader = "jpegload"
			}
			return runner.Result{
				ExitCode: 0,
				Stdout: runner.Output{Bytes: []byte(strings.Join([]string{
					"width: 3",
					"height: 2",
					"bands: 3",
					"format: uchar",
					"interpretation: srgb",
					"vips-loader: " + loader,
				}, "\n"))},
			}, nil
		case "vips":
			if len(command.Args) > 0 && command.Args[0] == "jpegsave" {
				if err := os.WriteFile(command.Args[2], []byte("mock jpeg"), 0o600); err != nil {
					return runner.Result{}, err
				}
			}
			return runner.Result{ExitCode: 0}, nil
		default:
			return runner.Result{}, errors.New("unexpected executable")
		}
	}
	prober, err := probe.New(run, probe.WithLookPath(func(name string) (string, error) {
		return filepath.Join("/tools", name), nil
	}))
	if err != nil {
		t.Fatalf("probe.New() error = %v", err)
	}
	converter, err := imageconv.New(run, prober, imageconv.Config{})
	if err != nil {
		t.Fatalf("imageconv.New() error = %v", err)
	}

	result, err := converter.Convert(context.Background(), imageconv.Request{
		InputPath: input,
		OutputDir: outputDir,
		Operation: corpus.OperationCompatiblePhoto,
		Input: imageconv.Info{
			Format: imageconv.FormatPNG, MIMEType: "image/png",
			Width: 3, Height: 2, Bands: 3, BandFormat: "uchar",
			Interpretation: "srgb", Orientation: 1, Pages: 1,
		},
	})
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if result.Output.Path != filepath.Join(outputDir, "source-compatible.jpg") {
		t.Errorf("output path = %q; want deterministic path", result.Output.Path)
	}
	if _, err := os.Stat(result.Output.Path); err != nil {
		t.Fatalf("published output: %v", err)
	}
	if result.Observed.Format != imageconv.FormatJPEG {
		t.Errorf("observed format = %q; want JPEG", result.Observed.Format)
	}
	if len(result.Commands) != 2 {
		t.Errorf("conversion command results = %d; want 2", len(result.Commands))
	}
}

func TestConvertDoesNotReplaceConcurrentOutput(t *testing.T) {
	input := filepath.Join(t.TempDir(), "source.bin")
	if err := os.WriteFile(input, []byte("mock image"), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	outputDir := t.TempDir()
	finalPath := filepath.Join(outputDir, "source-compatible.jpg")

	run := func(_ context.Context, command runner.Command) (runner.Result, error) {
		switch filepath.Base(command.Path) {
		case "vipsheader":
			loader := "pngload"
			if command.Args[len(command.Args)-1] != input {
				loader = "jpegload"
			}
			return runner.Result{
				ExitCode: 0,
				Stdout: runner.Output{Bytes: []byte(strings.Join([]string{
					"width: 3",
					"height: 2",
					"bands: 3",
					"format: uchar",
					"interpretation: srgb",
					"vips-loader: " + loader,
				}, "\n"))},
			}, nil
		case "vips":
			if len(command.Args) > 0 && command.Args[0] == "jpegsave" {
				if err := os.WriteFile(command.Args[2], []byte("conversion"), 0o600); err != nil {
					return runner.Result{}, err
				}
				if err := os.WriteFile(finalPath, []byte("concurrent output"), 0o600); err != nil {
					return runner.Result{}, err
				}
			}
			return runner.Result{ExitCode: 0}, nil
		default:
			return runner.Result{}, errors.New("unexpected executable")
		}
	}
	prober, err := probe.New(run, probe.WithLookPath(func(name string) (string, error) {
		return filepath.Join("/tools", name), nil
	}))
	if err != nil {
		t.Fatalf("probe.New() error = %v", err)
	}
	converter, err := imageconv.New(run, prober, imageconv.Config{})
	if err != nil {
		t.Fatalf("imageconv.New() error = %v", err)
	}

	_, err = converter.Convert(context.Background(), imageconv.Request{
		InputPath: input,
		OutputDir: outputDir,
		Operation: corpus.OperationCompatiblePhoto,
		Input: imageconv.Info{
			Format: imageconv.FormatPNG, MIMEType: "image/png",
			Width: 3, Height: 2, Bands: 3, BandFormat: "uchar",
			Interpretation: "srgb", Orientation: 1, Pages: 1,
		},
	})
	assertErrorCode(t, err, imageconv.CodeOutputExists)
	data, readErr := os.ReadFile(finalPath)
	if readErr != nil {
		t.Fatalf("read concurrent output: %v", readErr)
	}
	if string(data) != "concurrent output" {
		t.Errorf("final output = %q; want concurrent file preserved", data)
	}
}

func assertErrorCode(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("error = nil; want code %q", want)
	}
	var imageErr *imageconv.Error
	if !errors.As(err, &imageErr) {
		t.Fatalf("error type = %T; want *imageconv.Error", err)
	}
	if imageErr.Code != want {
		t.Fatalf("error code = %q; want %q", imageErr.Code, want)
	}
}

func withoutGPS(info imageconv.Info) imageconv.Info {
	info.Metadata.GPS = false
	return info
}
