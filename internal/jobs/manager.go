// Package jobs runs bounded, cancellable conversion work for the filetwist
// web service. It owns the upload intake, the global process semaphore, the
// job queue, per-job cancellation, expiry cleanup with leases, and the atomic
// manifest transitions that back the HTMX interface.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/jobs/storage"
)

// deleteLeaseGrace bounds how long an explicit delete waits for an in-flight
// conversion to observe its cancellation and release the job lease.
const deleteLeaseGrace = 3 * time.Second

// Errors returned by Manager operations.
var (
	// ErrNotFound reports an unknown or already removed job.
	ErrNotFound = storage.ErrNotFound
	// ErrShuttingDown reports that intake stopped because the service is
	// shutting down.
	ErrShuttingDown = errors.New("jobs: service is shutting down")
	// ErrQueueFull reports that the bounded job queue has no free slot.
	ErrQueueFull = errors.New("jobs: queue is full")
	// ErrNotStartable reports a start request for a job that is not pending.
	ErrNotStartable = errors.New("jobs: job is not startable")
	// ErrNoConvertibleFiles reports a start request for a job whose files all
	// failed probing.
	ErrNoConvertibleFiles = errors.New("jobs: job has no convertible files")
	// ErrIncompatibleOperation reports a selection the probed file rejects.
	ErrIncompatibleOperation = errors.New("jobs: operation is not compatible with the file")
	// ErrLeased reports an operation blocked by an active lease.
	ErrLeased = errors.New("jobs: job is in use")
)

// Converter probes and converts local files. conversion.Service implements it.
type Converter interface {
	// Inspect probes one local file and reports the recommended and compatible
	// operations.
	Inspect(ctx context.Context, inputPath string) (conversion.Inspection, error)
	// Convert runs one named operation and reports the stable result.
	Convert(ctx context.Context, request conversion.Request) (conversion.Result, error)
}

// Options configures a Manager.
type Options struct {
	// Store owns the on-disk job tree.
	Store *storage.Store
	// Converter probes and converts files.
	Converter Converter
	// MaxConcurrentProcesses bounds simultaneous converter processes and sizes
	// the worker pool.
	MaxConcurrentProcesses int
	// MaxFilesPerJob bounds uploaded files in one job.
	MaxFilesPerJob int
	// MaxUploadSize bounds aggregate uploaded bytes in one job.
	MaxUploadSize int64
	// MinFreeSpace is the free-space floor enforced before accepting bytes.
	MinFreeSpace int64
	// JobTTL is the retention window applied to new jobs.
	JobTTL time.Duration
	// CommandTimeout bounds one converter invocation.
	CommandTimeout time.Duration
	// QueueDepth bounds jobs waiting for a worker. Zero selects a default.
	QueueDepth int
	// Logger receives operational messages. Zero selects the default logger.
	Logger *slog.Logger
	// Now returns the current time. Zero selects time.Now.
	Now func() time.Time
	// FreeSpace reports available bytes for upload admission. Zero selects the
	// platform implementation in storage.
	FreeSpace func(string) (int64, error)
}

// Manager coordinates every job lifecycle transition.
type Manager struct {
	store          *storage.Store
	converter      Converter
	maxFiles       int
	maxUploadSize  int64
	minFreeSpace   int64
	jobTTL         time.Duration
	commandTimeout time.Duration
	logger         *slog.Logger
	now            func() time.Time
	freeSpace      func(string) (int64, error)

	// semaphore is the single global bound shared by upload probes and
	// conversions.
	semaphore chan struct{}
	queue     chan string
	workers   sync.WaitGroup // Workers and accepted uploads drain together.

	baseCtx    context.Context
	stopWorker context.CancelFunc
	shutdown   sync.Once
	drained    chan struct{}

	mutex     sync.Mutex
	accepting bool
	jobLocks  map[string]*jobMutex
	controls  map[string]*jobControl
	leases    map[string]int
	uploads   int64
	queued    int
	running   int
}

// jobControl carries the cancellation state of one started job.
type jobControl struct {
	ctx    context.Context
	cancel context.CancelFunc
}

type jobMutex struct {
	sync.Mutex
	users int
}

