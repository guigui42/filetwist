package jobs

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/jobs/storage"
)

// SafeLogFields returns stable structured error fields without exposing paths
// or stored file names.
func SafeLogFields(err error) []any {
	fields := []any{slog.String("category", safeLogCategory(err))}
	if op := filesystemOperation(err); op != "" {
		fields = append(fields, slog.String("fs_op", op))
	}
	var classified *conversion.Error
	if errors.As(err, &classified) && classified.Code != "" {
		fields = append(fields, slog.String("code", classified.Code))
	}
	return fields
}

func safeLogCategory(err error) string {
	var classified *conversion.Error
	if errors.As(err, &classified) {
		return string(classified.Kind)
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, storage.ErrInvalidID):
		return "invalid_id"
	case errors.Is(err, storage.ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrLeased):
		return "leased"
	case errors.Is(err, ErrNotStartable):
		return "invalid_state"
	case errors.Is(err, ErrQueueFull):
		return "queue_full"
	case errors.Is(err, ErrShuttingDown):
		return "shutting_down"
	case filesystemOperation(err) != "":
		return "filesystem"
	default:
		return "internal"
	}
}

func filesystemOperation(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Op
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return linkErr.Op
	}
	var syscallErr *os.SyscallError
	if errors.As(err, &syscallErr) {
		return syscallErr.Syscall
	}
	return ""
}
