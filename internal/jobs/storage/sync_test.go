package storage

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

func TestDirectorySyncErrors(t *testing.T) {
	for _, test := range []struct {
		name     string
		syncErr  error
		closeErr error
		want     error
	}{
		{name: "success"},
		{name: "unsupported", syncErr: syscall.ENOTSUP},
		{name: "invalid operation", syncErr: syscall.EINVAL},
		{name: "invalid handle", syncErr: os.ErrInvalid},
		{name: "io failure", syncErr: syscall.EIO, want: syscall.EIO},
		{name: "disk full", syncErr: syscall.ENOSPC, want: syscall.ENOSPC},
		{name: "close failure", closeErr: syscall.EIO, want: syscall.EIO},
		{name: "unsupported and close failure", syncErr: syscall.ENOTSUP, closeErr: syscall.EIO, want: syscall.EIO},
	} {
		t.Run(test.name, func(t *testing.T) {
			syncErr := test.syncErr
			if syncErr != nil {
				syncErr = &os.PathError{Op: "sync", Path: "job", Err: syncErr}
			}
			err := directorySyncError(syncErr, test.closeErr)
			if !errors.Is(err, test.want) {
				t.Fatalf("directorySyncError = %v; want %v", err, test.want)
			}
		})
	}
}