// NewManager validates options, prepares the worker pool, and returns a
// Manager that is already accepting work.
func NewManager(options Options) (*Manager, error) {
	if options.Store == nil {
		return nil, errors.New("jobs: store must not be nil")
	}
	if options.Converter == nil {
		return nil, errors.New("jobs: converter must not be nil")
	}
	if options.MaxConcurrentProcesses < 1 {
		return nil, errors.New("jobs: max concurrent processes must be at least 1")
	}
	if options.MaxFilesPerJob < 1 {
		return nil, errors.New("jobs: max files per job must be at least 1")
	}
	if options.MaxUploadSize < 1 {
		return nil, errors.New("jobs: max upload size must be positive")
	}
	if options.MinFreeSpace < 0 {
		return nil, errors.New("jobs: min free space must not be negative")
	}
	if options.JobTTL <= 0 {
		return nil, errors.New("jobs: job ttl must be positive")
	}
	if options.CommandTimeout <= 0 {
		return nil, errors.New("jobs: command timeout must be positive")
	}
	queueDepth := options.QueueDepth
	if queueDepth <= 0 {
		queueDepth = 128
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	nowFunc := options.Now
	if nowFunc == nil {
		nowFunc = time.Now
	}
	freeSpace := options.FreeSpace
	if freeSpace == nil {
		freeSpace = storage.FreeSpace
	}

	baseCtx, stop := context.WithCancel(context.Background())
	manager := &Manager{
		store:          options.Store,
		converter:      options.Converter,
		maxFiles:       options.MaxFilesPerJob,
		maxUploadSize:  options.MaxUploadSize,
		minFreeSpace:   options.MinFreeSpace,
		jobTTL:         options.JobTTL,
		commandTimeout: options.CommandTimeout,
		logger:         logger,
		now:            nowFunc,
		freeSpace:      freeSpace,
		semaphore:      make(chan struct{}, options.MaxConcurrentProcesses),
		queue:          make(chan string, queueDepth),
		baseCtx:        baseCtx,
		stopWorker:     stop,
		accepting:      true,
		jobLocks:       make(map[string]*jobMutex),
		controls:       make(map[string]*jobControl),
		leases:         make(map[string]int),
		drained:        make(chan struct{}),
	}
	for worker := 0; worker < options.MaxConcurrentProcesses; worker++ {
		manager.workers.Add(1)
		go manager.work()
	}
	return manager, nil
}

// StopIntake prevents new uploads and conversion starts while allowing
// already accepted requests and workers to finish or be canceled separately.
func (manager *Manager) StopIntake() {
	manager.mutex.Lock()
	manager.accepting = false
	manager.mutex.Unlock()
}

// Store exposes the underlying job store for read-only path resolution.
func (manager *Manager) Store() *storage.Store {
	return manager.store
}

// MaxFilesPerJob reports the configured per-job file ceiling.
func (manager *Manager) MaxFilesPerJob() int {
	return manager.maxFiles
}

// MaxUploadSize reports the configured aggregate per-job byte ceiling.
func (manager *Manager) MaxUploadSize() int64 {
	return manager.maxUploadSize
}

// Get returns one manifest.
func (manager *Manager) Get(id string) (storage.Manifest, error) {
	return manager.store.Load(id)
}

// List returns every stored manifest, newest first.
func (manager *Manager) List() ([]storage.Manifest, error) {
	return manager.store.List()
}

// Stats is the queue and capacity summary exposed by diagnostics.
type Stats struct {
	// Queued is the number of jobs waiting for a worker.
	Queued int `json:"queued"`
	// Running is the number of jobs actively converting.
	Running int `json:"running"`
	// Capacity is the configured concurrent process ceiling.
	Capacity int `json:"capacity"`
	// QueueDepth is the bounded queue size.
	QueueDepth int `json:"queue_depth"`
	// Accepting reports whether intake is open.
	Accepting bool `json:"accepting"`
	// Leases is the number of jobs currently protected from cleanup.
	Leases int `json:"leases"`
}

// Stats reports the current queue and capacity summary.
func (manager *Manager) Stats() Stats {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	return Stats{
		Queued:     manager.queued,
		Running:    manager.running,
		Capacity:   cap(manager.semaphore),
		QueueDepth: cap(manager.queue),
		Accepting:  manager.accepting,
		Leases:     len(manager.leases),
	}
}

// Lease protects a job from cleanup until the returned release function runs.
// It fails when the job does not exist.
func (manager *Manager) Lease(id string) (func(), error) {
	unlock := manager.lockJob(id)
	defer unlock()
	if _, err := manager.store.Load(id); err != nil {
		return nil, err
	}
	manager.acquireLease(id)
	var once sync.Once
	return func() {
		once.Do(func() { manager.releaseLease(id) })
	}, nil
}

func (manager *Manager) acquireLease(id string) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	manager.leases[id]++
}

