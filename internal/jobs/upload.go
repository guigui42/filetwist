package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/jobs/storage"
)

// Upload rejection reasons. Callers map these onto transport status codes.
var (
	// ErrNoFiles reports an upload that contained no file parts.
	ErrNoFiles = errors.New("jobs: upload contained no files")
	// ErrTooManyFiles reports an upload above the per-job file ceiling.
	ErrTooManyFiles = errors.New("jobs: too many files in one job")
	// ErrUploadTooLarge reports an upload above the aggregate byte ceiling.
	ErrUploadTooLarge = errors.New("jobs: upload exceeds the size limit")
	// ErrInsufficientSpace reports that the data directory is too full to
	// accept the upload.
	ErrInsufficientSpace = errors.New("jobs: insufficient free disk space")
	// ErrInvalidUpload reports a malformed multipart body or unusable file
	// name.
	ErrInvalidUpload = errors.New("jobs: upload is not a valid multipart request")
	// ErrUploadStalled reports a request body that stopped delivering bytes
	// long enough for the transport deadline to fire.
	ErrUploadStalled = errors.New("jobs: upload stopped making progress")
)

const (
	// inspectTimeout bounds probing one uploaded file during intake so an
	// upload request cannot block on a pathological input for the full command
	// timeout.
	inspectTimeout = 2 * time.Minute
	// maxFormFields bounds the non-file parts one upload may carry. The
	// interface sends none, so this only exists to stop an endless stream of
	// fields from holding intake capacity forever.
	maxFormFields = 32
	// maxFormFieldBytes bounds one non-file part. multipart.Part.Close drains
	// whatever a reader left behind, so an oversized field has to abort the
	// upload instead of being discarded.
	maxFormFieldBytes = 64 << 10
)

type uploadBudget struct {
	bytes    int64
	limitErr error
}

// Accept streams one multipart upload directly into a new isolated job
// directory, probes every stored file, and returns the persisted manifest.
//
// The reader is consumed part by part and each part is copied to a temporary
// ".part" file that is renamed into the job input directory only after the
// whole part was written. Nothing is buffered in memory and no multipart form
// is parsed into a temporary system directory.
//
// The caller owns the reader's transport and must close it or expire its read
// deadline to interrupt a blocked body read. Context cancellation alone cannot
// unblock an arbitrary multipart reader.
func (manager *Manager) Accept(ctx context.Context, reader *multipart.Reader) (storage.Manifest, error) {
	if reader == nil {
		return storage.Manifest{}, ErrInvalidUpload
	}
	manager.mutex.Lock()
	accepting := manager.accepting
	manager.mutex.Unlock()
	if !accepting {
		return storage.Manifest{}, ErrShuttingDown
	}

	budget, releaseBudget, err := manager.reserveUploadBudget()
	if err != nil {
		return storage.Manifest{}, err
	}
	defer releaseBudget()

	// Creation publishes a manifest. Register its lease before a cleanup
	// sweep can observe it without protection.
	manager.mutex.Lock()
	manifest, err := manager.store.Create(manager.now(), manager.jobTTL)
	if err == nil {
		manager.leases[manifest.ID]++
	}
	manager.mutex.Unlock()
	if err != nil {
		return storage.Manifest{}, err
	}
	defer manager.releaseLease(manifest.ID)
	if err := manager.receive(ctx, reader, &manifest, budget); err != nil {
		if deleteErr := manager.store.Delete(manifest.ID); deleteErr != nil &&
			!errors.Is(deleteErr, storage.ErrNotFound) {
			manager.logger.Warn("rejected upload could not be removed", slog.String("job", manifest.ID))
		}
		return storage.Manifest{}, err
	}

	// Probing runs on the service context rather than the request context. The
	// bytes are already committed to disk, so a client that disconnects after
	// the upload completed must still end up with a usable job instead of one
	// whose files are permanently marked as failed. Probing stays bounded by
	// the per-file timeout, the process semaphore, and service shutdown.
	manager.inspect(manager.baseCtx, &manifest)
	updated, err := manager.update(manifest.ID, func(current *storage.Manifest) error {
		current.Files = manifest.Files
		current.TotalBytes = manifest.TotalBytes
		if current.State == storage.JobCanceled {
			finished := manager.now().UTC()
			for index := range current.Files {
				file := &current.Files[index]
				if file.State.Terminal() {
					continue
				}
				file.State = storage.FileCanceled
				file.FinishedAt = &finished
				file.Error = &storage.Failure{
					Kind:    string(conversion.FailureCanceled),
					Code:    "canceled",
					Message: "conversion was canceled",
				}
			}
		}
		return nil
	})
	if err != nil {
		if deleteErr := manager.store.Delete(manifest.ID); deleteErr != nil &&
			!errors.Is(deleteErr, storage.ErrNotFound) {
			manager.logger.Warn("unsaved upload could not be removed", slog.String("job", manifest.ID))
		}
		return storage.Manifest{}, err
	}
	return updated, nil
}

