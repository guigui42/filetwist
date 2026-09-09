package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/media"
	"github.com/guigui42/filetwist/internal/probe"
	"github.com/guigui42/filetwist/internal/profiles"
	"github.com/guigui42/filetwist/internal/runner"
)

const (
	exitSuccess       = 0
	exitRejection     = 2
	exitConfiguration = 3
	exitProbe         = 4
	exitConversion    = 5
	exitValidation    = 6
	exitCanceled      = 7
)

var version = "dev"

type converter interface {
	Convert(context.Context, conversion.Request) (conversion.Result, error)
}

type dependencies struct {
	lookupEnv  func(string) (string, bool)
	newService func(media.AccelerationConfig) (converter, error)
}

type options struct {
	input        string
	output       string
	operation    profiles.Operation
	operationSet bool
	human        bool
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, defaultDependencies()))
}

func run(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	deps dependencies,
) int {
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		if _, err := fmt.Fprintf(stdout, "filetwist %s\n", version); err != nil {
			_, _ = fmt.Fprintln(stderr, "filetwist: could not write version")
			return exitConversion
		}
		return exitSuccess
	}
	if len(args) == 1 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		writeUsage(stdout)
		return exitSuccess
	}
	opts, err := parseOptions(args, stderr)
	if err != nil {
		result := failedResult(conversion.FailureConfiguration, "invalid_arguments", err.Error())
		return writeAndExit(stdout, stderr, result, opts.human, exitConfiguration)
	}

	acceleration, err := media.LoadAccelerationConfig(deps.lookupEnv)
	if err != nil {
		result := failedResult(
			conversion.FailureConfiguration,
			"invalid_acceleration_configuration",
			"ACCELERATION or VAAPI_DEVICE is invalid",
		)
		return writeAndExit(stdout, stderr, result, opts.human, exitConfiguration)
	}

	service, err := deps.newService(acceleration)
	if err != nil {
		result := failedResult(
			conversion.FailureConfiguration,
			"converter_initialization_failed",
			"local converter initialization failed",
		)
		return writeAndExit(stdout, stderr, result, opts.human, exitConfiguration)
	}

	result, convertErr := service.Convert(ctx, conversion.Request{
		InputPath:    opts.input,
		Output:       opts.output,
		Operation:    opts.operation,
		OperationSet: opts.operationSet,
	})
	code := exitSuccess
	if convertErr != nil {
		var classified *conversion.Error
		if !errors.As(convertErr, &classified) {
			classified = &conversion.Error{
				Kind:    conversion.FailureConversion,
				Code:    "conversion_failed",
				Message: "conversion failed",
				Cause:   convertErr,
			}
			result.Error = classified
		}
		code = exitCode(classified.Kind)
	}
	return writeAndExit(stdout, stderr, result, opts.human, code)
}

func parseOptions(args []string, stderr io.Writer) (options, error) {
	var opts options
	var operation string
	var outputLong string
	var outputShort string
	var jsonMode bool

	flags := flag.NewFlagSet("filetwist", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&outputLong, "output", "", "output directory or file path")
	flags.StringVar(&outputShort, "o", "", "output directory or file path")
	flags.StringVar(&operation, "operation", "", "named conversion operation")
	flags.BoolVar(&opts.human, "human", false, "emit concise human-readable output")
	flags.BoolVar(&jsonMode, "json", false, "emit JSON output (default)")
	flags.Usage = func() {
		writeUsage(stderr)
	}
	if err := flags.Parse(args); err != nil {
		return opts, errors.New("invalid command arguments")
	}
	if opts.human && jsonMode {
		return opts, errors.New("--human and --json cannot be used together")
	}
	if outputLong != "" && outputShort != "" && outputLong != outputShort {
		return opts, errors.New("--output and -o must not specify different paths")
	}
	opts.output = outputLong
	if opts.output == "" {
		opts.output = outputShort
	}
	if opts.output == "" {
		return opts, errors.New("--output is required")
	}
	if flags.NArg() != 1 {
		return opts, errors.New("exactly one input path is required")
	}
	opts.input = flags.Arg(0)

	if operation != "" {
		parsed, err := profiles.Parse(operation)
		if err != nil {
			return opts, errors.New("operation is not supported")
		}
		opts.operation = parsed
		opts.operationSet = true
	}
	return opts, nil
}

func writeUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "usage: filetwist --output PATH [--operation NAME] [--human] INPUT")
}

func defaultDependencies() dependencies {
	return dependencies{
		lookupEnv: os.LookupEnv,
		newService: func(acceleration media.AccelerationConfig) (converter, error) {
			processRunner, err := runner.New(runner.Config{
				Timeout:     10 * time.Minute,
				StdoutLimit: 256 * 1024,
				StderrLimit: 256 * 1024,
			})
			if err != nil {
				return nil, err
			}
			prober, err := probe.New(processRunner.Run)
			if err != nil {
				return nil, err
			}
			return conversion.NewLocal(conversion.LocalConfig{
				Run:            processRunner.Run,
				Prober:         prober,
				Acceleration:   acceleration,
				CommandTimeout: 10 * time.Minute,
				ProbeTimeout:   30 * time.Second,
			})
		},
	}
}

func writeAndExit(
	stdout io.Writer,
	stderr io.Writer,
	result conversion.Result,
	human bool,
	code int,
) int {
	if human {
		writer := stdout
		if code != exitSuccess {
			writer = stderr
		}
		if err := writeHuman(writer, result); err != nil {
			_, _ = fmt.Fprintln(stderr, "filetwist: could not write result")
			return exitConversion
		}
		return code
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		_, _ = fmt.Fprintln(stderr, "filetwist: could not write result")
		return exitConversion
	}
	return code
}

func writeHuman(writer io.Writer, result conversion.Result) error {
	if result.Error != nil {
		_, err := fmt.Fprintf(writer, "Error [%s]: %s\n", result.Error.Code, result.Error.Message)
		return err
	}
	if _, err := fmt.Fprintf(
		writer,
		"Converted %s with %s\nOutput: %s\nValidation: %s\n",
		result.DetectedMedia.Kind,
		result.SelectedOperation,
		result.OutputPath,
		result.Validation.Status,
	); err != nil {
		return err
	}
	if result.Execution.Initial != result.Execution.Final || result.Execution.FallbackReason != "" {
		if _, err := fmt.Fprintf(
			writer,
			"Execution: %s -> %s (%s)\n",
			result.Execution.Initial,
			result.Execution.Final,
			result.Execution.FallbackReason,
		); err != nil {
			return err
		}
	}
	for _, warning := range result.Warnings {
		if _, err := fmt.Fprintf(writer, "Warning [%s]: %s\n", warning.Code, warning.Message); err != nil {
			return err
		}
	}
	return nil
}

func failedResult(kind conversion.FailureKind, code, message string) conversion.Result {
	return conversion.Result{
		SchemaVersion: conversion.SchemaVersion,
		Status:        "error",
		Validation:    conversion.ValidationResult{Status: "not_run"},
		Warnings:      []conversion.Warning{},
		Error: &conversion.Error{
			Kind:    kind,
			Code:    code,
			Message: message,
		},
	}
}

func exitCode(kind conversion.FailureKind) int {
	switch kind {
	case conversion.FailureRejection:
		return exitRejection
	case conversion.FailureConfiguration:
		return exitConfiguration
	case conversion.FailureProbe:
		return exitProbe
	case conversion.FailureValidation:
		return exitValidation
	case conversion.FailureCanceled:
		return exitCanceled
	default:
		return exitConversion
	}
}
