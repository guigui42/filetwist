package conversion

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/imageconv"
	"github.com/guigui42/filetwist/internal/media"
	"github.com/guigui42/filetwist/internal/probe"
)

const (
	validationNotRun = "not_run"
	validationPassed = "passed"
	validationFailed = "failed"
)

// Service probes local media and dispatches named operations to existing engines.
type Service struct {
	image ImageEngine
	media MediaEngine
}

// New constructs a conversion Service with injectable image and media engines.
func New(image ImageEngine, mediaEngine MediaEngine) (*Service, error) {
	if image == nil {
		return nil, errors.New("conversion: image engine must not be nil")
	}
	if mediaEngine == nil {
		return nil, errors.New("conversion: media engine must not be nil")
	}
	return &Service{image: image, media: mediaEngine}, nil
}

// Convert probes, selects or validates an operation, converts, and reports the result.
func (service *Service) Convert(ctx context.Context, request Request) (result Result, returnErr error) {
	started := time.Now()
	result = Result{
		SchemaVersion: SchemaVersion,
		Status:        "error",
		Validation:    ValidationResult{Status: validationNotRun},
		Warnings:      []Warning{},
	}
	defer func() {
		result.Timing.TotalMillis = milliseconds(time.Since(started))
		if conversionErr, ok := returnErr.(*Error); ok {
			result.Error = conversionErr
		}
	}()

	if ctx == nil {
		return result, failure(FailureConfiguration, "invalid_context", "conversion context is required", nil)
	}
	if err := validateRequest(request); err != nil {
		return result, err
	}

	probeStarted := time.Now()
	detection, imageInfo, mediaProbe, err := service.detect(ctx, request.InputPath)
	result.Timing.ProbeMillis = milliseconds(time.Since(probeStarted))
	if err != nil {
		return result, err
	}
	result.DetectedMedia = &detection
	if detection.Kind == corpus.MediaAudio || detection.Kind == corpus.MediaVideo {
		result.Warnings = mediaWarnings(media.SelectStreams(mediaProbe).Warnings)
	}

	operation := request.Operation
	if request.OperationSet {
		result.OperationSource = "requested"
	} else {
		operation = recommendedOperation(detection.Kind)
		result.OperationSource = "recommended"
	}
	result.SelectedOperation = operation
	if err := validateOperation(operation, detection.Kind, imageInfo, mediaProbe); err != nil {
		return result, err
	}

	outputPath, outputIsDirectory, err := resolveOutput(request.InputPath, request.Output, operation)
	result.OutputPath = outputPath
	if err != nil {
		return result, err
	}

	conversionStarted := time.Now()
	var conversionErr error
	switch detection.Kind {
	case corpus.MediaImage:
		conversionErr = service.convertImage(ctx, request.InputPath, outputPath, outputIsDirectory, operation, imageInfo, &result)
	case corpus.MediaAudio, corpus.MediaVideo:
		conversionErr = service.convertMedia(ctx, request.InputPath, outputPath, operation, mediaProbe, &result)
	default:
		conversionErr = failure(FailureProbe, "unsupported_media", "input is not supported image, audio, or video content", nil)
	}
	result.Timing.ConversionMillis = milliseconds(time.Since(conversionStarted))
	if conversionErr != nil {
		return result, conversionErr
	}

	result.Status = "success"
	result.Validation.Status = validationPassed
	return result, nil
}

func validateRequest(request Request) *Error {
	if strings.TrimSpace(request.InputPath) == "" {
		return failure(FailureConfiguration, "missing_input", "input path is required", nil)
	}
	if strings.TrimSpace(request.Output) == "" {
		return failure(FailureConfiguration, "missing_output", "output path or directory is required", nil)
	}
	if request.OperationSet {
		if _, err := ParseOperation(string(request.Operation)); err != nil {
			return failure(FailureConfiguration, "invalid_operation", "operation is not supported", err)
		}
	}
	info, err := os.Stat(request.InputPath)
	if err != nil {
		return failure(FailureConfiguration, "invalid_input", "input must be an accessible local file", err)
	}
	if !info.Mode().IsRegular() {
		return failure(FailureConfiguration, "invalid_input", "input must be a regular local file", nil)
	}
	return nil
}

