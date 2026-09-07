package conversion_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/imageconv"
	"github.com/guigui42/filetwist/internal/media"
	"github.com/guigui42/filetwist/internal/probe"
)

type fakeImageEngine struct {
	probeResult  imageconv.Info
	probeErr     error
	convertErr   error
	convertCalls int
}

func (engine *fakeImageEngine) Probe(context.Context, string) (imageconv.Info, error) {
	return engine.probeResult, engine.probeErr
}

func (engine *fakeImageEngine) Convert(
	_ context.Context,
	request conversion.ImageRequest,
) (imageconv.Result, error) {
	engine.convertCalls++
	if engine.convertErr != nil {
		return imageconv.Result{}, engine.convertErr
	}
	name, err := imageconv.OutputName(request.InputPath, request.Operation)
	if err != nil {
		return imageconv.Result{}, err
	}
	path := filepath.Join(request.OutputDir, name)
	if err := os.WriteFile(path, []byte("converted image"), 0o600); err != nil {
		return imageconv.Result{}, err
	}
	return imageconv.Result{
		Output: imageconv.Output{Path: path},
	}, nil
}

type fakeMediaEngine struct {
	probeResult   media.Probe
	probeErr      error
	convertResult conversion.MediaResult
	convertErr    error
	convertCalls  int
}

type cancelingImageEngine struct {
	fakeImageEngine
	cancel context.CancelFunc
}

func (engine *cancelingImageEngine) Convert(ctx context.Context, request conversion.ImageRequest) (imageconv.Result, error) {
	result, err := engine.fakeImageEngine.Convert(ctx, request)
	engine.cancel()
	return result, err
}

func TestServiceDoesNotPublishExplicitImageAfterCancellation(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.png")
	if err := os.WriteFile(input, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := conversion.New(&cancelingImageEngine{
		fakeImageEngine: fakeImageEngine{probeResult: imageInfo()},
		cancel:          cancel,
	}, &fakeMediaEngine{})
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "output.jpg")
	_, err = service.Convert(ctx, conversion.Request{InputPath: input, Output: output})
	var classified *conversion.Error
	if !errors.As(err, &classified) || classified.Kind != conversion.FailureCanceled {
		t.Errorf("error = %v; want cancellation", err)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("canceled conversion published output: %v", err)
	}
}

func TestServicePreservesOptionalImageDecodeRejections(t *testing.T) {
	for _, code := range []string{imageconv.CodeHEIFUnavailable, imageconv.CodeAVIFUnavailable} {
		t.Run(code, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "input.bin")
			if err := os.WriteFile(input, []byte("fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			service, err := conversion.New(&fakeImageEngine{
				probeResult: imageInfo(),
				convertErr: &imageconv.Error{Code: code, Cause: &probe.Error{
					Stage: probe.StageFunctional, Cause: errors.New("decode unavailable"),
				}},
			}, &fakeMediaEngine{})
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Convert(context.Background(), conversion.Request{InputPath: input, Output: dir})
			var classified *conversion.Error
			if !errors.As(err, &classified) || classified.Kind != conversion.FailureRejection || classified.Code != code {
				t.Errorf("error = %+v; want rejection %s", classified, code)
			}
		})
	}
}

func (engine *fakeMediaEngine) Probe(context.Context, string) (media.Probe, error) {
	return engine.probeResult, engine.probeErr
}

func (engine *fakeMediaEngine) Convert(
	_ context.Context,
	request conversion.MediaRequest,
) (conversion.MediaResult, error) {
	engine.convertCalls++
	if engine.convertErr == nil {
		if err := os.WriteFile(request.OutputPath, []byte("converted media"), 0o600); err != nil {
			return conversion.MediaResult{}, err
		}
	}
	return engine.convertResult, engine.convertErr
}

