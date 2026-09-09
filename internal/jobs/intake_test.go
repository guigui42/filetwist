package jobs_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/jobs"
	"github.com/guigui42/filetwist/internal/jobs/storage"
)

// endlessFields streams non-file multipart fields forever. It stands in for a
// client that keeps an intake slot occupied without ever uploading a file.
type endlessFields struct {
	boundary string
	pending  string
	read     int64
}

func (reader *endlessFields) Read(buffer []byte) (int, error) {
	if reader.pending == "" {
		reader.pending = fmt.Sprintf(
			"--%s\r\nContent-Disposition: form-data; name=%q\r\n\r\n%s\r\n",
			reader.boundary,
			"noise",
			strings.Repeat("x", 256),
		)
	}
	count := copy(buffer, reader.pending)
	reader.pending = reader.pending[count:]
	reader.read += int64(count)
	return count, nil
}

func TestAcceptRejectsAnEndlessStreamOfFormFields(t *testing.T) {
	manager := newManager(t, &fakeConverter{})
	source := &endlessFields{boundary: "filetwistboundary"}
	reader := multipart.NewReader(source, source.boundary)

	done := make(chan error, 1)
	go func() {
		_, err := manager.Accept(context.Background(), reader)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, jobs.ErrInvalidUpload) {
			t.Fatalf("err = %v; want ErrInvalidUpload", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Accept never returned; intake read an unbounded number of form fields")
	}
	if source.read > 1<<20 {
		t.Errorf("intake read %d bytes of form fields; want a bounded prefix", source.read)
	}
}

func TestAcceptRejectsAnOversizedFormFieldWithoutDrainingIt(t *testing.T) {
	manager := newManager(t, &fakeConverter{})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	field, err := writer.CreateFormField("noise")
	if err != nil {
		t.Fatalf("CreateFormField: %v", err)
	}
	// multipart.Part.Close reads the remainder of a part, so an oversized field
	// must abort intake rather than be discarded byte by byte.
	if _, err := io.WriteString(field, strings.Repeat("y", 4<<20)); err != nil {
		t.Fatalf("write field: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	source := &countingReader{reader: bytes.NewReader(body.Bytes())}
	reader := multipart.NewReader(source, writer.Boundary())
	if _, err := manager.Accept(context.Background(), reader); !errors.Is(err, jobs.ErrInvalidUpload) {
		t.Fatalf("err = %v; want ErrInvalidUpload", err)
	}
	if source.read > 1<<20 {
		t.Errorf("intake read %d bytes of a rejected field; want a bounded prefix", source.read)
	}
}

// countingReader reports how many bytes intake pulled from a request body.
type countingReader struct {
	reader io.Reader
	read   int64
}

func (reader *countingReader) Read(buffer []byte) (int, error) {
	count, err := reader.reader.Read(buffer)
	reader.read += int64(count)
	return count, err
}

func TestAcceptFinishesProbingAfterTheClientDisconnects(t *testing.T) {
	probing := make(chan struct{})
	disconnected := make(chan struct{})
	manager := newManager(t, &fakeConverter{
		inspect: func(ctx context.Context, path string) (conversion.Inspection, error) {
			select {
			case <-probing:
			default:
				close(probing)
			}
			<-disconnected
			// A canceled request context must not reach the probe, otherwise a
			// client that navigates away leaves a job whose files can never be
			// converted.
			if ctx.Err() != nil {
				return conversion.Inspection{}, ctx.Err()
			}
			return conversion.Inspection{
				Media:       conversion.DetectedMedia{Kind: corpus.MediaImage, Format: "jpeg"},
				Recommended: corpus.OperationCompatiblePhoto,
				Compatible:  compatibleImageOperations(),
			}, nil
		},
	})

	requestCtx, cancelRequest := context.WithCancel(context.Background())
	reader, _ := multipartBody(t, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	accepted := make(chan storage.Manifest, 1)
	failed := make(chan error, 1)
	go func() {
		manifest, err := manager.Accept(requestCtx, reader)
		if err != nil {
			failed <- err
			return
		}
		accepted <- manifest
	}()

	select {
	case <-probing:
	case err := <-failed:
		t.Fatalf("Accept: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("probing never started")
	}
	cancelRequest()
	close(disconnected)

	select {
	case manifest := <-accepted:
		if len(manifest.Files) != 1 {
			t.Fatalf("files = %d; want 1", len(manifest.Files))
		}
		if manifest.Files[0].State != storage.FileInspected {
			t.Fatalf("state = %q; want %q", manifest.Files[0].State, storage.FileInspected)
		}
		if _, err := manager.Start(manifest.ID, nil); err != nil {
			t.Fatalf("Start after client disconnect: %v", err)
		}
	case err := <-failed:
		t.Fatalf("Accept: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("Accept never returned")
	}
}

func TestAcceptBoundsTheWaitForAProcessSlot(t *testing.T) {
	blocking := make(chan struct{})
	released := make(chan struct{})
	defer close(released)
	manager := newManager(t, &fakeConverter{
		inspect: func(ctx context.Context, path string) (conversion.Inspection, error) {
			select {
			case <-blocking:
			default:
				close(blocking)
			}
			<-released
			return conversion.Inspection{
				Media:       conversion.DetectedMedia{Kind: corpus.MediaImage, Format: "jpeg"},
				Recommended: corpus.OperationCompatiblePhoto,
				Compatible:  compatibleImageOperations(),
			}, nil
		},
	}, func(options *jobs.Options) {
		options.MaxConcurrentProcesses = 1
		// The slot wait is bounded by the smaller of the intake probe budget
		// and the command timeout.
		options.CommandTimeout = 300 * time.Millisecond
	})

	occupied := make(chan struct{})
	go func() {
		defer close(occupied)
		reader, _ := multipartBody(t, []uploadFile{{name: "held.jpg", content: []byte("held")}})
		_, _ = manager.Accept(context.Background(), reader)
	}()
	select {
	case <-blocking:
	case <-time.After(10 * time.Second):
		t.Fatal("the first upload never occupied the process slot")
	}

	done := make(chan storage.Manifest, 1)
	go func() {
		reader, _ := multipartBody(t, []uploadFile{{name: "waiting.jpg", content: []byte("waiting")}})
		manifest, err := manager.Accept(context.Background(), reader)
		if err != nil {
			t.Errorf("Accept: %v", err)
		}
		done <- manifest
	}()

	select {
	case manifest := <-done:
		if len(manifest.Files) != 1 {
			t.Fatalf("files = %d; want 1", len(manifest.Files))
		}
		file := manifest.Files[0]
		if file.State != storage.FileFailed {
			t.Fatalf("state = %q; want %q", file.State, storage.FileFailed)
		}
		if file.Error == nil {
			t.Fatal("slot timeout did not persist a failure")
		}
		if file.Error.Kind != string(conversion.FailureProbe) {
			t.Fatalf("failure kind = %q; want %q", file.Error.Kind, conversion.FailureProbe)
		}
		if file.Error.Code != "probe_backpressure" {
			t.Fatalf("failure code = %q; want probe_backpressure", file.Error.Code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("intake waited for a process slot without a bound")
	}
}

// timeoutAfter delivers a valid multipart prefix and then reports a transport
// deadline, the way a stalled connection does once its read deadline fires.
type timeoutAfter struct {
	payload []byte
	offset  int
	limit   int
}

func (reader *timeoutAfter) Read(buffer []byte) (int, error) {
	if reader.offset >= reader.limit {
		return 0, os.ErrDeadlineExceeded
	}
	end := reader.limit
	if end > len(reader.payload) {
		end = len(reader.payload)
	}
	count := copy(buffer, reader.payload[reader.offset:end])
	reader.offset += count
	return count, nil
}

// TestAcceptClassifiesATransportTimeout keeps a stalled body distinguishable
// from a malformed one, so the handler can answer 408 rather than 400.
func TestAcceptClassifiesATransportTimeout(t *testing.T) {
	manager := newManager(t, &fakeConverter{})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "stalled.jpg")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(bytes.Repeat([]byte("z"), 8192)); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	// The cut lands inside the file part, past the headers multipart parses
	// through a buffered reader.
	source := &timeoutAfter{payload: body.Bytes(), limit: 4096}
	reader := multipart.NewReader(source, writer.Boundary())

	_, err = manager.Accept(context.Background(), reader)
	if !errors.Is(err, jobs.ErrUploadStalled) {
		t.Fatalf("err = %v; want ErrUploadStalled", err)
	}
	manifests, err := manager.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(manifests) != 0 {
		t.Errorf("stalled upload left %d job(s) behind", len(manifests))
	}
}

func TestAcceptRejectsAdmissionLimitsWithoutDraining(t *testing.T) {
	writeFirstFile := func(t *testing.T, writer *multipart.Writer) {
		t.Helper()
		part, err := writer.CreateFormFile("files", "first.jpg")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(part, "first"); err != nil {
			t.Fatal(err)
		}
	}
	writeOversizedFile := func(t *testing.T, writer *multipart.Writer) {
		t.Helper()
		part, err := writer.CreateFormFile("files", "oversized.jpg")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(bytes.Repeat([]byte("x"), 4<<20)); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name      string
		configure func(*jobs.Options)
		buildBody func(*testing.T, *multipart.Writer)
		want      error
	}{
		{
			name: "configured size limit",
			configure: func(options *jobs.Options) {
				options.MaxUploadSize = 8
			},
			buildBody: writeOversizedFile,
			want:      jobs.ErrUploadTooLarge,
		},
		{
			name: "disk headroom limit",
			configure: func(options *jobs.Options) {
				options.MaxUploadSize = 64
				options.MinFreeSpace = 10
				options.FreeSpace = func(string) (int64, error) {
					return 18, nil
				}
			},
			buildBody: writeOversizedFile,
			want:      jobs.ErrInsufficientSpace,
		},
		{
			name: "file count limit",
			configure: func(options *jobs.Options) {
				options.MaxFilesPerJob = 1
				options.MaxUploadSize = 8
			},
			buildBody: func(t *testing.T, writer *multipart.Writer) {
				t.Helper()
				writeFirstFile(t, writer)
				writeOversizedFile(t, writer)
			},
			want: jobs.ErrTooManyFiles,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := newManager(t, &fakeConverter{}, test.configure)
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			test.buildBody(t, writer)
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}

			source := &countingReader{reader: bytes.NewReader(body.Bytes())}
			_, err := manager.Accept(context.Background(), multipart.NewReader(source, writer.Boundary()))
			if !errors.Is(err, test.want) {
				t.Fatalf("Accept = %v; want %v", err, test.want)
			}
			if source.read > 64<<10 {
				t.Fatalf("read %d rejected bytes; want a bounded prefix", source.read)
			}

			manifests, err := manager.List()
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(manifests) != 0 {
				t.Fatalf("rejected upload left %d job(s) behind", len(manifests))
			}
		})
	}
}

func TestAcceptProtectsProbingFromCleanupAndPreservesCancel(t *testing.T) {
	for _, action := range []string{"cleanup", "cancel"} {
		t.Run(action, func(t *testing.T) {
			started := make(chan struct{})
			resume := make(chan struct{})
			manager := newManager(t, &fakeConverter{
				inspect: func(ctx context.Context, path string) (conversion.Inspection, error) {
					close(started)
					<-resume
					return (&fakeConverter{}).Inspect(ctx, path)
				},
			})
			reader, _ := multipartBody(t, []uploadFile{{name: "a.jpg", content: []byte("a")}})
			done := make(chan error, 1)
			go func() {
				_, err := manager.Accept(context.Background(), reader)
				done <- err
			}()
			<-started
			manifests, err := manager.List()
			if err != nil || len(manifests) != 1 {
				close(resume)
				t.Fatalf("List = %v, %v", manifests, err)
			}
			id := manifests[0].ID
			if action == "cleanup" {
				removed, err := manager.CleanupExpired(time.Now().Add(2 * time.Hour))
				if err != nil || len(removed) != 0 {
					t.Errorf("cleanup removed an uploading job: %v, %v", removed, err)
				}
			} else if _, err := manager.Cancel(id); err != nil {
				t.Errorf("Cancel: %v", err)
			}
			close(resume)
			if err := <-done; err != nil {
				t.Fatalf("Accept: %v", err)
			}
			manifest, err := manager.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			if action == "cancel" && (manifest.State != storage.JobCanceled ||
				manifest.Files[0].State != storage.FileCanceled) {
				t.Fatalf("upload overwrote cancellation: %+v", manifest)
			}
			if manager.Leased(id) {
				t.Fatal("finished upload retained its lease")
			}
		})
	}
}

func TestAcceptSupportsMaximumInt64Budget(t *testing.T) {
	manager := newManager(t, &fakeConverter{}, func(options *jobs.Options) {
		options.MaxUploadSize = math.MaxInt64
		options.MinFreeSpace = 0
		options.FreeSpace = func(string) (int64, error) { return math.MaxInt64, nil }
	})
	manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.TotalBytes != 1 {
		t.Fatalf("stored %d bytes; want 1", manifest.TotalBytes)
	}
}

func TestAcceptTimestampsProbeFailures(t *testing.T) {
	created := time.Now().UTC()
	calls := 0
	manager := newManager(t, &fakeConverter{
		inspect: func(ctx context.Context, path string) (conversion.Inspection, error) {
			return conversion.Inspection{}, errors.New("probe failure")
		},
	}, func(options *jobs.Options) {
		options.Now = func() time.Time {
			calls++
			return created.Add(time.Duration(calls) * time.Second)
		}
	})
	manifest, err := upload(t, manager, []uploadFile{{name: "a.jpg", content: []byte("a")}})
	if err != nil {
		t.Fatal(err)
	}
	file := manifest.Files[0]
	if file.State != storage.FileFailed || file.FinishedAt == nil {
		t.Fatalf("probe failure has no terminal timestamp: %+v", file)
	}
	if !manifest.UpdatedAt.After(manifest.CreatedAt) || manifest.UpdatedAt.Before(*file.FinishedAt) {
		t.Fatalf("upload timestamps are not ordered: %+v", manifest)
	}
}