func (service *Service) detect(
	ctx context.Context,
	inputPath string,
) (DetectedMedia, imageconv.Info, media.Probe, *Error) {
	imageInfo, imageErr := service.image.Probe(ctx, inputPath)
	if imageErr == nil {
		return detectedImage(imageInfo), imageInfo, media.Probe{}, nil
	}
	if err := probeContextFailure(imageErr); err != nil {
		return DetectedMedia{}, imageconv.Info{}, media.Probe{}, err
	}

	mediaProbe, mediaErr := service.media.Probe(ctx, inputPath)
	if mediaErr == nil {
		if looksLikeStillImage(mediaProbe) {
			return DetectedMedia{}, imageconv.Info{}, media.Probe{}, classifyImageError(imageErr)
		}
		detection, err := detectedMedia(mediaProbe)
		if err == nil {
			return detection, imageconv.Info{}, mediaProbe, nil
		}
		mediaErr = err
	}
	if err := probeContextFailure(mediaErr); err != nil {
		return DetectedMedia{}, imageconv.Info{}, media.Probe{}, err
	}
	var mediaProbeErr *probe.Error
	if errors.As(mediaErr, &mediaProbeErr) && mediaProbeErr.Stage == probe.StagePresence {
		return DetectedMedia{}, imageconv.Info{}, media.Probe{}, failure(
			FailureConfiguration,
			"converter_unavailable",
			"required media probe executables are unavailable",
			errors.Join(imageErr, mediaErr),
		)
	}
	return DetectedMedia{}, imageconv.Info{}, media.Probe{}, failure(
		FailureProbe,
		"content_probe_failed",
		"input content could not be identified",
		errors.Join(imageErr, mediaErr),
	)
}

func (service *Service) convertImage(
	ctx context.Context,
	inputPath string,
	outputPath string,
	outputIsDirectory bool,
	operation corpus.Operation,
	info imageconv.Info,
	result *Result,
) error {
	result.Execution = ExecutionResult{
		Requested:    "libvips",
		Initial:      "libvips",
		Final:        "libvips",
		AttemptCount: 1,
	}

	outputDir := filepath.Dir(outputPath)
	var stagingDir string
	if !outputIsDirectory {
		var err error
		stagingDir, err = os.MkdirTemp(filepath.Dir(outputPath), ".filetwist-image-output-*")
		if err != nil {
			return failure(FailureConfiguration, "invalid_output", "output directory is not writable", err)
		}
		defer func() {
			_ = os.RemoveAll(stagingDir)
		}()
		outputDir = stagingDir
	}

	imageResult, err := service.image.Convert(ctx, ImageRequest{
		InputPath: inputPath,
		OutputDir: outputDir,
		Operation: operation,
		Input:     info,
	})
	if imageResult.ObservedProperties.Container != "" {
		observed := imageResult.ObservedProperties
		result.Observed = &observed
	}
	if err != nil {
		classified := classifyImageError(err)
		setValidationFailure(result, classified, nil)
		return classified
	}

	if !outputIsDirectory {
		if err := ctx.Err(); err != nil {
			return contextFailure(err)
		}
		if err := publishNoReplace(imageResult.Output.Path, outputPath); err != nil {
			if errors.Is(err, os.ErrExist) {
				return failure(FailureRejection, "output_exists", "output already exists", err)
			}
			return failure(FailureConversion, "publish_failed", "validated output could not be published", err)
		}
	}
	return nil
}

func (service *Service) convertMedia(
	ctx context.Context,
	inputPath string,
	outputPath string,
	operation corpus.Operation,
	input media.Probe,
	result *Result,
) error {
	mediaResult, err := service.media.Convert(ctx, MediaRequest{
		InputPath:  inputPath,
		OutputPath: outputPath,
		Operation:  operation,
		Input:      input,
	})
	if mediaResult.Conversion.Output.Format.FormatName != "" ||
		len(mediaResult.Conversion.Output.Streams) > 0 {
		observed := mediaCorpusProperties(mediaResult.Conversion.Output)
		result.Observed = &observed
	}
	result.Warnings = mediaWarnings(mediaResult.Warnings)
	result.Execution = executionResult(mediaResult.Conversion.Execution)
	if err != nil {
		classified := classifyMediaError(err)
		var validationErr *media.ValidationError
		if errors.As(err, &validationErr) {
			issues := make([]ValidationIssue, 0, len(validationErr.Issues))
			for _, issue := range validationErr.Issues {
				issues = append(issues, ValidationIssue{
					Code:     string(issue.Code),
					Field:    issue.Field,
					Expected: issue.Expected,
					Actual:   issue.Actual,
				})
			}
			setValidationFailure(result, classified, issues)
		} else {
			setValidationFailure(result, classified, nil)
		}
		return classified
	}
	return nil
}

