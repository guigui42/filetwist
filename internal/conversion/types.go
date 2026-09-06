package conversion

import (
	"context"
	"fmt"
	"time"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/imageconv"
	"github.com/guigui42/filetwist/internal/media"
)

const (
	// SchemaVersion is the stable JSON result schema emitted by the CLI.
	SchemaVersion = "1.0"
)

// FailureKind determines the process exit code for a failed conversion.
type FailureKind string

const (
	// FailureRejection identifies unsupported input or an incompatible operation.
	FailureRejection FailureKind = "rejection"
	// FailureConfiguration identifies invalid arguments or runtime configuration.
	FailureConfiguration FailureKind = "configuration"
	// FailureProbe identifies content or output probing failures.
	FailureProbe FailureKind = "probe"
	// FailureConversion identifies converter execution or publication failures.
	FailureConversion FailureKind = "conversion"
	// FailureValidation identifies output compatibility validation failures.
	FailureValidation FailureKind = "validation"
	// FailureCanceled identifies caller cancellation.
	FailureCanceled FailureKind = "canceled"
)

// Error is a stable, privacy-safe orchestration failure.
type Error struct {
	Kind    FailureKind `json:"kind"`
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Cause   error       `json:"-"`
}

// Error implements error without exposing paths, command output, or probe data.
func (err *Error) Error() string {
	return err.Message
}

// Unwrap returns the underlying engine or filesystem error.
func (err *Error) Unwrap() error {
	return err.Cause
}

// Request describes one local conversion.
type Request struct {
	InputPath    string
	Output       string
	Operation    corpus.Operation
	OperationSet bool
}

// DetectedMedia is the privacy-safe subset of probe data exposed to callers.
type DetectedMedia struct {
	Kind           corpus.MediaKind `json:"kind"`
	Format         string           `json:"format"`
	MIMEType       string           `json:"mime_type,omitempty"`
	Width          int              `json:"width,omitempty"`
	Height         int              `json:"height,omitempty"`
	DurationMillis int64            `json:"duration_millis,omitempty"`
	Streams        []DetectedStream `json:"streams"`
}

// DetectedStream describes one content stream without tags or file metadata.
type DetectedStream struct {
	Index      int    `json:"index"`
	Kind       string `json:"kind"`
	Codec      string `json:"codec"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	Channels   int    `json:"channels,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
}

// Warning reports one non-fatal conversion policy decision.
type Warning struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	StreamIndex *int   `json:"stream_index,omitempty"`
	Codec       string `json:"codec,omitempty"`
}

// ValidationResult describes whether output validation ran and passed.
type ValidationResult struct {
	Status string            `json:"status"`
	Issues []ValidationIssue `json:"issues,omitempty"`
}

// ValidationIssue is one structured compatibility mismatch.
type ValidationIssue struct {
	Code     string `json:"code"`
	Field    string `json:"field,omitempty"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
}

// ExecutionResult reports the requested, initial, and final engine paths.
type ExecutionResult struct {
	Requested      string `json:"requested"`
	Initial        string `json:"initial"`
	Final          string `json:"final"`
	FallbackReason string `json:"fallback_reason,omitempty"`
	AttemptCount   int    `json:"attempt_count"`
}

// Timing reports wall-clock orchestration phases in milliseconds.
type Timing struct {
	ProbeMillis      int64 `json:"probe_ms"`
	ConversionMillis int64 `json:"conversion_ms"`
	TotalMillis      int64 `json:"total_ms"`
}

// Result is the stable JSON-compatible conversion result.
type Result struct {
	SchemaVersion     string                  `json:"schema_version"`
	Status            string                  `json:"status"`
	DetectedMedia     *DetectedMedia          `json:"detected_media,omitempty"`
	Observed          *corpus.MediaProperties `json:"observed,omitempty"`
	SelectedOperation corpus.Operation        `json:"selected_operation,omitempty"`
	OperationSource   string                  `json:"operation_source,omitempty"`
	OutputPath        string                  `json:"output_path,omitempty"`
	Validation        ValidationResult        `json:"validation"`
	Warnings          []Warning               `json:"warnings"`
	Execution         ExecutionResult         `json:"execution"`
	Timing            Timing                  `json:"timing"`
	Error             *Error                  `json:"error,omitempty"`
}

// ImageEngine is the still-image contract required by Service.
type ImageEngine interface {
	Probe(context.Context, string) (imageconv.Info, error)
	Convert(context.Context, ImageRequest) (imageconv.Result, error)
}

// ImageRequest passes a previously probed image to the image engine.
type ImageRequest struct {
	InputPath string
	OutputDir string
	Operation corpus.Operation
	Input     imageconv.Info
}

// MediaEngine is the audio/video contract required by Service.
type MediaEngine interface {
	Probe(context.Context, string) (media.Probe, error)
	Convert(context.Context, MediaRequest) (MediaResult, error)
}

// MediaRequest passes a previously probed media file to the media engine.
type MediaRequest struct {
	InputPath  string
	OutputPath string
	Operation  corpus.Operation
	Input      media.Probe
}

// MediaResult contains the existing media engine result and planning warnings.
type MediaResult struct {
	Conversion media.ConversionResult
	Warnings   []media.Warning
}

// ParseOperation validates one named operation.
func ParseOperation(value string) (corpus.Operation, error) {
	operation := corpus.Operation(value)
	switch operation {
	case corpus.OperationCompatiblePhoto,
		corpus.OperationSmallerPhoto,
		corpus.OperationLosslessImage,
		corpus.OperationCompatibleVideo,
		corpus.OperationSmallerVideo,
		corpus.OperationExtractAudio,
		corpus.OperationCompatibleAudio,
		corpus.OperationLosslessAudio:
		return operation, nil
	default:
		return "", fmt.Errorf("unsupported operation %q", value)
	}
}

func milliseconds(duration time.Duration) int64 {
	return duration.Milliseconds()
}