func (manager *Manager) releaseLease(id string) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if manager.leases[id] <= 1 {
		delete(manager.leases, id)
		return
	}
	manager.leases[id]--
}

// Leased reports whether a job currently holds at least one lease.
func (manager *Manager) Leased(id string) bool {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	return manager.leases[id] > 0
}

func (manager *Manager) lockJob(id string) func() {
	manager.mutex.Lock()
	lock, ok := manager.jobLocks[id]
	if !ok {
		lock = &jobMutex{}
		manager.jobLocks[id] = lock
	}
	lock.users++
	manager.mutex.Unlock()
	lock.Lock()
	return func() {
		lock.Unlock()
		manager.mutex.Lock()
		defer manager.mutex.Unlock()
		lock.users--
		if lock.users == 0 {
			delete(manager.jobLocks, id)
		}
	}
}

// update serializes manifest mutations for one job.
func (manager *Manager) update(id string, mutate func(*storage.Manifest) error) (storage.Manifest, error) {
	unlock := manager.lockJob(id)
	defer unlock()
	return manager.store.Update(id, manager.now(), mutate)
}

// Start validates the requested per-file operations and enqueues the job. The
// selections map is keyed by file identifier; a missing or empty entry keeps
// the recommended operation.
func (manager *Manager) Start(id string, selections map[string]corpus.Operation) (storage.Manifest, error) {
	unlock := manager.lockJob(id)
	defer unlock()
	// Register and enqueue under the intake lock so shutdown cannot close the
	// queue between accepting a start and publishing it to a worker.
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if !manager.accepting {
		return storage.Manifest{}, ErrShuttingDown
	}

	var convertible int
	manifest, err := manager.store.Update(id, manager.now(), func(manifest *storage.Manifest) error {
		if manifest.State != storage.JobPending {
			return ErrNotStartable
		}
		for index := range manifest.Files {
			file := &manifest.Files[index]
			if file.State != storage.FileInspected {
				continue
			}
			selected := file.Recommended
			source := "recommended"
			if requested, ok := selections[file.ID]; ok && requested != "" {
				if !containsOperation(file.Compatible, requested) {
					return fmt.Errorf("%w: %s", ErrIncompatibleOperation, requested)
				}
				selected = requested
				source = "requested"
			}
			file.Selected = selected
			file.OperationSource = source
			file.State = storage.FileQueued
			convertible++
		}
		if convertible == 0 {
			return ErrNoConvertibleFiles
		}
		if len(manager.queue) == cap(manager.queue) {
			return ErrQueueFull
		}
		manifest.State = storage.JobQueued
		return nil
	})
	if err != nil {
		return storage.Manifest{}, err
	}

	jobCtx, cancel := context.WithCancel(manager.baseCtx)
	manager.controls[id] = &jobControl{ctx: jobCtx, cancel: cancel}
	manager.queued++

	// Only Start enqueues, and concurrent starts hold the same mutex. The
	// available slot checked before persistence cannot be consumed meanwhile.
	manager.queue <- id
	return manifest, nil
}

// Cancel stops a queued or running job. Canceling a terminal job is a no-op
// that returns the current manifest.
func (manager *Manager) Cancel(id string) (storage.Manifest, error) {
	unlock := manager.lockJob(id)
	defer unlock()
	manifest, err := manager.store.Load(id)
	if err != nil {
		return storage.Manifest{}, err
	}
	if manifest.State.Terminal() {
		return manifest, nil
	}

	manager.mutex.Lock()
	control := manager.controls[id]
	manager.mutex.Unlock()
	if control != nil {
		control.cancel()
	}

	return manager.store.Update(id, manager.now(), func(manifest *storage.Manifest) error {
		if manifest.State.Terminal() {
			return nil
		}
		finished := manager.now().UTC()
		for index := range manifest.Files {
			file := &manifest.Files[index]
			if file.State.Terminal() {
				continue
			}
			file.State = storage.FileCanceled
			file.FinishedAt = &finished
			if file.Error == nil {
				file.Error = &storage.Failure{
					Kind:    string(conversion.FailureCanceled),
					Code:    "canceled",
					Message: "conversion was canceled",
				}
			}
		}
		manifest.State = storage.JobCanceled
		return nil
	})
}

