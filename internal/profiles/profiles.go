package profiles

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// Operation identifies one named conversion profile.
type Operation string

const (
	// OperationCompatiblePhoto creates a broadly compatible photo.
	OperationCompatiblePhoto Operation = "compatible_photo"
	// OperationSmallerPhoto creates a smaller lossy photo.
	OperationSmallerPhoto Operation = "smaller_photo"
	// OperationLosslessImage creates a lossless image.
	OperationLosslessImage Operation = "lossless_image"
	// OperationCompatibleVideo creates a broadly compatible video.
	OperationCompatibleVideo Operation = "compatible_video"
	// OperationSmallerVideo creates a smaller video.
	OperationSmallerVideo Operation = "smaller_video"
	// OperationExtractAudio extracts audio from media.
	OperationExtractAudio Operation = "extract_audio"
	// OperationCompatibleAudio creates broadly compatible audio.
	OperationCompatibleAudio Operation = "compatible_audio"
	// OperationLosslessAudio creates lossless audio.
	OperationLosslessAudio Operation = "lossless_audio"
)

// MediaKind identifies a broad input or output media category.
type MediaKind string

const (
	// MediaImage identifies image content.
	MediaImage MediaKind = "image"
	// MediaAudio identifies audio content.
	MediaAudio MediaKind = "audio"
	// MediaVideo identifies video content.
	MediaVideo MediaKind = "video"
)

// Engine identifies the conversion engine that implements a profile.
type Engine string

const (
	// EngineImage identifies the libvips still-image engine.
	EngineImage Engine = "image"
	// EngineMedia identifies the FFmpeg audio/video engine.
	EngineMedia Engine = "media"
)

// OutputSpec contains stable output naming and presentation facts.
type OutputSpec struct {
	Suffix      string
	Extension   string
	FormatLabel string
	MIMEType    string
	Container   string
}

// Spec describes one named conversion profile without engine-specific policy.
type Spec struct {
	Operation      Operation
	Label          string
	Engine         Engine
	AcceptedInputs []MediaKind
	OutputKind     MediaKind
	RecommendedFor []MediaKind
	Output         OutputSpec
}

var registry = []Spec{
	{
		Operation:      OperationCompatiblePhoto,
		Label:          "Compatible photo",
		Engine:         EngineImage,
		AcceptedInputs: []MediaKind{MediaImage},
		OutputKind:     MediaImage,
		RecommendedFor: []MediaKind{MediaImage},
		Output: OutputSpec{
			Suffix:      "-compatible.jpg",
			Extension:   ".jpg",
			FormatLabel: "JPEG",
			MIMEType:    "image/jpeg",
			Container:   "jpeg",
		},
	},
	{
		Operation:      OperationSmallerPhoto,
		Label:          "Smaller photo",
		Engine:         EngineImage,
		AcceptedInputs: []MediaKind{MediaImage},
		OutputKind:     MediaImage,
		Output: OutputSpec{
			Suffix:      "-smaller.webp",
			Extension:   ".webp",
			FormatLabel: "WebP",
			MIMEType:    "image/webp",
			Container:   "webp",
		},
	},
	{
		Operation:      OperationLosslessImage,
		Label:          "Lossless image",
		Engine:         EngineImage,
		AcceptedInputs: []MediaKind{MediaImage},
		OutputKind:     MediaImage,
		Output: OutputSpec{
			Suffix:      "-lossless.png",
			Extension:   ".png",
			FormatLabel: "PNG",
			MIMEType:    "image/png",
			Container:   "png",
		},
	},
	{
		Operation:      OperationCompatibleVideo,
		Label:          "Compatible video",
		Engine:         EngineMedia,
		AcceptedInputs: []MediaKind{MediaVideo},
		OutputKind:     MediaVideo,
		RecommendedFor: []MediaKind{MediaVideo},
		Output: OutputSpec{
			Suffix:      "-compatible.mp4",
			Extension:   ".mp4",
			FormatLabel: "MP4",
			MIMEType:    "video/mp4",
			Container:   "mp4",
		},
	},
	{
		Operation:      OperationSmallerVideo,
		Label:          "Smaller video",
		Engine:         EngineMedia,
		AcceptedInputs: []MediaKind{MediaVideo},
		OutputKind:     MediaVideo,
		Output: OutputSpec{
			Suffix:      "-smaller.mp4",
			Extension:   ".mp4",
			FormatLabel: "MP4",
			MIMEType:    "video/mp4",
			Container:   "mp4",
		},
	},
	{
		Operation:      OperationExtractAudio,
		Label:          "Extract audio",
		Engine:         EngineMedia,
		AcceptedInputs: []MediaKind{MediaAudio, MediaVideo},
		OutputKind:     MediaAudio,
		Output: OutputSpec{
			Suffix:      "-audio.m4a",
			Extension:   ".m4a",
			FormatLabel: "M4A",
			MIMEType:    "audio/mp4",
			Container:   "mp4",
		},
	},
	{
		Operation:      OperationCompatibleAudio,
		Label:          "Compatible audio",
		Engine:         EngineMedia,
		AcceptedInputs: []MediaKind{MediaAudio},
		OutputKind:     MediaAudio,
		RecommendedFor: []MediaKind{MediaAudio},
		Output: OutputSpec{
			Suffix:      "-compatible.mp3",
			Extension:   ".mp3",
			FormatLabel: "MP3",
			MIMEType:    "audio/mpeg",
			Container:   "mp3",
		},
	},
	{
		Operation:      OperationLosslessAudio,
		Label:          "Lossless audio",
		Engine:         EngineMedia,
		AcceptedInputs: []MediaKind{MediaAudio},
		OutputKind:     MediaAudio,
		Output: OutputSpec{
			Suffix:      "-lossless.flac",
			Extension:   ".flac",
			FormatLabel: "FLAC",
			MIMEType:    "audio/flac",
			Container:   "flac",
		},
	},
}

