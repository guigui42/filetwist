package profiles_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/guigui42/filetwist/internal/profiles"
)

func TestRegistryContract(t *testing.T) {
	t.Parallel()

	want := []profiles.Operation{
		profiles.OperationCompatiblePhoto,
		profiles.OperationSmallerPhoto,
		profiles.OperationLosslessImage,
		profiles.OperationCompatibleVideo,
		profiles.OperationSmallerVideo,
		profiles.OperationExtractAudio,
		profiles.OperationCompatibleAudio,
		profiles.OperationLosslessAudio,
	}
	specs := profiles.All()
	got := make([]profiles.Operation, 0, len(specs))
	seen := make(map[profiles.Operation]struct{}, len(specs))
	recommended := make(map[profiles.MediaKind]int)

	for _, spec := range specs {
		got = append(got, spec.Operation)
		if _, ok := seen[spec.Operation]; ok {
			t.Errorf("duplicate operation %q", spec.Operation)
		}
		seen[spec.Operation] = struct{}{}
		if spec.Label == "" {
			t.Errorf("%s label is empty", spec.Operation)
		}
		if spec.Engine != profiles.EngineImage && spec.Engine != profiles.EngineMedia {
			t.Errorf("%s engine = %q", spec.Operation, spec.Engine)
		}
		if len(spec.AcceptedInputs) == 0 {
			t.Errorf("%s has no accepted input kinds", spec.Operation)
		}
		for _, kind := range spec.AcceptedInputs {
			if !validMediaKind(kind) {
				t.Errorf("%s accepts invalid media kind %q", spec.Operation, kind)
			}
		}
		if !validMediaKind(spec.OutputKind) {
			t.Errorf("%s output kind = %q", spec.Operation, spec.OutputKind)
		}
		if spec.Engine == profiles.EngineImage && spec.OutputKind != profiles.MediaImage {
			t.Errorf("%s image engine output kind = %q", spec.Operation, spec.OutputKind)
		}
		if spec.Engine == profiles.EngineMedia && spec.OutputKind == profiles.MediaImage {
			t.Errorf("%s media engine output kind = %q", spec.Operation, spec.OutputKind)
		}
		for _, kind := range spec.RecommendedFor {
			recommended[kind]++
			if !spec.Accepts(kind) {
				t.Errorf("%s is recommended for unaccepted kind %q", spec.Operation, kind)
			}
		}
		if spec.Output.Suffix == "" ||
			spec.Output.Extension == "" ||
			spec.Output.FormatLabel == "" ||
			spec.Output.MIMEType == "" ||
			spec.Output.Container == "" {
			t.Errorf("%s output facts are incomplete: %+v", spec.Operation, spec.Output)
		}
		if !strings.HasSuffix(spec.Output.Suffix, spec.Output.Extension) {
			t.Errorf("%s suffix %q does not end with %q", spec.Operation, spec.Output.Suffix, spec.Output.Extension)
		}
	}

	if !slices.Equal(got, want) {
		t.Fatalf("operations = %v; want %v", got, want)
	}
	for _, kind := range []profiles.MediaKind{
		profiles.MediaImage,
		profiles.MediaAudio,
		profiles.MediaVideo,
	} {
		if recommended[kind] != 1 {
			t.Errorf("%s recommended profile count = %d; want 1", kind, recommended[kind])
		}
	}
}

func TestLookupAndParse(t *testing.T) {
	t.Parallel()

	for _, spec := range profiles.All() {
		got, ok := profiles.Lookup(spec.Operation)
		if !ok || got.Operation != spec.Operation {
			t.Errorf("Lookup(%q) = %+v, %t", spec.Operation, got, ok)
		}
		parsed, err := profiles.Parse(string(spec.Operation))
		if err != nil || parsed != spec.Operation {
			t.Errorf("Parse(%q) = %q, %v", spec.Operation, parsed, err)
		}
	}
	if _, ok := profiles.Lookup("unknown"); ok {
		t.Error("Lookup(unknown) succeeded")
	}
	if _, err := profiles.Parse("unknown"); err == nil {
		t.Error("Parse(unknown) succeeded")
	}
}

