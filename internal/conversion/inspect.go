package conversion

import (
	"context"
	"fmt"
	"slices"

	"github.com/guigui42/filetwist/internal/imageconv"
	"github.com/guigui42/filetwist/internal/media"
	"github.com/guigui42/filetwist/internal/profiles"
)

// Inspection is the privacy-safe probe result used to offer operations to a
// caller before any conversion runs.
type Inspection struct {
	// Media is the detected content summary.
	Media DetectedMedia `json:"media"`
	// Recommended is the single eligible default when inspection succeeds.
	Recommended profiles.Operation `json:"recommended"`
	// Compatible lists operations accepted by the content planning rules,
	// including Recommended, in stable presentation order. Runtime converter
	// availability and functional capability checks remain conversion-time checks.
	Compatible []profiles.Operation `json:"compatible"`
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
	inspection := Inspection{Media: detection, Compatible: []profiles.Operation{}}
	preferred, _ := profiles.Recommended(detection.Kind)
	var preferredErr *Error
	for _, spec := range profiles.Compatible(detection.Kind) {
		operation := spec.Operation
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

func validateOperation(
	operation profiles.Operation,
	kind profiles.MediaKind,
	imageInfo imageconv.Info,
	mediaProbe media.Probe,
) *Error {
	spec, ok := profiles.Lookup(operation)
	if !ok || !spec.Accepts(kind) {
		return failure(
			FailureRejection,
			"incompatible_operation",
			fmt.Sprintf("operation %s is not compatible with detected %s input", operation, kind),
			nil,
		)
	}
	// Pure plans use placeholder paths and CPU execution to check content without
	// creating files or probing runtime capabilities (including VA-API).
	if kind == profiles.MediaImage {
		if spec.Engine != profiles.EngineImage {
			return failure(
				FailureConfiguration,
				"profile_engine_mismatch",
				"operation is not supported by its configured conversion engine",
				nil,
			)
		}
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
	if spec.Engine != profiles.EngineMedia {
		return failure(
			FailureConfiguration,
			"profile_engine_mismatch",
			"operation is not supported by its configured conversion engine",
			nil,
		)
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