var byOperation = func() map[Operation]Spec {
	specs := make(map[Operation]Spec, len(registry))
	for _, spec := range registry {
		specs[spec.Operation] = spec
	}
	return specs
}()

// All returns every profile in stable presentation order.
func All() []Spec {
	specs := make([]Spec, len(registry))
	for index, spec := range registry {
		specs[index] = cloneSpec(spec)
	}
	return specs
}

// Lookup returns the named profile.
func Lookup(operation Operation) (Spec, bool) {
	spec, ok := byOperation[operation]
	if !ok {
		return Spec{}, false
	}
	return cloneSpec(spec), true
}

// Parse validates one operation string.
func Parse(value string) (Operation, error) {
	operation := Operation(value)
	if _, ok := byOperation[operation]; !ok {
		return "", fmt.Errorf("unsupported operation %q", value)
	}
	return operation, nil
}

// Compatible returns profiles that accept kind in stable presentation order.
func Compatible(kind MediaKind) []Spec {
	specs := make([]Spec, 0, len(registry))
	for _, spec := range registry {
		if spec.Accepts(kind) {
			specs = append(specs, cloneSpec(spec))
		}
	}
	return specs
}

// Recommended returns the default profile for kind.
func Recommended(kind MediaKind) (Operation, bool) {
	for _, spec := range registry {
		if containsKind(spec.RecommendedFor, kind) {
			return spec.Operation, true
		}
	}
	return "", false
}

// Accepts reports whether the profile accepts kind as input.
func (spec Spec) Accepts(kind MediaKind) bool {
	return containsKind(spec.AcceptedInputs, kind)
}

// OutputName returns the deterministic output filename for an input and profile.
func OutputName(inputPath string, operation Operation) (string, error) {
	spec, ok := Lookup(operation)
	if !ok {
		return "", fmt.Errorf("unsupported operation %q", operation)
	}
	stem := sanitizeStem(strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath)))
	if stem == "" {
		if spec.OutputKind == MediaImage {
			stem = "image"
		} else {
			stem = "media"
		}
	}
	return stem + spec.Output.Suffix, nil
}

func cloneSpec(spec Spec) Spec {
	spec.AcceptedInputs = append([]MediaKind(nil), spec.AcceptedInputs...)
	spec.RecommendedFor = append([]MediaKind(nil), spec.RecommendedFor...)
	return spec
}

func containsKind(kinds []MediaKind, candidate MediaKind) bool {
	for _, kind := range kinds {
		if kind == candidate {
			return true
		}
	}
	return false
}

func sanitizeStem(stem string) string {
	var builder strings.Builder
	dash := false
	for _, value := range strings.ToLower(stem) {
		if unicode.IsLetter(value) || unicode.IsDigit(value) {
			builder.WriteRune(value)
			dash = false
			continue
		}
		if builder.Len() > 0 && !dash {
			builder.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}
