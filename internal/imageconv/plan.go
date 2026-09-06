package imageconv

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/runner"
)

const maxWebPDimension = 16_383

// ValidateInput checks animation, optional loader capabilities, and decoded dimensions.
func ValidateInput(info Info, capabilities Capabilities, limits Limits) error {
	if info.Format == "" || info.Width <= 0 || info.Height <= 0 {
		return imageError(CodeUnsupportedInput, "validate input", errors.New("image properties are incomplete"))
	}
	if info.Pages != 1 {
		return imageError(CodeAnimatedUnsupported, "validate input", errors.New("only single-page images are supported"))
	}
	if info.HDR {
		return imageError(CodeHDRUnsupported, "validate input", errors.New("PQ and HLG HDR images are not supported"))
	}
	if !supportedCICP(info.CICP) {
		return imageError(CodeColorUnsupported, "validate input", errors.New("non-sRGB CICP color signaling is not supported"))
	}
	if err := validateColorRepresentation(info); err != nil {
		return err
	}
	if info.Format == FormatHEIF && !capabilities.HEIFDecode {
		return imageError(CodeHEIFUnavailable, "validate input", errors.New("HEIF decode was not functionally probed"))
	}
	if info.Format == FormatAVIF && !capabilities.AVIFDecode {
		return imageError(CodeAVIFUnavailable, "validate input", errors.New("AVIF decode was not functionally probed"))
	}
	if limits.MaxWidth <= 0 || limits.MaxHeight <= 0 || limits.MaxPixels <= 0 {
		return imageError(CodeInvalidRequest, "validate limits", errors.New("all decoded dimension limits must be positive"))
	}
	if info.Width > limits.MaxWidth || info.Height > limits.MaxHeight ||
		int64(info.Width) > limits.MaxPixels/int64(info.Height) {
		return imageError(CodeDimensionsExceeded, "validate input", errors.New("decoded dimensions exceed configured limits"))
	}
	return nil
}

// OutputName returns a deterministic operation-specific output filename.
func OutputName(inputPath string, operation corpus.Operation) (string, error) {
	stem := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	stem = sanitizeStem(stem)
	if stem == "" {
		stem = "image"
	}

	switch operation {
	case corpus.OperationCompatiblePhoto:
		return stem + "-compatible.jpg", nil
	case corpus.OperationSmallerPhoto:
		return stem + "-smaller.webp", nil
	case corpus.OperationLosslessImage:
		return stem + "-lossless.png", nil
	default:
		return "", imageError(CodeInvalidRequest, "name output", errors.New("unsupported image operation"))
	}
}

