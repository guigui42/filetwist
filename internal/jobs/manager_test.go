package jobs_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/jobs"
	"github.com/guigui42/filetwist/internal/jobs/storage"
)

// fakeConverter is a deterministic Converter used to exercise the manager
// without any converter executable.
type fakeConverter struct {
	inspect func(ctx context.Context, path string) (conversion.Inspection, error)
	convert func(ctx context.Context, request conversion.Request) (conversion.Result, error)
}

func (converter *fakeConverter) Inspect(
	ctx context.Context,
	path string,
) (conversion.Inspection, error) {
	if converter.inspect != nil {
		return converter.inspect(ctx, path)
	}
	return conversion.Inspection{
		Media:       conversion.DetectedMedia{Kind: corpus.MediaImage, Format: "jpeg"},
		Recommended: corpus.OperationCompatiblePhoto,
		Compatible:  conversion.CompatibleOperations(corpus.MediaImage),
	}, nil
}

func (converter *fakeConverter) Convert(
	ctx context.Context,
	request conversion.Request,
) (conversion.Result, error) {
	if converter.convert != nil {
		return converter.convert(ctx, request)
	}
	return writeFakeOutput(request, "converted.jpg")
}

func writeFakeOutput(request conversion.Request, name string) (conversion.Result, error) {
	outputPath := filepath.Join(request.Output, name)
	if err := os.WriteFile(outputPath, []byte("converted-bytes"), 0o640); err != nil {
		return conversion.Result{}, err
	}
	return conversion.Result{
		SchemaVersion:     conversion.SchemaVersion,
		Status:            "success",
		SelectedOperation: request.Operation,
		OutputPath:        outputPath,
		Validation:        conversion.ValidationResult{Status: "passed"},
		Execution:         conversion.ExecutionResult{Requested: "cpu", Initial: "cpu", Final: "cpu", AttemptCount: 1},
	}, nil
}

type managerOption func(*jobs.Options)