// reserveUploadBudget reserves the maximum aggregate bytes this upload may
// store. Concurrent requests share the same accounting so they cannot each
// consume the same free-space headroom.
func (manager *Manager) reserveUploadBudget() (uploadBudget, func(), error) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()

	if !manager.accepting {
		return uploadBudget{}, nil, ErrShuttingDown
	}
	free, err := manager.freeSpace(manager.store.Root())
	if err != nil {
		return uploadBudget{}, nil, err
	}
	if free <= manager.minFreeSpace || free-manager.minFreeSpace <= manager.uploads {
		return uploadBudget{}, nil, ErrInsufficientSpace
	}
	usable := free - manager.minFreeSpace - manager.uploads
	budget := uploadBudget{
		bytes:    manager.maxUploadSize,
		limitErr: ErrUploadTooLarge,
	}
	if usable < manager.maxUploadSize {
		budget.bytes = usable
		budget.limitErr = ErrInsufficientSpace
	}
	manager.uploads += budget.bytes
	manager.workers.Add(1)

	var once sync.Once
	release := func() {
		once.Do(func() {
			manager.mutex.Lock()
			manager.uploads -= budget.bytes
			manager.mutex.Unlock()
			manager.workers.Done()
		})
	}
	return budget, release, nil
}

func (manager *Manager) receive(
	ctx context.Context,
	reader *multipart.Reader,
	manifest *storage.Manifest,
	budget uploadBudget,
) error {
	inputDir, err := manager.store.InputDir(manifest.ID)
	if err != nil {
		return err
	}
	tempDir, err := manager.store.TempDir(manifest.ID)
	if err != nil {
		return err
	}

	taken := make(map[string]bool)
	remaining := budget.bytes
	count := 0
	fields := 0

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if limitErr := uploadLimitError(err); limitErr != nil {
				return limitErr
			}
			return fmt.Errorf("%w: %s", ErrInvalidUpload, "multipart stream ended unexpectedly")
		}
		if part.FileName() == "" {
			fields++
			if fields > maxFormFields {
				return fmt.Errorf("%w: %s", ErrInvalidUpload, "upload carried too many form fields")
			}
			if err := drainPart(part); err != nil {
				return err
			}
			continue
		}

		count++
		if count > manager.maxFiles {
			return ErrTooManyFiles
		}
		fileID := fmt.Sprintf("f%03d", count)
		displayName := storage.SafeFileName(part.FileName(), fileID)
		storedName := storage.UniqueFileName(taken, displayName)

		targetPath, err := storage.Resolve(inputDir, storedName)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrInvalidUpload, "file name is not usable")
		}
		tempPath, err := storage.Resolve(tempDir, fileID+".part")
		if err != nil {
			return fmt.Errorf("%w: %s", ErrInvalidUpload, "temporary name is not usable")
		}

		written, err := writePart(part, tempPath, remaining, budget.limitErr)
		if err != nil {
			_ = os.Remove(tempPath)
			return err
		}
		if closeErr := part.Close(); closeErr != nil {
			_ = os.Remove(tempPath)
			if limitErr := uploadLimitError(closeErr); limitErr != nil {
				return limitErr
			}
			return fmt.Errorf("%w: %s", ErrInvalidUpload, "file part could not be closed")
		}
		if err := os.Rename(tempPath, targetPath); err != nil {
			_ = os.Remove(tempPath)
			return fmt.Errorf("jobs: publish uploaded file: %w", err)
		}
		remaining -= written

		manifest.Files = append(manifest.Files, storage.File{
			ID:           fileID,
			Name:         storedName,
			OriginalName: displayName,
			Size:         written,
			State:        storage.FileUploaded,
			Validation:   conversion.ValidationResult{Status: "not_run"},
		})
		manifest.TotalBytes += written
	}

	if count == 0 {
		return ErrNoFiles
	}
	if err := os.RemoveAll(tempDir); err != nil {
		return fmt.Errorf("jobs: clear upload staging directory: %w", err)
	}
	if err := os.Mkdir(tempDir, 0o750); err != nil {
		return fmt.Errorf("jobs: recreate upload staging directory: %w", err)
	}
	return nil
}