// BuildPlan constructs direct libvips commands without executing them.
func BuildPlan(request PlanRequest) (Plan, error) {
	if request.InputPath == "" || request.OutputDir == "" || request.WorkDir == "" || request.VipsPath == "" {
		return Plan{}, imageError(CodeInvalidRequest, "build plan", errors.New("input, output, work, and vips paths are required"))
	}
	if request.CaptureDatePolicy != CaptureDateStrip &&
		request.CaptureDatePolicy != CaptureDatePreserveWhenSafe {
		return Plan{}, imageError(CodeInvalidRequest, "build plan", errors.New("unsupported capture-date policy"))
	}
	if request.Input.HDR {
		return Plan{}, imageError(CodeHDRUnsupported, "build plan", errors.New("PQ and HLG HDR images are not supported"))
	}
	if !supportedCICP(request.Input.CICP) {
		return Plan{}, imageError(CodeColorUnsupported, "build plan", errors.New("non-sRGB CICP color signaling is not supported"))
	}
	if err := validateColorRepresentation(request.Input); err != nil {
		return Plan{}, err
	}

	name, err := OutputName(request.InputPath, request.Operation)
	if err != nil {
		return Plan{}, err
	}
	if request.Operation == corpus.OperationSmallerPhoto &&
		(request.Input.Width > maxWebPDimension || request.Input.Height > maxWebPDimension) {
		return Plan{}, imageError(
			CodeDimensionsExceeded,
			"build plan",
			errors.New("WebP dimensions exceed 16,383 pixels"),
		)
	}

	output := Output{Path: filepath.Join(request.OutputDir, name)}
	oriented := filepath.Join(request.WorkDir, "oriented.v")
	commands := []runner.Command{{
		Path: request.VipsPath,
		Args: []string{"autorot", request.InputPath, oriented},
	}}
	temporaryPaths := []string{oriented}
	saveInput := oriented
	flattened := false

	outputMetadata := request.Input.Metadata
	normalizeColor := needsColorNormalization(request.Input)
	if normalizeColor && request.Input.HasAlpha {
		if request.Operation != corpus.OperationCompatiblePhoto && request.Input.BandFormat != "uchar" {
			return Plan{}, imageError(
				CodeAlphaUnsupported,
				"build plan",
				errors.New("preserving non-8-bit alpha through color conversion is not supported"),
			)
		}
		if request.Operation == corpus.OperationCompatiblePhoto {
			background, maxAlpha, err := preNormalizationFlatten(request.Input)
			if err != nil {
				return Plan{}, err
			}
			opaque := filepath.Join(request.WorkDir, "opaque.v")
			args := []string{
				"flatten", saveInput, opaque,
				"--background", background,
			}
			if maxAlpha != "" {
				args = append(args, "--max-alpha", maxAlpha)
			}
			commands = append(commands, runner.Command{Path: request.VipsPath, Args: args})
			temporaryPaths = append(temporaryPaths, opaque)
			saveInput = opaque
			flattened = true
		}
	}
	iccDepth := "8"
	if request.Operation == corpus.OperationLosslessImage && request.Input.BandFormat == "ushort" {
		iccDepth = "16"
	}
	switch request.Input.Interpretation {
	case "cmyk":
		colour := filepath.Join(request.WorkDir, "colour.v")
		args := []string{"icc_transform", saveInput, colour, "srgb"}
		if request.Input.Metadata.ICC {
			args = append(args, "--embedded")
		}
		args = append(args, "--depth", iccDepth)
		commands = append(commands, runner.Command{Path: request.VipsPath, Args: args})
		temporaryPaths = append(temporaryPaths, colour)
		saveInput = colour
		outputMetadata.ICC = true
	case "b-w", "grey16":
		if request.Input.Metadata.ICC {
			colour := filepath.Join(request.WorkDir, "colour.v")
			commands = append(commands, runner.Command{
				Path: request.VipsPath,
				Args: []string{
					"icc_transform", saveInput, colour, "srgb",
					"--embedded", "--depth", iccDepth,
				},
			})
			temporaryPaths = append(temporaryPaths, colour)
			saveInput = colour
			outputMetadata.ICC = true
		}
	case "lab", "lch", "xyz", "yxy", "hsv":
		colour := filepath.Join(request.WorkDir, "colour.v")
		commands = append(commands, runner.Command{
			Path: request.VipsPath,
			Args: []string{"colourspace", saveInput, colour, "srgb"},
		})
		temporaryPaths = append(temporaryPaths, colour)
		saveInput = colour
		outputMetadata.ICC = false
	}

	keep, preserveCaptureDate := metadataKeep(outputMetadata, request.CaptureDatePolicy)
	width, height := orientedDimensions(request.Input.Width, request.Input.Height, request.Input.Orientation)
	expect := Expectation{
		Width:       width,
		Height:      height,
		Orientation: 1,
		Metadata: Metadata{
			ICC:         outputMetadata.ICC,
			EXIF:        preserveCaptureDate,
			CaptureDate: preserveCaptureDate,
		},
	}

	var temporaryOutput string
	switch request.Operation {
	case corpus.OperationCompatiblePhoto:
		output.Extension = ".jpg"
		output.MIMEType = "image/jpeg"
		temporaryOutput = filepath.Join(request.WorkDir, "output.jpg")
		expect.Format = FormatJPEG
		expect.MIMEType = output.MIMEType
		expect.HasAlpha = false
		if request.Input.HasAlpha && !flattened {
			opaque := filepath.Join(request.WorkDir, "pixels.v")
			background := flattenBackground(request.Input, saveInput != oriented)
			commands = append(commands, runner.Command{
				Path: request.VipsPath,
				Args: []string{
					"flatten", saveInput, opaque,
					"--background", background,
				},
			})
			temporaryPaths = append(temporaryPaths, opaque)
			saveInput = opaque
		}
		commands = append(commands, runner.Command{
			Path: request.VipsPath,
			Args: []string{
				"jpegsave", saveInput, temporaryOutput,
				"--Q", "90",
				"--optimize-coding",
				"--interlace",
				"--keep", keep,
			},
		})
	case corpus.OperationSmallerPhoto:
		output.Extension = ".webp"
		output.MIMEType = "image/webp"
		temporaryOutput = filepath.Join(request.WorkDir, "output.webp")
		expect.Format = FormatWebP
		expect.MIMEType = output.MIMEType
		expect.HasAlpha = request.Input.HasTransparency
		args := []string{
			"webpsave", saveInput, temporaryOutput,
			"--Q", "80",
			"--effort", "6",
			"--alpha-q", "100",
		}
		if request.Input.HasAlpha {
			args = append(args, "--exact")
		}
		args = append(args, "--keep", keep)
		commands = append(commands, runner.Command{Path: request.VipsPath, Args: args})
	case corpus.OperationLosslessImage:
		output.Extension = ".png"
		output.MIMEType = "image/png"
		temporaryOutput = filepath.Join(request.WorkDir, "output.png")
		expect.Format = FormatPNG
		expect.MIMEType = output.MIMEType
		expect.HasAlpha = request.Input.HasAlpha
		expect.BandFormat = request.Input.BandFormat
		commands = append(commands, runner.Command{
			Path: request.VipsPath,
			Args: []string{
				"pngsave", saveInput, temporaryOutput,
				"--compression", "9",
				"--keep", keep,
			},
		})
	default:
		return Plan{}, imageError(CodeInvalidRequest, "build plan", errors.New("unsupported image operation"))
	}

	return Plan{
		Output:          output,
		TemporaryOutput: temporaryOutput,
		TemporaryPaths:  temporaryPaths,
		Commands:        commands,
		Expect:          expect,
	}, nil
}