func TestServiceRecommendsSafeDefaults(t *testing.T) {
	tests := []struct {
		name          string
		image         *fakeImageEngine
		media         *fakeMediaEngine
		inputName     string
		wantKind      corpus.MediaKind
		wantOperation corpus.Operation
		wantSuffix    string
	}{
		{
			name:          "image",
			image:         &fakeImageEngine{probeResult: imageInfo()},
			media:         &fakeMediaEngine{probeErr: errors.New("unused")},
			inputName:     "photo.png",
			wantKind:      corpus.MediaImage,
			wantOperation: corpus.OperationCompatiblePhoto,
			wantSuffix:    "photo-compatible.jpg",
		},
		{
			name:  "video",
			image: &fakeImageEngine{probeErr: errors.New("not image")},
			media: &fakeMediaEngine{
				probeResult: videoProbe(),
				convertResult: conversion.MediaResult{Conversion: media.ConversionResult{
					Execution: media.ExecutionMetadata{
						RequestedAcceleration: media.AccelerationAuto,
						InitialPath:           media.ExecutionPathCPU,
						FinalPath:             media.ExecutionPathCPU,
						AttemptCount:          1,
					},
				}},
			},
			inputName:     "clip.mov",
			wantKind:      corpus.MediaVideo,
			wantOperation: corpus.OperationCompatibleVideo,
			wantSuffix:    "clip-compatible.mp4",
		},
		{
			name:  "audio",
			image: &fakeImageEngine{probeErr: errors.New("not image")},
			media: &fakeMediaEngine{
				probeResult: audioProbe(),
				convertResult: conversion.MediaResult{Conversion: media.ConversionResult{
					Execution: media.ExecutionMetadata{
						RequestedAcceleration: media.AccelerationCPU,
						InitialPath:           media.ExecutionPathCPU,
						FinalPath:             media.ExecutionPathCPU,
						AttemptCount:          1,
					},
				}},
			},
			inputName:     "tone.wav",
			wantKind:      corpus.MediaAudio,
			wantOperation: corpus.OperationCompatibleAudio,
			wantSuffix:    "tone-compatible.mp3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			temp := t.TempDir()
			input := filepath.Join(temp, tt.inputName)
			if err := os.WriteFile(input, []byte("fixture"), 0o600); err != nil {
				t.Fatalf("write input: %v", err)
			}
			outputDir := filepath.Join(temp, "output")
			service, err := conversion.New(tt.image, tt.media)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			result, err := service.Convert(context.Background(), conversion.Request{
				InputPath: input,
				Output:    outputDir,
			})
			if err != nil {
				t.Fatalf("Convert() error = %v", err)
			}
			if result.DetectedMedia == nil || result.DetectedMedia.Kind != tt.wantKind {
				t.Errorf("detected media = %+v; want %s", result.DetectedMedia, tt.wantKind)
			}
			if result.SelectedOperation != tt.wantOperation || result.OperationSource != "recommended" {
				t.Errorf(
					"operation = %q (%s); want %q (recommended)",
					result.SelectedOperation,
					result.OperationSource,
					tt.wantOperation,
				)
			}
			if filepath.Base(result.OutputPath) != tt.wantSuffix {
				t.Errorf("output = %q; want suffix %q", result.OutputPath, tt.wantSuffix)
			}
			if result.Validation.Status != "passed" || result.Status != "success" {
				t.Errorf("result = %+v; want successful validation", result)
			}
		})
	}
}

