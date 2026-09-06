package corpus

import (
	"math"
	"strings"
	"testing"
)

func TestFixturePathRejectsParentAndNull(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"..", "file\x00.png"} {
		var errs ValidationErrors
		validateFixturePath("input_path", path, &errs)
		if len(errs) == 0 {
			t.Fatalf("accepted unsafe path %q", path)
		}
	}
}

func TestManifestRejectsUnobservableTolerances(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		tolerances Tolerances
		field      string
	}{
		{"frame rate", Tolerances{FrameRateMilliFPS: 1}, "frame_rate_millifps"},
		{"negative frame rate", Tolerances{FrameRateMilliFPS: -1}, "frame_rate_millifps"},
		{"bitrate", Tolerances{BitratePercent: 1}, "bitrate_percent"},
		{"negative bitrate", Tolerances{BitratePercent: -1}, "bitrate_percent"},
		{"nonfinite bitrate", Tolerances{BitratePercent: math.NaN()}, "bitrate_percent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := validManifest()
			manifest.Fixtures[0].Tolerances = tc.tolerances
			err := manifest.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("Validate() = %v, want unsupported %s", err, tc.field)
			}
		})
	}
	if err := validManifest().Validate(); err != nil {
		t.Fatalf("zero reserved tolerances rejected: %v", err)
	}
}