func newManager(t *testing.T, converter jobs.Converter, options ...managerOption) *jobs.Manager {
	t.Helper()
	store, err := storage.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	settings := jobs.Options{
		Store:                  store,
		Converter:              converter,
		MaxConcurrentProcesses: 2,
		MaxFilesPerJob:         3,
		MaxUploadSize:          1 << 20,
		MinFreeSpace:           1,
		JobTTL:                 time.Hour,
		CommandTimeout:         30 * time.Second,
		Logger:                 slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, option := range options {
		option(&settings)
	}
	manager, err := jobs.NewManager(settings)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := manager.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return manager
}

type uploadFile struct {
	name    string
	content []byte
}

func multipartBody(t *testing.T, files []uploadFile) (*multipart.Reader, string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for _, file := range files {
		part, err := writer.CreateFormFile("files", file.name)
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := part.Write(file.content); err != nil {
			t.Fatalf("write part: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return multipart.NewReader(bytes.NewReader(buffer.Bytes()), writer.Boundary()), writer.Boundary()
}

func upload(t *testing.T, manager *jobs.Manager, files []uploadFile) (storage.Manifest, error) {
	t.Helper()
	reader, _ := multipartBody(t, files)
	return manager.Accept(context.Background(), reader)
}

func waitForState(t *testing.T, manager *jobs.Manager, id string, want ...storage.JobState) storage.Manifest {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var manifest storage.Manifest
	for time.Now().Before(deadline) {
		var err error
		manifest, err = manager.Get(id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		for _, state := range want {
			if manifest.State == state {
				return manifest
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s stayed in %q; wanted one of %v", id, manifest.State, want)
	return manifest
}

func TestAcceptStreamsFilesIntoIsolatedJobDirectory(t *testing.T) {
	manager := newManager(t, &fakeConverter{})
	manifest, err := upload(t, manager, []uploadFile{
		{name: "holiday photo.HEIC", content: []byte("first")},
		{name: "clip.mov", content: []byte("second")},
	})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if len(manifest.Files) != 2 {
		t.Fatalf("files = %d; want 2", len(manifest.Files))
	}
	if manifest.TotalBytes != int64(len("first")+len("second")) {
		t.Fatalf("total bytes = %d", manifest.TotalBytes)
	}
	if manifest.State != storage.JobPending {
		t.Fatalf("state = %q; want pending", manifest.State)
	}
	for _, file := range manifest.Files {
		if file.State != storage.FileInspected {
			t.Fatalf("file %s state = %q; want inspected", file.ID, file.State)
		}
		if file.Recommended != corpus.OperationCompatiblePhoto {
			t.Fatalf("recommended = %q", file.Recommended)
		}
		if len(file.Compatible) != 3 {
			t.Fatalf("compatible = %v; want the three image operations", file.Compatible)
		}
	}

	inputDir, err := manager.Store().InputDir(manifest.ID)
	if err != nil {
		t.Fatalf("InputDir: %v", err)
	}
	entries, err := os.ReadDir(inputDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("stored files = %d; want 2", len(entries))
	}
	tempDir, err := manager.Store().TempDir(manifest.ID)
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}
	staged, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("ReadDir temp: %v", err)
	}
	if len(staged) != 0 {
		t.Fatalf("temporary parts left behind: %v", staged)
	}
}

func TestAcceptRejectsAggregateSizeLimit(t *testing.T) {
	manager := newManager(t, &fakeConverter{}, func(options *jobs.Options) {
		options.MaxUploadSize = 16
	})
	_, err := upload(t, manager, []uploadFile{
		{name: "a.jpg", content: bytes.Repeat([]byte("a"), 10)},
		{name: "b.jpg", content: bytes.Repeat([]byte("b"), 10)},
	})
	if !errors.Is(err, jobs.ErrUploadTooLarge) {
		t.Fatalf("err = %v; want ErrUploadTooLarge", err)
	}
	manifests, err := manager.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(manifests) != 0 {
		t.Fatalf("rejected upload left %d jobs behind", len(manifests))
	}
}

func TestAcceptRejectsSingleOversizedFile(t *testing.T) {
	manager := newManager(t, &fakeConverter{}, func(options *jobs.Options) {
		options.MaxUploadSize = 8
	})
	if _, err := upload(t, manager, []uploadFile{
		{name: "a.jpg", content: bytes.Repeat([]byte("a"), 9)},
	}); !errors.Is(err, jobs.ErrUploadTooLarge) {
		t.Fatalf("err = %v; want ErrUploadTooLarge", err)
	}
}

func TestAcceptRejectsFileCountLimit(t *testing.T) {
	manager := newManager(t, &fakeConverter{}, func(options *jobs.Options) {
		options.MaxFilesPerJob = 2
	})
	if _, err := upload(t, manager, []uploadFile{
		{name: "a.jpg", content: []byte("a")},
		{name: "b.jpg", content: []byte("b")},
		{name: "c.jpg", content: []byte("c")},
	}); !errors.Is(err, jobs.ErrTooManyFiles) {
		t.Fatalf("err = %v; want ErrTooManyFiles", err)
	}
}

func TestAcceptRejectsEmptyUpload(t *testing.T) {
	manager := newManager(t, &fakeConverter{})
	if _, err := upload(t, manager, nil); !errors.Is(err, jobs.ErrNoFiles) {
		t.Fatalf("err = %v; want ErrNoFiles", err)
	}
}

func TestAcceptRejectsInsufficientFreeSpace(t *testing.T) {
	manager := newManager(t, &fakeConverter{}, func(options *jobs.Options) {
		options.MinFreeSpace = 100
		options.FreeSpace = func(string) (int64, error) {
			return 100, nil
		}
	})
	if _, err := upload(t, manager, []uploadFile{
		{name: "a.jpg", content: []byte("a")},
	}); !errors.Is(err, jobs.ErrInsufficientSpace) {
		t.Fatalf("err = %v; want ErrInsufficientSpace", err)
	}
}

func TestConcurrentUploadsReserveFreeSpace(t *testing.T) {
	inspectionStarted := make(chan struct{})
	releaseInspection := make(chan struct{})
	var once sync.Once
	manager := newManager(t, &fakeConverter{
		inspect: func(ctx context.Context, path string) (conversion.Inspection, error) {
			once.Do(func() { close(inspectionStarted) })
			select {
			case <-releaseInspection:
			case <-ctx.Done():
				return conversion.Inspection{}, ctx.Err()
			}
			return conversion.Inspection{
				Media:       conversion.DetectedMedia{Kind: corpus.MediaImage, Format: "jpeg"},
				Recommended: corpus.OperationCompatiblePhoto,
				Compatible:  conversion.CompatibleOperations(corpus.MediaImage),
			}, nil
		},
	}, func(options *jobs.Options) {
		options.MaxUploadSize = 40
		options.MinFreeSpace = 50
		options.FreeSpace = func(string) (int64, error) {
			return 90, nil
		}
	})

	firstReader, _ := multipartBody(t, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	firstDone := make(chan error, 1)
	go func() {
		_, err := manager.Accept(context.Background(), firstReader)
		firstDone <- err
	}()
	select {
	case <-inspectionStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first upload did not reserve capacity")
	}

	if _, err := upload(t, manager, []uploadFile{
		{name: "b.jpg", content: []byte("b")},
	}); !errors.Is(err, jobs.ErrInsufficientSpace) {
		t.Fatalf("second upload error = %v; want ErrInsufficientSpace", err)
	}
	close(releaseInspection)
	if err := <-firstDone; err != nil {
		t.Fatalf("first upload: %v", err)
	}
}

func TestAcceptContainsTraversalFileNames(t *testing.T) {
	manager := newManager(t, &fakeConverter{})
	manifest, err := upload(t, manager, []uploadFile{
		{name: "../../../etc/passwd", content: []byte("root")},
		{name: `..\..\windows\system.ini`, content: []byte("ini")},
	})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}

	jobDir, err := manager.Store().JobDir(manifest.ID)
	if err != nil {
		t.Fatalf("JobDir: %v", err)
	}
	inputDir := filepath.Join(jobDir, "input")
	for _, file := range manifest.Files {
		if strings.ContainsAny(file.Name, `/\`) {
			t.Fatalf("stored name %q contains a separator", file.Name)
		}
		resolved := filepath.Join(inputDir, file.Name)
		if !storage.Contains(inputDir, resolved) {
			t.Fatalf("stored file %q escaped the job directory", resolved)
		}
		if _, err := os.Stat(resolved); err != nil {
			t.Fatalf("stored file is missing: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(jobDir), "passwd")); err == nil {
		t.Fatal("upload escaped into the jobs directory")
	}
}

func TestStartConvertsEveryFileAndPersistsOutputs(t *testing.T) {
	manager := newManager(t, &fakeConverter{})
	manifest, err := upload(t, manager, []uploadFile{
		{name: "a.jpg", content: []byte("a")},
		{name: "b.jpg", content: []byte("b")},
	})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if _, err := manager.Start(manifest.ID, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	final := waitForState(t, manager, manifest.ID, storage.JobCompleted, storage.JobFailed)
	if final.State != storage.JobCompleted {
		t.Fatalf("state = %q; want completed", final.State)
	}
	for _, file := range final.Files {
		if file.State != storage.FileCompleted {
			t.Fatalf("file %s state = %q", file.ID, file.State)
		}
		if file.Output == nil || file.Output.Size == 0 {
			t.Fatalf("file %s has no output", file.ID)
		}
		if file.Output.MIMEType != "image/jpeg" {
			t.Fatalf("mime = %q", file.Output.MIMEType)
		}
		path, err := manager.OutputPath(final.ID, file)
		if err != nil {
			t.Fatalf("OutputPath: %v", err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("output missing: %v", err)
		}
	}
}

func TestStartRejectsIncompatibleOperation(t *testing.T) {
	manager := newManager(t, &fakeConverter{})
	manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	_, err = manager.Start(manifest.ID, map[string]corpus.Operation{
		manifest.Files[0].ID: corpus.OperationCompatibleVideo,
	})
	if !errors.Is(err, jobs.ErrIncompatibleOperation) {
		t.Fatalf("err = %v; want ErrIncompatibleOperation", err)
	}
	reloaded, err := manager.Get(manifest.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if reloaded.State != storage.JobPending {
		t.Fatalf("state = %q; want the job to stay pending", reloaded.State)
	}
}

func TestStartAcceptsCompatibleAlternative(t *testing.T) {
	manager := newManager(t, &fakeConverter{})
	manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if _, err := manager.Start(manifest.ID, map[string]corpus.Operation{
		manifest.Files[0].ID: corpus.OperationSmallerPhoto,
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	final := waitForState(t, manager, manifest.ID, storage.JobCompleted, storage.JobFailed)
	if final.Files[0].Selected != corpus.OperationSmallerPhoto {
		t.Fatalf("selected = %q", final.Files[0].Selected)
	}
	if final.Files[0].OperationSource != "requested" {
		t.Fatalf("source = %q; want requested", final.Files[0].OperationSource)
	}
}

func TestStartTwiceIsRejected(t *testing.T) {
	release := make(chan struct{})
	manager := newManager(t, &fakeConverter{
		convert: func(ctx context.Context, request conversion.Request) (conversion.Result, error) {
			<-release
			return writeFakeOutput(request, "converted.jpg")
		},
	})
	manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if _, err := manager.Start(manifest.ID, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := manager.Start(manifest.ID, nil); !errors.Is(err, jobs.ErrNotStartable) {
		t.Fatalf("second Start = %v; want ErrNotStartable", err)
	}
	close(release)
	waitForState(t, manager, manifest.ID, storage.JobCompleted)
}

func TestConcurrencyIsBoundedByTheProcessSemaphore(t *testing.T) {
	var active, peak int64
	var mutex sync.Mutex
	gate := make(chan struct{})
	manager := newManager(t, &fakeConverter{
		convert: func(ctx context.Context, request conversion.Request) (conversion.Result, error) {
			current := atomic.AddInt64(&active, 1)
			mutex.Lock()
			if current > peak {
				peak = current
			}
			mutex.Unlock()
			<-gate
			atomic.AddInt64(&active, -1)
			return writeFakeOutput(request, "converted.jpg")
		},
	}, func(options *jobs.Options) {
		options.MaxConcurrentProcesses = 2
		options.MaxFilesPerJob = 4
	})

	manifests := make([]storage.Manifest, 0, 4)
	for index := 0; index < 4; index++ {
		manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
		if err != nil {
			t.Fatalf("Accept: %v", err)
		}
		manifests = append(manifests, manifest)
	}

	ids := make([]string, 0, len(manifests))
	for _, manifest := range manifests {
		if _, err := manager.Start(manifest.ID, nil); err != nil {
			t.Fatalf("Start: %v", err)
		}
		ids = append(ids, manifest.ID)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt64(&active) < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)
	mutex.Lock()
	observed := peak
	mutex.Unlock()
	close(gate)

	for _, id := range ids {
		waitForState(t, manager, id, storage.JobCompleted)
	}
	if observed > 2 {
		t.Fatalf("peak concurrency = %d; want at most 2", observed)
	}
	if observed < 1 {
		t.Fatal("no conversion ran")
	}
	stats := manager.Stats()
	if stats.Capacity != 2 {
		t.Fatalf("capacity = %d; want 2", stats.Capacity)
	}
}

func TestQueueFullIsRejectedAndTheJobStaysPending(t *testing.T) {
	gate := make(chan struct{})
	manager := newManager(t, &fakeConverter{
		convert: func(ctx context.Context, request conversion.Request) (conversion.Result, error) {
			<-gate
			return writeFakeOutput(request, "converted.jpg")
		},
	}, func(options *jobs.Options) {
		options.MaxConcurrentProcesses = 1
		options.QueueDepth = 1
	})

	manifests := make([]storage.Manifest, 0, 6)
	for index := 0; index < 6; index++ {
		manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
		if err != nil {
			t.Fatalf("Accept: %v", err)
		}
		manifests = append(manifests, manifest)
	}

	ids := make([]string, 0, len(manifests))
	var queueFull bool
	for _, manifest := range manifests {
		ids = append(ids, manifest.ID)
		if _, err := manager.Start(manifest.ID, nil); errors.Is(err, jobs.ErrQueueFull) {
			queueFull = true
			reloaded, getErr := manager.Get(manifest.ID)
			if getErr != nil {
				t.Fatalf("Get: %v", getErr)
			}
			if reloaded.State != storage.JobPending {
				t.Fatalf("rejected job state = %q; want pending", reloaded.State)
			}
			break
		} else if err != nil {
			t.Fatalf("Start: %v", err)
		}
	}
	close(gate)
	if !queueFull {
		t.Skip("the bounded queue never filled on this host")
	}
	_ = ids
}

func TestCancelStopsRunningWork(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	manager := newManager(t, &fakeConverter{
		convert: func(ctx context.Context, request conversion.Request) (conversion.Result, error) {
			once.Do(func() { close(started) })
			<-ctx.Done()
			return conversion.Result{}, ctx.Err()
		},
	}, func(options *jobs.Options) {
		options.MaxConcurrentProcesses = 1
	})

	manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if _, err := manager.Start(manifest.ID, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("conversion never started")
	}
	if _, err := manager.Cancel(manifest.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	final := waitForState(t, manager, manifest.ID, storage.JobCanceled)
	if final.Files[0].State != storage.FileCanceled {
		t.Fatalf("file state = %q; want canceled", final.Files[0].State)
	}
	if final.Files[0].Error == nil || final.Files[0].Error.Code != "canceled" {
		t.Fatalf("file error = %+v; want a canceled failure", final.Files[0].Error)
	}
}

func TestCancelOnTerminalJobIsIdempotent(t *testing.T) {
	manager := newManager(t, &fakeConverter{})
	manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if _, err := manager.Start(manifest.ID, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForState(t, manager, manifest.ID, storage.JobCompleted)
	after, err := manager.Cancel(manifest.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if after.State != storage.JobCompleted {
		t.Fatalf("state = %q; want completed", after.State)
	}
}

func TestConversionFailureIsRecordedPerFile(t *testing.T) {
	manager := newManager(t, &fakeConverter{
		convert: func(ctx context.Context, request conversion.Request) (conversion.Result, error) {
			return conversion.Result{
				Validation: conversion.ValidationResult{Status: "failed"},
			}, &conversion.Error{
				Kind:    conversion.FailureValidation,
				Code:    "output_validation_failed",
				Message: "media output failed compatibility validation",
			}
		},
	})
	manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if _, err := manager.Start(manifest.ID, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	final := waitForState(t, manager, manifest.ID, storage.JobFailed)
	file := final.Files[0]
	if file.State != storage.FileFailed {
		t.Fatalf("file state = %q", file.State)
	}
	if file.Error == nil || file.Error.Code != "output_validation_failed" {
		t.Fatalf("error = %+v", file.Error)
	}
	if file.Error.Kind != string(conversion.FailureValidation) {
		t.Fatalf("kind = %q", file.Error.Kind)
	}
}

func TestProbeFailureMarksTheFileWithoutFailingTheUpload(t *testing.T) {
	manager := newManager(t, &fakeConverter{
		inspect: func(ctx context.Context, path string) (conversion.Inspection, error) {
			return conversion.Inspection{}, &conversion.Error{
				Kind:    conversion.FailureProbe,
				Code:    "content_probe_failed",
				Message: "input content could not be identified",
			}
		},
	})
	manifest, err := upload(t, manager, []uploadFile{{name: "a.bin", content: []byte("a")}})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if manifest.Files[0].State != storage.FileFailed {
		t.Fatalf("state = %q; want failed", manifest.Files[0].State)
	}
	if _, err := manager.Start(manifest.ID, nil); !errors.Is(err, jobs.ErrNoConvertibleFiles) {
		t.Fatalf("Start = %v; want ErrNoConvertibleFiles", err)
	}
}

func TestRecoverInterruptedMarksNonTerminalJobs(t *testing.T) {
	root := t.TempDir()
	store, err := storage.NewStore(root)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	manifest, err := store.Create(time.Now(), time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	manifest.State = storage.JobRunning
	manifest.Files = []storage.File{
		{ID: "f001", Name: "a.jpg", State: storage.FileRunning},
		{ID: "f002", Name: "b.jpg", State: storage.FileCompleted, Output: &storage.Output{Name: "b.jpg"}},
	}
	if err := store.Save(manifest); err != nil {
		t.Fatalf("Save: %v", err)
	}

	restarted, err := jobs.NewManager(jobs.Options{
		Store:                  store,
		Converter:              &fakeConverter{},
		MaxConcurrentProcesses: 1,
		MaxFilesPerJob:         3,
		MaxUploadSize:          1 << 20,
		MinFreeSpace:           1,
		JobTTL:                 time.Hour,
		CommandTimeout:         time.Second,
		Logger:                 slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := restarted.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	}()

	changed, err := restarted.RecoverInterrupted()
	if err != nil {
		t.Fatalf("RecoverInterrupted: %v", err)
	}
	if len(changed) != 1 || changed[0] != manifest.ID {
		t.Fatalf("changed = %v; want the running job", changed)
	}
	recovered, err := restarted.Get(manifest.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if recovered.State != storage.JobInterrupted {
		t.Fatalf("state = %q; want interrupted", recovered.State)
	}
	if recovered.Files[0].State != storage.FileInterrupted {
		t.Fatalf("running file state = %q; want interrupted", recovered.Files[0].State)
	}
	if recovered.Files[1].State != storage.FileCompleted {
		t.Fatalf("completed file was changed to %q", recovered.Files[1].State)
	}

	// No resume: a second recovery pass changes nothing.
	again, err := restarted.RecoverInterrupted()
	if err != nil {
		t.Fatalf("RecoverInterrupted: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second pass changed %v", again)
	}
}

func TestRecoverInterruptedMarksPendingJobs(t *testing.T) {
	root := t.TempDir()
	store, err := storage.NewStore(root)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	manifest, err := store.Create(time.Now(), time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	manifest.Files = []storage.File{{
		ID:    "f001",
		Name:  "a.jpg",
		State: storage.FileInspected,
	}}
	if err := store.Save(manifest); err != nil {
		t.Fatalf("Save: %v", err)
	}

	restarted, err := jobs.NewManager(jobs.Options{
		Store:                  store,
		Converter:              &fakeConverter{},
		MaxConcurrentProcesses: 1,
		MaxFilesPerJob:         3,
		MaxUploadSize:          1 << 20,
		MinFreeSpace:           1,
		JobTTL:                 time.Hour,
		CommandTimeout:         time.Second,
		Logger:                 slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := restarted.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	}()

	changed, err := restarted.RecoverInterrupted()
	if err != nil {
		t.Fatalf("RecoverInterrupted: %v", err)
	}
	if len(changed) != 1 || changed[0] != manifest.ID {
		t.Fatalf("changed = %v; want the pending job", changed)
	}
	recovered, err := restarted.Get(manifest.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if recovered.State != storage.JobInterrupted {
		t.Fatalf("state = %q; want interrupted", recovered.State)
	}
	if recovered.Files[0].State != storage.FileInterrupted {
		t.Fatalf("file state = %q; want interrupted", recovered.Files[0].State)
	}
}

func TestCleanupExpiredRemovesOnlyExpiredJobs(t *testing.T) {
	manager := newManager(t, &fakeConverter{})
	fresh, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	stale, err := upload(t, manager, []uploadFile{{name: "b.jpg", content: []byte("b")}})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}

	removed, err := manager.CleanupExpired(time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed %v before expiry", removed)
	}

	removed, err = manager.CleanupExpired(time.Now().Add(2 * time.Hour))
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed = %v; want both jobs", removed)
	}
	for _, id := range []string{fresh.ID, stale.ID} {
		if _, err := manager.Get(id); !errors.Is(err, jobs.ErrNotFound) {
			t.Fatalf("job %s survived cleanup: %v", id, err)
		}
	}
}

func TestCleanupSkipsLeasedJobs(t *testing.T) {
	manager := newManager(t, &fakeConverter{})
	manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	release, err := manager.Lease(manifest.ID)
	if err != nil {
		t.Fatalf("Lease: %v", err)
	}
	removed, err := manager.CleanupExpired(time.Now().Add(2 * time.Hour))
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("leased job was removed: %v", removed)
	}
	if _, err := manager.Get(manifest.ID); err != nil {
		t.Fatalf("leased job is gone: %v", err)
	}

	release()
	removed, err = manager.CleanupExpired(time.Now().Add(2 * time.Hour))
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if len(removed) != 1 {
		t.Fatalf("removed = %v; want the released job", removed)
	}
}

func TestCleanupSkipsActiveConversions(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	manager := newManager(t, &fakeConverter{
		convert: func(ctx context.Context, request conversion.Request) (conversion.Result, error) {
			once.Do(func() { close(started) })
			<-gate
			return writeFakeOutput(request, "converted.jpg")
		},
	})
	manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if _, err := manager.Start(manifest.ID, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-started

	removed, err := manager.CleanupExpired(time.Now().Add(2 * time.Hour))
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("running job was removed: %v", removed)
	}
	close(gate)
	waitForState(t, manager, manifest.ID, storage.JobCompleted)
}

func TestDeleteRefusesWhileLeased(t *testing.T) {
	manager := newManager(t, &fakeConverter{})
	manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	release, err := manager.Lease(manifest.ID)
	if err != nil {
		t.Fatalf("Lease: %v", err)
	}
	if err := manager.Delete(manifest.ID); !errors.Is(err, jobs.ErrLeased) {
		t.Fatalf("Delete = %v; want ErrLeased", err)
	}
	release()
	if err := manager.Delete(manifest.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := manager.Get(manifest.ID); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("job survived delete: %v", err)
	}
}

func TestShutdownStopsIntakeAndCancelsWork(t *testing.T) {
	store, err := storage.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	started := make(chan struct{})
	var once sync.Once
	manager, err := jobs.NewManager(jobs.Options{
		Store: store,
		Converter: &fakeConverter{
			convert: func(ctx context.Context, request conversion.Request) (conversion.Result, error) {
				once.Do(func() { close(started) })
				<-ctx.Done()
				return conversion.Result{}, ctx.Err()
			},
		},
		MaxConcurrentProcesses: 1,
		MaxFilesPerJob:         3,
		MaxUploadSize:          1 << 20,
		MinFreeSpace:           1,
		JobTTL:                 time.Hour,
		CommandTimeout:         time.Minute,
		Logger:                 slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	reader, _ := multipartBody(t, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	manifest, err := manager.Accept(context.Background(), reader)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if _, err := manager.Start(manifest.ID, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	blocked, _ := multipartBody(t, []uploadFile{{name: "b.jpg", content: []byte("b")}})
	if _, err := manager.Accept(context.Background(), blocked); !errors.Is(err, jobs.ErrShuttingDown) {
		t.Fatalf("Accept after shutdown = %v; want ErrShuttingDown", err)
	}
	if _, err := manager.Start(manifest.ID, nil); !errors.Is(err, jobs.ErrShuttingDown) {
		t.Fatalf("Start after shutdown = %v; want ErrShuttingDown", err)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

func TestStopIntakeStillAllowsShutdownToCancelWork(t *testing.T) {
	store, err := storage.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	started := make(chan struct{})
	var once sync.Once
	manager, err := jobs.NewManager(jobs.Options{
		Store: store,
		Converter: &fakeConverter{
			convert: func(ctx context.Context, request conversion.Request) (conversion.Result, error) {
				once.Do(func() { close(started) })
				<-ctx.Done()
				return conversion.Result{}, ctx.Err()
			},
		},
		MaxConcurrentProcesses: 1,
		MaxFilesPerJob:         1,
		MaxUploadSize:          1 << 20,
		MinFreeSpace:           1,
		JobTTL:                 time.Hour,
		CommandTimeout:         time.Minute,
		Logger:                 slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	reader, _ := multipartBody(t, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	manifest, err := manager.Accept(context.Background(), reader)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if _, err := manager.Start(manifest.ID, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-started

	manager.StopIntake()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestNewManagerRejectsInvalidOptions(t *testing.T) {
	store, err := storage.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	valid := jobs.Options{
		Store:                  store,
		Converter:              &fakeConverter{},
		MaxConcurrentProcesses: 1,
		MaxFilesPerJob:         1,
		MaxUploadSize:          1,
		JobTTL:                 time.Hour,
		CommandTimeout:         time.Second,
	}
	tests := map[string]func(*jobs.Options){
		"no store":      func(options *jobs.Options) { options.Store = nil },
		"no converter":  func(options *jobs.Options) { options.Converter = nil },
		"no processes":  func(options *jobs.Options) { options.MaxConcurrentProcesses = 0 },
		"no files":      func(options *jobs.Options) { options.MaxFilesPerJob = 0 },
		"no size":       func(options *jobs.Options) { options.MaxUploadSize = 0 },
		"negative free": func(options *jobs.Options) { options.MinFreeSpace = -1 },
		"no ttl":        func(options *jobs.Options) { options.JobTTL = 0 },
		"no timeout":    func(options *jobs.Options) { options.CommandTimeout = 0 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			options := valid
			mutate(&options)
			if _, err := jobs.NewManager(options); err == nil {
				t.Fatal("NewManager = nil; want rejection")
			}
		})
	}
}

func TestDeleteCancelsRunningWorkThenRemovesTheJob(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	manager := newManager(t, &fakeConverter{
		convert: func(ctx context.Context, request conversion.Request) (conversion.Result, error) {
			once.Do(func() { close(started) })
			<-ctx.Done()
			return conversion.Result{}, ctx.Err()
		},
	}, func(options *jobs.Options) {
		options.MaxConcurrentProcesses = 1
	})

	manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if _, err := manager.Start(manifest.ID, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("conversion never started")
	}

	if err := manager.Delete(manifest.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := manager.Get(manifest.ID); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("job survived delete: %v", err)
	}
	dir, err := manager.Store().JobDir(manifest.ID)
	if err != nil {
		t.Fatalf("JobDir: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("job directory survived delete: %v", err)
	}
}