func TestServiceRejectsIncompatibleOperationBeforeConversion(t *testing.T) {
	temp := t.TempDir()
	input := filepath.Join(temp, "photo.png")
	if err := os.WriteFile(input, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	image := &fakeImageEngine{probeResult: imageInfo()}
	mediaEngine := &fakeMediaEngine{}
	service, err := conversion.New(image, mediaEngine)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.Convert(context.Background(), conversion.Request{
		InputPath:    input,
		Output:       filepath.Join(temp, "output"),
		Operation:    corpus.OperationCompatibleVideo,
		OperationSet: true,
	})
	var conversionErr *conversion.Error
	if !errors.As(err, &conversionErr) || conversionErr.Kind != conversion.FailureRejection {
		t.Fatalf("Convert() error = %v; want rejection", err)
	}
	if conversionErr.Code != "incompatible_operation" {
		t.Errorf("error code = %q; want incompatible_operation", conversionErr.Code)
	}
	if image.convertCalls != 0 || mediaEngine.convertCalls != 0 {
		t.Fatalf("convert calls = image %d media %d; want zero", image.convertCalls, mediaEngine.convertCalls)
	}
	if result.OutputPath != "" {
		t.Errorf("output path = %q; want unresolved", result.OutputPath)
	}
}

func TestServiceDoesNotImplicitlyReplaceVideoWithAudio(t *testing.T) {
	input := videoProbe()
	input.Streams[0].ColorTransfer = "smpte2084"
	engine := &fakeMediaEngine{probeResult: input}
	service, err := conversion.New(&fakeImageEngine{probeErr: errors.New("not an image")}, engine)
	if err != nil {
		t.Fatal(err)
	}
	path := writeTemporaryFile(t)
	inspection, err := service.Inspect(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Recommended != corpus.OperationExtractAudio {
		t.Fatalf("inspection recommendation = %s; want extract_audio", inspection.Recommended)
	}
	output := filepath.Join(filepath.Dir(path), "output")
	result, err := service.Convert(context.Background(), conversion.Request{
		InputPath: path, Output: output,
	})
	var classified *conversion.Error
	if !errors.As(err, &classified) || classified.Kind != conversion.FailureRejection ||
		classified.Code != string(media.ErrorUnsupportedHDR) {
		t.Fatalf("implicit video conversion error = %v; want unsupported HDR rejection", err)
	}
	if result.SelectedOperation != corpus.OperationCompatibleVideo || engine.convertCalls != 0 {
		t.Fatalf("implicit default changed media kind or called the engine: %+v", result)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected default created output: %v", err)
	}
	result, err = service.Convert(context.Background(), conversion.Request{
		InputPath: path, Output: output,
		Operation: inspection.Recommended, OperationSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SelectedOperation != corpus.OperationExtractAudio ||
		result.OperationSource != "requested" || engine.convertCalls != 1 {
		t.Fatalf("explicit audio extraction did not use the selected operation: %+v", result)
	}
	if filepath.Ext(result.OutputPath) != ".m4a" {
		t.Errorf("output = %s; want audio output", result.OutputPath)
	}
}

func TestServiceRejectsIneligibleOperationBeforeWritingOutput(t *testing.T) {
	tests := []struct {
		name      string
		operation corpus.Operation
		image     imageconv.Info
		input     media.Probe
		code      string
	}{
		{
			name: "silent video extraction", operation: corpus.OperationExtractAudio,
			input: media.Probe{Streams: videoProbe().Streams[:1]},
			code:  string(media.ErrorNoSupportedAudio),
		},
		{
			name: "floating PCM lossless", operation: corpus.OperationLosslessAudio,
			input: media.Probe{Streams: []media.Stream{{CodecType: "audio", CodecName: "pcm_f32le", SampleRate: "48000", Channels: 2}}},
			code:  string(media.ErrorUnsupportedLosslessAudio),
		},
		{
			name: "oversized WebP", operation: corpus.OperationSmallerPhoto,
			image: imageconv.Info{
				Format: imageconv.FormatPNG, Width: 16384, Height: 2, Pages: 1,
				Bands: 3, BandFormat: "uchar", Interpretation: "srgb",
			},
			code: imageconv.CodeDimensionsExceeded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			image := &fakeImageEngine{probeResult: tt.image}
			if tt.image.Format == "" {
				image.probeErr = errors.New("not an image")
			}
			engine := &fakeMediaEngine{probeResult: tt.input}
			service, err := conversion.New(image, engine)
			if err != nil {
				t.Fatal(err)
			}
			input := writeTemporaryFile(t)
			output := filepath.Join(filepath.Dir(input), "output")
			_, err = service.Convert(context.Background(), conversion.Request{
				InputPath: input, Output: output, Operation: tt.operation, OperationSet: true,
			})
			var rejected *conversion.Error
			if !errors.As(err, &rejected) || rejected.Kind != conversion.FailureRejection || rejected.Code != tt.code {
				t.Fatalf("error = %v; want rejection %s", err, tt.code)
			}
			if engine.convertCalls != 0 || image.convertCalls != 0 {
				t.Fatal("ineligible conversion ran")
			}
			if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("ineligible conversion created output directory: %v", err)
			}
		})
	}
}

func TestServiceDoesNotTreatStillImageAsVideoWhenImageToolingFails(t *testing.T) {
	temp := t.TempDir()
	input := filepath.Join(temp, "photo.png")
	if err := os.WriteFile(input, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	imageProbeErr := &probe.Error{
		Stage:      probe.StagePresence,
		Executable: "vipsheader",
		Cause:      errors.New("not found"),
	}
	mediaEngine := &fakeMediaEngine{probeResult: media.Probe{
		Streams: []media.Stream{{
			Index:       0,
			CodecName:   "png",
			CodecType:   "video",
			Width:       2,
			Height:      2,
			PixelFormat: "rgba",
		}},
		Format: media.Format{FormatName: "png_pipe"},
	}}
	service, err := conversion.New(
		&fakeImageEngine{probeErr: imageProbeErr},
		mediaEngine,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.Convert(context.Background(), conversion.Request{
		InputPath: input,
		Output:    filepath.Join(temp, "output"),
	})
	var conversionErr *conversion.Error
	if !errors.As(err, &conversionErr) || conversionErr.Kind != conversion.FailureConfiguration {
		t.Fatalf("Convert() error = %v; want image configuration failure", err)
	}
	if conversionErr.Code != "image_converter_unavailable" {
		t.Errorf("error code = %q; want image_converter_unavailable", conversionErr.Code)
	}
	if result.DetectedMedia != nil || mediaEngine.convertCalls != 0 {
		t.Errorf("result = %+v, media calls = %d; still image must not become video", result, mediaEngine.convertCalls)
	}
}

func TestServiceTreatsMissingFFprobeAsConfigurationFailure(t *testing.T) {
	temp := t.TempDir()
	input := filepath.Join(temp, "clip.mov")
	if err := os.WriteFile(input, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	service, err := conversion.New(
		&fakeImageEngine{probeErr: errors.New("not image")},
		&fakeMediaEngine{probeErr: &probe.Error{
			Stage:      probe.StagePresence,
			Executable: "ffprobe",
			Cause:      errors.New("not found"),
		}},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.Convert(context.Background(), conversion.Request{
		InputPath: input,
		Output:    filepath.Join(temp, "output"),
	})
	var conversionErr *conversion.Error
	if !errors.As(err, &conversionErr) || conversionErr.Kind != conversion.FailureConfiguration {
		t.Fatalf("Convert() error = %v; want configuration failure", err)
	}
	if conversionErr.Code != "converter_unavailable" {
		t.Errorf("error code = %q; want converter_unavailable", conversionErr.Code)
	}
	if result.DetectedMedia != nil {
		t.Errorf("detected media = %+v; want none", result.DetectedMedia)
	}
}

func TestServiceTreatsUnsupportedAudioAsClearRejection(t *testing.T) {
	temp := t.TempDir()
	input := filepath.Join(temp, "spatial.mov")
	if err := os.WriteFile(input, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	mediaEngine := &fakeMediaEngine{
		probeResult: media.Probe{
			Streams: []media.Stream{{
				Index:      0,
				CodecName:  "apac",
				CodecType:  "audio",
				SampleRate: "48000",
				Channels:   6,
			}},
			Format: media.Format{
				FormatName:   "mov,mp4,m4a,3gp,3g2,mj2",
				DurationText: "1.000000",
			},
		},
		convertResult: conversion.MediaResult{Warnings: []media.Warning{{
			Code:        media.WarningUnsupportedAudioCodec,
			StreamIndex: 0,
			Codec:       "apac",
			Message:     "audio stream dropped because its codec is not supported",
		}}},
		convertErr: &media.PlanError{
			Code:    media.ErrorNoSupportedAudio,
			Message: "input does not contain an allowlisted audio stream",
		},
	}
	service, err := conversion.New(
		&fakeImageEngine{probeErr: errors.New("not image")},
		mediaEngine,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.Convert(context.Background(), conversion.Request{
		InputPath: input,
		Output:    filepath.Join(temp, "output"),
	})
	var conversionErr *conversion.Error
	if !errors.As(err, &conversionErr) || conversionErr.Kind != conversion.FailureRejection {
		t.Fatalf("Convert() error = %v; want clear rejection", err)
	}
	if result.DetectedMedia == nil || result.DetectedMedia.Kind != corpus.MediaAudio {
		t.Errorf("detected media = %+v; want audio", result.DetectedMedia)
	}
	if result.SelectedOperation != corpus.OperationCompatibleAudio {
		t.Errorf("operation = %q; want compatible_audio", result.SelectedOperation)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Codec != "apac" {
		t.Errorf("warnings = %+v; want APAC warning", result.Warnings)
	}
}

func TestServicePublishesImageToExplicitPath(t *testing.T) {
	temp := t.TempDir()
	input := filepath.Join(temp, "photo.png")
	target := filepath.Join(temp, "custom-name.jpg")
	if err := os.WriteFile(input, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	service, err := conversion.New(
		&fakeImageEngine{probeResult: imageInfo()},
		&fakeMediaEngine{},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.Convert(context.Background(), conversion.Request{
		InputPath:    input,
		Output:       target,
		Operation:    corpus.OperationCompatiblePhoto,
		OperationSet: true,
	})
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if result.OutputPath != target {
		t.Errorf("output path = %q; want %q", result.OutputPath, target)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(content) != "converted image" {
		t.Errorf("output content = %q; want converted image", content)
	}
}

func TestServiceReportsMediaWarningsAndValidationIssues(t *testing.T) {
	temp := t.TempDir()
	input := filepath.Join(temp, "clip.mov")
	if err := os.WriteFile(input, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	validationErr := &media.ValidationError{Issues: []media.ValidationIssue{{
		Code:     media.ValidationCodec,
		Field:    "streams.video.codec",
		Expected: "h264",
		Actual:   "hevc",
	}}}
	mediaEngine := &fakeMediaEngine{
		probeResult: videoProbe(),
		convertResult: conversion.MediaResult{
			Conversion: media.ConversionResult{
				Execution: media.ExecutionMetadata{
					RequestedAcceleration: media.AccelerationAuto,
					InitialPath:           media.ExecutionPathVAAPISoftwareDecode,
					FinalPath:             media.ExecutionPathCPU,
					FallbackReason:        media.FallbackVAAPIEncoder,
					AttemptCount:          2,
				},
			},
			Warnings: []media.Warning{{
				Code:        media.WarningUnsupportedAudioCodec,
				StreamIndex: 2,
				Codec:       "apac",
				Message:     "audio stream dropped because its codec is not supported",
			}},
		},
		convertErr: &media.ConversionError{
			Stage: media.ConversionStageValidate,
			Cause: validationErr,
		},
	}
	service, err := conversion.New(
		&fakeImageEngine{probeErr: errors.New("not image")},
		mediaEngine,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.Convert(context.Background(), conversion.Request{
		InputPath:    input,
		Output:       filepath.Join(temp, "output.mp4"),
		Operation:    corpus.OperationCompatibleVideo,
		OperationSet: true,
	})
	var conversionErr *conversion.Error
	if !errors.As(err, &conversionErr) || conversionErr.Kind != conversion.FailureValidation {
		t.Fatalf("Convert() error = %v; want validation failure", err)
	}
	if result.Validation.Status != "failed" || len(result.Validation.Issues) != 1 {
		t.Errorf("validation = %+v; want one failed issue", result.Validation)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Codec != "apac" {
		t.Errorf("warnings = %+v; want APAC warning", result.Warnings)
	}
	if result.Execution.Initial != string(media.ExecutionPathVAAPISoftwareDecode) ||
		result.Execution.Final != string(media.ExecutionPathCPU) ||
		result.Execution.FallbackReason != string(media.FallbackVAAPIEncoder) {
		t.Errorf("execution = %+v; want visible CPU fallback", result.Execution)
	}
}

func TestServiceReportsImageOutputValidationFailures(t *testing.T) {
	for _, code := range []string{imageconv.CodeOutputPrecision, imageconv.CodeOutputFormat} {
		t.Run(code, func(t *testing.T) {
			info := imageInfo()
			info.BandFormat, info.Interpretation = "ushort", "rgb16"
			engineErr := &imageconv.Error{
				Code: code, Stage: "validate output", Cause: errors.New("output differs from expected profile"),
			}
			service, err := conversion.New(&fakeImageEngine{
				probeResult: info, convertErr: engineErr,
			}, &fakeMediaEngine{})
			if err != nil {
				t.Fatal(err)
			}
			input := writeTemporaryFile(t)
			result, err := service.Convert(context.Background(), conversion.Request{
				InputPath: input, Output: filepath.Join(filepath.Dir(input), "output"),
				Operation: corpus.OperationLosslessImage, OperationSet: true,
			})
			var classified *conversion.Error
			if !errors.As(err, &classified) || classified.Kind != conversion.FailureValidation || classified.Code != code {
				t.Fatalf("error = %+v; want validation failure %s", classified, code)
			}
			if !errors.Is(err, engineErr) {
				t.Error("validation failure lost its image engine cause")
			}
			if result.Status != "error" || result.Validation.Status != "failed" || result.Error != classified {
				t.Fatalf("result did not retain validation failure: %+v", result)
			}
		})
	}
}

func TestServiceRejectsExistingOutputWithoutCallingEngine(t *testing.T) {
	temp := t.TempDir()
	input := filepath.Join(temp, "tone.wav")
	output := filepath.Join(temp, "tone-compatible.mp3")
	if err := os.WriteFile(input, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	if err := os.WriteFile(output, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write output: %v", err)
	}
	mediaEngine := &fakeMediaEngine{probeResult: audioProbe()}
	service, err := conversion.New(
		&fakeImageEngine{probeErr: errors.New("not image")},
		mediaEngine,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.Convert(context.Background(), conversion.Request{
		InputPath:    input,
		Output:       output,
		Operation:    corpus.OperationCompatibleAudio,
		OperationSet: true,
	})
	var conversionErr *conversion.Error
	if !errors.As(err, &conversionErr) || conversionErr.Code != "output_exists" {
		t.Fatalf("Convert() error = %v; want output_exists", err)
	}
	if mediaEngine.convertCalls != 0 {
		t.Errorf("convert calls = %d; want zero", mediaEngine.convertCalls)
	}
	if result.OutputPath != output {
		t.Errorf("result output = %q; want %q", result.OutputPath, output)
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(content) != "keep" {
		t.Errorf("existing output = %q; want keep", content)
	}
}

func imageInfo() imageconv.Info {
	return imageconv.Info{
		Format:         imageconv.FormatPNG,
		MIMEType:       "image/png",
		Width:          640,
		Height:         480,
		Bands:          3,
		BandFormat:     "uchar",
		Interpretation: "srgb",
		Orientation:    1,
		Pages:          1,
	}
}

func videoProbe() media.Probe {
	return media.Probe{
		Streams: []media.Stream{
			{
				Index:       0,
				CodecName:   "hevc",
				CodecType:   "video",
				Width:       1920,
				Height:      1080,
				PixelFormat: "yuv420p",
			},
			{
				Index:         1,
				CodecName:     "aac",
				CodecType:     "audio",
				SampleRate:    "48000",
				Channels:      2,
				ChannelLayout: "stereo",
			},
		},
		Format: media.Format{
			FormatName:   "mov,mp4,m4a,3gp,3g2,mj2",
			DurationText: "2.000000",
		},
	}
}

func audioProbe() media.Probe {
	return media.Probe{
		Streams: []media.Stream{{
			Index:         0,
			CodecName:     "pcm_s16le",
			CodecType:     "audio",
			SampleRate:    "8000",
			Channels:      1,
			ChannelLayout: "mono",
		}},
		Format: media.Format{
			FormatName:   "wav",
			DurationText: "1.000000",
		},
	}
}
