package conversion

import (
	"context"
	"errors"
	"time"

	"github.com/guigui42/filetwist/internal/imageconv"
	"github.com/guigui42/filetwist/internal/media"
	"github.com/guigui42/filetwist/internal/probe"
	"github.com/guigui42/filetwist/internal/profiles"
)

// LocalConfig configures the subprocess-backed local conversion engines.
type LocalConfig struct {
	Run               probe.RunFunc
	Prober            *probe.Prober
	Acceleration      media.AccelerationConfig
	VAAPIReadiness    *media.VAAPIReadiness
	CommandTimeout    time.Duration
	ProbeTimeout      time.Duration
	TemporaryRoot     string
	VipsExecutable    string
	HeaderExecutable  string
	FFmpegExecutable  string
	FFprobeExecutable string
}

// NewLocal constructs a Service backed by libvips, FFmpeg, and ffprobe.
func NewLocal(config LocalConfig) (*Service, error) {
	if config.Run == nil {
		return nil, errors.New("conversion: run function must not be nil")
	}
	if config.Prober == nil {
		return nil, errors.New("conversion: prober must not be nil")
	}
	if config.CommandTimeout < 0 || config.ProbeTimeout < 0 {
		return nil, errors.New("conversion: timeouts must not be negative")
	}
	if config.CommandTimeout == 0 {
		config.CommandTimeout = 10 * time.Minute
	}
	if config.ProbeTimeout == 0 {
		config.ProbeTimeout = 30 * time.Second
	}
	if config.VipsExecutable == "" {
		config.VipsExecutable = "vips"
	}
	if config.HeaderExecutable == "" {
		config.HeaderExecutable = "vipsheader"
	}
	if config.FFmpegExecutable == "" {
		config.FFmpegExecutable = "ffmpeg"
	}
	if config.FFprobeExecutable == "" {
		config.FFprobeExecutable = "ffprobe"
	}

	return New(
		&localImageEngine{config: config},
		&localMediaEngine{config: config},
	)
}

type localImageEngine struct {
	config LocalConfig
}

func (engine *localImageEngine) Probe(ctx context.Context, inputPath string) (imageconv.Info, error) {
	headerPath, err := engine.executable(engine.config.HeaderExecutable)
	if err != nil {
		return imageconv.Info{}, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, engine.config.ProbeTimeout)
	defer cancel()
	return imageconv.ProbeFile(probeCtx, engine.config.Run, headerPath, inputPath)
}

func (engine *localImageEngine) Convert(
	ctx context.Context,
	request ImageRequest,
) (imageconv.Result, error) {
	// Reject unsafe headers before an optional capability probe decodes pixels.
	if err := imageconv.ValidateContent(request.Input, imageconv.DefaultLimits()); err != nil {
		return imageconv.Result{}, err
	}
	vipsPath, err := engine.executable(engine.config.VipsExecutable)
	if err != nil {
		return imageconv.Result{}, err
	}
	headerPath, err := engine.executable(engine.config.HeaderExecutable)
	if err != nil {
		return imageconv.Result{}, err
	}

	probeCtx, cancelProbe := context.WithTimeout(ctx, engine.config.ProbeTimeout)
	defer cancelProbe()
	var capabilities imageconv.CapabilitySet
	switch request.Input.Format {
	case imageconv.FormatHEIF:
		capabilities, err = imageconv.ProbeHEIF(
			probeCtx,
			engine.config.Prober,
			vipsPath,
			request.InputPath,
			engine.temporaryRoot(request.OutputDir),
		)
		if err != nil {
			return imageconv.Result{}, classifyDecodeProbeError(err, imageconv.CodeHEIFUnavailable, "probe HEIF decode")
		}
	case imageconv.FormatAVIF:
		capabilities, err = imageconv.ProbeAVIF(
			probeCtx,
			engine.config.Prober,
			vipsPath,
			request.InputPath,
			engine.temporaryRoot(request.OutputDir),
		)
		if err != nil {
			return imageconv.Result{}, classifyDecodeProbeError(err, imageconv.CodeAVIFUnavailable, "probe AVIF decode")
		}
	}
	cancelProbe()
	if err := imageconv.ValidateCapabilities(request.Input, capabilities); err != nil {
		return imageconv.Result{}, err
	}

	converter, err := imageconv.New(engine.config.Run, engine.config.Prober, imageconv.Config{
		VipsExecutable:    vipsPath,
		HeaderExecutable:  headerPath,
		Limits:            imageconv.DefaultLimits(),
		CaptureDatePolicy: imageconv.CaptureDateStrip,
		Capabilities:      capabilities,
		CommandTimeout:    engine.config.CommandTimeout,
		ProbeTimeout:      engine.config.ProbeTimeout,
	})
	if err != nil {
		return imageconv.Result{}, err
	}
	return converter.Convert(ctx, imageconv.Request{
		InputPath: request.InputPath,
		OutputDir: request.OutputDir,
		Operation: request.Operation,
		Input:     request.Input,
	})
}

