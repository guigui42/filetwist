package web_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/config"
	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/jobs"
	"github.com/guigui42/filetwist/internal/jobs/storage"
	"github.com/guigui42/filetwist/internal/media"
	"github.com/guigui42/filetwist/internal/probe"
	"github.com/guigui42/filetwist/internal/runner"
	"github.com/guigui42/filetwist/internal/web"
)

// newLocalServer wires the real subprocess-backed conversion service into the
// web application so the whole upload, convert, and download path is
// exercised end to end.
func newLocalServer(t *testing.T) *testServer {
	t.Helper()
	processRunner, err := runner.New(runner.Config{
		Timeout:     3 * time.Minute,
		StdoutLimit: 256 * 1024,
		StderrLimit: 256 * 1024,
	})
	if err != nil {
		t.Fatalf("runner.New: %v", err)
	}
	prober, err := probe.New(processRunner.Run)
	if err != nil {
		t.Fatalf("probe.New: %v", err)
	}
	service, err := conversion.NewLocal(conversion.LocalConfig{
		Run:            processRunner.Run,
		Prober:         prober,
		Acceleration:   media.AccelerationConfig{Mode: media.AccelerationCPU},
		CommandTimeout: 3 * time.Minute,
		ProbeTimeout:   30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}

	settings, err := config.Load(func(name string) (string, bool) {
		if name == config.DataDirEnv {
			return t.TempDir(), true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	settings.MaxFilesPerJob = 4
	settings.MinFreeSpace = 1
	settings.MaxConcurrentProcesses = 2
	settings.CommandTimeout = 3 * time.Minute

	store, err := storage.NewStore(settings.DataDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager, err := jobs.NewManager(jobs.Options{
		Store:                  store,
		Converter:              service,
		MaxConcurrentProcesses: settings.MaxConcurrentProcesses,
		MaxFilesPerJob:         settings.MaxFilesPerJob,
		MaxUploadSize:          settings.MaxUploadSize,
		MinFreeSpace:           settings.MinFreeSpace,
		JobTTL:                 settings.JobTTL,
		CommandTimeout:         settings.CommandTimeout,
		Logger:                 discard,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := manager.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})

	app, err := web.NewApp(web.Options{
		Manager: manager,
		Config:  settings,
		Logger:  discard,
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	return &testServer{handler: app.Handler(), manager: manager}
}

func uploadFixture(t *testing.T, target, name string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", name)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write fixture part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body.Bytes()))
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("HX-Request", "true")
	return request
}

func TestSilentVideoFixtureOffersOnlyVideoOperations(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is unavailable on this host")
	}
	fixture := filepath.Join("..", "..", "fixtures", "generated", "h264-silent-32x24.mp4")
	content, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read silent video fixture: %v", err)
	}
	server := newLocalServer(t)
	response := server.do(t, uploadFixture(t, "/jobs", filepath.Base(fixture), content))
	if response.Code != http.StatusOK {
		t.Fatalf("upload status = %d: %s", response.Code, response.Body.String())
	}
	manifests, err := server.manager.List()
	if err != nil || len(manifests) != 1 {
		t.Fatalf("List = %v, %v", manifests, err)
	}
	file := manifests[0].Files[0]
	want := []corpus.Operation{corpus.OperationCompatibleVideo, corpus.OperationSmallerVideo}
	if file.State != storage.FileInspected || !slices.Equal(file.Compatible, want) ||
		file.Recommended != corpus.OperationCompatibleVideo || file.Selected != corpus.OperationCompatibleVideo {
		t.Fatalf("silent video persisted unexpected eligibility: %+v", file)
	}
	for _, operation := range conversion.AllOperations() {
		rendered := strings.Contains(response.Body.String(), `<option value="`+string(operation)+`"`)
		if rendered != slices.Contains(want, operation) {
			t.Errorf("rendered %s = %t; want only %v", operation, rendered, want)
		}
	}
}

func TestGeneratedFixturesConvertEndToEnd(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	tests := []struct {
		name       string
		required   []string
		fixture    string
		operation  string
		wantSuffix string
	}{
		{
			name:       "image",
			required:   []string{"vips", "vipsheader"},
			fixture:    filepath.Join(root, "fixtures", "generated", "rgba-2x2.png"),
			operation:  "lossless_image",
			wantSuffix: "-lossless.png",
		},
		{
			name:       "audio",
			required:   []string{"ffmpeg", "ffprobe"},
			fixture:    filepath.Join(root, "fixtures", "generated", "tone-8khz-mono.wav"),
			operation:  "compatible_audio",
			wantSuffix: "-compatible.mp3",
		},
		{
			name:       "video",
			required:   []string{"ffmpeg", "ffprobe"},
			fixture:    filepath.Join(root, "fixtures", "generated", "h264-aac-32x24.mp4"),
			operation:  "compatible_video",
			wantSuffix: "-compatible.mp4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, executable := range tt.required {
				if _, err := exec.LookPath(executable); err != nil {
					t.Skipf("%s is unavailable on this host", executable)
				}
			}
			content, err := os.ReadFile(tt.fixture)
			if err != nil {
				t.Skipf("fixture %s is unavailable: %v", tt.fixture, err)
			}

			server := newLocalServer(t)
			response := server.do(t, uploadFixture(t, "/jobs", filepath.Base(tt.fixture), content))
			if response.Code != http.StatusOK {
				t.Fatalf("upload status = %d; body = %s", response.Code, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), tt.operation) {
				t.Fatalf("fragment did not offer %s\n%s", tt.operation, response.Body.String())
			}

			manifests, err := server.manager.List()
			if err != nil || len(manifests) != 1 {
				t.Fatalf("List: %v (%d jobs)", err, len(manifests))
			}
			manifest := manifests[0]
			if manifest.Files[0].State != storage.FileInspected {
				t.Fatalf("file state = %q; want inspected: %+v",
					manifest.Files[0].State, manifest.Files[0].Error)
			}

			form := strings.NewReader("operation." + manifest.Files[0].ID + "=" + tt.operation)
			start := httptest.NewRequest(http.MethodPost, "/jobs/"+manifest.ID+"/start", form)
			start.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if code := server.do(t, start).Code; code != http.StatusOK {
				t.Fatalf("start status = %d", code)
			}

			final := server.waitForState(t, manifest.ID, storage.JobCompleted, storage.JobFailed)
			if final.State != storage.JobCompleted {
				t.Fatalf("state = %q; file error = %+v", final.State, final.Files[0].Error)
			}
			file := final.Files[0]
			if file.Output == nil || !strings.HasSuffix(file.Output.Name, tt.wantSuffix) {
				t.Fatalf("output = %+v; want suffix %q", file.Output, tt.wantSuffix)
			}
			if file.Validation.Status != "passed" {
				t.Fatalf("validation = %+v", file.Validation)
			}

			download := server.do(t, httptest.NewRequest(
				http.MethodGet,
				"/jobs/"+manifest.ID+"/files/"+file.ID,
				nil,
			))
			if download.Code != http.StatusOK {
				t.Fatalf("download status = %d", download.Code)
			}
			if int64(download.Body.Len()) != file.Output.Size {
				t.Fatalf("downloaded %d bytes; want %d", download.Body.Len(), file.Output.Size)
			}
			if !strings.HasPrefix(download.Header().Get("Content-Disposition"), "attachment;") {
				t.Errorf("Content-Disposition = %q", download.Header().Get("Content-Disposition"))
			}

			archive := server.do(t, httptest.NewRequest(
				http.MethodGet,
				"/jobs/"+manifest.ID+"/download",
				nil,
			))
			if archive.Code != http.StatusOK {
				t.Fatalf("archive status = %d", archive.Code)
			}
			payload := archive.Body.Bytes()
			reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
			if err != nil {
				t.Fatalf("zip.NewReader: %v", err)
			}
			if len(reader.File) != 1 {
				t.Fatalf("archive entries = %d; want 1", len(reader.File))
			}
			if reader.File[0].Method != zip.Store {
				t.Errorf("archive entry method = %d; want Store", reader.File[0].Method)
			}
		})
	}
}
