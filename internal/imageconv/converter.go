package imageconv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/guigui42/filetwist/internal/probe"
	"github.com/guigui42/filetwist/internal/profiles"
	"github.com/guigui42/filetwist/internal/runner"
)

// Converter probes, converts, validates, and publishes still image outputs.
type Converter struct {
	run    probe.RunFunc
	prober *probe.Prober
	config Config
}

// New constructs an image Converter around the shared runner and prober contracts.
func New(run probe.RunFunc, prober *probe.Prober, config Config) (*Converter, error) {
	if run == nil {
		return nil, errors.New("imageconv: run function must not be nil")
	}
	if prober == nil {
		return nil, errors.New("imageconv: prober must not be nil")
	}
	if config.VipsExecutable == "" {
		config.VipsExecutable = "vips"
	}
	if config.HeaderExecutable == "" {
		config.HeaderExecutable = "vipsheader"
	}
	if config.Limits == (Limits{}) {
		config.Limits = DefaultLimits()
	}
	if config.CommandTimeout < 0 || config.ProbeTimeout < 0 {
		return nil, errors.New("imageconv: timeouts must not be negative")
	}
	if config.ProbeTimeout == 0 {
		config.ProbeTimeout = 30 * time.Second
	}
	if err := ValidateContent(Info{
		Format:         FormatJPEG,
		Width:          1,
		Height:         1,
		Bands:          3,
		BandFormat:     "uchar",
		Interpretation: "srgb",
		Pages:          1,
	}, config.Limits); err != nil {
		return nil, fmt.Errorf("imageconv: invalid limits: %w", err)
	}
	return &Converter{run: run, prober: prober, config: config}, nil
}

// Convert executes one named operation and publishes only a validated output.
func (converter *Converter) Convert(ctx context.Context, request Request) (conversion Result, returnErr error) {
	return converter.convertWithFS(ctx, request, imageFilesystem{
		mkdirTemp: os.MkdirTemp,
		removeAll: os.RemoveAll,
		link:      os.Link,
		remove:    os.Remove,
	})
}

type imageFilesystem struct {
	mkdirTemp func(string, string) (string, error)
	removeAll func(string) error
	link      func(string, string) error
	remove    func(string) error
}

