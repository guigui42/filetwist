package runner_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/runner"
)

func TestRunCompletesAndCapturesBoundedOutput(t *testing.T) {
	tests := []struct {
		name            string
		stdoutLimit     int
		stderrLimit     int
		helperArgs      []string
		wantStdout      string
		wantStderr      string
		wantStdoutShort bool
		wantStderrShort bool
	}{
		{
			name:        "successful execution",
			stdoutLimit: 64,
			stderrLimit: 64,
			helperArgs:  []string{"emit", "hello", "warning", "0"},
			wantStdout:  "hello",
			wantStderr:  "warning",
		},
		{
			name:            "output is bounded",
			stdoutLimit:     8,
			stderrLimit:     10,
			helperArgs:      []string{"spam", "32", "24"},
			wantStdout:      strings.Repeat("o", 8),
			wantStderr:      strings.Repeat("e", 10),
			wantStdoutShort: true,
			wantStderrShort: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := mustRunner(t, runner.Config{
				Timeout:     10 * time.Second,
				StdoutLimit: tt.stdoutLimit,
				StderrLimit: tt.stderrLimit,
			})

			result, err := r.Run(context.Background(), helperCommand(tt.helperArgs...))
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != 0 {
				t.Errorf("ExitCode = %d; want 0", result.ExitCode)
			}
			if got := string(result.Stdout.Bytes); got != tt.wantStdout {
				t.Errorf("stdout = %q; want %q", got, tt.wantStdout)
			}
			if got := string(result.Stderr.Bytes); got != tt.wantStderr {
				t.Errorf("stderr = %q; want %q", got, tt.wantStderr)
			}
			if result.Stdout.Truncated != tt.wantStdoutShort {
				t.Errorf("stdout truncated = %t; want %t", result.Stdout.Truncated, tt.wantStdoutShort)
			}
			if result.Stderr.Truncated != tt.wantStderrShort {
				t.Errorf("stderr truncated = %t; want %t", result.Stderr.Truncated, tt.wantStderrShort)
			}
			if result.Stdout.Limit != tt.stdoutLimit {
				t.Errorf("stdout limit = %d; want %d", result.Stdout.Limit, tt.stdoutLimit)
			}
			if result.Stderr.Limit != tt.stderrLimit {
				t.Errorf("stderr limit = %d; want %d", result.Stderr.Limit, tt.stderrLimit)
			}
			if result.Duration <= 0 {
				t.Errorf("Duration = %s; want positive", result.Duration)
			}
		})
	}
}

func TestRunReturnsStructuredExitErrorWithoutOutputOrArguments(t *testing.T) {
	const (
		secretOutput = "private file contents"
		secretArg    = "secret-input.mov"
	)
	r := mustRunner(t, runner.Config{
		Timeout:     10 * time.Second,
		StdoutLimit: 64,
		StderrLimit: 64,
	})
	command := helperCommand("emit", "", secretOutput, "7", secretArg)

	result, err := r.Run(context.Background(), command)
	if err == nil {
		t.Fatal("Run() error = nil; want nonzero exit error")
	}

	var exitErr *runner.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Run() error type = %T; want *runner.ExitError", err)
	}
	if exitErr.ExitCode != 7 || result.ExitCode != 7 {
		t.Errorf("exit codes = (%d, %d); want (7, 7)", exitErr.ExitCode, result.ExitCode)
	}
	if got := string(result.Stderr.Bytes); got != secretOutput {
		t.Errorf("stderr = %q; want captured output", got)
	}
	if strings.Contains(err.Error(), secretOutput) {
		t.Errorf("error %q exposes captured output", err)
	}
	if strings.Contains(err.Error(), secretArg) {
		t.Errorf("error %q exposes command arguments", err)
	}
}

