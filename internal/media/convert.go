package media

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/guigui42/filetwist/internal/runner"
)

// ConversionStage identifies the failed stage of a media conversion.
type ConversionStage string

const (
	// ConversionStagePrepare identifies temporary output preparation.
	ConversionStagePrepare ConversionStage = "prepare"
	// ConversionStageEncode identifies the FFmpeg command.
	ConversionStageEncode ConversionStage = "encode"
	// ConversionStageProgress identifies FFmpeg progress parsing.
	ConversionStageProgress ConversionStage = "progress"
	// ConversionStageProbe identifies the mandatory output ffprobe.
	ConversionStageProbe ConversionStage = "probe"
	// ConversionStageValidate identifies output compatibility validation.
	ConversionStageValidate ConversionStage = "validate"
	// ConversionStagePublish identifies atomic output publication.
	ConversionStagePublish ConversionStage = "publish"
)

// ErrOutputExists reports that atomic publication refused to overwrite an
// existing output.
var ErrOutputExists = errors.New("media: output already exists")

// ConversionError reports a failed conversion stage without exposing paths,
// argv, or captured media-tool output.
type ConversionError struct {
	Stage ConversionStage
	Cause error
}

// Error implements error.
func (err *ConversionError) Error() string {
	return fmt.Sprintf("media: conversion %s stage failed", err.Stage)
}

// Unwrap returns the underlying runner, parser, probe, or validation error.
func (err *ConversionError) Unwrap() error {
	return err.Cause
}

// ConversionResult contains bounded process results, parsed progress, and the
// validated output probe.
type ConversionResult struct {
	Command           runner.Result
	Progress          []Progress
	ProgressTruncated bool
	ProbeCommand      runner.Result
	Output            Probe
	Execution         ExecutionMetadata
	Attempts          []ConversionAttempt
}

// ExecutionMetadata reports the requested, initial, and final conversion path.
type ExecutionMetadata struct {
	RequestedAcceleration AccelerationMode `json:"requested_acceleration"`
	InitialPath           ExecutionPath    `json:"initial_path"`
	FinalPath             ExecutionPath    `json:"final_path"`
	FallbackReason        FallbackReason   `json:"fallback_reason,omitempty"`
	AttemptCount          int              `json:"attempt_count"`
}

// ConversionAttempt contains the bounded result for one CPU or VA-API attempt.
type ConversionAttempt struct {
	Path              ExecutionPath `json:"path"`
	Command           runner.Result `json:"command"`
	Progress          []Progress    `json:"progress,omitempty"`
	ProgressTruncated bool          `json:"progress_truncated"`
	ProbeCommand      runner.Result `json:"probe_command"`
	Output            Probe         `json:"output"`
}

// Convert executes a plan, retries one recognized VA-API failure on CPU,
// re-probes the output, and validates the common compatibility profile before
// atomic publication.
func Convert(
	ctx context.Context,
	run RunFunc,
	ffmpegPath string,
	ffprobePath string,
	plan Plan,
) (ConversionResult, error) {
	if ctx == nil {
		return ConversionResult{}, errors.New("media: context must not be nil")
	}
	if run == nil {
		return ConversionResult{}, errors.New("media: run function must not be nil")
	}
	if ffmpegPath == "" || ffprobePath == "" {
		return ConversionResult{}, errors.New("media: FFmpeg and ffprobe paths must not be empty")
	}
	if err := requireFFmpegFilters(ctx, run, ffmpegPath, plan.RequiredFilters, plan.Timeout); err != nil {
		return ConversionResult{}, err
	}

	initialPath := plan.ExecutionPath
	if initialPath == "" {
		initialPath = ExecutionPathCPU
	}
	result, err := convertOnce(ctx, run, ffmpegPath, ffprobePath, plan)
	result.Execution = ExecutionMetadata{
		RequestedAcceleration: plan.RequestedAcceleration,
		InitialPath:           initialPath,
		FinalPath:             initialPath,
		FallbackReason:        plan.FallbackReason,
		AttemptCount:          1,
	}
	result.Attempts = []ConversionAttempt{attemptFromResult(initialPath, result)}
	if err == nil || !isVAAPIPath(initialPath) || len(plan.cpuArgs) == 0 {
		return result, err
	}

	reason := ClassifyVAAPIFailure(err, result.Command.Stderr.Bytes)
	if reason == FallbackNone {
		return result, err
	}

	cpuPlan := plan
	cpuPlan.Args = append([]string(nil), plan.cpuArgs...)
	cpuPlan.cpuArgs = nil
	cpuPlan.ExecutionPath = ExecutionPathCPU
	cpuPlan.FallbackReason = reason
	cpuResult, cpuErr := convertOnce(ctx, run, ffmpegPath, ffprobePath, cpuPlan)
	cpuResult.Execution = ExecutionMetadata{
		RequestedAcceleration: plan.RequestedAcceleration,
		InitialPath:           initialPath,
		FinalPath:             ExecutionPathCPU,
		FallbackReason:        reason,
		AttemptCount:          2,
	}
	cpuResult.Attempts = append(
		result.Attempts,
		attemptFromResult(ExecutionPathCPU, cpuResult),
	)
	return cpuResult, cpuErr
}