func detectedImage(info imageconv.Info) DetectedMedia {
	properties := info.CorpusProperties()
	stream := properties.Streams[0]
	return DetectedMedia{
		Kind:     corpus.MediaImage,
		Format:   properties.Container,
		MIMEType: info.MIMEType,
		Width:    info.Width,
		Height:   info.Height,
		Streams: []DetectedStream{{
			Index:  0,
			Kind:   string(stream.Kind),
			Codec:  stream.Codec,
			Width:  info.Width,
			Height: info.Height,
		}},
	}
}

func detectedMedia(input media.Probe) (DetectedMedia, error) {
	var (
		hasVideo bool
		hasAudio bool
	)
	for _, stream := range input.Streams {
		switch stream.CodecType {
		case "video":
			if stream.Disposition.AttachedPic == 0 {
				hasVideo = true
			}
		case "audio":
			hasAudio = true
		}
	}
	kind := corpus.MediaAudio
	if hasVideo {
		kind = corpus.MediaVideo
	} else if !hasAudio {
		return DetectedMedia{}, errors.New("no usable audio or video streams")
	}

	detected := DetectedMedia{
		Kind:    kind,
		Format:  input.Format.FormatName,
		Streams: make([]DetectedStream, 0, len(input.Streams)),
	}
	if duration, ok := input.Format.Duration(); ok {
		detected.DurationMillis = duration.Milliseconds()
	}
	for _, stream := range input.Streams {
		if stream.CodecType != "audio" && stream.CodecType != "video" {
			continue
		}
		sampleRate, _ := strconv.Atoi(stream.SampleRate)
		detected.Streams = append(detected.Streams, DetectedStream{
			Index:      stream.Index,
			Kind:       stream.CodecType,
			Codec:      stream.CodecName,
			Width:      stream.Width,
			Height:     stream.Height,
			Channels:   stream.Channels,
			SampleRate: sampleRate,
		})
	}
	if kind == corpus.MediaVideo {
		for _, stream := range input.Streams {
			if stream.CodecType == "video" && stream.Disposition.AttachedPic == 0 {
				detected.Width, detected.Height = displayedDimensions(stream)
				break
			}
		}
	}
	return detected, nil
}

func looksLikeStillImage(input media.Probe) bool {
	for _, format := range strings.Split(strings.ToLower(input.Format.FormatName), ",") {
		switch strings.TrimSpace(format) {
		case "apng", "avif", "bmp_pipe", "gif", "heif", "heic",
			"image2", "image2pipe", "jpeg_pipe", "jxl_pipe", "png_pipe",
			"tiff_pipe", "webp_pipe":
			return true
		}
	}
	return false
}

func displayedDimensions(stream media.Stream) (int, int) {
	if rotation := stream.Rotation(); rotation == 90 || rotation == 270 {
		return stream.Height, stream.Width
	}
	return stream.Width, stream.Height
}

func recommendedOperation(kind corpus.MediaKind) corpus.Operation {
	switch kind {
	case corpus.MediaImage:
		return corpus.OperationCompatiblePhoto
	case corpus.MediaVideo:
		return corpus.OperationCompatibleVideo
	default:
		return corpus.OperationCompatibleAudio
	}
}

func operationAccepts(operation corpus.Operation, kind corpus.MediaKind) bool {
	switch operation {
	case corpus.OperationCompatiblePhoto, corpus.OperationSmallerPhoto, corpus.OperationLosslessImage:
		return kind == corpus.MediaImage
	case corpus.OperationCompatibleVideo, corpus.OperationSmallerVideo:
		return kind == corpus.MediaVideo
	case corpus.OperationExtractAudio:
		return kind == corpus.MediaVideo || kind == corpus.MediaAudio
	case corpus.OperationCompatibleAudio, corpus.OperationLosslessAudio:
		return kind == corpus.MediaAudio
	default:
		return false
	}
}