// Delete cancels any active work and removes the job directory. It refuses
// while a download lease is held.
func (manager *Manager) Delete(id string) error {
	if err := storage.ValidateID(id); err != nil {
		return err
	}
	unlock := manager.lockJob(id)
	if _, err := manager.store.Load(id); err != nil {
		unlock()
		return err
	}
	manager.mutex.Lock()
	control := manager.controls[id]
	manager.mutex.Unlock()
	if control != nil {
		control.cancel()
	}
	unlock()
	if !manager.waitForLeaseRelease(id, deleteLeaseGrace) {
		return ErrLeased
	}

	unlock = manager.lockJob(id)
	defer unlock()
	if manager.Leased(id) {
		return ErrLeased
	}
	manager.mutex.Lock()
	if control := manager.controls[id]; control != nil {
		control.cancel()
	}
	manager.mutex.Unlock()
	if err := manager.store.Delete(id); err != nil {
		return err
	}
	manager.mutex.Lock()
	delete(manager.controls, id)
	manager.mutex.Unlock()
	return nil
}

// waitForLeaseRelease polls until no lease remains on a job or the grace
// period elapses. It bounds the wait so a delete request never blocks on a
// long download.
func (manager *Manager) waitForLeaseRelease(id string, grace time.Duration) bool {
	deadline := time.Now().Add(grace)
	for {
		if !manager.Leased(id) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// RecoverInterrupted marks every non-terminal job as interrupted. Interrupted
// jobs are never resumed; the user restarts or deletes them. It runs once at
// startup and returns the identifiers it changed.
func (manager *Manager) RecoverInterrupted() ([]string, error) {
	manifests, err := manager.store.List()
	if err != nil {
		return nil, err
	}
	changed := make([]string, 0, len(manifests))
	for _, manifest := range manifests {
		if manifest.State.Terminal() {
			continue
		}
		if _, err := manager.update(manifest.ID, func(current *storage.Manifest) error {
			finished := manager.now().UTC()
			for index := range current.Files {
				file := &current.Files[index]
				if file.State.Terminal() {
					continue
				}
				file.State = storage.FileInterrupted
				file.FinishedAt = &finished
				file.Error = &storage.Failure{
					Kind:    string(conversion.FailureConversion),
					Code:    "interrupted",
					Message: "conversion was interrupted by a service restart",
				}
			}
			current.State = storage.JobInterrupted
			current.Error = &storage.Failure{
				Kind:    string(conversion.FailureConversion),
				Code:    "interrupted",
				Message: "the service restarted before this job finished",
			}
			return nil
		}); err != nil {
			return changed, err
		}
		changed = append(changed, manifest.ID)
	}
	return changed, nil
}

// CleanupExpired removes every job whose retention window closed and that is
// neither leased nor actively converting. It returns the removed identifiers.
func (manager *Manager) CleanupExpired(now time.Time) ([]string, error) {
	manifests, err := manager.store.List()
	if err != nil {
		return nil, err
	}
	removed := make([]string, 0, len(manifests))
	for _, manifest := range manifests {
		if !manifest.Expired(now) {
			continue
		}
		if manager.Leased(manifest.ID) {
			continue
		}
		if manifest.State.Active() {
			continue
		}
		// The listing is a snapshot, so the job is re-read under its own lock
		// and re-checked. Without this a job started or leased during the sweep
		// could be deleted from under an enqueued worker or an active download.
		unlock := manager.lockJob(manifest.ID)
		current, loadErr := manager.store.Load(manifest.ID)
		if loadErr != nil || !current.Expired(now) ||
			current.State.Active() || manager.Leased(manifest.ID) {
			unlock()
			if loadErr != nil && !errors.Is(loadErr, storage.ErrNotFound) {
				manager.logger.Warn("job cleanup could not read manifest", slog.String("job", manifest.ID))
			}
			continue
		}
		deleteErr := manager.store.Delete(manifest.ID)
		if deleteErr != nil && !errors.Is(deleteErr, storage.ErrNotFound) {
			unlock()
			manager.logger.Warn("job cleanup failed", slog.String("job", manifest.ID))
			continue
		}
		manager.mutex.Lock()
		delete(manager.controls, manifest.ID)
		manager.mutex.Unlock()
		unlock()
		removed = append(removed, manifest.ID)
	}
	return removed, nil
}

// RunCleanup sweeps expired jobs on interval until ctx is done.
func (manager *Manager) RunCleanup(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			removed, err := manager.CleanupExpired(manager.now())
			if err != nil {
				manager.logger.Warn("expired job sweep failed", slog.String("error", err.Error()))
				continue
			}
			if len(removed) > 0 {
				manager.logger.Info("expired jobs removed", slog.Int("count", len(removed)))
			}
		}
	}
}

