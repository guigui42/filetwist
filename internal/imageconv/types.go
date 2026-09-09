package imageconv

import (
	"fmt"
	"time"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/profiles"
	"github.com/guigui42/filetwist/internal/runner"
)

const (
	// CodeUnsupportedInput identifies content that no supported image loader recognized.
	CodeUnsupportedInput = "unsupported-image"
	// CodeHEIFUnavailable identifies HEIF input without a successful functional decode probe.
	CodeHEIFUnavailable = "heif-loader-unavailable"
	// CodeAVIFUnavailable identifies AVIF input without a successful functional decode probe.
	CodeAVIFUnavailable = "avif-loader-unavailable"
	// CodeAnimatedUnsupported identifies multi-page or animated image input.
	CodeAnimatedUnsupported = "animated-image-unsupported"
	// CodeHDRUnsupported identifies input using PQ or HLG transfer characteristics.
	CodeHDRUnsupported = "hdr-image-unsupported"
	// CodeColorUnsupported identifies input whose color signaling is not safely handled.
	CodeColorUnsupported = "image-color-unsupported"
	// CodeAlphaUnsupported identifies alpha that cannot be preserved without precision loss.
	CodeAlphaUnsupported = "alpha-preservation-unsupported"
	// CodeDimensionsExceeded identifies decoded dimensions outside configured limits.
	CodeDimensionsExceeded = "decoded-dimensions-exceeded"
	// CodeInvalidRequest identifies an invalid conversion request.
	CodeInvalidRequest = "invalid-image-request"
	// CodeProbeFailed identifies a failed or incomplete image header probe.
	CodeProbeFailed = "image-probe-failed"
	// CodeCommandFailed identifies a failed libvips conversion command.
	CodeCommandFailed = "image-command-failed"
	// CodeOutputExists identifies a deterministic output path that already exists.
	CodeOutputExists = "image-output-exists"
	// CodePublishFailed identifies a failure to publish a validated output.
	CodePublishFailed = "image-publish-failed"
	// CodeCleanupFailed identifies a failure to remove temporary conversion files.
	CodeCleanupFailed = "image-cleanup-failed"
	// CodeOutputFormat identifies output content with the wrong format or MIME type.
	CodeOutputFormat = "invalid-output-format"
	// CodeOutputDimensions identifies output with incorrect oriented dimensions.
	CodeOutputDimensions = "invalid-output-dimensions"
	// CodeOutputOrientation identifies output whose orientation was not baked into pixels.
	CodeOutputOrientation = "invalid-output-orientation"
	// CodeOutputAlpha identifies output that violated its preserve-or-flatten policy.
	CodeOutputAlpha = "invalid-output-alpha"
	// CodeOutputPrecision identifies output that did not preserve the expected sample precision.
	CodeOutputPrecision = "invalid-output-precision"
	// CodeOutputColor identifies output that retained unsupported HDR transfer metadata.
	CodeOutputColor = "invalid-output-color"
	// CodeOutputMetadata identifies output that violated its metadata policy.
	CodeOutputMetadata = "invalid-output-metadata"
)

// Format identifies image content from the libvips loader and header fields.
type Format string

const (
	// FormatJPEG is JPEG image content.
	FormatJPEG Format = "jpeg"
	// FormatPNG is PNG image content.
	FormatPNG Format = "png"
	// FormatWebP is WebP image content.
	FormatWebP Format = "webp"
	// FormatHEIF is HEIF or HEIC image content encoded with HEVC.
	FormatHEIF Format = "heif"
	// FormatAVIF is HEIF image content encoded with AV1.
	FormatAVIF Format = "avif"
	// FormatTIFF is TIFF image content.
	FormatTIFF Format = "tiff"
	// FormatGIF is GIF image content.
	FormatGIF Format = "gif"
	// FormatBMP is BMP image content.
	FormatBMP Format = "bmp"
)

// Metadata records compatibility-relevant metadata discovered by libvips.
type Metadata struct {
	GPS         bool
	ICC         bool
	EXIF        bool
	XMP         bool
	GainMap     bool
	CaptureDate bool
}

// CICP records coded color signaling reported by the image loader.
type CICP struct {
	Present   bool
	Primaries int
	Transfer  int
	Matrix    int
	FullRange int
}

// Info is the normalized result of a content-based libvips header probe.
type Info struct {
	Format          Format
	MIMEType        string
	Loader          string
	Width           int
	Height          int
	Bands           int
	BandFormat      string
	Coding          string
	Interpretation  string
	Orientation     int
	HDR             bool
	HasAlpha        bool
	HasTransparency bool
	Pages           int
	CICP            CICP
	Metadata        Metadata
}