func (converter *Converter) convertWithFS(
	ctx context.Context,
	request Request,
	fs imageFilesystem,
) (conversion Result, returnErr error) {
	if ctx == nil {
		return conversion, imageError(CodeInvalidRequest, "convert", errors.New("context must not be nil"))
	}
	if request.InputPath == "" || request.OutputDir == "" {
		return conversion, imageError(CodeInvalidRequest, "convert", errors.New("input and output paths are required"))
	}
	input := request.Input
	conversion.Input = input
	if input.Format == "" || input.Width <= 0 || input.Height <= 0 {
		return conversion, imageError(CodeInvalidRequest, "convert", errors.New("fresh input probe is incomplete"))
	}
	if err := ValidateContent(input, converter.config.Limits); err != nil {
		return conversion, err
	}
	if err := ValidateCapabilities(input, converter.config.Capabilities); err != nil {
		return conversion, err
	}
	if fs.mkdirTemp == nil || fs.removeAll == nil || fs.link == nil || fs.remove == nil {
		return conversion, imageError(CodeInvalidRequest, "convert", errors.New("filesystem helpers are required"))
	}
	if err := os.MkdirAll(request.OutputDir, 0o755); err != nil {
		return conversion, imageError(CodeInvalidRequest, "prepare output directory", err)
	}

	vipsPath, err := converter.executablePath(converter.config.VipsExecutable)
	if err != nil {
		return conversion, err
	}
	headerPath, err := converter.executablePath(converter.config.HeaderExecutable)
	if err != nil {
		return conversion, err
	}

	spec, ok := profiles.Lookup(request.Operation)
	if !ok || spec.Engine != profiles.EngineImage {
		return conversion, imageError(CodeInvalidRequest, "name output", errors.New("unsupported image operation"))
	}
	outputName, err := profiles.OutputName(request.InputPath, request.Operation)
	if err != nil {
		return conversion, imageError(CodeInvalidRequest, "name output", errors.New("unsupported image operation"))
	}
	finalPath := filepath.Join(request.OutputDir, outputName)
	if _, err := os.Stat(finalPath); err == nil {
		return conversion, imageError(CodeOutputExists, "prepare output", errors.New("output already exists"))
	} else if !errors.Is(err, os.ErrNotExist) {
		return conversion, imageError(CodeOutputExists, "prepare output", err)
	}

	workDir, err := fs.mkdirTemp(request.OutputDir, ".filetwist-image-")
	if err != nil {
		return conversion, imageError(CodeInvalidRequest, "prepare work directory", err)
	}
	defer func() {
		if cleanupErr := fs.removeAll(workDir); cleanupErr != nil {
			err := imageError(CodeCleanupFailed, "clean temporary files", cleanupErr)
			if returnErr != nil {
				returnErr = errors.Join(returnErr, err)
			}
		}
	}()

	if request.Operation == profiles.OperationSmallerPhoto && input.HasAlpha {
		transparent, results, err := converter.probeTransparency(ctx, vipsPath, request.InputPath, workDir, input)
		conversion.Commands = append(conversion.Commands, results...)
		if err != nil {
			return conversion, err
		}
		input.HasTransparency = transparent
		conversion.Input = input
	}

	plan, err := BuildPlan(PlanRequest{
		InputPath:         request.InputPath,
		OutputDir:         request.OutputDir,
		WorkDir:           workDir,
		Operation:         request.Operation,
		Input:             input,
		CaptureDatePolicy: converter.config.CaptureDatePolicy,
		VipsPath:          vipsPath,
	})
	if err != nil {
		return conversion, err
	}
	conversion.Output = plan.Output

	for _, command := range plan.Commands {
		command.Timeout = converter.config.CommandTimeout
		commandResult, runErr := converter.run(ctx, command)
		conversion.Commands = append(conversion.Commands, commandResult)
		if runErr != nil {
			return conversion, imageError(CodeCommandFailed, "run libvips", runErr)
		}
	}

	observed, err := converter.probeFile(ctx, headerPath, plan.TemporaryOutput)
	if err != nil {
		return conversion, err
	}
	conversion.Observed = observed
	conversion.ObservedProperties = observed.CorpusProperties()
	if err := ValidateOutput(plan.Expect, observed); err != nil {
		return conversion, err
	}
	if err := ctx.Err(); err != nil {
		return conversion, imageError(CodePublishFailed, "publish output", err)
	}
	if err := publishNoReplaceWith(plan.TemporaryOutput, finalPath, fs.link, fs.remove); err != nil {
		if errors.Is(err, os.ErrExist) {
			return conversion, imageError(CodeOutputExists, "publish output", err)
		}
		return conversion, imageError(CodePublishFailed, "publish output", err)
	}
	return conversion, nil
}

func (converter *Converter) probeFile(ctx context.Context, headerPath, inputPath string) (Info, error) {
	probeCtx, cancel := context.WithTimeout(ctx, converter.config.ProbeTimeout)
	defer cancel()
	return ProbeFile(probeCtx, converter.run, headerPath, inputPath)
}

func (converter *Converter) probeTransparency(
	ctx context.Context,
	vipsPath string,
	inputPath string,
	workDir string,
	info Info,
) (bool, []runner.Result, error) {
	plan, err := BuildTransparencyProbe(vipsPath, inputPath, workDir, info)
	if err != nil {
		return false, nil, err
	}
	results := make([]runner.Result, 0, len(plan.Commands))
	for _, command := range plan.Commands {
		command.Timeout = converter.config.CommandTimeout
		result, runErr := converter.run(ctx, command)
		results = append(results, result)
		if runErr != nil {
			return false, results, imageError(CodeProbeFailed, "probe transparency", runErr)
		}
	}
	minimum := results[len(results)-1].Stdout
	if minimum.Truncated {
		return false, results, imageError(CodeProbeFailed, "probe transparency", errors.New("alpha minimum output was truncated"))
	}
	transparent, err := ParseTransparency(minimum.Bytes, plan.MaxAlpha)
	if err != nil {
		return false, results, err
	}
	return transparent, results, nil
}

func publishNoReplaceWith(
	temporaryPath string,
	finalPath string,
	link func(string, string) error,
	remove func(string) error,
) error {
	if link == nil || remove == nil {
		return errors.New("filesystem helpers are required")
	}
	if err := link(temporaryPath, finalPath); err != nil {
		return err
	}
	_ = remove(temporaryPath)
	return nil
}

func (converter *Converter) executablePath(executable string) (string, error) {
	if filepath.IsAbs(executable) {
		return executable, nil
	}
	result, err := converter.prober.Executable(executable)
	if err != nil {
		return "", imageError(CodeProbeFailed, "locate executable", err)
	}
	return result.Path, nil
}

func imageError(code, stage string, cause error) *Error {
	return &Error{
		Code:  code,
		Stage: stage,
		Cause: cause,
	}
}