func requireFFmpegFilters(
	ctx context.Context,
	run RunFunc,
	ffmpegPath string,
	required []string,
	timeout time.Duration,
) error {
	if len(required) == 0 {
		return nil
	}
	result, err := run(ctx, runner.Command{
		Path:    ffmpegPath,
		Args:    []string{"-hide_banner", "-filters"},
		Timeout: timeout,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return &PlanError{
			Code:    ErrorUnsupportedHDR,
			Message: "HDR conversion requires FFmpeg filter capability detection",
		}
	}
	available := make(map[string]bool)
	output := string(result.Stdout.Bytes) + "\n" + string(result.Stderr.Bytes)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && strings.Contains(fields[2], "->") {
			available[fields[1]] = true
		}
	}
	for _, filter := range required {
		if !available[filter] {
			return &PlanError{
				Code:    ErrorUnsupportedHDR,
				Message: "HDR conversion requires FFmpeg zscale and tonemap filters",
			}
		}
	}
	return nil
}

// ClassifyVAAPIFailure returns a CPU fallback reason only for recognized
// VA-API encode failures. Context, validation, probe, disk, start, wait, and
// generic conversion failures are never classified for retry.
func ClassifyVAAPIFailure(err error, stderr []byte) FallbackReason {
	var conversionErr *ConversionError
	if !errors.As(err, &conversionErr) || conversionErr.Stage != ConversionStageEncode {
		return FallbackNone
	}
	var exitErr *runner.ExitError
	if !errors.As(conversionErr.Cause, &exitErr) {
		return FallbackNone
	}
	return classifyVAAPIStderr(stderr)
}

func convertOnce(
	ctx context.Context,
	run RunFunc,
	ffmpegPath string,
	ffprobePath string,
	plan Plan,
) (ConversionResult, error) {
	result := ConversionResult{}
	tempFile, err := os.CreateTemp(
		filepath.Dir(plan.OutputPath),
		"."+filepath.Base(plan.OutputPath)+".filetwist-*",
	)
	if err != nil {
		return result, &ConversionError{
			Stage: ConversionStagePrepare,
			Cause: fmt.Errorf("create temporary output: %w", err),
		}
	}
	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return result, &ConversionError{
			Stage: ConversionStagePrepare,
			Cause: fmt.Errorf("close temporary output: %w", err),
		}
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()

	executionPlan := plan
	executionPlan.OutputPath = tempPath
	executionPlan.Args = append([]string(nil), plan.Args...)
	if len(executionPlan.Args) == 0 {
		return result, &ConversionError{
			Stage: ConversionStageEncode,
			Cause: errors.New("plan has no FFmpeg arguments"),
		}
	}
	executionPlan.Args[len(executionPlan.Args)-1] = tempPath

	commandResult, err := run(ctx, executionPlan.Command(ffmpegPath))
	result.Command = commandResult
	if err != nil {
		return result, &ConversionError{
			Stage: ConversionStageEncode,
			Cause: err,
		}
	}

	result.ProgressTruncated = commandResult.Stdout.Truncated
	progress, err := ParseProgress(strings.NewReader(string(commandResult.Stdout.Bytes)))
	if err != nil {
		return result, &ConversionError{
			Stage: ConversionStageProgress,
			Cause: err,
		}
	}
	result.Progress = progress

	output, probeResult, err := ProbeFile(ctx, run, ffprobePath, tempPath, plan.Timeout)
	result.ProbeCommand = probeResult
	result.Output = output
	if err != nil {
		return result, &ConversionError{
			Stage: ConversionStageProbe,
			Cause: err,
		}
	}

	if err := ValidateOutput(executionPlan, output); err != nil {
		return result, &ConversionError{
			Stage: ConversionStageValidate,
			Cause: err,
		}
	}
	if err := ctx.Err(); err != nil {
		return result, &ConversionError{Stage: ConversionStagePublish, Cause: err}
	}
	if err := publishNoReplace(tempPath, plan.OutputPath); err != nil {
		return result, &ConversionError{
			Stage: ConversionStagePublish,
			Cause: fmt.Errorf("publish validated output: %w", err),
		}
	}
	committed = true
	return result, nil
}

func publishNoReplace(temporaryPath, finalPath string) error {
	if err := os.Link(temporaryPath, finalPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrOutputExists
		}
		return err
	}
	if err := os.Remove(temporaryPath); err != nil {
		return fmt.Errorf("remove temporary output after publish: %w", err)
	}
	return nil
}

func attemptFromResult(path ExecutionPath, result ConversionResult) ConversionAttempt {
	return ConversionAttempt{
		Path:              path,
		Command:           result.Command,
		Progress:          append([]Progress(nil), result.Progress...),
		ProgressTruncated: result.ProgressTruncated,
		ProbeCommand:      result.ProbeCommand,
		Output:            result.Output,
	}
}

func isVAAPIPath(path ExecutionPath) bool {
	return path == ExecutionPathVAAPISoftwareDecode ||
		path == ExecutionPathVAAPIHardwareDecode
}