func resolveOutput(inputPath, target string, operation corpus.Operation) (string, bool, *Error) {
	name := outputName(inputPath, operation)
	expectedExtension := filepath.Ext(name)

	info, err := os.Stat(target)
	switch {
	case err == nil && info.IsDir():
		if err := os.MkdirAll(target, 0o755); err != nil {
			return "", false, failure(FailureConfiguration, "invalid_output", "output directory is not writable", err)
		}
		return filepath.Join(target, name), true, nil
	case err == nil:
		return target, false, failure(FailureRejection, "output_exists", "output already exists", os.ErrExist)
	case !errors.Is(err, os.ErrNotExist):
		return "", false, failure(FailureConfiguration, "invalid_output", "output location is not accessible", err)
	}

	isDirectory := strings.HasSuffix(target, string(filepath.Separator)) || filepath.Ext(target) == ""
	if isDirectory {
		if err := os.MkdirAll(target, 0o755); err != nil {
			return "", false, failure(FailureConfiguration, "invalid_output", "output directory could not be created", err)
		}
		return filepath.Join(target, name), true, nil
	}
	if !strings.EqualFold(filepath.Ext(target), expectedExtension) {
		return "", false, failure(
			FailureConfiguration,
			"invalid_output_extension",
			fmt.Sprintf("output path must use the %s extension for %s", expectedExtension, operation),
			nil,
		)
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", false, failure(FailureConfiguration, "invalid_output", "output directory could not be created", err)
	}
	return target, false, nil
}