// writePart copies at most remaining bytes from part into path and reports the
// number of bytes written. Exceeding remaining returns limitErr so callers can
// distinguish configured size ceilings from disk-headroom admission failures.
func writePart(part *multipart.Part, path string, remaining int64, limitErr error) (int64, error) {
	if limitErr == nil {
		limitErr = ErrUploadTooLarge
	}
	if remaining <= 0 {
		return 0, limitErr
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return 0, fmt.Errorf("jobs: create upload part: %w", err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(part, remaining))
	if copyErr == nil {
		// Read the overflow byte without writing beyond the reserved budget.
		var overflow [1]byte
		read, err := part.Read(overflow[:])
		if read > 0 {
			_ = file.Close()
			return written, limitErr
		}
		if err != nil && !errors.Is(err, io.EOF) {
			copyErr = err
		}
	}
	if copyErr != nil {
		_ = file.Close()
		if limitErr := uploadLimitError(copyErr); limitErr != nil {
			return written, limitErr
		}
		return written, fmt.Errorf("%w: %s", ErrInvalidUpload, "file part could not be stored")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return written, fmt.Errorf("jobs: flush upload part: %w", err)
	}
	if err := file.Close(); err != nil {
		return written, fmt.Errorf("jobs: close upload part: %w", err)
	}
	if written == 0 {
		return 0, fmt.Errorf("%w: %s", ErrInvalidUpload, "file part was empty")
	}
	return written, nil
}

// drainPart discards one bounded non-file field. multipart.Part.Close reads
// the remainder of a part, so a field larger than the limit aborts the whole
// upload instead of being closed and silently consumed.
func drainPart(part *multipart.Part) error {
	read, err := io.Copy(io.Discard, io.LimitReader(part, maxFormFieldBytes+1))
	if err != nil {
		if limitErr := uploadLimitError(err); limitErr != nil {
			return limitErr
		}
		return fmt.Errorf("%w: %s", ErrInvalidUpload, "form field could not be read")
	}
	if read > maxFormFieldBytes {
		return fmt.Errorf("%w: %s", ErrInvalidUpload, "form field exceeds the field size limit")
	}
	if err := part.Close(); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidUpload, "form field could not be closed")
	}
	return nil
}

// uploadLimitError reports ErrUploadTooLarge when the transport refused to
// deliver more of the request body because it exceeded its own ceiling, and
// ErrUploadStalled when the body missed its read deadline or its throughput
// floor. It returns nil for every other error so callers keep their own
// classification.
func uploadLimitError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return ErrUploadTooLarge
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return ErrUploadStalled
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return ErrUploadStalled
	}
	return nil
}

func probeWaitFailure(err error) *storage.Failure {
	if errors.Is(err, context.DeadlineExceeded) {
		return &storage.Failure{
			Kind:    string(conversion.FailureProbe),
			Code:    "probe_backpressure",
			Message: "content probing timed out while waiting for capacity",
		}
	}
	return failureFrom(err)
}

// inspect probes every uploaded file and records the recommendation and the
// compatible alternatives. Probe failures mark the file failed without
// stopping the rest of the job.
func (manager *Manager) inspect(ctx context.Context, manifest *storage.Manifest) {
	inputDir, err := manager.store.InputDir(manifest.ID)
	if err != nil {
		return
	}
	timeout := inspectTimeout
	if manager.commandTimeout < timeout {
		timeout = manager.commandTimeout
	}

	for index := range manifest.Files {
		file := &manifest.Files[index]
		path, resolveErr := storage.Resolve(inputDir, file.Name)
		if resolveErr != nil {
			file.State = storage.FileFailed
			finished := manager.now().UTC()
			file.FinishedAt = &finished
			file.Error = &storage.Failure{
				Kind:    string(conversion.FailureConfiguration),
				Code:    "invalid_input",
				Message: "the uploaded file could not be located",
			}
			continue
		}

		// The wait for a process slot is bounded so intake never blocks
		// indefinitely, and its upload reservation is never held forever, when
		// every slot is occupied by long conversions.
		waitCtx, cancelWait := context.WithTimeout(ctx, timeout)
		select {
		case manager.semaphore <- struct{}{}:
			cancelWait()
		case <-waitCtx.Done():
			cancelWait()
			file.State = storage.FileFailed
			finished := manager.now().UTC()
			file.FinishedAt = &finished
			file.Error = probeWaitFailure(waitCtx.Err())
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		inspection, inspectErr := manager.converter.Inspect(probeCtx, path)
		cancel()
		<-manager.semaphore

		if inspectErr != nil {
			file.State = storage.FileFailed
			finished := manager.now().UTC()
			file.FinishedAt = &finished
			file.Error = failureFrom(inspectErr)
			continue
		}
		media := inspection.Media
		file.Media = &media
		file.Recommended = inspection.Recommended
		file.Compatible = inspection.Compatible
		file.Selected = inspection.Recommended
		file.OperationSource = "recommended"
		file.State = storage.FileInspected
	}
}

// OutputPath resolves the absolute path of a completed output inside a job.
func (manager *Manager) OutputPath(jobID string, file storage.File) (string, error) {
	if file.Output == nil {
		return "", fmt.Errorf("jobs: file %q has no output", file.ID)
	}
	outputRoot, err := manager.store.OutputDir(jobID)
	if err != nil {
		return "", err
	}
	fileDir, err := storage.Resolve(outputRoot, file.ID)
	if err != nil {
		return "", err
	}
	path, err := storage.Resolve(fileDir, filepath.Base(file.Output.Name))
	if err != nil {
		return "", err
	}
	return path, nil
}
