package jobs_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/jobs"
	"github.com/guigui42/filetwist/internal/jobs/storage"
)

func TestRemoveFilePreservesRemainingInputsAndMetadata(t *testing.T) {
	for index, name := range []string{"first", "middle", "last"} {
		t.Run(name, func(t *testing.T) {
			now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
			manager := newManager(t, &fakeConverter{}, func(options *jobs.Options) {
				options.Now = func() time.Time { return now }
			})
			files := []uploadFile{
				{name: "photo.jpg", content: []byte("first")},
				{name: "photo.jpg", content: []byte("second photo")},
				{name: "photo.jpg", content: []byte("third photograph")},
			}
			manifest, err := upload(t, manager, files)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err = manager.Store().Update(manifest.ID, now, func(current *storage.Manifest) error {
				for index := range current.Files {
					current.Files[index].Selected = corpus.OperationCompatiblePhoto
					current.Files[index].OperationSource = "requested"
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			inputDir, err := manager.Store().InputDir(manifest.ID)
			if err != nil {
				t.Fatal(err)
			}
			unrelatedPath := filepath.Join(inputDir, "unrelated.txt")
			if err := os.WriteFile(unrelatedPath, []byte("unrelated"), 0o640); err != nil {
				t.Fatal(err)
			}
			removed := manifest.Files[index]
			now = now.Add(time.Minute)
			updated, err := manager.RemoveFile(manifest.ID, removed.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := manifest
			want.Files = append([]storage.File{}, manifest.Files[:index]...)
			want.Files = append(want.Files, manifest.Files[index+1:]...)
			want.TotalBytes -= removed.Size
			want.UpdatedAt = now
			assertRemovalManifest(t, manager, want, updated)
			if _, err := os.Stat(filepath.Join(inputDir, removed.Name)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("removed input still exists: %v", err)
			}
			for remaining, file := range manifest.Files {
				if remaining != index {
					assertInputContent(t, filepath.Join(inputDir, file.Name), files[remaining].content)
				}
			}
			assertInputContent(t, unrelatedPath, []byte("unrelated"))
			outputDir, err := manager.Store().OutputDir(manifest.ID)
			if err != nil {
				t.Fatal(err)
			}
			outputs, err := os.ReadDir(outputDir)
			if err != nil || len(outputs) != 0 {
				t.Fatalf("pending outputs = %v, %v; want empty directory", outputs, err)
			}
			if _, err := manager.RemoveFile(manifest.ID, removed.ID); !errors.Is(err, jobs.ErrNotFound) {
				t.Fatalf("repeated removal = %v; want ErrNotFound", err)
			}
			assertRemovalManifest(t, manager, want, updated)
		})
	}
}

func TestRemoveFileDeletesJobAfterFinalInput(t *testing.T) {
	manager := newManager(t, &fakeConverter{})
	manifest, err := upload(t, manager, []uploadFile{
		{name: "a.jpg", content: []byte("a")},
		{name: "b.jpg", content: []byte("bb")},
		{name: "c.jpg", content: []byte("ccc")},
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("other job")}})
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{1, 0, 2} {
		updated, err := manager.RemoveFile(manifest.ID, manifest.Files[index].ID)
		if err != nil {
			t.Fatal(err)
		}
		if index == 2 && !reflect.DeepEqual(updated, storage.Manifest{}) {
			t.Fatalf("final removal = %+v; want zero manifest", updated)
		}
	}
	if _, err := manager.Get(manifest.ID); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("Get removed job = %v; want ErrNotFound", err)
	}
	jobDir, err := manager.Store().JobDir(manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jobDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed job directory still exists: %v", err)
	}
	current, err := manager.Get(other.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertRemovalManifest(t, manager, other, current)
	inputDir, err := manager.Store().InputDir(other.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertInputContent(t, filepath.Join(inputDir, other.Files[0].Name), []byte("other job"))
}

func TestRemoveFileRejectsInvalidAndUnknownIDs(t *testing.T) {
	manager := newManager(t, &fakeConverter{})
	manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	if err != nil {
		t.Fatal(err)
	}
	unknownID, err := storage.NewID()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		jobID  string
		fileID string
		want   error
	}{
		{name: "empty job", fileID: "f001", want: storage.ErrInvalidID},
		{name: "job traversal", jobID: "../outside", fileID: "f001", want: storage.ErrInvalidID},
		{name: "short job", jobID: "short", fileID: "f001", want: storage.ErrInvalidID},
		{name: "empty file", jobID: manifest.ID, want: storage.ErrInvalidID},
		{name: "file traversal", jobID: manifest.ID, fileID: "../f001", want: storage.ErrInvalidID},
		{name: "file windows path", jobID: manifest.ID, fileID: `..\f001`, want: storage.ErrInvalidID},
		{name: "uppercase file", jobID: manifest.ID, fileID: "F001", want: storage.ErrInvalidID},
		{name: "long file", jobID: manifest.ID, fileID: strings.Repeat("f", 25), want: storage.ErrInvalidID},
		{name: "unknown job", jobID: unknownID, fileID: "f001", want: jobs.ErrNotFound},
		{name: "unknown file", jobID: manifest.ID, fileID: "f999", want: jobs.ErrNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := manager.RemoveFile(test.jobID, test.fileID)
			if !errors.Is(err, test.want) {
				t.Fatalf("RemoveFile = %v; want %v", err, test.want)
			}
			if !reflect.DeepEqual(result, storage.Manifest{}) {
				t.Fatalf("failed removal returned a manifest: %+v", result)
			}
			current, err := manager.Get(manifest.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertRemovalManifest(t, manager, manifest, current)
		})
	}
}

func TestRemoveFileRejectsNonPendingJobs(t *testing.T) {
	for _, state := range []storage.JobState{
		storage.JobQueued, storage.JobRunning, storage.JobCompleted,
		storage.JobFailed, storage.JobCanceled, storage.JobInterrupted,
	} {
		t.Run(string(state), func(t *testing.T) {
			manager := newManager(t, &fakeConverter{})
			manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
			if err != nil {
				t.Fatal(err)
			}
			manifest.State = state
			if state == storage.JobFailed {
				manifest.Files[0].State = storage.FileFailed
			}
			if err := manager.Store().Save(manifest); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.RemoveFile(manifest.ID, manifest.Files[0].ID); !errors.Is(err, jobs.ErrNotStartable) {
				t.Fatalf("RemoveFile = %v; want ErrNotStartable", err)
			}
			current, err := manager.Get(manifest.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertRemovalManifest(t, manager, manifest, current)
			inputDir, err := manager.Store().InputDir(manifest.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertInputContent(t, filepath.Join(inputDir, manifest.Files[0].Name), []byte("a"))
		})
	}
}

func TestRemoveFileRejectsLeasedJobs(t *testing.T) {
	for _, count := range []int{1, 2} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			manager := newManager(t, &fakeConverter{})
			files := []uploadFile{
				{name: "a.jpg", content: []byte("a")},
				{name: "b.jpg", content: []byte("bb")},
			}
			manifest, err := upload(t, manager, files[:count])
			if err != nil {
				t.Fatal(err)
			}
			release, err := manager.Lease(manifest.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if _, err := manager.RemoveFile(manifest.ID, manifest.Files[0].ID); !errors.Is(err, jobs.ErrLeased) {
				t.Fatalf("RemoveFile = %v; want ErrLeased", err)
			}
			current, err := manager.Get(manifest.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertRemovalManifest(t, manager, manifest, current)
			release()
			if _, err := manager.RemoveFile(manifest.ID, manifest.Files[0].ID); err != nil {
				t.Fatalf("RemoveFile after lease release: %v", err)
			}
		})
	}
}

func TestRemoveFileAllowsFailedInputInPendingJob(t *testing.T) {
	manager := newManager(t, &fakeConverter{
		inspect: func(ctx context.Context, path string) (conversion.Inspection, error) {
			if filepath.Base(path) == "bad.jpg" {
				return conversion.Inspection{}, errors.New("invalid image")
			}
			return (&fakeConverter{}).Inspect(ctx, path)
		},
	})
	manifest, err := upload(t, manager, []uploadFile{
		{name: "bad.jpg", content: []byte("bad")},
		{name: "good.jpg", content: []byte("good")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.State != storage.JobPending || manifest.Files[0].State != storage.FileFailed {
		t.Fatalf("expected pending job with failed probe: %+v", manifest)
	}
	updated, err := manager.RemoveFile(manifest.ID, manifest.Files[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != storage.JobPending || len(updated.Files) != 1 ||
		updated.Files[0].ID != manifest.Files[1].ID || updated.TotalBytes != 4 {
		t.Fatalf("removal changed pending job behavior: %+v", updated)
	}
}

func TestRemoveFileRejectsStoredPathTraversal(t *testing.T) {
	for _, name := range []string{"../outside", `..\outside`, "/outside", ""} {
		t.Run(name, func(t *testing.T) {
			manager := newManager(t, &fakeConverter{})
			manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
			if err != nil {
				t.Fatal(err)
			}
			inputDir, err := manager.Store().InputDir(manifest.ID)
			if err != nil {
				t.Fatal(err)
			}
			outsidePath := filepath.Join(filepath.Dir(inputDir), "outside")
			if err := os.WriteFile(outsidePath, []byte("outside"), 0o640); err != nil {
				t.Fatal(err)
			}
			manifest.Files[0].Name = name
			if err := manager.Store().Save(manifest); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.RemoveFile(manifest.ID, manifest.Files[0].ID); err == nil {
				t.Fatal("RemoveFile accepted invalid stored path")
			}
			current, err := manager.Get(manifest.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertRemovalManifest(t, manager, manifest, current)
			assertInputContent(t, filepath.Join(inputDir, "a.jpg"), []byte("a"))
			assertInputContent(t, outsidePath, []byte("outside"))
		})
	}
}

func TestRemoveFileRollsBackOnInputRemovalFailure(t *testing.T) {
	for _, count := range []int{1, 2} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
			manager := newManager(t, &fakeConverter{}, func(options *jobs.Options) {
				options.Now = func() time.Time { return now }
			})
			files := []uploadFile{
				{name: "a.jpg", content: []byte("a")},
				{name: "b.jpg", content: []byte("bb")},
			}
			manifest, err := upload(t, manager, files[:count])
			if err != nil {
				t.Fatal(err)
			}
			inputDir, err := manager.Store().InputDir(manifest.ID)
			if err != nil {
				t.Fatal(err)
			}
			inputPath := filepath.Join(inputDir, manifest.Files[0].Name)
			if err := os.Rename(inputPath, inputPath+".original"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(inputPath, 0o750); err != nil {
				t.Fatal(err)
			}
			blockerPath := filepath.Join(inputPath, "keep")
			if err := os.WriteFile(blockerPath, []byte("keep"), 0o640); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Minute)
			result, err := manager.RemoveFile(manifest.ID, manifest.Files[0].ID)
			if err == nil || !reflect.DeepEqual(result, storage.Manifest{}) {
				t.Fatalf("failed unlink returned success: %+v, %v", result, err)
			}
			var pathErr *os.PathError
			if !errors.As(err, &pathErr) {
				t.Fatalf("RemoveFile lost the filesystem error: %v", err)
			}
			current, err := manager.Get(manifest.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertRemovalManifest(t, manager, manifest, current)
			assertInputContent(t, inputPath+".original", []byte("a"))
			assertInputContent(t, blockerPath, []byte("keep"))
			if count == 2 {
				assertInputContent(t, filepath.Join(inputDir, manifest.Files[1].Name), []byte("bb"))
			}
		})
	}
}

func TestRemoveFileReportsMissingStoredInput(t *testing.T) {
	manager := newManager(t, &fakeConverter{})
	manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	if err != nil {
		t.Fatal(err)
	}
	inputDir, err := manager.Store().InputDir(manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(inputDir, manifest.Files[0].Name)); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RemoveFile(manifest.ID, manifest.Files[0].ID); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("RemoveFile = %v; want ErrNotFound", err)
	}
	current, err := manager.Get(manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertRemovalManifest(t, manager, manifest, current)
}

func TestRemoveFilePreservesInputWhenManifestSaveFails(t *testing.T) {
	now := time.Now()
	manager := newManager(t, &fakeConverter{}, func(options *jobs.Options) {
		options.Now = func() time.Time { return now }
	})
	manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	if err != nil {
		t.Fatal(err)
	}
	now = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := manager.RemoveFile(manifest.ID, manifest.Files[0].ID); err == nil {
		t.Fatal("RemoveFile accepted an unencodable timestamp")
	}
	current, err := manager.Get(manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertRemovalManifest(t, manager, manifest, current)
	inputDir, err := manager.Store().InputDir(manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertInputContent(t, filepath.Join(inputDir, manifest.Files[0].Name), []byte("a"))
}

func TestRemoveFileReportsManifestRollbackFailure(t *testing.T) {
	var disruptSave func()
	manager := newManager(t, &fakeConverter{}, func(options *jobs.Options) {
		options.Now = func() time.Time {
			if disruptSave != nil {
				disruptSave()
			}
			return time.Now()
		}
	})
	manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	if err != nil {
		t.Fatal(err)
	}
	jobDir, err := manager.Store().JobDir(manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	heldDir := jobDir + "-held"
	disruptSave = func() {
		if err := os.Rename(jobDir, heldDir); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Remove(jobDir); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Error(err)
			}
			if err := os.Rename(heldDir, jobDir); err != nil {
				t.Error(err)
			}
		})
		if err := os.WriteFile(jobDir, []byte("blocks manifest writes"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	_, err = manager.RemoveFile(manifest.ID, manifest.Files[0].ID)
	disruptSave = nil
	if !errors.Is(err, syscall.ENOTDIR) || !strings.Contains(err.Error(), "restore manifest") {
		t.Fatalf("RemoveFile did not report both save and rollback failures: %v", err)
	}
	assertInputContent(t, filepath.Join(heldDir, "input", manifest.Files[0].Name), []byte("a"))
}

func TestRemoveFileReportsFinalJobDeletionFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("directory permission failure requires an unprivileged process")
	}
	manager := newManager(t, &fakeConverter{})
	manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	if err != nil {
		t.Fatal(err)
	}
	stagingDir, err := manager.Store().TempDir(manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stagingDir, "blocked"), []byte("staging"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stagingDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(stagingDir, 0o750); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Error(err)
		}
	})
	result, err := manager.RemoveFile(manifest.ID, manifest.Files[0].ID)
	if !errors.Is(err, os.ErrPermission) || !reflect.DeepEqual(result, storage.Manifest{}) {
		t.Fatalf("failed final deletion = %+v, %v; want zero manifest and permission error", result, err)
	}
	inputDir, err := manager.Store().InputDir(manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(inputDir, manifest.Files[0].Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("input was not removed before directory deletion: %v", err)
	}
	current, err := manager.Get(manifest.ID)
	if err != nil {
		t.Fatalf("empty manifest must remain readable after failed deletion: %v", err)
	}
	if len(current.Files) != 0 || current.TotalBytes != 0 || current.ID != manifest.ID {
		t.Fatalf("failed directory deletion lost the empty manifest: %+v", current)
	}
	expired := manifest.ExpiresAt.Add(time.Second)
	removed, err := manager.CleanupExpired(expired)
	if err != nil || len(removed) != 0 {
		t.Fatalf("cleanup with blocked directory = %v, %v", removed, err)
	}
	current, err = manager.Get(manifest.ID)
	if err != nil || len(current.Files) != 0 {
		t.Fatalf("failed cleanup must preserve the empty manifest: %+v, %v", current, err)
	}
	if err := os.Chmod(stagingDir, 0o750); err != nil {
		t.Fatal(err)
	}
	removed, err = manager.CleanupExpired(expired)
	if err != nil || len(removed) != 1 || removed[0] != manifest.ID {
		t.Fatalf("cleanup after restoring access = %v, %v", removed, err)
	}
	if _, err := manager.Get(manifest.ID); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("empty job still exists after successful cleanup: %v", err)
	}
}

func TestRemoveFileSerializesWithStart(t *testing.T) {
	for _, first := range []string{"remove", "start"} {
		t.Run(first, func(t *testing.T) {
			entered := make(chan struct{})
			resume := make(chan struct{})
			var armed bool
			var once sync.Once
			manager := newManager(t, &fakeConverter{
				convert: func(ctx context.Context, request conversion.Request) (conversion.Result, error) {
					<-ctx.Done()
					return conversion.Result{}, ctx.Err()
				},
			}, func(options *jobs.Options) {
				options.Now = func() time.Time {
					if armed {
						once.Do(func() {
							close(entered)
							<-resume
						})
					}
					return time.Now()
				}
			})
			manifest, err := upload(t, manager, []uploadFile{
				{name: "a.jpg", content: []byte("a")},
				{name: "b.jpg", content: []byte("bb")},
			})
			if err != nil {
				t.Fatal(err)
			}
			armed = true
			remove := func() error {
				_, err := manager.RemoveFile(manifest.ID, manifest.Files[0].ID)
				return err
			}
			start := func() error {
				_, err := manager.Start(manifest.ID, nil)
				return err
			}
			firstAction, secondAction := remove, start
			if first == "start" {
				firstAction, secondAction = start, remove
			}
			firstDone := make(chan error, 1)
			secondDone := make(chan error, 1)
			go func() { firstDone <- firstAction() }()
			<-entered
			secondStarted := make(chan struct{})
			go func() {
				close(secondStarted)
				secondDone <- secondAction()
			}()
			<-secondStarted
			close(resume)
			if err := <-firstDone; err != nil {
				t.Fatal(err)
			}
			secondErr := <-secondDone
			wantFiles := 1
			if first == "start" {
				wantFiles = 2
				if !errors.Is(secondErr, jobs.ErrNotStartable) {
					t.Fatalf("RemoveFile after start = %v; want ErrNotStartable", secondErr)
				}
			} else if secondErr != nil {
				t.Fatalf("Start after removal: %v", secondErr)
			}
			if err := manager.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			current, err := manager.Get(manifest.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(current.Files) != wantFiles {
				t.Fatalf("files = %d; want %d", len(current.Files), wantFiles)
			}
			if first == "remove" && current.Files[0].ID != manifest.Files[1].ID {
				t.Fatalf("start restored a removed file: %+v", current.Files)
			}
		})
	}
}

func assertRemovalManifest(t *testing.T, manager *jobs.Manager, want, got storage.Manifest) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest = %+v; want %+v", got, want)
	}
	persisted, err := manager.Get(want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persisted, want) {
		t.Fatalf("persisted manifest = %+v; want %+v", persisted, want)
	}
}

func assertInputContent(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("input contents = %q; want %q", got, want)
	}
}
