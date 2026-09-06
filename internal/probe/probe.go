package probe

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/guigui42/filetwist/internal/runner"
)

const (
	// VersionTextLimit bounds the normalized version string returned by a
	// version probe. The underlying command output remains available through
	// runner.Result with its own explicit limits.
	VersionTextLimit = 512
)

// RunFunc is the process execution contract required by Prober. A
// *runner.Runner can be supplied with its Run method.
type RunFunc func(context.Context, runner.Command) (runner.Result, error)

// LookPathFunc resolves an executable using exec.LookPath semantics.
type LookPathFunc func(string) (string, error)

// VersionParser extracts version text from bounded stdout and stderr.
type VersionParser func(stdout, stderr []byte) (string, error)

// Option configures a Prober.
type Option func(*Prober) error

// WithLookPath replaces executable lookup, primarily for deterministic tests
// or controlled runtime environments.
func WithLookPath(lookPath LookPathFunc) Option {
	return func(prober *Prober) error {
		if lookPath == nil {
			return errors.New("probe: look path function must not be nil")
		}
		prober.lookPath = lookPath
		return nil
	}
}

// Prober performs converter-neutral capability checks.
type Prober struct {
	run      RunFunc
	lookPath LookPathFunc
}

// New constructs a Prober from a process execution function.
func New(run RunFunc, options ...Option) (*Prober, error) {
	if run == nil {
		return nil, errors.New("probe: run function must not be nil")
	}

	prober := &Prober{
		run:      run,
		lookPath: exec.LookPath,
	}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("probe: option must not be nil")
		}
		if err := option(prober); err != nil {
			return nil, err
		}
	}
	return prober, nil
}

// PresenceResult reports whether an executable resolved to a runnable path.
type PresenceResult struct {
	Executable string
	Path       string
	Found      bool
}

// Executable checks whether executable can be resolved without running it.
func (p *Prober) Executable(executable string) (PresenceResult, error) {
	result := PresenceResult{Executable: executable}
	if executable == "" {
		return result, &Error{
			Stage:      StagePresence,
			Executable: "executable",
			Cause:      errors.New("executable must not be empty"),
		}
	}

	path, err := p.lookPath(executable)
	if err != nil {
		return result, &Error{
			Stage:      StagePresence,
			Executable: executableName(executable),
			Cause:      err,
		}
	}
	result.Path = path
	result.Found = true
	return result, nil
}

// VersionSpec defines a version command and optional output parser.
type VersionSpec struct {
	Executable string
	Args       []string
	Timeout    time.Duration
	Parse      VersionParser
}

// VersionResult contains presence, bounded command output, and normalized
// version text.
type VersionResult struct {
	Presence         PresenceResult
	Command          runner.Result
	Version          string
	VersionTruncated bool
}

// Version resolves an executable and runs its version command.
func (p *Prober) Version(ctx context.Context, spec VersionSpec) (VersionResult, error) {
	presence, err := p.Executable(spec.Executable)
	result := VersionResult{Presence: presence}
	if err != nil {
		return result, err
	}

	commandResult, err := p.run(ctx, runner.Command{
		Path:    presence.Path,
		Args:    cloneStrings(spec.Args),
		Timeout: spec.Timeout,
	})
	result.Command = commandResult
	if err != nil {
		return result, &Error{
			Stage:      StageVersion,
			Executable: executableName(spec.Executable),
			Cause:      err,
		}
	}

	parser := spec.Parse
	if parser == nil {
		parser = parseFirstLine
	}
	version, err := parser(commandResult.Stdout.Bytes, commandResult.Stderr.Bytes)
	if err != nil {
		return result, &Error{
			Stage:      StageVersion,
			Executable: executableName(spec.Executable),
			Cause:      err,
		}
	}
	result.Version, result.VersionTruncated = boundVersion(version)
	return result, nil
}

// FunctionalSpec defines a real command used to verify a capability. Policy
// such as fallback decisions remains with the caller.
type FunctionalSpec struct {
	Name       string
	Executable string
	Args       []string
	Dir        string
	Env        []string
	Timeout    time.Duration
}

// FunctionalResult contains the executable lookup and command result.
type FunctionalResult struct {
	Name     string
	Presence PresenceResult
	Command  runner.Result
	Passed   bool
}

// Functional resolves an executable and runs a real capability test command.
func (p *Prober) Functional(ctx context.Context, spec FunctionalSpec) (FunctionalResult, error) {
	presence, err := p.Executable(spec.Executable)
	result := FunctionalResult{
		Name:     spec.Name,
		Presence: presence,
	}
	if err != nil {
		return result, err
	}

	commandResult, err := p.run(ctx, runner.Command{
		Path:    presence.Path,
		Args:    cloneStrings(spec.Args),
		Dir:     spec.Dir,
		Env:     cloneStrings(spec.Env),
		Timeout: spec.Timeout,
	})
	result.Command = commandResult
	if err != nil {
		return result, &Error{
			Stage:      StageFunctional,
			Executable: executableName(spec.Executable),
			Cause:      err,
		}
	}
	if commandResult.ExitCode != 0 {
		return result, &Error{
			Stage:      StageFunctional,
			Executable: executableName(spec.Executable),
			Cause:      fmt.Errorf("unexpected exit code %d", commandResult.ExitCode),
		}
	}

	result.Passed = true
	return result, nil
}

// Stage identifies the part of a capability probe that failed.
type Stage string

const (
	// StagePresence identifies executable lookup.
	StagePresence Stage = "presence"
	// StageVersion identifies a version command or parser.
	StageVersion Stage = "version"
	// StageFunctional identifies a real capability test command.
	StageFunctional Stage = "functional"
)

// Error reports a probe failure without including command arguments, captured
// output, or dependency error text.
type Error struct {
	Stage      Stage
	Executable string
	Cause      error
}

// Error implements error.
func (e *Error) Error() string {
	return fmt.Sprintf("probe: %s check for %s failed", e.Stage, e.Executable)
}

// Unwrap returns the underlying lookup, runner, or parser error.
func (e *Error) Unwrap() error {
	return e.Cause
}

func parseFirstLine(stdout, stderr []byte) (string, error) {
	for _, output := range [][]byte{stdout, stderr} {
		for _, line := range strings.Split(string(output), "\n") {
			if version := strings.TrimSpace(line); version != "" {
				return version, nil
			}
		}
	}
	return "", errors.New("version output was empty")
}

func boundVersion(version string) (string, bool) {
	version = strings.TrimSpace(version)
	if len(version) <= VersionTextLimit {
		return version, false
	}
	return version[:VersionTextLimit], true
}

func executableName(path string) string {
	name := filepath.Base(path)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "executable"
	}
	return name
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}
