//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package runner_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/runner"
)

func TestRunKillsChildProcessesOnCancellation(t *testing.T) {
	tempDir := t.TempDir()
	marker := filepath.Join(tempDir, "child-survived")
	ready := filepath.Join(tempDir, "child-started")
	r := mustRunner(t, runner.Config{
		Timeout:     10 * time.Second,
		StdoutLimit: 128,
		StderrLimit: 128,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type outcome struct {
		result runner.Result
		err    error
	}
	outcomeCh := make(chan outcome, 1)
	go func() {
		result, err := r.Run(ctx, helperCommand("spawn-child", marker, ready))
		outcomeCh <- outcome{result: result, err: err}
	}()

	waitForFile(t, ready, 5*time.Second)
	cancel()
	got := <-outcomeCh
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("Run() error = %v; want canceled", got.err)
	}

	childPIDText := strings.TrimSpace(string(got.result.Stdout.Bytes))
	childPID, parseErr := strconv.Atoi(childPIDText)
	if parseErr != nil {
		t.Fatalf("child PID output = %q: %v", childPIDText, parseErr)
	}

	deadline := time.Now().Add(time.Second)
	for processExists(childPID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processExists(childPID) {
		t.Errorf("child process %d still exists after process-group termination", childPID)
	}

	time.Sleep(450 * time.Millisecond)
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("child marker exists or cannot be checked: %v", statErr)
	}
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stat ready file: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", filepath.Base(path))
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
