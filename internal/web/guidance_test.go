package web

import (
	"strings"
	"testing"

	"github.com/guigui42/filetwist/internal/profiles"
)

func TestEveryProfileHasWebGuidance(t *testing.T) {
	want := map[profiles.Operation]struct {
		label  string
		format string
	}{
		profiles.OperationCompatiblePhoto: {"Compatible photo", "JPEG"},
		profiles.OperationSmallerPhoto:    {"Smaller photo", "WebP"},
		profiles.OperationLosslessImage:   {"Lossless image", "PNG"},
		profiles.OperationCompatibleVideo: {"Compatible video", "MP4"},
		profiles.OperationSmallerVideo:    {"Smaller video", "MP4"},
		profiles.OperationExtractAudio:    {"Extract audio", "M4A"},
		profiles.OperationCompatibleAudio: {"Compatible audio", "MP3"},
		profiles.OperationLosslessAudio:   {"Lossless audio", "FLAC"},
	}
	if len(want) != len(profiles.All()) {
		t.Fatalf("guidance expectations = %d; registry profiles = %d", len(want), len(profiles.All()))
	}

	for _, spec := range profiles.All() {
		t.Run(string(spec.Operation), func(t *testing.T) {
			expected, ok := want[spec.Operation]
			if !ok {
				t.Fatalf("profile %q has no guidance expectation", spec.Operation)
			}
			option := operationOption(spec.Operation)
			if option.Value != string(spec.Operation) {
				t.Errorf("value = %q; want %q", option.Value, spec.Operation)
			}
			if option.Label != expected.label {
				t.Errorf("label = %q; want %q", option.Label, expected.label)
			}
			if option.Format != expected.format {
				t.Errorf("format = %q; want %q", option.Format, expected.format)
			}
			if strings.TrimSpace(option.Description) == "" {
				t.Error("description is empty")
			}
		})
	}
}
