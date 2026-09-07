package jobs_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/jobs"
	"github.com/guigui42/filetwist/internal/jobs/storage"
)

func TestCancelPreservesTerminalFilesAfterConverterReturns(t *testing.T) {
	for _, outcome := range []string{"success", "failure"} {
		t.Run(outcome, func(t *testing.T) {
			started := make(chan struct{})
			resume := make(chan struct{})
			manager := newManager(t, &fakeConverter{
				convert: func(ctx context.Context, request conversion.Request) (conversion.Result, error) {
					close(started)
					<-resume
					if outcome == "failure" {
						return conversion.Result{}, errors.New("late conversion error")
					}
					return writeFakeOutput(request, "converted.jpg")
				},
			})
			manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manager.Start(manifest.ID, nil); err != nil {
				t.Fatal(err)
			}
			<-started
			canceled, err := manager.Cancel(manifest.ID)
			close(resume)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := manager.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			final, err := manager.Get(manifest.ID)
			if err != nil {
				t.Fatal(err)
			}
			file := final.Files[0]
			if final.State != storage.JobCanceled || file.State != storage.FileCanceled ||
				file.Output != nil || file.Error == nil || file.Error.Code != "canceled" ||
				!file.FinishedAt.Equal(*canceled.Files[0].FinishedAt) {
				t.Fatalf("late converter result changed canceled file: %+v", file)
			}
		})
	}
}

func TestShutdownDrainsQueuedJobs(t *testing.T) {
	started := make(chan struct{})
	manager := newManager(t, &fakeConverter{
		convert: func(ctx context.Context, request conversion.Request) (conversion.Result, error) {
			close(started)
			<-ctx.Done()
			return conversion.Result{}, ctx.Err()
		},
	}, func(options *jobs.Options) {
		options.MaxConcurrentProcesses = 1
		options.QueueDepth = 8
	})
	manifests := make([]storage.Manifest, 8)
	for index := range manifests {
		manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
		if err != nil {
			t.Fatal(err)
		}
		manifests[index] = manifest
	}
	for index, manifest := range manifests {
		if _, err := manager.Start(manifest.ID, nil); err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			<-started
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	for _, manifest := range manifests {
		final, err := manager.Get(manifest.ID)
		if err != nil {
			t.Fatal(err)
		}
		if final.State != storage.JobCanceled || final.Files[0].State != storage.FileCanceled {
			t.Errorf("shutdown left job %s in %s/%s", final.ID, final.State, final.Files[0].State)
		}
	}
	if stats := manager.Stats(); stats.Queued != 0 || stats.Running != 0 || stats.Leases != 0 {
		t.Fatalf("shutdown retained work: %+v", stats)
	}
}

func TestLeaseSerializesWithCleanup(t *testing.T) {
	manager := newManager(t, &fakeConverter{})
	for attempt := 0; attempt < 50; attempt++ {
		manifest, err := manager.Store().Create(time.Now().Add(-2*time.Hour), time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		begin := make(chan struct{})
		cleaned := make(chan error, 1)
		go func() {
			<-begin
			_, err := manager.CleanupExpired(time.Now())
			cleaned <- err
		}()
		close(begin)
		release, leaseErr := manager.Lease(manifest.ID)
		if err := <-cleaned; err != nil {
			t.Fatal(err)
		}
		if leaseErr != nil {
			if !errors.Is(leaseErr, jobs.ErrNotFound) {
				t.Fatalf("Lease: %v", leaseErr)
			}
			continue
		}
		_, err = manager.Get(manifest.ID)
		release()
		release()
		if err != nil {
			t.Fatalf("cleanup deleted a leased job: %v", err)
		}
		if err := manager.Delete(context.Background(), manifest.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrentStartCancelAndShutdown(t *testing.T) {
	for attempt := 0; attempt < 10; attempt++ {
		manager := newManager(t, &fakeConverter{
			convert: func(ctx context.Context, request conversion.Request) (conversion.Result, error) {
				<-ctx.Done()
				return conversion.Result{}, ctx.Err()
			},
		})
		manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
		if err != nil {
			t.Fatal(err)
		}
		begin := make(chan struct{})
		var finished sync.WaitGroup
		finished.Add(3)
		go func() {
			defer finished.Done()
			<-begin
			_, err := manager.Start(manifest.ID, nil)
			if err != nil && !errors.Is(err, jobs.ErrNotStartable) && !errors.Is(err, jobs.ErrShuttingDown) {
				t.Errorf("Start: %v", err)
			}
		}()
		go func() {
			defer finished.Done()
			<-begin
			if _, err := manager.Cancel(manifest.ID); err != nil {
				t.Errorf("Cancel: %v", err)
			}
		}()
		go func() {
			defer finished.Done()
			<-begin
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := manager.Shutdown(ctx); err != nil {
				t.Errorf("Shutdown: %v", err)
			}
		}()
		close(begin)
		finished.Wait()
		final, err := manager.Get(manifest.ID)
		if err != nil {
			t.Fatal(err)
		}
		if final.State != storage.JobCanceled || final.Files[0].State != storage.FileCanceled {
			t.Fatalf("concurrent lifecycle operations lost cancellation: %+v", final)
		}
		if stats := manager.Stats(); stats.Queued != 0 || stats.Running != 0 || stats.Leases != 0 {
			t.Fatalf("shutdown retained work: %+v", stats)
		}
	}
}

func TestShutdownWaitsForUploadProbes(t *testing.T) {
	started := make(chan struct{})
	finish := make(chan struct{})
	manager := newManager(t, &fakeConverter{
		inspect: func(ctx context.Context, path string) (conversion.Inspection, error) {
			close(started)
			<-ctx.Done()
			<-finish
			return conversion.Inspection{}, ctx.Err()
		},
	})
	reader, _ := multipartBody(t, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	accepted := make(chan error, 1)
	go func() {
		_, err := manager.Accept(context.Background(), reader)
		accepted <- err
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := manager.Shutdown(ctx)
	close(finish)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Shutdown returned before the upload finished: %v", err)
	}
	if err := <-accepted; err != nil {
		t.Fatal(err)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stats := manager.Stats(); stats.Leases != 0 {
		t.Fatalf("shutdown retained upload leases: %+v", stats)
	}
}
