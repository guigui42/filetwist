package probe_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/probe"
	"github.com/guigui42/filetwist/internal/runner"
)

func TestExecutable(t *testing.T) {
	lookupFailure := errors.New("not found")
	tests := []struct {
		name       string
		executable string
		lookupPath string
		lookupErr  error
		wantFound  bool
		wantErr    bool
	}{
		{
			name:       "present",
			executable: "ffmpeg",
			lookupPath: "/usr/bin/ffmpeg",
			wantFound:  true,
		},
		{
			name:       "missing",
			executable: "missing-tool",
			lookupErr:  lookupFailure,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := mustProber(t, unusedRun, probe.WithLookPath(func(name string) (string, error) {
				if name != tt.executable {
					t.Fatalf("lookPath(%q); want %q", name, tt.executable)
				}
				return tt.lookupPath, tt.lookupErr
			}))

			result, err := p.Executable(tt.executable)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Executable() error = %v; wantErr %t", err, tt.wantErr)
			}
			if result.Found != tt.wantFound {
				t.Errorf("Found = %t; want %t", result.Found, tt.wantFound)
			}
			if result.Path != tt.lookupPath {
				t.Errorf("Path = %q; want %q", result.Path, tt.lookupPath)
			}
			if err != nil {
				var probeErr *probe.Error
				if !errors.As(err, &probeErr) {
					t.Errorf("error type = %T; want *probe.Error", err)
				}
				if strings.Contains(err.Error(), lookupFailure.Error()) {
					t.Errorf("error %q exposes dependency error details", err)
				}
			}
		})
	}
}

func TestVersion(t *testing.T) {
	tests := []struct {
		name             string
		stdout           string
		stderr           string
		parser           probe.VersionParser
		wantVersion      string
		wantTruncated    bool
		wantExecutable   string
		wantArgs         []string
		wantCommandError bool
	}{
		{
			name:           "captures first stdout line",
			stdout:         "ffmpeg version 7.1\nconfiguration details",
			wantVersion:    "ffmpeg version 7.1",
			wantExecutable: "/tools/ffmpeg",
			wantArgs:       []string{"-version"},
		},
		{
			name:           "falls back to stderr",
			stderr:         "vips-8.16.0\n",
			wantVersion:    "vips-8.16.0",
			wantExecutable: "/tools/vips",
			wantArgs:       []string{"--version"},
		},
		{
			name:   "supports parser and bounds version text",
			stdout: "release=" + strings.Repeat("x", probe.VersionTextLimit+20),
			parser: func(stdout, _ []byte) (string, error) {
				return strings.TrimPrefix(string(stdout), "release="), nil
			},
			wantVersion:    strings.Repeat("x", probe.VersionTextLimit),
			wantTruncated:  true,
			wantExecutable: "/tools/custom",
			wantArgs:       []string{"version"},
		},
		{
			name:             "reports command failure",
			wantExecutable:   "/tools/broken",
			wantArgs:         []string{"--version"},
			wantCommandError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := func(_ context.Context, command runner.Command) (runner.Result, error) {
				if command.Path != tt.wantExecutable {
					t.Errorf("command path = %q; want %q", command.Path, tt.wantExecutable)
				}
				if strings.Join(command.Args, "\x00") != strings.Join(tt.wantArgs, "\x00") {
					t.Errorf("command args = %q; want %q", command.Args, tt.wantArgs)
				}
				result := runner.Result{
					Stdout: runner.Output{Bytes: []byte(tt.stdout), Limit: 1024},
					Stderr: runner.Output{Bytes: []byte(tt.stderr), Limit: 1024},
				}
				if tt.wantCommandError {
					return result, errors.New("run failed with private output")
				}
				return result, nil
			}
			executable := strings.TrimPrefix(tt.wantExecutable, "/tools/")
			p := mustProber(t, run, probe.WithLookPath(func(string) (string, error) {
				return tt.wantExecutable, nil
			}))

			result, err := p.Version(context.Background(), probe.VersionSpec{
				Executable: executable,
				Args:       tt.wantArgs,
				Timeout:    time.Second,
				Parse:      tt.parser,
			})
			if (err != nil) != tt.wantCommandError {
				t.Fatalf("Version() error = %v; command error %t", err, tt.wantCommandError)
			}
			if result.Version != tt.wantVersion {
				t.Errorf("Version = %q; want %q", result.Version, tt.wantVersion)
			}
			if result.VersionTruncated != tt.wantTruncated {
				t.Errorf("VersionTruncated = %t; want %t", result.VersionTruncated, tt.wantTruncated)
			}
			if err != nil && strings.Contains(err.Error(), "private output") {
				t.Errorf("error %q exposes dependency error details", err)
			}
		})
	}
}

func TestFunctional(t *testing.T) {
	tests := []struct {
		name    string
		runErr  error
		wantOK  bool
		wantErr bool
	}{
		{name: "passes", wantOK: true},
		{name: "fails", runErr: errors.New("exit output is private"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := func(_ context.Context, command runner.Command) (runner.Result, error) {
				if command.Path != "/tools/encoder" {
					t.Errorf("command path = %q; want resolved executable", command.Path)
				}
				if command.Dir != "/work" {
					t.Errorf("command dir = %q; want /work", command.Dir)
				}
				return runner.Result{ExitCode: 0}, tt.runErr
			}
			p := mustProber(t, run, probe.WithLookPath(func(string) (string, error) {
				return "/tools/encoder", nil
			}))

			result, err := p.Functional(context.Background(), probe.FunctionalSpec{
				Name:       "test encode",
				Executable: "encoder",
				Args:       []string{"--self-test"},
				Dir:        "/work",
				Env:        []string{"MODE=probe"},
				Timeout:    time.Second,
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("Functional() error = %v; wantErr %t", err, tt.wantErr)
			}
			if result.Passed != tt.wantOK {
				t.Errorf("Passed = %t; want %t", result.Passed, tt.wantOK)
			}
			if err != nil {
				var probeErr *probe.Error
				if !errors.As(err, &probeErr) {
					t.Errorf("error type = %T; want *probe.Error", err)
				}
				if strings.Contains(err.Error(), "private") {
					t.Errorf("error %q exposes dependency error details", err)
				}
			}
		})
	}
}

func TestNewRejectsNilRunFunction(t *testing.T) {
	if _, err := probe.New(nil); err == nil {
		t.Fatal("New() error = nil; want validation error")
	}
}

func unusedRun(context.Context, runner.Command) (runner.Result, error) {
	return runner.Result{}, fmt.Errorf("unexpected run")
}

func mustProber(t *testing.T, run probe.RunFunc, options ...probe.Option) *probe.Prober {
	t.Helper()

	p, err := probe.New(run, options...)
	if err != nil {
		t.Fatalf("probe.New() error = %v", err)
	}
	return p
}