func (engine *localImageEngine) executable(name string) (string, error) {
	if result, err := engine.config.Prober.Executable(name); err == nil {
		return result.Path, nil
	} else {
		return "", err
	}
}

func (engine *localImageEngine) temporaryRoot(outputDir string) string {
	if engine.config.TemporaryRoot != "" {
		return engine.config.TemporaryRoot
	}
	return outputDir
}

func classifyDecodeProbeError(err error, unavailableCode, stage string) error {
	var probeErr *probe.Error
	if errors.As(err, &probeErr) {
		return &imageconv.Error{
			Code:  unavailableCode,
			Stage: stage,
			Cause: err,
		}
	}
	return err
}

type localMediaEngine struct {
	config LocalConfig
}

func (engine *localMediaEngine) Probe(ctx context.Context, inputPath string) (media.Probe, error) {
	ffprobePath, err := engine.executable(engine.config.FFprobeExecutable)
	if err != nil {
		return media.Probe{}, err
	}
	result, _, err := media.ProbeFile(
		ctx,
		media.RunFunc(engine.config.Run),
		ffprobePath,
		inputPath,
		engine.config.ProbeTimeout,
	)
	return result, err
}

func (engine *localMediaEngine) Convert(ctx context.Context, request MediaRequest) (MediaResult, error) {
	ffmpegPath, err := engine.executable(engine.config.FFmpegExecutable)
	if err != nil {
		return MediaResult{}, err
	}
	ffprobePath, err := engine.executable(engine.config.FFprobeExecutable)
	if err != nil {
		return MediaResult{}, err
	}

	var readiness *media.VAAPIReadiness
	if isVideoOperation(request.Operation) && engine.config.Acceleration.Mode != media.AccelerationCPU {
		readiness = engine.config.VAAPIReadiness
		if readiness == nil {
			probeResult, probeErr := media.ProbeVAAPI(
				ctx,
				media.RunFunc(engine.config.Run),
				ffmpegPath,
				ffprobePath,
				engine.config.Acceleration,
				engine.config.ProbeTimeout,
			)
			readiness = &probeResult.Readiness
			if probeErr != nil && (errors.Is(probeErr, context.Canceled) ||
				errors.Is(probeErr, context.DeadlineExceeded)) {
				return MediaResult{}, probeErr
			}
		}
	}

	plan, err := media.BuildPlan(media.PlanRequest{
		Operation:    request.Operation,
		InputPath:    request.InputPath,
		OutputPath:   request.OutputPath,
		Input:        request.Input,
		Timeout:      engine.config.CommandTimeout,
		Acceleration: engine.config.Acceleration,
		VAAPI:        readiness,
	})
	if err != nil {
		return MediaResult{
			Warnings: mediaWarningsFromProbe(request.Input),
		}, err
	}
	result, err := media.Convert(
		ctx,
		media.RunFunc(engine.config.Run),
		ffmpegPath,
		ffprobePath,
		plan,
	)
	return MediaResult{
		Conversion: result,
		Warnings:   append([]media.Warning(nil), plan.Warnings...),
	}, err
}

func (engine *localMediaEngine) executable(name string) (string, error) {
	if result, err := engine.config.Prober.Executable(name); err == nil {
		return result.Path, nil
	} else {
		return "", err
	}
}

func isVideoOperation(operation profiles.Operation) bool {
	return operation == profiles.OperationCompatibleVideo ||
		operation == profiles.OperationSmallerVideo
}

func mediaWarningsFromProbe(input media.Probe) []media.Warning {
	return append([]media.Warning(nil), media.SelectStreams(input).Warnings...)
}