// CorpusProperties maps a probe result into the shared media properties.
func (info Info) CorpusProperties() corpus.MediaProperties {
	rotation, mirrored := displayOrientation(info.Orientation)
	return corpus.MediaProperties{
		Container: string(info.Format),
		Streams: []corpus.Stream{{
			Kind:  corpus.StreamImage,
			Codec: corpusCodec(info.Format),
			Count: 1,
		}},
		Dimensions: &corpus.Dimensions{
			Width:  info.Width,
			Height: info.Height,
		},
		Orientation: &corpus.Orientation{
			RotationDegrees: rotation,
			Mirrored:        mirrored,
			PixelNormalized: info.Orientation == 1,
		},
		Alpha: presence(info.HasAlpha),
		Metadata: corpus.Metadata{
			GPS:          presence(info.Metadata.GPS),
			ColorProfile: presence(info.Metadata.ICC),
			EXIF:         presence(info.Metadata.EXIF),
			XMP:          presence(info.Metadata.XMP),
			GainMap:      presence(info.Metadata.GainMap),
		},
	}
}

// Limits bounds dimensions before pixel decoding is requested.
type Limits struct {
	MaxWidth  int
	MaxHeight int
	MaxPixels int64
}

// DefaultLimits returns conservative MVP limits for decoded still images.
func DefaultLimits() Limits {
	return Limits{
		MaxWidth:  32_768,
		MaxHeight: 32_768,
		MaxPixels: 100_000_000,
	}
}

// Capability identifies optional image runtime support verified by a functional probe.
type Capability uint8

// CapabilitySet records functionally tested optional image runtime support.
// Its zero value contains no optional capabilities.
type CapabilitySet uint64

// CaptureDatePolicy controls whether capture-date EXIF is retained.
type CaptureDatePolicy int

const (
	// CaptureDateStrip removes EXIF, including capture dates.
	CaptureDateStrip CaptureDatePolicy = iota
	// CaptureDatePreserveWhenSafe retains EXIF only when no GPS fields were detected.
	CaptureDatePreserveWhenSafe
)

// Output declares the deterministic path, extension, and MIME type.
type Output struct {
	Path      string
	Extension string
	MIMEType  string
}

// Expectation describes the output properties enforced after conversion.
type Expectation struct {
	Format      Format
	MIMEType    string
	Width       int
	Height      int
	Orientation int
	HasAlpha    bool
	Metadata    Metadata
	// BandFormat requires the decoded sample precision when nonempty.
	BandFormat string
}

// Plan is an independently testable sequence of direct libvips invocations.
type Plan struct {
	Output          Output
	TemporaryOutput string
	TemporaryPaths  []string
	Commands        []runner.Command
	Expect          Expectation
}

// PlanRequest contains all deterministic inputs needed to build a Plan.
type PlanRequest struct {
	InputPath         string
	OutputDir         string
	WorkDir           string
	Operation         profiles.Operation
	Input             Info
	CaptureDatePolicy CaptureDatePolicy
	VipsPath          string
}

// Request asks Converter to execute one named image operation.
type Request struct {
	InputPath string
	OutputDir string
	Operation profiles.Operation
	// Input is the fresh header probe produced for InputPath by the current conversion.
	Input Info
}

// Result contains the declared artifact, probes, and bounded command results.
type Result struct {
	Output             Output
	Input              Info
	Observed           Info
	ObservedProperties corpus.MediaProperties
	Commands           []runner.Result
}

// Config controls converter executables, policies, and limits.
type Config struct {
	VipsExecutable    string
	HeaderExecutable  string
	Limits            Limits
	CaptureDatePolicy CaptureDatePolicy
	Capabilities      CapabilitySet
	CommandTimeout    time.Duration
	// ProbeTimeout bounds the staged output header probe, defaulting to 30 seconds.
	ProbeTimeout time.Duration
}

// Error reports a stable failure code without exposing input paths or command output.
type Error struct {
	Code  string
	Stage string
	Cause error
}

// Error implements error.
func (e *Error) Error() string {
	return fmt.Sprintf("image conversion: %s failed (%s)", e.Stage, e.Code)
}

// Unwrap returns the underlying error.
func (e *Error) Unwrap() error {
	return e.Cause
}

func corpusCodec(format Format) string {
	switch format {
	case FormatJPEG:
		return "mjpeg"
	case FormatPNG:
		return "png"
	case FormatWebP:
		return "webp"
	case FormatHEIF:
		return "hevc"
	case FormatAVIF:
		return "av1"
	case FormatTIFF:
		return "tiff"
	case FormatGIF:
		return "gif"
	case FormatBMP:
		return "bmp"
	default:
		return "unknown"
	}
}

func displayOrientation(orientation int) (int, bool) {
	switch orientation {
	case 2:
		return 0, true
	case 3:
		return 180, false
	case 4:
		return 180, true
	case 5:
		return 90, true
	case 6:
		return 90, false
	case 7:
		return 270, true
	case 8:
		return 270, false
	default:
		return 0, false
	}
}

func presence(present bool) corpus.Presence {
	if present {
		return corpus.PresenceRequired
	}
	return corpus.PresenceForbidden
}