// Shutdown stops intake, cancels in-flight work, and waits for the worker pool
// and accepted uploads to drain within the supplied context deadline.
func (manager *Manager) Shutdown(ctx context.Context) error {
	manager.shutdown.Do(func() {
		manager.StopIntake()
		manager.mutex.Lock()
		manager.stopWorker()
		close(manager.queue)
		manager.mutex.Unlock()
		go func() {
			manager.workers.Wait()
			close(manager.drained)
		}()
	})

	select {
	case <-manager.drained:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("jobs: worker drain did not finish: %w", ctx.Err())
	}
}

func (manager *Manager) work() {
	defer manager.workers.Done()
	for id := range manager.queue {
		manager.mutex.Lock()
		manager.queued--
		manager.running++
		control := manager.controls[id]
		manager.mutex.Unlock()

		manager.process(id, control)

		manager.mutex.Lock()
		manager.running--
		delete(manager.controls, id)
		manager.mutex.Unlock()
		if control != nil {
			control.cancel()
		}
	}
}

func (manager *Manager) process(id string, control *jobControl) {
	release, err := manager.Lease(id)
	if err != nil {
		return
	}
	defer release()

	jobCtx := manager.baseCtx
	if control != nil {
		jobCtx = control.ctx
	}

	manifest, err := manager.update(id, func(manifest *storage.Manifest) error {
		if manifest.State != storage.JobQueued {
			return ErrNotStartable
		}
		manifest.State = storage.JobRunning
		return nil
	})
	if err != nil {
		if !errors.Is(err, ErrNotStartable) && !errors.Is(err, storage.ErrNotFound) {
			manager.logger.Warn("job could not start", slog.String("job", id))
		}
		return
	}

	for _, queuedFile := range manifest.Files {
		if queuedFile.State != storage.FileQueued {
			continue
		}
		if jobCtx.Err() != nil {
			break
		}
		manager.convertFile(jobCtx, id, queuedFile)
	}
	manager.finalize(id, jobCtx.Err() != nil)
}

func (manager *Manager) convertFile(ctx context.Context, id string, file storage.File) {
	started := manager.now().UTC()
	if _, err := manager.update(id, func(manifest *storage.Manifest) error {
		target := manifest.FileByID(file.ID)
		if manifest.State != storage.JobRunning || target == nil || target.State != storage.FileQueued {
			return ErrNotStartable
		}
		target.State = storage.FileRunning
		target.StartedAt = &started
		return nil
	}); err != nil {
		if !errors.Is(err, ErrNotStartable) {
			manager.logger.Warn("file transition failed", slog.String("job", id))
		}
		return
	}

	select {
	case manager.semaphore <- struct{}{}:
	case <-ctx.Done():
		manager.recordCancel(id, file.ID)
		return
	}
	defer func() { <-manager.semaphore }()

	if ctx.Err() != nil {
		manager.recordCancel(id, file.ID)
		return
	}

	inputDir, err := manager.store.InputDir(id)
	if err != nil {
		manager.recordFailure(id, file.ID, failureFrom(err))
		return
	}
	inputPath, err := storage.Resolve(inputDir, file.Name)
	if err != nil {
		manager.recordFailure(id, file.ID, failureFrom(err))
		return
	}
	outputRoot, err := manager.store.OutputDir(id)
	if err != nil {
		manager.recordFailure(id, file.ID, failureFrom(err))
		return
	}
	outputDir, err := storage.Resolve(outputRoot, file.ID)
	if err != nil {
		manager.recordFailure(id, file.ID, failureFrom(err))
		return
	}
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		manager.recordFailure(id, file.ID, &storage.Failure{
			Kind:    string(conversion.FailureConfiguration),
			Code:    "output_unavailable",
			Message: "the output directory could not be created",
		})
		return
	}

	convertCtx, cancel := context.WithTimeout(ctx, manager.commandTimeout)
	defer cancel()
	result, convertErr := manager.converter.Convert(convertCtx, conversion.Request{
		InputPath:    inputPath,
		Output:       outputDir,
		Operation:    file.Selected,
		OperationSet: true,
	})

	finished := manager.now().UTC()
	if _, err := manager.update(id, func(manifest *storage.Manifest) error {
		target := manifest.FileByID(file.ID)
		if manifest.State.Terminal() || target == nil || target.State.Terminal() {
			return nil
		}
		target.FinishedAt = &finished
		target.Validation = result.Validation
		target.Warnings = result.Warnings
		target.Execution = result.Execution
		target.FallbackReason = result.Execution.FallbackReason
		if convertErr != nil {
			failure := failureFrom(convertErr)
			target.Error = failure
			if failure.Kind == string(conversion.FailureCanceled) {
				target.State = storage.FileCanceled
			} else {
				target.State = storage.FileFailed
			}
			return nil
		}
		name := filepath.Base(result.OutputPath)
		size := int64(0)
		if info, statErr := os.Stat(result.OutputPath); statErr == nil {
			size = info.Size()
		}
		target.State = storage.FileCompleted
		target.Output = &storage.Output{
			Name:     name,
			Size:     size,
			MIMEType: storage.ContentType(name),
		}
		return nil
	}); err != nil {
		manager.logger.Warn("file result could not be persisted", slog.String("job", id))
	}
}