// ValidateOutput verifies the output content rather than trusting its filename.
func ValidateOutput(expect Expectation, observed Info) error {
	if observed.Format != expect.Format || observed.MIMEType != expect.MIMEType {
		return imageError(CodeOutputFormat, "validate output", fmt.Errorf("format or MIME type did not match"))
	}
	if observed.Width != expect.Width || observed.Height != expect.Height || observed.Pages != 1 {
		return imageError(CodeOutputDimensions, "validate output", errors.New("dimensions or page count did not match"))
	}
	if observed.Orientation != expect.Orientation {
		return imageError(CodeOutputOrientation, "validate output", errors.New("orientation was not normalized"))
	}
	if observed.HasAlpha != expect.HasAlpha {
		return imageError(CodeOutputAlpha, "validate output", errors.New("alpha policy was not satisfied"))
	}
	if expect.BandFormat != "" && observed.BandFormat != expect.BandFormat {
		return imageError(CodeOutputPrecision, "validate output", fmt.Errorf(
			"image band format is %q; expected %q", observed.BandFormat, expect.BandFormat,
		))
	}
	if observed.HDR {
		return imageError(CodeOutputColor, "validate output", errors.New("output retained PQ or HLG transfer characteristics"))
	}
	if !supportedCICP(observed.CICP) {
		return imageError(CodeOutputColor, "validate output", errors.New("output retained unsupported CICP color signaling"))
	}
	if observed.Metadata.GPS ||
		observed.Metadata.ICC != expect.Metadata.ICC ||
		observed.Metadata.EXIF != expect.Metadata.EXIF ||
		observed.Metadata.CaptureDate != expect.Metadata.CaptureDate ||
		observed.Metadata.XMP ||
		observed.Metadata.GainMap {
		return imageError(CodeOutputMetadata, "validate output", errors.New("metadata policy was not satisfied"))
	}
	return nil
}

func supportedCICP(cicp CICP) bool {
	return !cicp.Present || (cicp.Primaries == 1 && cicp.Transfer == 13)
}

