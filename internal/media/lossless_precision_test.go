package media_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/media"
)

func TestLosslessAudioRejectsQuantizing32BitSources(t *testing.T) {
	for _, test := range []struct {
		codec  string
		bits   string
		reject bool
	}{
		{"pcm_s32le", "32", true},
		{"pcm_s32be", "", true},
		{"pcm_u32le", "", true},
		{"flac", "32", true},
		{"pcm_s32le", "24", false},
		{"pcm_s24le", "24", false},
		{"flac", "24", false},
	} {
		t.Run(test.codec+"/"+test.bits, func(t *testing.T) {
			input, err := media.DecodeProbe(strings.NewReader(fmt.Sprintf(`{
				"streams":[{"index":0,"codec_type":"audio","codec_name":%q,
				"bits_per_raw_sample":%q,"sample_fmt":"s32","sample_rate":"48000","channels":2}]
			}`, test.codec, test.bits)))
			if err != nil {
				t.Fatal(err)
			}
			_, err = media.BuildPlan(media.PlanRequest{
				Operation: corpus.OperationLosslessAudio, InputPath: "input.wav", OutputPath: "output.flac", Input: input,
			})
			if test.reject {
				var planErr *media.PlanError
				if !errors.As(err, &planErr) || planErr.Code != media.ErrorUnsupportedLosslessAudio {
					t.Errorf("BuildPlan() error = %v; want unsupported lossless audio", err)
				}
			} else if err != nil {
				t.Errorf("24-bit input rejected: %v", err)
			}
		})
	}
}