func outputName(inputPath string, operation corpus.Operation) string {
	if name, err := imageconv.OutputName(inputPath, operation); err == nil {
		return name
	}
	stem := sanitizeStem(strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath)))
	if stem == "" {
		stem = "media"
	}
	switch operation {
	case corpus.OperationCompatibleVideo:
		return stem + "-compatible.mp4"
	case corpus.OperationSmallerVideo:
		return stem + "-smaller.mp4"
	case corpus.OperationExtractAudio:
		return stem + "-audio.m4a"
	case corpus.OperationCompatibleAudio:
		return stem + "-compatible.mp3"
	case corpus.OperationLosslessAudio:
		return stem + "-lossless.flac"
	default:
		return stem + "-converted"
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

func publishNoReplace(source, target string) error {
	return publishNoReplaceWith(source, target, os.Link, os.Remove)
}

func publishNoReplaceWith(
	source string,
	target string,
	link func(string, string) error,
	remove func(string) error,
) error {
	if link == nil || remove == nil {
		return errors.New("filesystem helpers are required")
	}
	if err := link(source, target); err != nil {
		return err
	}
	_ = remove(source)
	return nil
}

func mediaWarnings(warnings []media.Warning) []Warning {
	result := make([]Warning, 0, len(warnings))
	for _, warning := range warnings {
		index := warning.StreamIndex
		result = append(result, Warning{
			Code:        string(warning.Code),
			Message:     warning.Message,
			StreamIndex: &index,
			Codec:       warning.Codec,
		})
	}
	return result
}

func mediaCorpusProperties(probed media.Probe) corpus.MediaProperties {
	metadata := media.DetectSensitiveMetadata(probed)
	properties := corpus.MediaProperties{
		Container: normalizedContainer(probed.Format.FormatName),
		Alpha:     corpus.PresenceIgnored,
		Metadata: corpus.Metadata{
			GPS:          metadataPresence(metadata.GPS),
			ColorProfile: corpus.PresenceIgnored,
			EXIF:         metadataPresence(metadata.EXIF),
			XMP:          metadataPresence(metadata.XMP),
			GainMap:      metadataPresence(metadata.GainMap),
		},
	}
	if duration, ok := probed.Format.Duration(); ok {
		milliseconds := duration.Milliseconds()
		properties.DurationMillis = &milliseconds
	}

	type streamKey struct {
		kind        corpus.StreamKind
		codec       string
		channels    int
		sampleRate  int
		pixelFormat string
	}
	streamIndexes := make(map[streamKey]int)
	for _, stream := range probed.Streams {
		key := streamKey{
			codec:       normalizedToken(stream.CodecName),
			channels:    stream.Channels,
			pixelFormat: normalizedToken(stream.PixelFormat),
		}
		switch stream.CodecType {
		case "video":
			if stream.Disposition.AttachedPic != 0 {
				key.kind = corpus.StreamImage
			} else {
				key.kind = corpus.StreamVideo
			}
		case "audio":
			key.kind = corpus.StreamAudio
			key.sampleRate, _ = strconv.Atoi(stream.SampleRate)
		case "subtitle":
			key.kind = corpus.StreamSubtitle
		default:
			key.kind = corpus.StreamData
		}
		if key.codec == "" {
			key.codec = "unknown"
		}

		index, ok := streamIndexes[key]
		if !ok {
			corpusStream := corpus.Stream{
				Kind:        key.kind,
				Codec:       key.codec,
				Count:       1,
				PixelFormat: key.pixelFormat,
			}
			if key.channels > 0 {
				channels := key.channels
				corpusStream.Channels = &channels
			}
			if key.sampleRate > 0 {
				sampleRate := key.sampleRate
				corpusStream.SampleRate = &sampleRate
			}
			properties.Streams = append(properties.Streams, corpusStream)
			streamIndexes[key] = len(properties.Streams) - 1
		} else {
			properties.Streams[index].Count++
		}

		if key.kind == corpus.StreamVideo && properties.Dimensions == nil {
			width, height := displayedDimensions(stream)
			properties.Dimensions = &corpus.Dimensions{Width: width, Height: height}
			rotation := stream.Rotation()
			properties.Orientation = &corpus.Orientation{
				RotationDegrees: rotation,
				PixelNormalized: rotation == 0,
			}
		}
	}
	return properties
}

func normalizedContainer(value string) string {
	for _, candidate := range strings.Split(strings.ToLower(value), ",") {
		switch strings.TrimSpace(candidate) {
		case "mov", "mp4", "m4a", "3gp", "3g2", "mj2":
			return "mp4"
		case "matroska", "webm":
			return strings.TrimSpace(candidate)
		case "wav":
			return "wav"
		case "mp3":
			return "mp3"
		case "flac":
			return "flac"
		case "ogg":
			return "ogg"
		}
	}
	return normalizedToken(strings.Split(value, ",")[0])
}

func normalizedToken(value string) string {
	var builder strings.Builder
	separator := false
	for _, character := range strings.ToLower(strings.TrimSpace(value)) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			builder.WriteRune(character)
			separator = false
			continue
		}
		if builder.Len() > 0 && !separator {
			builder.WriteByte('_')
			separator = true
		}
	}
	return strings.Trim(builder.String(), "_")
}

func metadataPresence(present bool) corpus.Presence {
	if present {
		return corpus.PresenceRequired
	}
	return corpus.PresenceForbidden
}

func executionResult(execution media.ExecutionMetadata) ExecutionResult {
	return ExecutionResult{
		Requested:      string(execution.RequestedAcceleration),
		Initial:        string(execution.InitialPath),
		Final:          string(execution.FinalPath),
		FallbackReason: string(execution.FallbackReason),
		AttemptCount:   execution.AttemptCount,
	}
}

