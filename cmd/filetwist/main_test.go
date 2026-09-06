package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/media"
)

type fakeConverter struct {
	result  conversion.Result
	err     error
	request conversion.Request
}

func (converter *fakeConverter) Convert(
	_ context.Context,
	request conversion.Request,
) (conversion.Result, error) {
	converter.request = request
	return converter.result, converter.err
}

func TestRunEmitsJSONByDefault(t *testing.T) {
	fake := &fakeConverter{result: successfulResult()}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(
		context.Background(),
		[]string{"--output", "out", "--operation", "compatible_audio", "input.wav"},
		&stdout,
		&stderr,
		testDependencies(fake),
	)
	if code != exitSuccess {
		t.Fatalf("exit code = %d; want %d; stderr = %s", code, exitSuccess, stderr.String())
	}
	var result conversion.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON: %v; output = %s", err, stdout.String())
	}
	if result.SchemaVersion != conversion.SchemaVersion || result.Status != "success" {
		t.Errorf("result = %+v; want successful schema %s", result, conversion.SchemaVersion)
	}
	if fake.request.Operation != corpus.OperationCompatibleAudio || !fake.request.OperationSet {
		t.Errorf("request = %+v; want requested compatible_audio", fake.request)
	}
}

func TestRunReportsVersionAndHelp(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "version", args: []string{"--version"}, want: "filetwist dev\n"},
		{name: "help", args: []string{"--help"}, want: "usage: filetwist"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(
				context.Background(),
				tt.args,
				&stdout,
				&stderr,
				testDependencies(nil),
			)
			if code != exitSuccess {
				t.Fatalf("exit code = %d; want %d; stderr = %s", code, exitSuccess, stderr.String())
			}
			if !strings.Contains(stdout.String(), tt.want) {
				t.Errorf("output = %q; want substring %q", stdout.String(), tt.want)
			}
		})
	}
}

func TestRunHumanOutputIsConcise(t *testing.T) {
	fake := &fakeConverter{result: successfulResult()}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(
		context.Background(),
		[]string{"--human", "-o", "out", "input.wav"},
		&stdout,
		&stderr,
		testDependencies(fake),
	)
	if code != exitSuccess {
		t.Fatalf("exit code = %d; want success", code)
	}
	output := stdout.String()
	for _, want := range []string{
		"Converted audio with compatible_audio",
		"Output: out/input-compatible.mp3",
		"Validation: passed",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("human output %q does not contain %q", output, want)
		}
	}
}

func TestRunMapsFailuresToStableExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		kind     conversion.FailureKind
		wantCode int
	}{
		{name: "rejection", kind: conversion.FailureRejection, wantCode: exitRejection},
		{name: "configuration", kind: conversion.FailureConfiguration, wantCode: exitConfiguration},
		{name: "probe", kind: conversion.FailureProbe, wantCode: exitProbe},
		{name: "conversion", kind: conversion.FailureConversion, wantCode: exitConversion},
		{name: "validation", kind: conversion.FailureValidation, wantCode: exitValidation},
		{name: "canceled", kind: conversion.FailureCanceled, wantCode: exitCanceled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			classified := &conversion.Error{
				Kind:    tt.kind,
				Code:    "test_failure",
				Message: "safe failure",
			}
			fake := &fakeConverter{
				result: conversion.Result{
					SchemaVersion: conversion.SchemaVersion,
					Status:        "error",
					Validation:    conversion.ValidationResult{Status: "not_run"},
					Warnings:      []conversion.Warning{},
					Error:         classified,
				},
				err: classified,
			}
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(
				context.Background(),
				[]string{"--output", "out", "input.wav"},
				&stdout,
				&stderr,
				testDependencies(fake),
			)
			if code != tt.wantCode {
				t.Errorf("exit code = %d; want %d", code, tt.wantCode)
			}
			var result conversion.Result
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatalf("decode JSON: %v", err)
			}
			if result.Error == nil || result.Error.Kind != tt.kind {
				t.Errorf("result error = %+v; want kind %s", result.Error, tt.kind)
			}
		})
	}
}

func TestRunRejectsInvalidArgumentsAndEnvironment(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		lookup func(string) (string, bool)
	}{
		{
			name:   "missing output",
			args:   []string{"input.wav"},
			lookup: func(string) (string, bool) { return "", false },
		},
		{
			name:   "unknown operation",
			args:   []string{"--output", "out", "--operation", "raw_codec", "input.wav"},
			lookup: func(string) (string, bool) { return "", false },
		},
		{
			name: "invalid acceleration",
			args: []string{"--output", "out", "input.wav"},
			lookup: func(key string) (string, bool) {
				if key == media.AccelerationEnv {
					return "gpu", true
				}
				return "", false
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			deps := testDependencies(&fakeConverter{})
			deps.lookupEnv = tt.lookup
			code := run(context.Background(), tt.args, &stdout, &stderr, deps)
			if code != exitConfiguration {
				t.Errorf("exit code = %d; want %d", code, exitConfiguration)
			}
			var result conversion.Result
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatalf("decode JSON: %v", err)
			}
			if result.Error == nil || result.Error.Kind != conversion.FailureConfiguration {
				t.Errorf("result error = %+v; want configuration failure", result.Error)
			}
		})
	}
}

func testDependencies(fake converter) dependencies {
	return dependencies{
		lookupEnv: func(string) (string, bool) {
			return "", false
		},
		newService: func(media.AccelerationConfig) (converter, error) {
			if fake == nil {
				return nil, errors.New("missing fake")
			}
			return fake, nil
		},
	}
}

func successfulResult() conversion.Result {
	return conversion.Result{
		SchemaVersion:     conversion.SchemaVersion,
		Status:            "success",
		DetectedMedia:     &conversion.DetectedMedia{Kind: corpus.MediaAudio, Format: "wav"},
		SelectedOperation: corpus.OperationCompatibleAudio,
		OperationSource:   "recommended",
		OutputPath:        "out/input-compatible.mp3",
		Validation:        conversion.ValidationResult{Status: "passed"},
		Warnings:          []conversion.Warning{},
		Execution: conversion.ExecutionResult{
			Requested:    "cpu",
			Initial:      "cpu",
			Final:        "cpu",
			AttemptCount: 1,
		},
	}
}