func (manager *Manager) recordCancel(id, fileID string) {
	finished := manager.now().UTC()
	if _, err := manager.update(id, func(manifest *storage.Manifest) error {
		target := manifest.FileByID(fileID)
		if target == nil || target.State.Terminal() {
			return nil
		}
		target.State = storage.FileCanceled
		target.FinishedAt = &finished
		target.Error = &storage.Failure{
			Kind:    string(conversion.FailureCanceled),
			Code:    "canceled",
			Message: "conversion was canceled",
		}
		return nil
	}); err != nil {
		manager.logger.Warn("cancel state could not be persisted", slog.String("job", id))
	}
}

func (manager *Manager) recordFailure(id, fileID string, failure *storage.Failure) {
	finished := manager.now().UTC()
	if _, err := manager.update(id, func(manifest *storage.Manifest) error {
		target := manifest.FileByID(fileID)
		if target == nil || target.State.Terminal() {
			return nil
		}
		target.State = storage.FileFailed
		target.FinishedAt = &finished
		target.Error = failure
		return nil
	}); err != nil {
		manager.logger.Warn("failure state could not be persisted", slog.String("job", id))
	}
}

func (manager *Manager) finalize(id string, canceled bool) {
	if _, err := manager.update(id, func(manifest *storage.Manifest) error {
		if manifest.State.Terminal() {
			return nil
		}
		finished := manager.now().UTC()
		var completed, failed int
		for index := range manifest.Files {
			file := &manifest.Files[index]
			if !file.State.Terminal() {
				file.State = storage.FileCanceled
				file.FinishedAt = &finished
				if file.Error == nil {
					file.Error = &storage.Failure{
						Kind:    string(conversion.FailureCanceled),
						Code:    "canceled",
						Message: "conversion was canceled",
					}
				}
			}
			switch file.State {
			case storage.FileCompleted:
				completed++
			case storage.FileFailed:
				failed++
			}
		}
		switch {
		case completed > 0:
			manifest.State = storage.JobCompleted
		case canceled:
			manifest.State = storage.JobCanceled
		case failed > 0:
			manifest.State = storage.JobFailed
		default:
			manifest.State = storage.JobCanceled
		}
		return nil
	}); err != nil && !errors.Is(err, storage.ErrNotFound) {
		manager.logger.Warn("job could not be finalized", slog.String("job", id))
	}
}

func containsOperation(operations []corpus.Operation, candidate corpus.Operation) bool {
	for _, operation := range operations {
		if operation == candidate {
			return true
		}
	}
	return false
}

func failureFrom(err error) *storage.Failure {
	var classified *conversion.Error
	if errors.As(err, &classified) {
		return &storage.Failure{
			Kind:    string(classified.Kind),
			Code:    classified.Code,
			Message: classified.Message,
		}
	}
	if errors.Is(err, context.Canceled) {
		return &storage.Failure{
			Kind:    string(conversion.FailureCanceled),
			Code:    "canceled",
			Message: "conversion was canceled",
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &storage.Failure{
			Kind:    string(conversion.FailureConversion),
			Code:    "timeout",
			Message: "conversion exceeded the command timeout",
		}
	}
	return &storage.Failure{
		Kind:    string(conversion.FailureConversion),
		Code:    "conversion_failed",
		Message: "conversion failed",
	}
}
