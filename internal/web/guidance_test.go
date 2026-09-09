package web

import (
	"strings"
	"testing"

	"github.com/guigui42/filetwist/internal/profiles"
)

func TestEveryProfileHasWebGuidance(t *testing.T) {
	for _, spec := range profiles.All() {
		t.Run(string(spec.Operation), func(t *testing.T) {
			option := operationOption(spec.Operation)
			if option.Value != string(spec.Operation) {
				t.Errorf("value = %q; want %q", option.Value, spec.Operation)
			}
			if option.Label != spec.Label {
				t.Errorf("label = %q; want %q", option.Label, spec.Label)
			}
			if option.Format != spec.Output.FormatLabel {
				t.Errorf("format = %q; want %q", option.Format, spec.Output.FormatLabel)
			}
			if strings.TrimSpace(option.Description) == "" {
				t.Error("description is empty")
			}
		})
	}
}