func classifyImageError(err error) *Error {
	if canceled := contextFailure(err); canceled != nil {
		return canceled
	}
	var dependencyErr *probe.Error
	if errors.As(err, &dependencyErr) && dependencyErr.Stage == probe.StagePresence {
		return failure(FailureConfiguration, "image_converter_unavailable", "required image converter executable is unavailable", err)
	}
	var imageErr *imageconv.Error
	if !errors.As(err, &imageErr) {
		if dependencyErr != nil {
			return failure(FailureProbe, "image_probe_failed", "image probing failed", err)
		}
		return failure(FailureConversion, "image_conversion_failed", "image conversion failed", err)
	}
	switch imageErr.Code {
	case imageconv.CodeUnsupportedInput,
		imageconv.CodeHEIFUnavailable,
		imageconv.CodeAVIFUnavailable,
		imageconv.CodeAnimatedUnsupported,
		imageconv.CodeHDRUnsupported,
		imageconv.CodeColorUnsupported,
		imageconv.CodeAlphaUnsupported,
		imageconv.CodeDimensionsExceeded:
		return failure(FailureRejection, imageErr.Code, "image input is not supported by the selected operation", err)
	case imageconv.CodeInvalidRequest:
		if isImageOutputPreparationFailure(imageErr) {
			return failure(FailureConfiguration, "invalid_output", "output directory is not writable", err)
		}
		return failure(FailureConfiguration, imageErr.Code, "image conversion request is invalid", err)
	case imageconv.CodeProbeFailed:
		if isImageOutputPreparationFailure(imageErr) {
			return failure(FailureConfiguration, "invalid_output", "output directory is not writable", err)
		}
		return failure(FailureProbe, imageErr.Code, "image probing failed", err)
	case imageconv.CodeOutputExists:
		return failure(FailureRejection, "output_exists", "output already exists", err)
	case imageconv.CodeOutputFormat,
		imageconv.CodeOutputDimensions,
		imageconv.CodeOutputOrientation,
		imageconv.CodeOutputAlpha,
		imageconv.CodeOutputPrecision,
		imageconv.CodeOutputColor,
		imageconv.CodeOutputMetadata:
		return failure(FailureValidation, imageErr.Code, "image output failed compatibility validation", err)
	default:
		return failure(FailureConversion, imageErr.Code, "image conversion failed", err)
	}
}

func isImageOutputPreparationFailure(err *imageconv.Error) bool {
	return err.Stage == "prepare output directory" ||
		err.Stage == "prepare work directory" ||
		err.Stage == "create image decode probe directory"
}

func classifyMediaError(err error) *Error {
	if canceled := contextFailure(err); canceled != nil {
		return canceled
	}
	var dependencyErr *probe.Error
	if errors.As(err, &dependencyErr) {
		kind := FailureProbe
		code := "media_probe_failed"
		message := "media probing failed"
		if dependencyErr.Stage == probe.StagePresence {
			kind = FailureConfiguration
			code = "media_converter_unavailable"
			message = "required media converter executable is unavailable"
		}
		return failure(kind, code, message, err)
	}
	if errors.Is(err, media.ErrOutputExists) {
		return failure(FailureRejection, "output_exists", "output already exists", err)
	}
	var planErr *media.PlanError
	if errors.As(err, &planErr) {
		if planErr.Code == media.ErrorInvalidRequest {
			return failure(FailureConfiguration, string(planErr.Code), "media conversion request is invalid", err)
		}
		return failure(FailureRejection, string(planErr.Code), planErr.Message, err)
	}
	var conversionErr *media.ConversionError
	if errors.As(err, &conversionErr) {
		switch conversionErr.Stage {
		case media.ConversionStagePrepare:
			return failure(FailureConfiguration, "invalid_output", "output directory is not writable", err)
		case media.ConversionStageProbe:
			return failure(FailureProbe, "output_probe_failed", "converted output could not be probed", err)
		case media.ConversionStageValidate:
			return failure(FailureValidation, "output_validation_failed", "media output failed compatibility validation", err)
		default:
			return failure(FailureConversion, "media_conversion_failed", "media conversion failed", err)
		}
	}
	return failure(FailureConversion, "media_conversion_failed", "media conversion failed", err)
}

func contextFailure(err error) *Error {
	switch {
	case errors.Is(err, context.Canceled):
		return failure(FailureCanceled, "canceled", "conversion was canceled", err)
	case errors.Is(err, context.DeadlineExceeded):
		return failure(FailureConversion, "timeout", "conversion timed out", err)
	default:
		return nil
	}
}

func probeContextFailure(err error) *Error {
	switch {
	case errors.Is(err, context.Canceled):
		return failure(FailureCanceled, "canceled", "conversion was canceled", err)
	case errors.Is(err, context.DeadlineExceeded):
		return failure(FailureProbe, "probe_timeout", "content probing timed out", err)
	default:
		return nil
	}
}

func setValidationFailure(result *Result, err *Error, issues []ValidationIssue) {
	if err.Kind != FailureValidation {
		return
	}
	result.Validation = ValidationResult{
		Status: validationFailed,
		Issues: issues,
	}
}

func failure(kind FailureKind, code, message string, cause error) *Error {
	return &Error{
		Kind:    kind,
		Code:    code,
		Message: message,
		Cause:   cause,
	}
}