func validateColorRepresentation(info Info) error {
	if info.Coding != "" && info.Coding != "none" {
		return imageError(CodeColorUnsupported, "validate input", errors.New("coded image pixels are not supported"))
	}
	if info.Interpretation == "cmyk" && !info.Metadata.ICC {
		return imageError(CodeColorUnsupported, "validate input", errors.New("CMYK input requires an embedded ICC profile"))
	}
	if (info.Format == FormatHEIF || info.Format == FormatAVIF) &&
		info.BandFormat == "ushort" &&
		!info.CICP.Present {
		return imageError(
			CodeColorUnsupported,
			"validate input",
			errors.New("high-bit-depth HEIF color signaling is unavailable"),
		)
	}
	if info.BandFormat != "uchar" && info.BandFormat != "ushort" {
		code := CodeColorUnsupported
		if info.HasAlpha {
			code = CodeAlphaUnsupported
		}
		return imageError(code, "validate input", errors.New("image band format is not supported"))
	}
	colorBands := interpretationBands(info.Interpretation)
	if colorBands == 0 {
		return imageError(CodeColorUnsupported, "validate input", errors.New("image color interpretation is not supported"))
	}
	hasAlpha := info.Bands == colorBands+1
	if info.Bands != colorBands && !hasAlpha {
		return imageError(CodeAlphaUnsupported, "validate input", errors.New("image has an ambiguous extra-band layout"))
	}
	if info.HasAlpha != hasAlpha {
		return imageError(CodeAlphaUnsupported, "validate input", errors.New("image alpha metadata does not match its band layout"))
	}
	if info.HasAlpha {
		switch info.Interpretation {
		case "lab", "lch", "xyz", "yxy", "hsv":
			return imageError(
				CodeAlphaUnsupported,
				"validate input",
				errors.New("alpha in this color interpretation is not supported"),
			)
		}
	}
	return nil
}

func interpretationBands(interpretation string) int {
	switch interpretation {
	case "b-w", "grey16":
		return 1
	case "cmyk":
		return 4
	case "srgb", "rgb", "rgb16", "hsv", "lab", "lch", "xyz", "yxy":
		return 3
	default:
		return 0
	}
}

func needsColorNormalization(info Info) bool {
	switch info.Interpretation {
	case "cmyk", "lab", "lch", "xyz", "yxy", "hsv":
		return true
	case "b-w", "grey16":
		return info.Metadata.ICC
	default:
		return false
	}
}

func preNormalizationFlatten(info Info) (background, maxAlpha string, err error) {
	switch info.Interpretation {
	case "cmyk":
		switch info.BandFormat {
		case "uchar":
			return "0,0,0,0", "", nil
		case "ushort":
			return "0,0,0,0", "65535", nil
		}
	case "b-w":
		if info.BandFormat == "uchar" {
			return "255", "", nil
		}
	case "grey16":
		if info.BandFormat == "ushort" {
			return "65535", "65535", nil
		}
	default:
		if info.BandFormat == "uchar" {
			return "255", "", nil
		}
	}
	return "", "", imageError(
		CodeAlphaUnsupported,
		"build plan",
		errors.New("flattening alpha in this color space and band format is not supported"),
	)
}

func flattenBackground(info Info, normalized bool) string {
	if normalized {
		return "255"
	}
	switch info.Interpretation {
	case "rgb16", "grey16":
		return "65535"
	default:
		return "255"
	}
}

func metadataKeep(metadata Metadata, policy CaptureDatePolicy) (string, bool) {
	values := make([]string, 0, 2)
	if metadata.ICC {
		values = append(values, "icc")
	}
	preserveCaptureDate := policy == CaptureDatePreserveWhenSafe &&
		metadata.CaptureDate &&
		!metadata.GPS
	if preserveCaptureDate {
		values = append(values, "exif")
	}
	if len(values) == 0 {
		return "none", false
	}
	return strings.Join(values, ","), preserveCaptureDate
}

func orientedDimensions(width, height, orientation int) (int, int) {
	switch orientation {
	case 5, 6, 7, 8:
		return height, width
	default:
		return width, height
	}
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
