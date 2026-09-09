package conversion_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/imageconv"
	"github.com/guigui42/filetwist/internal/media"
	"github.com/guigui42/filetwist/internal/profiles"
)

// inspectImageEngine reports one still image and never converts.
type inspectImageEngine struct {
	info imageconv.Info
	err  error
}

func (engine *inspectImageEngine) Probe(context.Context, string) (imageconv.Info, error) {
	return engine.info, engine.err
}

func (engine *inspectImageEngine) Convert(
	context.Context,
	conversion.ImageRequest,
) (imageconv.Result, error) {
	return imageconv.Result{}, errors.New("convert must not run during inspection")
}

// inspectMediaEngine reports one probed media file and never converts.
type inspectMediaEngine struct {
	probe media.Probe
	err   error
}

func (engine *inspectMediaEngine) Probe(context.Context, string) (media.Probe, error) {
	return engine.probe, engine.err
}

func (engine *inspectMediaEngine) Convert(
	context.Context,
	conversion.MediaRequest,
) (conversion.MediaResult, error) {
	return conversion.MediaResult{}, errors.New("convert must not run during inspection")
}

func writeTemporaryFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.bin")
	if err := os.WriteFile(path, []byte("bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestInspectRecommendsAndListsAlternativesForImages(t *testing.T) {
	service, err := conversion.New(
		&inspectImageEngine{info: imageconv.Info{
			Format:         imageconv.FormatJPEG,
			MIMEType:       "image/jpeg",
			Width:          4032,
			Height:         3024,
			Bands:          3,
			BandFormat:     "uchar",
			Interpretation: "srgb",
			Pages:          1,
		}},
		&inspectMediaEngine{err: errors.New("media probe must not decide")},
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	inspection, err := service.Inspect(context.Background(), writeTemporaryFile(t))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if inspection.Media.Kind != corpus.MediaImage {
		t.Fatalf("kind = %q; want image", inspection.Media.Kind)
	}
	if inspection.Media.Width != 4032 || inspection.Media.Height != 3024 {
		t.Fatalf("dimensions = %dx%d", inspection.Media.Width, inspection.Media.Height)
	}
	if inspection.Recommended != corpus.OperationCompatiblePhoto {
		t.Fatalf("recommended = %q", inspection.Recommended)
	}
	for _, operation := range inspection.Compatible {
		spec, ok := profiles.Lookup(operation)
		if !ok || !spec.Accepts(profiles.MediaImage) {
			t.Fatalf("offered incompatible operation %q", operation)
		}
	}
}

func TestInspectRecommendsCompatibleVideo(t *testing.T) {
	service, err := conversion.New(
		&inspectImageEngine{err: errors.New("not an image")},
		&inspectMediaEngine{probe: media.Probe{
			Format: media.Format{FormatName: "mov,mp4,m4a"},
			Streams: []media.Stream{
				{Index: 0, CodecType: "video", CodecName: "h264", Width: 1920, Height: 1080},
				{Index: 1, CodecType: "audio", CodecName: "aac", Channels: 2, SampleRate: "48000"},
			},
		}},
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	inspection, err := service.Inspect(context.Background(), writeTemporaryFile(t))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if inspection.Media.Kind != corpus.MediaVideo {
		t.Fatalf("kind = %q; want video", inspection.Media.Kind)
	}
	if inspection.Recommended != corpus.OperationCompatibleVideo {
		t.Fatalf("recommended = %q", inspection.Recommended)
	}
	if len(inspection.Media.Streams) != 2 {
		t.Fatalf("streams = %d; want 2", len(inspection.Media.Streams))
	}
	for _, operation := range []corpus.Operation{
		corpus.OperationCompatiblePhoto,
		corpus.OperationCompatibleAudio,
		corpus.OperationLosslessAudio,
	} {
		for _, offered := range inspection.Compatible {
			if offered == operation {
				t.Fatalf("video input offered %q", operation)
			}
		}
	}
}

func TestInspectRecommendsCompatibleAudio(t *testing.T) {
	service, err := conversion.New(
		&inspectImageEngine{err: errors.New("not an image")},
		&inspectMediaEngine{probe: media.Probe{
			Format: media.Format{FormatName: "wav"},
			Streams: []media.Stream{
				{Index: 0, CodecType: "audio", CodecName: "pcm_s16le", Channels: 1, SampleRate: "8000"},
			},
		}},
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	inspection, err := service.Inspect(context.Background(), writeTemporaryFile(t))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if inspection.Media.Kind != corpus.MediaAudio {
		t.Fatalf("kind = %q; want audio", inspection.Media.Kind)
	}
	if inspection.Recommended != corpus.OperationCompatibleAudio {
		t.Fatalf("recommended = %q", inspection.Recommended)
	}
}

func TestInspectFiltersOperationsUsingMediaDetails(t *testing.T) {
	video := videoProbe().Streams[0]
	audio := audioProbe().Streams[0]
	unsupported := audio
	unsupported.CodecName = "dts"
	hdr := video
	hdr.ColorTransfer = "smpte2084"
	hdr.ColorPrimaries = "unknown"
	supportedHDR := hdr
	supportedHDR.ColorPrimaries = "bt2020"
	supportedHDR.ColorSpace = "bt2020nc"
	unusableVideo := video
	unusableVideo.Width = 0
	floating := audio
	floating.CodecName = "pcm_f32le"

	type testCase struct {
		name        string
		streams     []media.Stream
		want        []corpus.Operation
		recommended corpus.Operation
		code        media.ErrorCode
	}
	tests := []testCase{
		{"video with audio", []media.Stream{video, audio}, compatibleOperations(profiles.MediaVideo), corpus.OperationCompatibleVideo, ""},
		{"silent video", []media.Stream{video}, []corpus.Operation{corpus.OperationCompatibleVideo, corpus.OperationSmallerVideo}, corpus.OperationCompatibleVideo, ""},
		{"video unsupported audio", []media.Stream{video, unsupported}, []corpus.Operation{corpus.OperationCompatibleVideo, corpus.OperationSmallerVideo}, corpus.OperationCompatibleVideo, ""},
		{"video skips unsupported audio", []media.Stream{video, unsupported, audio}, compatibleOperations(profiles.MediaVideo), corpus.OperationCompatibleVideo, ""},
		{"supported HDR", []media.Stream{supportedHDR, audio}, compatibleOperations(profiles.MediaVideo), corpus.OperationCompatibleVideo, ""},
		{"unsupported HDR with audio", []media.Stream{hdr, audio}, []corpus.Operation{corpus.OperationExtractAudio}, corpus.OperationExtractAudio, ""},
		{"unsupported silent HDR", []media.Stream{hdr}, nil, "", media.ErrorUnsupportedHDR},
		{"unusable video with audio", []media.Stream{unusableVideo, audio}, []corpus.Operation{corpus.OperationExtractAudio}, corpus.OperationExtractAudio, ""},
		{"unusable silent video", []media.Stream{unusableVideo}, nil, "", media.ErrorNoUsableVideo},
		{"audio", []media.Stream{audio}, compatibleOperations(profiles.MediaAudio), corpus.OperationCompatibleAudio, ""},
		{"first audio is float", []media.Stream{floating, audio}, []corpus.Operation{corpus.OperationExtractAudio, corpus.OperationCompatibleAudio}, corpus.OperationCompatibleAudio, ""},
		{"later float audio is ignored", []media.Stream{audio, floating}, compatibleOperations(profiles.MediaAudio), corpus.OperationCompatibleAudio, ""},
		{"unsupported audio", []media.Stream{unsupported}, nil, "", media.ErrorNoSupportedAudio},
	}
	for _, precision := range []struct {
		name, codec, bits string
		lossless          bool
	}{
		{"float32 PCM", "pcm_f32le", "", false},
		{"float64 PCM", "pcm_f64le", "", false},
		{"32-bit PCM", "pcm_s32le", "32", false},
		{"32-bit PCM unknown depth", "pcm_s32le", "", false},
		{"64-bit PCM", "pcm_s64le", "", false},
		{"24-bit PCM", "pcm_s24le", "24", true},
		{"24-bit in 32-bit PCM", "pcm_s32le", "24", true},
		{"high-precision ALAC", "alac", "32", false},
		{"AAC float decoder samples", "aac", "", true},
	} {
		stream := audio
		stream.CodecName = precision.codec
		stream.BitsPerRawSample = precision.bits
		if precision.codec == "aac" {
			stream.SampleFormat = "fltp"
		}
		want := []corpus.Operation{corpus.OperationExtractAudio, corpus.OperationCompatibleAudio}
		if precision.lossless {
			want = append(want, corpus.OperationLosslessAudio)
		}
		tests = append(tests, testCase{precision.name, []media.Stream{stream}, want, corpus.OperationCompatibleAudio, ""})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := media.Probe{Format: media.Format{FormatName: "matroska"}, Streams: tt.streams}
			engine := &fakeMediaEngine{probeResult: input}
			image := &fakeImageEngine{probeErr: errors.New("not an image")}
			service, err := conversion.New(image, engine)
			if err != nil {
				t.Fatal(err)
			}
			path := writeTemporaryFile(t)
			inspection, err := service.Inspect(context.Background(), path)
			if tt.code != "" {
				var rejected *conversion.Error
				if !errors.As(err, &rejected) || rejected.Kind != conversion.FailureRejection || rejected.Code != string(tt.code) {
					t.Fatalf("Inspect error = %v; want rejection %s", err, tt.code)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(inspection.Compatible, tt.want) {
					t.Errorf("compatible = %v; want %v", inspection.Compatible, tt.want)
				}
				if inspection.Recommended != tt.recommended || !slices.Contains(inspection.Compatible, inspection.Recommended) {
					t.Errorf("recommended = %q; want eligible %q", inspection.Recommended, tt.recommended)
				}
			}
			if engine.convertCalls != 0 || image.convertCalls != 0 {
				t.Fatal("inspection ran a conversion")
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil || len(entries) != 1 {
				t.Fatalf("inspection changed input directory: %v, %v", entries, err)
			}
		})
	}
}

func TestInspectFiltersOperationsUsingImageDetails(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*imageconv.Info)
		want   []corpus.Operation
		code   string
	}{
		{"normal image", func(*imageconv.Info) {}, compatibleOperations(profiles.MediaImage), ""},
		{"too wide for WebP", func(info *imageconv.Info) { info.Width = 16384; info.Height = 2 }, []corpus.Operation{corpus.OperationCompatiblePhoto, corpus.OperationLosslessImage}, ""},
		{"16-bit RGB", func(info *imageconv.Info) { info.BandFormat = "ushort"; info.Interpretation = "rgb16" }, compatibleOperations(profiles.MediaImage), ""},
		{"16-bit alpha requiring color conversion", func(info *imageconv.Info) {
			info.BandFormat, info.Interpretation = "ushort", "grey16"
			info.Bands, info.HasAlpha, info.Metadata.ICC = 2, true, true
		}, []corpus.Operation{corpus.OperationCompatiblePhoto}, ""},
		{"animated", func(info *imageconv.Info) { info.Pages = 2 }, nil, imageconv.CodeAnimatedUnsupported},
		{"HDR", func(info *imageconv.Info) { info.HDR = true }, nil, imageconv.CodeHDRUnsupported},
		{"oversized", func(info *imageconv.Info) { info.Width = 32769 }, nil, imageconv.CodeDimensionsExceeded},
		{"unsupported precision", func(info *imageconv.Info) { info.BandFormat = "float" }, nil, imageconv.CodeColorUnsupported},
		{"HEIF eligibility defers functional availability", func(info *imageconv.Info) { info.Format = imageconv.FormatHEIF }, compatibleOperations(profiles.MediaImage), ""},
		{"AVIF eligibility defers functional availability", func(info *imageconv.Info) { info.Format = imageconv.FormatAVIF }, compatibleOperations(profiles.MediaImage), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := imageInfo()
			tt.mutate(&info)
			image := &fakeImageEngine{probeResult: info}
			service, err := conversion.New(image, &fakeMediaEngine{probeErr: errors.New("not media")})
			if err != nil {
				t.Fatal(err)
			}
			inspection, err := service.Inspect(context.Background(), writeTemporaryFile(t))
			if tt.code != "" {
				var rejected *conversion.Error
				if !errors.As(err, &rejected) || rejected.Kind != conversion.FailureRejection || rejected.Code != tt.code {
					t.Fatalf("Inspect error = %v; want rejection %s", err, tt.code)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(inspection.Compatible, tt.want) {
					t.Errorf("compatible = %v; want %v", inspection.Compatible, tt.want)
				}
				if !slices.Contains(inspection.Compatible, inspection.Recommended) {
					t.Errorf("recommended %q is not compatible", inspection.Recommended)
				}
			}
			if image.convertCalls != 0 {
				t.Fatal("inspection ran a conversion")
			}
		})
	}
}

func TestInspectReportsProbeFailures(t *testing.T) {
	service, err := conversion.New(
		&inspectImageEngine{err: errors.New("not an image")},
		&inspectMediaEngine{err: errors.New("not media")},
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, inspectErr := service.Inspect(context.Background(), writeTemporaryFile(t))
	var classified *conversion.Error
	if !errors.As(inspectErr, &classified) {
		t.Fatalf("err = %v; want a classified conversion error", inspectErr)
	}
	if classified.Kind != conversion.FailureProbe {
		t.Fatalf("kind = %q; want probe", classified.Kind)
	}
}

func TestInspectRejectsMissingInput(t *testing.T) {
	service, err := conversion.New(&inspectImageEngine{}, &inspectMediaEngine{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := service.Inspect(context.Background(), ""); err == nil {
		t.Fatal("Inspect(\"\") = nil; want rejection")
	}
	if _, err := service.Inspect(context.Background(), filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("Inspect(absent) = nil; want rejection")
	}
	if _, err := service.Inspect(nil, writeTemporaryFile(t)); err == nil { //nolint:staticcheck // nil context is the case under test
		t.Fatal("Inspect(nil ctx) = nil; want rejection")
	}
}

func compatibleOperations(kind profiles.MediaKind) []profiles.Operation {
	specs := profiles.Compatible(kind)
	operations := make([]profiles.Operation, 0, len(specs))
	for _, spec := range specs {
		operations = append(operations, spec.Operation)
	}
	return operations
}
