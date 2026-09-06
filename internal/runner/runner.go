package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	// DefaultTimeout is used when Config.Timeout is zero.
	DefaultTimeout = 30 * time.Second
	// DefaultOutputLimit is the maximum number of bytes captured per output
	// stream when its configured limit is zero.
	DefaultOutputLimit = 64 * 1024
)

// Config defines defaults shared by commands run by a Runner.
type Config struct {
	Timeout     time.Duration
	StdoutLimit int
	StderrLimit int
}

// Command describes one direct executable invocation. Args are passed without
// a shell. Env entries are appended to the current process environment.
type Command struct {
	Path    string
	Args    []string
	Dir     string
	Env     []string
	Timeout time.Duration
}

// Output contains bounded bytes captured from one process stream.
type Output struct {
	Bytes     []byte
	Limit     int
	Truncated bool
}

// Result contains process metadata and bounded output. ExitCode is -1 when no
// process exit status is available.
type Result struct {
	ExitCode int
	Duration time.Duration
	Stdout   Output
	Stderr   Output
}

// Runner executes commands using a fixed timeout and output limits.
type Runner struct {
	timeout     time.Duration
	stdoutLimit int
	stderrLimit int
}

// New constructs a Runner. Zero values select the documented defaults.
func New(config Config) (*Runner, error) {
	if config.Timeout < 0 {
		return nil, errors.New("runner: timeout must not be negative")
	}
	if config.StdoutLimit < 0 {
		return nil, errors.New("runner: stdout limit must not be negative")
	}
	if config.StderrLimit < 0 {
		return nil, errors.New("runner: stderr limit must not be negative")
	}

	if config.Timeout == 0 {
		config.Timeout = DefaultTimeout
	}
	if config.StdoutLimit == 0 {
		config.StdoutLimit = DefaultOutputLimit
	}
	if config.StderrLimit == 0 {
		config.StderrLimit = DefaultOutputLimit
	}

	return &Runner{
		timeout:     config.Timeout,
		stdoutLimit: config.StdoutLimit,
		stderrLimit: config.StderrLimit,
	}, nil
}

// Run executes command until it exits, its command deadline expires, or ctx is
// canceled. It never invokes a shell. Errors intentionally omit arguments and
// captured output; callers can inspect the bounded Result explicitly.
func (r *Runner) Run(ctx context.Context, command Command) (Result, error) {
	if ctx == nil {
		return emptyResult(r), errors.New("runner: context must not be nil")
	}
	if command.Path == "" {
		return emptyResult(r), errors.New("runner: executable path must not be empty")
	}
	if command.Timeout < 0 {
		return emptyResult(r), errors.New("runner: command timeout must not be negative")
	}

	timeout := command.Timeout
	if timeout == 0 {
		timeout = r.timeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started := time.Now()
	if err := runCtx.Err(); err != nil {
		result := emptyResult(r)
		result.Duration = time.Since(started)
		return result, &ContextError{
			Executable: executableName(command.Path),
			Cause:      err,
			Result:     result,
		}
	}

	stdout := newBoundedBuffer(r.stdoutLimit)
	stderr := newBoundedBuffer(r.stderrLimit)
	cmd := exec.Command(command.Path, command.Args...)
	cmd.Dir = command.Dir
	if len(command.Env) > 0 {
		cmd.Env = append(os.Environ(), command.Env...)
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	configureProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		result := buildResult(started, nil, stdout, stderr)
		return result, &StartError{
			Executable: executableName(command.Path),
			Cause:      err,
			Result:     result,
		}
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	select {
	case err := <-waitCh:
		return classifyWait(command.Path, started, cmd.ProcessState, stdout, stderr, err)
	case <-runCtx.Done():
		select {
		case err := <-waitCh:
			return classifyWait(command.Path, started, cmd.ProcessState, stdout, stderr, err)
		default:
		}

		terminationErr := terminateProcessGroup(cmd)
		waitErr := <-waitCh
		result := buildResult(started, cmd.ProcessState, stdout, stderr)
		return result, &ContextError{
			Executable:     executableName(command.Path),
			Cause:          runCtx.Err(),
			TerminationErr: terminationErr,
			WaitErr:        waitErr,
			Result:         result,
		}
	}
}

func classifyWait(
	path string,
	started time.Time,
	state *os.ProcessState,
	stdout *boundedBuffer,
	stderr *boundedBuffer,
	waitErr error,
) (Result, error) {
	result := buildResult(started, state, stdout, stderr)
	if waitErr == nil {
		return result, nil
	}

	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return result, &ExitError{
			Executable: executableName(path),
			ExitCode:   result.ExitCode,
			Result:     result,
		}
	}

	return result, &WaitError{
		Executable: executableName(path),
		Cause:      waitErr,
		Result:     result,
	}
}

func emptyResult(r *Runner) Result {
	return Result{
		ExitCode: -1,
		Stdout:   Output{Limit: r.stdoutLimit},
		Stderr:   Output{Limit: r.stderrLimit},
	}
}

func buildResult(
	started time.Time,
	state *os.ProcessState,
	stdout *boundedBuffer,
	stderr *boundedBuffer,
) Result {
	exitCode := -1
	if state != nil {
		exitCode = state.ExitCode()
	}
	return Result{
		ExitCode: exitCode,
		Duration: time.Since(started),
		Stdout:   stdout.output(),
		Stderr:   stderr.output(),
	}
}

func executableName(path string) string {
	name := filepath.Base(path)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "executable"
	}
	return name
}

// ExitError reports a completed command with a nonzero exit status. Error does
// not include command arguments or output.
type ExitError struct {
	Executable string
	ExitCode   int
	Result     Result
}

// Error implements error.
func (e *ExitError) Error() string {
	return fmt.Sprintf("runner: %s exited with code %d", e.Executable, e.ExitCode)
}

// ContextError reports cancellation or deadline expiry. TerminationErr and
// WaitErr are available for diagnostics but are not included in Error.
type ContextError struct {
	Executable     string
	Cause          error
	TerminationErr error
	WaitErr        error
	Result         Result
}

// Error implements error without exposing command arguments or output.
func (e *ContextError) Error() string {
	if errors.Is(e.Cause, context.DeadlineExceeded) {
		return fmt.Sprintf("runner: %s timed out", e.Executable)
	}
	return fmt.Sprintf("runner: %s canceled", e.Executable)
}

// Unwrap returns the context cancellation cause.
func (e *ContextError) Unwrap() error {
	return e.Cause
}

// StartError reports that an executable could not be started.
type StartError struct {
	Executable string
	Cause      error
	Result     Result
}

// Error implements error without exposing command arguments or dependency
// error details.
func (e *StartError) Error() string {
	return fmt.Sprintf("runner: could not start %s", e.Executable)
}

// Unwrap returns the underlying start error.
func (e *StartError) Unwrap() error {
	return e.Cause
}

// WaitError reports a process wait failure that is not a normal nonzero exit.
type WaitError struct {
	Executable string
	Cause      error
	Result     Result
}

// Error implements error without exposing command arguments or dependency
// error details.
func (e *WaitError) Error() string {
	return fmt.Sprintf("runner: could not wait for %s", e.Executable)
}

// Unwrap returns the underlying wait error.
func (e *WaitError) Unwrap() error {
	return e.Cause
}