func TestCompatibleAndRecommended(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		kind        profiles.MediaKind
		compatible  []profiles.Operation
		recommended profiles.Operation
	}{
		{
			name: "image",
			kind: profiles.MediaImage,
			compatible: []profiles.Operation{
				profiles.OperationCompatiblePhoto,
				profiles.OperationSmallerPhoto,
				profiles.OperationLosslessImage,
			},
			recommended: profiles.OperationCompatiblePhoto,
		},
		{
			name: "audio",
			kind: profiles.MediaAudio,
			compatible: []profiles.Operation{
				profiles.OperationExtractAudio,
				profiles.OperationCompatibleAudio,
				profiles.OperationLosslessAudio,
			},
			recommended: profiles.OperationCompatibleAudio,
		},
		{
			name: "video",
			kind: profiles.MediaVideo,
			compatible: []profiles.Operation{
				profiles.OperationCompatibleVideo,
				profiles.OperationSmallerVideo,
				profiles.OperationExtractAudio,
			},
			recommended: profiles.OperationCompatibleVideo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			specs := profiles.Compatible(tt.kind)
			got := make([]profiles.Operation, 0, len(specs))
			for _, spec := range specs {
				got = append(got, spec.Operation)
			}
			if !slices.Equal(got, tt.compatible) {
				t.Errorf("Compatible(%s) = %v; want %v", tt.kind, got, tt.compatible)
			}
			recommended, ok := profiles.Recommended(tt.kind)
			if !ok || recommended != tt.recommended {
				t.Errorf("Recommended(%s) = %q, %t; want %q", tt.kind, recommended, ok, tt.recommended)
			}
		})
	}
	if specs := profiles.Compatible("document"); len(specs) != 0 {
		t.Errorf("Compatible(document) = %v; want empty", specs)
	}
	if operation, ok := profiles.Recommended("document"); ok || operation != "" {
		t.Errorf("Recommended(document) = %q, %t", operation, ok)
	}
}

func TestRegistryReturnsDefensiveCopies(t *testing.T) {
	t.Parallel()

	all := profiles.All()
	all[0].AcceptedInputs[0] = profiles.MediaVideo
	all[0].RecommendedFor[0] = profiles.MediaVideo
	all[0].Label = "changed"

	spec, ok := profiles.Lookup(profiles.OperationCompatiblePhoto)
	if !ok {
		t.Fatal("compatible photo missing")
	}
	if spec.Label != "Compatible photo" ||
		!slices.Equal(spec.AcceptedInputs, []profiles.MediaKind{profiles.MediaImage}) ||
		!slices.Equal(spec.RecommendedFor, []profiles.MediaKind{profiles.MediaImage}) {
		t.Fatalf("registry mutated through All: %+v", spec)
	}

	compatible := profiles.Compatible(profiles.MediaImage)
	compatible[0].AcceptedInputs[0] = profiles.MediaAudio
	spec, _ = profiles.Lookup(profiles.OperationCompatiblePhoto)
	if !slices.Equal(spec.AcceptedInputs, []profiles.MediaKind{profiles.MediaImage}) {
		t.Fatalf("registry mutated through Compatible: %+v", spec)
	}
}

func TestOutputName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		inputPath string
		operation profiles.Operation
		want      string
	}{
		{"compatible photo", "/tmp/My Holiday.HEIC", profiles.OperationCompatiblePhoto, "my-holiday-compatible.jpg"},
		{"smaller photo", "/tmp/My Holiday.HEIC", profiles.OperationSmallerPhoto, "my-holiday-smaller.webp"},
		{"lossless image", "/tmp/My Holiday.HEIC", profiles.OperationLosslessImage, "my-holiday-lossless.png"},
		{"compatible video", "/tmp/Holiday.MOV", profiles.OperationCompatibleVideo, "holiday-compatible.mp4"},
		{"smaller video", "/tmp/Holiday.MOV", profiles.OperationSmallerVideo, "holiday-smaller.mp4"},
		{"extract audio", "/tmp/Holiday.MOV", profiles.OperationExtractAudio, "holiday-audio.m4a"},
		{"compatible audio", "/tmp/Tone.WAV", profiles.OperationCompatibleAudio, "tone-compatible.mp3"},
		{"lossless audio", "/tmp/Tone.WAV", profiles.OperationLosslessAudio, "tone-lossless.flac"},
		{"image fallback", "...", profiles.OperationLosslessImage, "image-lossless.png"},
		{"media fallback", "...", profiles.OperationLosslessAudio, "media-lossless.flac"},
		{"unicode letters and digits", filepath.Join("tmp", " Été ２０２６ .png"), profiles.OperationSmallerPhoto, "été-２０２６-smaller.webp"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := profiles.OutputName(tt.inputPath, tt.operation)
			if err != nil {
				t.Fatalf("OutputName() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("OutputName() = %q; want %q", got, tt.want)
			}
		})
	}
	if _, err := profiles.OutputName("input.bin", "unknown"); err == nil {
		t.Error("OutputName(unknown) succeeded")
	}
}

func validMediaKind(kind profiles.MediaKind) bool {
	return kind == profiles.MediaImage ||
		kind == profiles.MediaAudio ||
		kind == profiles.MediaVideo
}
