package conversion

import (
	"context"
	"fmt"
	"slices"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/imageconv"
	"github.com/guigui42/filetwist/internal/media"
)

// Inspection is the privacy-safe probe result used to offer operations to a
// caller before any conversion runs.
type Inspection struct {
	// Media is the detected content summary.
	Media DetectedMedia `json:"media"`
	// Recommended is the single eligible default when inspection succeeds.
	Recommended corpus.Operation `json:"recommended"`
	// Compatible lists operations accepted by the content planning rules,
	// including Recommended, in stable presentation order. Runtime converter
	// availability and functional capability checks remain conversion-time checks.
	Compatible []corpus.Operation `json:"compatible"`
}

// AllOperations returns the eight named operations in stable presentation
// order.
func AllOperations() []corpus.Operation {
	return []corpus.Operation{
		corpus.OperationCompatiblePhoto,
		corpus.OperationSmallerPhoto,
		corpus.OperationLosslessImage,
		corpus.OperationCompatibleVideo,
		corpus.OperationSmallerVideo,
		corpus.OperationExtractAudio,
		corpus.OperationCompatibleAudio,
		corpus.OperationLosslessAudio,
	}
}

// RecommendOperation returns the single default operation for a media kind.
func RecommendOperation(kind corpus.MediaKind) corpus.Operation {
	return recommendedOperation(kind)
}

// OperationAccepts reports whether operation accepts kind in principle.
// Use Service.Inspect to account for the actual streams and image properties.
func OperationAccepts(operation corpus.Operation, kind corpus.MediaKind) bool {
	return operationAccepts(operation, kind)
}

// CompatibleOperations returns every named operation that accepts kind in
// principle, in stable presentation order. It does not inspect content.
func CompatibleOperations(kind corpus.MediaKind) []corpus.Operation {
	compatible := make([]corpus.Operation, 0, len(AllOperations()))
	for _, operation := range AllOperations() {
		if operationAccepts(operation, kind) {
			compatible = append(compatible, operation)
		}
	}
	return compatible
}

// OperationLabel returns a short human-readable label for a named operation.
func OperationLabel(operation corpus.Operation) string {
	switch operation {
	case corpus.OperationCompatiblePhoto:
		return "Compatible photo"
	case corpus.OperationSmallerPhoto:
		return "Smaller photo"
	case corpus.OperationLosslessImage:
		return "Lossless image"
	case corpus.OperationCompatibleVideo:
		return "Compatible video"
	case corpus.OperationSmallerVideo:
		return "Smaller video"
	case corpus.OperationExtractAudio:
		return "Extract audio"
	case corpus.OperationCompatibleAudio:
		return "Compatible audio"
	case corpus.OperationLosslessAudio:
		return "Lossless audio"
	default:
		return string(operation)
	}
}

// OutputName returns the deterministic output file name the service publishes
// for an input path and named operation.
func OutputName(inputPath string, operation corpus.Operation) string {
	return outputName(inputPath, operation)
}

// Inspect probes one local file and reports the detected media together with
// the recommended and compatible operations. It never converts and never
// writes to disk. Inputs without an eligible operation return a rejection.
func (service *Service) Inspect(ctx context.Context, inputPath string) (Inspection, error) {
	if ctx == nil {
		return Inspection{}, failure(
			FailureConfiguration,
			"invalid_context",
			"inspection context is required",
			nil,
		)
	}
	if err := validateRequest(Request{InputPath: inputPath, Output: "."}); err != nil {
		return Inspection{}, err
	}

	detection, imageInfo, mediaProbe, detectErr := service.detect(ctx, inputPath)
	if detectErr != nil {
		return Inspection{}, detectErr
	}
	inspection, err := inspectDetected(detection, imageInfo, mediaProbe)
	if err != nil {
		return inspection, err
	}
	return inspection, nil
}

func inspectDetected(detection DetectedMedia, imageInfo imageconv.Info, mediaProbe media.Probe) (Inspection, *Error) {
	inspection := Inspection{Media: detection, Compatible: []corpus.Operation{}}
	preferred := recommendedOperation(detection.Kind)
	var preferredErr *Error
	for _, operation := range CompatibleOperations(detection.Kind) {
		if err := validateOperation(operation, detection.Kind, imageInfo, mediaProbe); err != nil {
			if err.Kind != FailureRejection {
				return inspection, err
			}
			if operation == preferred {
				preferredErr = err
			}
			continue
		}
		inspection.Compatible = append(inspection.Compatible, operation)
	}
	if len(inspection.Compatible) == 0 {
		if preferredErr != nil {
			return inspection, preferredErr
		}
		return inspection, failure(FailureRejection, "no_compatible_operations", "no operation supports the detected input", nil)
	}
	inspection.Recommended = preferred
	if !slices.Contains(inspection.Compatible, preferred) {
		inspection.Recommended = inspection.Compatible[0]
	}
	return inspection, nil
}

func validateOperation(operation corpus.Operation, kind corpus.MediaKind, imageInfo imageconv.Info, mediaProbe media.Probe) *Error {
	if !operationAccepts(operation, kind) {
		return failure(
			FailureRejection,
			"incompatible_operation",
			fmt.Sprintf("operation %s is not compatible with detected %s input", operation, kind),
			nil,
		)
	}
	// Pure plans use placeholder paths and CPU execution to check content without
	// creating files or probing runtime capabilities (including VA-API).
	if kind == corpus.MediaImage {
		if err := imageconv.ValidateInput(imageInfo, imageconv.Capabilities{
			HEIFDecode: true,
			AVIFDecode: true,
		}, imageconv.DefaultLimits()); err != nil {
			return classifyImageError(err)
		}
		_, err := imageconv.BuildPlan(imageconv.PlanRequest{
			InputPath: "input", OutputDir: "output", WorkDir: "work", VipsPath: "vips",
			Operation: operation, Input: imageInfo, CaptureDatePolicy: imageconv.CaptureDateStrip,
		})
		if err != nil {
			return classifyImageError(err)
		}
		return nil
	}
	_, err := media.BuildPlan(media.PlanRequest{
		InputPath: "input", OutputPath: "output",
		Operation: operation, Input: mediaProbe,
		Acceleration: media.AccelerationConfig{Mode: media.AccelerationCPU},
	})
	if err != nil {
		return classifyMediaError(err)
	}
	return nil
}