func TestRunHonorsCancellationAndDeadline(t *testing.T) {
	tests := []struct {
		name      string
		timeout   time.Duration
		context   func() (context.Context, context.CancelFunc)
		wantCause error
	}{
		{
			name:    "command deadline",
			timeout: 30 * time.Millisecond,
			context: func() (context.Context, context.CancelFunc) {
				return context.Background(), func() {}
			},
			wantCause: context.DeadlineExceeded,
		},
		{
			name:    "caller cancellation",
			timeout: time.Second,
			context: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				time.AfterFunc(30*time.Millisecond, cancel)
				return ctx, cancel
			},
			wantCause: context.Canceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := mustRunner(t, runner.Config{
				Timeout:     tt.timeout,
				StdoutLimit: 64,
				StderrLimit: 64,
			})
			ctx, cancel := tt.context()
			defer cancel()

			result, err := r.Run(ctx, helperCommand("block"))
			if !errors.Is(err, tt.wantCause) {
				t.Fatalf("Run() error = %v; want errors.Is(%v)", err, tt.wantCause)
			}
			var contextErr *runner.ContextError
			if !errors.As(err, &contextErr) {
				t.Fatalf("Run() error type = %T; want *runner.ContextError", err)
			}
			if result.Duration <= 0 {
				t.Errorf("Duration = %s; want positive", result.Duration)
			}
		})
	}
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		config runner.Config
	}{
		{name: "negative timeout", config: runner.Config{Timeout: -time.Second}},
		{name: "negative stdout limit", config: runner.Config{StdoutLimit: -1}},
		{name: "negative stderr limit", config: runner.Config{StderrLimit: -1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := runner.New(tt.config); err == nil {
				t.Fatal("New() error = nil; want validation error")
			}
		})
	}
}

func mustRunner(t *testing.T, config runner.Config) *runner.Runner {
	t.Helper()

	r, err := runner.New(config)
	if err != nil {
		t.Fatalf("runner.New() error = %v", err)
	}
	return r
}

func helperCommand(args ...string) runner.Command {
	return runner.Command{
		Path: os.Args[0],
		Args: append([]string{"-test.run=TestHelperProcess", "--"}, args...),
		Env:  []string{"GO_WANT_HELPER_PROCESS=1"},
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	args := argsAfterSeparator(os.Args)
	if len(args) == 0 {
		os.Exit(2)
	}

	switch args[0] {
	case "emit":
		if len(args) < 4 {
			os.Exit(2)
		}
		_, _ = fmt.Fprint(os.Stdout, args[1])
		_, _ = fmt.Fprint(os.Stderr, args[2])
		code, err := strconv.Atoi(args[3])
		if err != nil {
			os.Exit(2)
		}
		os.Exit(code)
	case "spam":
		if len(args) != 3 {
			os.Exit(2)
		}
		stdoutBytes, err := strconv.Atoi(args[1])
		if err != nil {
			os.Exit(2)
		}
		stderrBytes, err := strconv.Atoi(args[2])
		if err != nil {
			os.Exit(2)
		}
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("o", stdoutBytes))
		_, _ = fmt.Fprint(os.Stderr, strings.Repeat("e", stderrBytes))
		os.Exit(0)
	case "block":
		time.Sleep(time.Hour)
		os.Exit(0)
	case "spawn-child":
		if len(args) != 3 {
			os.Exit(2)
		}
		child := exec.Command(
			os.Args[0],
			"-test.run=TestHelperProcess",
			"--",
			"child-marker",
			args[1],
		)
		child.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
		_, _ = fmt.Fprintln(os.Stdout, child.Process.Pid)
		_ = os.Stdout.Sync()
		if err := os.WriteFile(args[2], []byte("ready"), 0o600); err != nil {
			os.Exit(4)
		}
		if err := child.Wait(); err != nil {
			os.Exit(5)
		}
		os.Exit(0)
	case "child-marker":
		if len(args) != 2 {
			os.Exit(2)
		}
		time.Sleep(400 * time.Millisecond)
		if err := os.WriteFile(args[1], []byte("child survived"), 0o600); err != nil {
			os.Exit(6)
		}
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

func argsAfterSeparator(args []string) []string {
	for index, arg := range args {
		if arg == "--" {
			return args[index+1:]
		}
	}
	return nil
}
