//go:build unix

package storage

import (
	"fmt"
	"syscall"
)

// FreeSpace returns the bytes available to an unprivileged process in the
// filesystem holding dir.
func FreeSpace(dir string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return 0, fmt.Errorf("storage: read filesystem statistics: %w", err)
	}
	available := uint64(stat.Bavail) * uint64(stat.Bsize)
	if available > 1<<62 {
		return 1 << 62, nil
	}
	return int64(available), nil
}
