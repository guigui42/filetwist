package web_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/imageconv"
	"github.com/guigui42/filetwist/internal/jobs/storage"
	"github.com/guigui42/filetwist/internal/media"
)

type eligibilityImageEngine struct{ info imageconv.Info }

func (engine eligibilityImageEngine) Probe(context.Context, string) (imageconv.Info, error) {
	if engine.info.Format == "" {
		return imageconv.Info{}, errors.New("not an image")
	}
	return engine.info, nil
}

func (eligibilityImageEngine) Convert(context.Context, conversion.ImageRequest) (imageconv.Result, error) {
	return imageconv.Result{}, errors.New("conversion must not run")
}

type eligibilityMediaEngine struct{ input media.Probe }

func (engine eligibilityMediaEngine) Probe(context.Context, string) (media.Probe, error) {
	return engine.input, nil
}

func (eligibilityMediaEngine) Convert(context.Context, conversion.MediaRequest) (conversion.MediaResult, error) {
	return conversion.MediaResult{}, errors.New("conversion must not run")
}

func TestUploadRendersAndEnforcesDetailedEligibility(t *testing.T) {
	video := media.Stream{Index: 0, CodecType: "video", CodecName: "h264", Width: 640, Height: 480}
	audio := media.Stream{Index: 1, CodecType: "audio", CodecName: "pcm_s16le", Channels: 2, SampleRate: "48000"}
	unsupported := audio
	unsupported.CodecName = "apac"
	floating := audio
	floating.CodecName = "pcm_f32le"
	highPrecision := audio
	highPrecision.CodecName, highPrecision.BitsPerRawSample = "pcm_s32le", "32"
	hdr := video
	hdr.ColorTransfer = "smpte2084"
	wide := imageconv.Info{
		Format: imageconv.FormatPNG, Width: 16384, Height: 2, Pages: 1,
		Bands: 3, BandFormat: "uchar", Interpretation: "srgb",
	}
	animated := wide
	animated.Pages = 2
	tests := []struct {
		name        string
		streams     []media.Stream
		image       imageconv.Info
		want        []corpus.Operation
		recommended corpus.Operation
		rejected    corpus.Operation
		code        string
	}{
		{"silent video", []media.Stream{video}, imageconv.Info{}, []corpus.Operation{corpus.OperationCompatibleVideo, corpus.OperationSmallerVideo}, corpus.OperationCompatibleVideo, corpus.OperationExtractAudio, ""},
		{"unsupported video audio", []media.Stream{video, unsupported}, imageconv.Info{}, []corpus.Operation{corpus.OperationCompatibleVideo, corpus.OperationSmallerVideo}, corpus.OperationCompatibleVideo, corpus.OperationExtractAudio, ""},
		{"floating-point audio", []media.Stream{floating}, imageconv.Info{}, []corpus.Operation{corpus.OperationExtractAudio, corpus.OperationCompatibleAudio}, corpus.OperationCompatibleAudio, corpus.OperationLosslessAudio, ""},
		{"high-precision audio", []media.Stream{highPrecision}, imageconv.Info{}, []corpus.Operation{corpus.OperationExtractAudio, corpus.OperationCompatibleAudio}, corpus.OperationCompatibleAudio, corpus.OperationLosslessAudio, ""},
		{"normal audio", []media.Stream{audio}, imageconv.Info{}, conversion.CompatibleOperations(corpus.MediaAudio), corpus.OperationCompatibleAudio, corpus.OperationCompatibleVideo, ""},
		{"unsupported HDR defaults to audio", []media.Stream{hdr, audio}, imageconv.Info{}, []corpus.Operation{corpus.OperationExtractAudio}, corpus.OperationExtractAudio, corpus.OperationCompatibleVideo, ""},
		{"wide image", nil, wide, []corpus.Operation{corpus.OperationCompatiblePhoto, corpus.OperationLosslessImage}, corpus.OperationCompatiblePhoto, corpus.OperationSmallerPhoto, ""},
		{"unsupported audio", []media.Stream{unsupported}, imageconv.Info{}, nil, "", corpus.OperationCompatibleAudio, string(media.ErrorNoSupportedAudio)},
		{"unsupported image", nil, animated, nil, "", corpus.OperationCompatiblePhoto, imageconv.CodeAnimatedUnsupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := conversion.New(eligibilityImageEngine{tt.image}, eligibilityMediaEngine{
				media.Probe{Format: media.Format{FormatName: "matroska"}, Streams: tt.streams},
			})
			if err != nil {
				t.Fatal(err)
			}
			server := newServer(t, nil, service)
			response := server.do(t, uploadRequest(t, "/jobs", []string{"input.bin"}))
			if response.Code != http.StatusOK {
				t.Fatalf("upload status = %d: %s", response.Code, response.Body.String())
			}
			manifests, err := server.manager.List()
			if err != nil || len(manifests) != 1 {
				t.Fatalf("List = %v, %v", manifests, err)
			}
			manifest := manifests[0]
			file := manifest.Files[0]
			if !slices.Equal(file.Compatible, tt.want) || file.Recommended != tt.recommended || file.Selected != tt.recommended {
				t.Fatalf("persisted eligibility = %v, recommended %q, selected %q", file.Compatible, file.Recommended, file.Selected)
			}
			for _, operation := range conversion.AllOperations() {
				rendered := strings.Contains(response.Body.String(), `<option value="`+string(operation)+`"`)
				if rendered != slices.Contains(tt.want, operation) {
					t.Errorf("rendered %s = %t; eligible = %v", operation, rendered, tt.want)
				}
			}
			if tt.code != "" {
				if file.State != storage.FileFailed || file.Error == nil || file.Error.Code != tt.code {
					t.Fatalf("rejected file = %+v; want %s", file, tt.code)
				}
				if strings.Contains(response.Body.String(), `type="submit"`) {
					t.Error("a rejected upload offers conversion")
				}
			} else {
				if file.State != storage.FileInspected {
					t.Fatalf("file state = %s; want inspected", file.State)
				}
				if !strings.Contains(response.Body.String(), `<option value="`+string(tt.recommended)+`" selected>`) {
					t.Error("eligible recommendation is not selected in the form")
				}
			}
			start := httptest.NewRequest(http.MethodPost, "/jobs/"+manifest.ID+"/start",
				strings.NewReader("operation."+file.ID+"="+string(tt.rejected)))
			start.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if response := server.do(t, start); response.Code != http.StatusBadRequest {
				t.Fatalf("ineligible selection status = %d: %s", response.Code, response.Body.String())
			}
			persisted, err := server.manager.Get(manifest.ID)
			if err != nil || persisted.State != storage.JobPending {
				t.Fatalf("rejected selection changed job state: %s, %v", persisted.State, err)
			}
		})
	}
}
