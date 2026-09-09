package web_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/config"
	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/jobs"
	"github.com/guigui42/filetwist/internal/jobs/storage"
	"github.com/guigui42/filetwist/internal/profiles"
	"github.com/guigui42/filetwist/internal/web"
)

// stubConverter probes every file as a still image and writes a small output.
type stubConverter struct {
	convert func(ctx context.Context, request conversion.Request) (conversion.Result, error)
}

func allOperations() []profiles.Operation {
	specs := profiles.All()
	operations := make([]profiles.Operation, 0, len(specs))
	for _, spec := range specs {
		operations = append(operations, spec.Operation)
	}
	return operations
}

func compatibleOperations(kind profiles.MediaKind) []profiles.Operation {
	specs := profiles.Compatible(kind)
	operations := make([]profiles.Operation, 0, len(specs))
	for _, spec := range specs {
		operations = append(operations, spec.Operation)
	}
	return operations
}

func (converter *stubConverter) Inspect(
	ctx context.Context,
	path string,
) (conversion.Inspection, error) {
	return conversion.Inspection{
		Media: conversion.DetectedMedia{
			Kind:   corpus.MediaImage,
			Format: "jpeg",
			Width:  1920,
			Height: 1080,
		},
		Recommended: corpus.OperationCompatiblePhoto,
		Compatible:  compatibleOperations(corpus.MediaImage),
	}, nil
}

func (converter *stubConverter) Convert(
	ctx context.Context,
	request conversion.Request,
) (conversion.Result, error) {
	if converter.convert != nil {
		return converter.convert(ctx, request)
	}
	name, err := profiles.OutputName(request.InputPath, request.Operation)
	if err != nil {
		return conversion.Result{}, err
	}
	outputPath := filepath.Join(request.Output, name)
	if err := os.WriteFile(outputPath, []byte("converted-image-bytes"), 0o640); err != nil {
		return conversion.Result{}, err
	}
	return conversion.Result{
		SchemaVersion:     conversion.SchemaVersion,
		Status:            "success",
		SelectedOperation: request.Operation,
		OutputPath:        outputPath,
		Validation:        conversion.ValidationResult{Status: "passed"},
		Execution: conversion.ExecutionResult{
			Requested: "cpu", Initial: "cpu", Final: "cpu", AttemptCount: 1,
		},
	}, nil
}

type testServer struct {
	handler http.Handler
	manager *jobs.Manager
	base    string
}

func newServer(t *testing.T, mutate func(*config.Config), converter jobs.Converter) *testServer {
	t.Helper()
	return newServerWithLogger(t, mutate, converter, nil)
}

func newServerWithLogger(
	t *testing.T,
	mutate func(*config.Config),
	converter jobs.Converter,
	logger *slog.Logger,
) *testServer {
	t.Helper()
	settings, err := config.Load(func(name string) (string, bool) {
		if name == config.DataDirEnv {
			return t.TempDir(), true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	settings.MaxFilesPerJob = 3
	settings.MaxUploadSize = 1 << 20
	settings.MinFreeSpace = 1
	settings.MaxConcurrentProcesses = 2
	if mutate != nil {
		mutate(&settings)
	}
	store, err := storage.NewStore(settings.DataDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if converter == nil {
		converter = &stubConverter{}
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	if logger == nil {
		logger = discard
	}
	manager, err := jobs.NewManager(jobs.Options{
		Store:                  store,
		Converter:              converter,
		MaxConcurrentProcesses: settings.MaxConcurrentProcesses,
		MaxFilesPerJob:         settings.MaxFilesPerJob,
		MaxUploadSize:          settings.MaxUploadSize,
		MinFreeSpace:           settings.MinFreeSpace,
		JobTTL:                 settings.JobTTL,
		CommandTimeout:         settings.CommandTimeout,
		Logger:                 logger,
	})
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

	app, err := web.NewApp(web.Options{
		Manager:    manager,
		Config:     settings,
		Logger:     logger,
		LookupTool: func(string) bool { return true },
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	base, err := config.NormalizeWebRoot(settings.WebRoot)
	if err != nil {
		t.Fatalf("NormalizeWebRoot: %v", err)
	}
	return &testServer{handler: app.Handler(), manager: manager, base: base}
}

func (server *testServer) do(t *testing.T, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.handler.ServeHTTP(recorder, request)
	return recorder
}

func (server *testServer) path(suffix string) string {
	return server.base + suffix
}

func uploadRequest(t *testing.T, target string, names []string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, name := range names {
		part, err := writer.CreateFormFile("files", name)
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := part.Write([]byte("original-bytes-" + name)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body.Bytes()))
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("HX-Request", "true")
	return request
}

func (server *testServer) upload(t *testing.T, names []string) storage.Manifest {
	t.Helper()
	response := server.do(t, uploadRequest(t, server.path("/jobs"), names))
	if response.Code != http.StatusOK {
		t.Fatalf("upload status = %d; body = %s", response.Code, response.Body.String())
	}
	manifests, err := server.manager.List()
	if err != nil || len(manifests) == 0 {
		t.Fatalf("no job was created: %v", err)
	}
	return manifests[0]
}

func (server *testServer) waitForState(
	t *testing.T,
	id string,
	want ...storage.JobState,
) storage.Manifest {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		manifest, err := server.manager.Get(id)
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
	t.Fatalf("job %s never reached %v", id, want)
	return storage.Manifest{}
}

func TestIndexPageRendersUploadForm(t *testing.T) {
	server := newServer(t, nil, nil)
	response := server.do(t, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	for _, want := range []string{
		`<form id="upload-form"`,
		`enctype="multipart/form-data"`,
		`id="drop-zone"`,
		`id="job-panel"`,
		`id="request-notice"`,
		`/static/htmx.min.js`,
		`/static/app.js`,
		`/static/app.css`,
		`Skip to content`,
		`aria-live="polite"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index page is missing %q", want)
		}
	}
	if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("nosniff = %q", got)
	}
	policy := response.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(policy, want) {
			t.Errorf("CSP %q is missing %q", policy, want)
		}
	}
}

func TestStaticAssetsAreServed(t *testing.T) {
	server := newServer(t, nil, nil)
	for _, name := range []string{"app.css", "app.js", "htmx.min.js"} {
		response := server.do(t, httptest.NewRequest(http.MethodGet, "/static/"+name, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d", name, response.Code)
		}
		if response.Body.Len() == 0 {
			t.Fatalf("%s is empty", name)
		}
		if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s nosniff = %q", name, got)
		}
	}
}

func TestOfficialHTMXIsVendored(t *testing.T) {
	server := newServer(t, nil, nil)
	response := server.do(t, httptest.NewRequest(http.MethodGet, "/static/htmx.min.js", nil))
	script := response.Body.String()
	if !strings.Contains(script, `version:"2.0.4"`) {
		t.Fatalf("vendored script is not htmx 2.0.4")
	}
	if len(script) < 50_000 {
		t.Fatalf("vendored htmx is unexpectedly small: %d bytes", len(script))
	}
}

func TestUploadReturnsJobFragmentWithRecommendation(t *testing.T) {
	server := newServer(t, nil, nil)
	response := server.do(t, uploadRequest(t, "/jobs", []string{"holiday.jpg", "second.jpg"}))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{
		`id="job"`,
		`compatible_photo`,
		`Recommended: Compatible photo`,
		`hx-post=`,
		`/start`,
		`holiday.jpg`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("fragment is missing %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "compatible_video") {
		t.Error("fragment offered an operation incompatible with the detected image")
	}
	if strings.Contains(body, "<html") {
		t.Error("fragment must not be a full page")
	}
}

func TestUploadWithoutJavaScriptRedirectsToJobPage(t *testing.T) {
	server := newServer(t, nil, nil)
	request := uploadRequest(t, "/jobs", []string{"holiday.jpg"})
	request.Header.Del("HX-Request")
	response := server.do(t, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d; want 303", response.Code)
	}
	location := response.Header().Get("Location")
	if !strings.HasPrefix(location, "/jobs/") {
		t.Fatalf("Location = %q; want job page", location)
	}
}

func TestUploadRejectsNonMultipartBodies(t *testing.T) {
	server := newServer(t, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader("files=1"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := server.do(t, request)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d; want 415", response.Code)
	}
}

func TestUploadRejectsTooManyFiles(t *testing.T) {
	server := newServer(t, func(settings *config.Config) {
		settings.MaxFilesPerJob = 2
	}, nil)
	response := server.do(t, uploadRequest(t, "/jobs", []string{"a.jpg", "b.jpg", "c.jpg"}))
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d; want 413", response.Code)
	}
	if !strings.Contains(response.Body.String(), "at most 2 files") {
		t.Errorf("body = %q", response.Body.String())
	}
}

func TestUploadRejectsOversizedBodies(t *testing.T) {
	server := newServer(t, func(settings *config.Config) {
		settings.MaxUploadSize = 4
	}, nil)
	response := server.do(t, uploadRequest(t, "/jobs", []string{"a.jpg"}))
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d; want 413", response.Code)
	}
}

func TestUploadedTraversalNamesStayInsideTheJobDirectory(t *testing.T) {
	server := newServer(t, nil, nil)
	manifest := server.upload(t, []string{"../../../../etc/passwd"})
	inputDir, err := server.manager.Store().InputDir(manifest.ID)
	if err != nil {
		t.Fatalf("InputDir: %v", err)
	}
	entries, err := os.ReadDir(inputDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "passwd" {
		t.Fatalf("entries = %v; want a single contained file", entries)
	}
}

func TestJobStatusFragmentPollsWhileActiveAndStopsWhenTerminal(t *testing.T) {
	gate := make(chan struct{})
	server := newServer(t, nil, &stubConverter{
		convert: func(ctx context.Context, request conversion.Request) (conversion.Result, error) {
			<-gate
			outputPath := filepath.Join(request.Output, "done.jpg")
			if err := os.WriteFile(outputPath, []byte("bytes"), 0o640); err != nil {
				return conversion.Result{}, err
			}
			return conversion.Result{
				Status:     "success",
				OutputPath: outputPath,
				Validation: conversion.ValidationResult{Status: "passed"},
			}, nil
		},
	})
	manifest := server.upload(t, []string{"a.jpg"})

	start := httptest.NewRequest(http.MethodPost, "/jobs/"+manifest.ID+"/start", nil)
	if code := server.do(t, start).Code; code != http.StatusOK {
		t.Fatalf("start status = %d", code)
	}

	status := server.do(t, httptest.NewRequest(http.MethodGet, "/jobs/"+manifest.ID+"/status", nil))
	if status.Code != http.StatusOK {
		t.Fatalf("status fragment = %d", status.Code)
	}
	body := status.Body.String()
	for _, want := range []string{`hx-get=`, `hx-trigger="every 2s"`, `hx-target="this"`, `hx-swap="outerHTML"`} {
		if !strings.Contains(body, want) {
			t.Errorf("active fragment is missing %q\n%s", want, body)
		}
	}

	close(gate)
	server.waitForState(t, manifest.ID, storage.JobCompleted)
	final := server.do(t, httptest.NewRequest(http.MethodGet, "/jobs/"+manifest.ID+"/status", nil))
	finalBody := final.Body.String()
	if strings.Contains(finalBody, `hx-trigger="every 2s"`) {
		t.Error("terminal fragment still polls")
	}
	if !strings.Contains(finalBody, `data-job-state="completed"`) {
		t.Errorf("terminal fragment has no machine-readable completed state\n%s", finalBody)
	}
	if !strings.Contains(finalBody, "Download all as ZIP") {
		t.Errorf("terminal fragment has no archive link\n%s", finalBody)
	}
}

func TestStartUsesTheSelectedCompatibleOperation(t *testing.T) {
	server := newServer(t, nil, nil)
	manifest := server.upload(t, []string{"a.jpg"})
	fileID := manifest.Files[0].ID

	form := strings.NewReader("operation." + fileID + "=smaller_photo")
	request := httptest.NewRequest(http.MethodPost, "/jobs/"+manifest.ID+"/start", form)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if code := server.do(t, request).Code; code != http.StatusOK {
		t.Fatalf("start status = %d", code)
	}
	final := server.waitForState(t, manifest.ID, storage.JobCompleted, storage.JobFailed)
	if final.Files[0].Selected != corpus.OperationSmallerPhoto {
		t.Fatalf("selected = %q", final.Files[0].Selected)
	}
}

func TestStartRejectsIncompatibleOperation(t *testing.T) {
	server := newServer(t, nil, nil)
	manifest := server.upload(t, []string{"a.jpg"})
	form := strings.NewReader("operation." + manifest.Files[0].ID + "=compatible_video")
	request := httptest.NewRequest(http.MethodPost, "/jobs/"+manifest.ID+"/start", form)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := server.do(t, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", response.Code)
	}
}

func TestStartRejectsUnknownOperationName(t *testing.T) {
	server := newServer(t, nil, nil)
	manifest := server.upload(t, []string{"a.jpg"})
	form := strings.NewReader("operation." + manifest.Files[0].ID + "=rm_rf")
	request := httptest.NewRequest(http.MethodPost, "/jobs/"+manifest.ID+"/start", form)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if code := server.do(t, request).Code; code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", code)
	}
}

func TestCancelFragmentReportsCanceledState(t *testing.T) {
	release := make(chan struct{})
	server := newServer(t, nil, &stubConverter{
		convert: func(ctx context.Context, request conversion.Request) (conversion.Result, error) {
			select {
			case <-ctx.Done():
				return conversion.Result{}, ctx.Err()
			case <-release:
				return conversion.Result{}, context.Canceled
			}
		},
	})
	manifest := server.upload(t, []string{"a.jpg"})
	server.do(t, httptest.NewRequest(http.MethodPost, "/jobs/"+manifest.ID+"/start", nil))

	response := server.do(t, httptest.NewRequest(http.MethodPost, "/jobs/"+manifest.ID+"/cancel", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("cancel status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "Canceled") {
		t.Errorf("body = %s", response.Body.String())
	}
	close(release)
	server.waitForState(t, manifest.ID, storage.JobCanceled)
}

func TestDownloadServesAttachmentWithSecurityHeaders(t *testing.T) {
	server := newServer(t, nil, nil)
	manifest := server.upload(t, []string{"holiday.jpg"})
	server.do(t, httptest.NewRequest(http.MethodPost, "/jobs/"+manifest.ID+"/start", nil))
	final := server.waitForState(t, manifest.ID, storage.JobCompleted)

	file := final.Files[0]
	response := server.do(t, httptest.NewRequest(
		http.MethodGet,
		"/jobs/"+manifest.ID+"/files/"+file.ID,
		nil,
	))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	header := response.Header()
	if !strings.HasPrefix(header.Get("Content-Disposition"), "attachment;") {
		t.Errorf("Content-Disposition = %q; want an attachment", header.Get("Content-Disposition"))
	}
	if !strings.Contains(header.Get("Content-Disposition"), "filename*=UTF-8''") {
		t.Errorf("Content-Disposition = %q; want a UTF-8 name", header.Get("Content-Disposition"))
	}
	if header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("nosniff = %q", header.Get("X-Content-Type-Options"))
	}
	if header.Get("Content-Security-Policy") != "default-src 'none'; sandbox" {
		t.Errorf("CSP = %q", header.Get("Content-Security-Policy"))
	}
	if header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("Content-Type = %q", header.Get("Content-Type"))
	}
	if response.Body.String() != "converted-image-bytes" {
		t.Errorf("body = %q", response.Body.String())
	}
}

func TestDownloadRejectsTraversalAndUnknownIdentifiers(t *testing.T) {
	server := newServer(t, nil, nil)
	manifest := server.upload(t, []string{"a.jpg"})
	for _, target := range []string{
		"/jobs/" + manifest.ID + "/files/..%2f..%2fmanifest.json",
		"/jobs/" + manifest.ID + "/files/f999",
		"/jobs/notavalididhere1234/files/f001",
		"/jobs/" + manifest.ID + "/download",
	} {
		response := server.do(t, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code == http.StatusOK {
			t.Errorf("%s returned 200; want a rejection", target)
		}
	}
}

func TestArchiveStreamsStoredEntries(t *testing.T) {
	server := newServer(t, nil, nil)
	manifest := server.upload(t, []string{"a.jpg", "b.jpg"})
	server.do(t, httptest.NewRequest(http.MethodPost, "/jobs/"+manifest.ID+"/start", nil))
	final := server.waitForState(t, manifest.ID, storage.JobCompleted)

	response := server.do(t, httptest.NewRequest(http.MethodGet, "/jobs/"+manifest.ID+"/download", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if response.Header().Get("Content-Type") != "application/zip" {
		t.Errorf("Content-Type = %q", response.Header().Get("Content-Type"))
	}
	if !strings.Contains(response.Header().Get("Content-Disposition"), "attachment;") {
		t.Errorf("Content-Disposition = %q", response.Header().Get("Content-Disposition"))
	}
	if response.Header().Get("Content-Length") != "" {
		t.Error("archive declared a Content-Length, so it was not streamed")
	}

	payload := response.Body.Bytes()
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	if len(archive.File) != len(final.CompletedOutputs()) {
		t.Fatalf("entries = %d; want %d", len(archive.File), len(final.CompletedOutputs()))
	}
	for _, entry := range archive.File {
		if entry.Method != zip.Store {
			t.Errorf("entry %s uses method %d; want Store", entry.Name, entry.Method)
		}
		reader, err := entry.Open()
		if err != nil {
			t.Fatalf("open entry: %v", err)
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read entry: %v", err)
		}
		if err := reader.Close(); err != nil {
			t.Fatalf("close entry: %v", err)
		}
		if string(content) != "converted-image-bytes" {
			t.Errorf("entry %s content = %q", entry.Name, content)
		}
	}

	// The archive is streamed, so nothing is materialized in the job directory.
	jobDir, err := server.manager.Store().JobDir(manifest.ID)
	if err != nil {
		t.Fatalf("JobDir: %v", err)
	}
	if err := filepath.WalkDir(jobDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".zip") {
			t.Errorf("archive was written to disk at %s", path)
		}
		return nil
	}); err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
}

func TestDeleteRemovesEveryStoredFile(t *testing.T) {
	server := newServer(t, nil, nil)
	manifest := server.upload(t, []string{"a.jpg"})
	jobDir, err := server.manager.Store().JobDir(manifest.ID)
	if err != nil {
		t.Fatalf("JobDir: %v", err)
	}

	response := server.do(t, httptest.NewRequest(http.MethodPost, "/jobs/"+manifest.ID+"/delete", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Fatalf("job directory survived deletion: %v", err)
	}
	missing := server.do(t, httptest.NewRequest(http.MethodGet, "/jobs/"+manifest.ID+"/status", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("status after delete = %d; want 404", missing.Code)
	}
}

func TestDeleteContinuesAfterRequestCancellation(t *testing.T) {
	server := newServer(t, nil, nil)
	manifest := server.upload(t, []string{"a.jpg"})
	jobDir, err := server.manager.Store().JobDir(manifest.ID)
	if err != nil {
		t.Fatalf("JobDir: %v", err)
	}
	release, err := server.manager.Lease(manifest.ID)
	if err != nil {
		t.Fatalf("Lease: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/jobs/"+manifest.ID+"/delete", nil)
	ctx, cancel := context.WithCancel(request.Context())
	request = request.WithContext(ctx)
	recorder := httptest.NewRecorder()

	responseDone := make(chan struct{})
	go func() {
		server.handler.ServeHTTP(recorder, request)
		close(responseDone)
	}()

	time.Sleep(30 * time.Millisecond)
	cancel()
	time.Sleep(30 * time.Millisecond)
	release()

	select {
	case <-responseDone:
	case <-time.After(2 * time.Second):
		t.Fatal("delete request did not finish")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", recorder.Code)
	}
	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Fatalf("job directory survived deletion: %v", err)
	}
	if _, err := server.manager.Get(manifest.ID); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("job survived delete after request cancellation: %v", err)
	}
}

func TestRemoveFileLogsStableFieldsWithoutPaths(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	server := newServerWithLogger(t, nil, nil, logger)
	manifest := server.upload(t, []string{"private-upload-name.jpg"})

	inputDir, err := server.manager.Store().InputDir(manifest.ID)
	if err != nil {
		t.Fatalf("InputDir: %v", err)
	}
	inputPath, err := storage.Resolve(inputDir, manifest.Files[0].Name)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := os.Remove(inputPath); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := os.Mkdir(inputPath, 0o750); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inputPath, "child"), []byte("x"), 0o640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	target := server.path("/jobs/" + manifest.ID + "/files/" + manifest.Files[0].ID + "/remove")
	response := server.do(t, httptest.NewRequest(http.MethodPost, target, nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", response.Code)
	}

	output := logs.String()
	if !strings.Contains(output, "file removal failed") {
		t.Fatalf("log = %q; want file removal failure", output)
	}
	if !strings.Contains(output, manifest.ID) {
		t.Fatalf("log = %q; want job id", output)
	}
	if !strings.Contains(output, "category=filesystem") {
		t.Fatalf("log = %q; want filesystem category", output)
	}
	if !strings.Contains(output, "fs_op=remove") {
		t.Fatalf("log = %q; want remove operation", output)
	}
	if strings.Contains(output, inputPath) || strings.Contains(output, manifest.Files[0].Name) {
		t.Fatalf("log leaked file path details: %q", output)
	}
}

func TestJobPageRendersFullDocument(t *testing.T) {
	server := newServer(t, nil, nil)
	manifest := server.upload(t, []string{"a.jpg"})
	response := server.do(t, httptest.NewRequest(http.MethodGet, "/jobs/"+manifest.ID, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") || !strings.Contains(body, `id="job"`) {
		t.Errorf("job page body = %s", body)
	}
}

func TestJobPageReportsMissingJob(t *testing.T) {
	server := newServer(t, nil, nil)
	response := server.do(t, httptest.NewRequest(http.MethodGet, "/jobs/AAAAAAAAAAAAAAAAAAAAAA", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", response.Code)
	}
	if !strings.Contains(response.Body.String(), "no longer available") {
		t.Errorf("body = %s", response.Body.String())
	}
}

func TestHealthzReportsHealthy(t *testing.T) {
	server := newServer(t, nil, nil)
	response := server.do(t, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"status":"ok"`) {
		t.Errorf("body = %s", response.Body.String())
	}
}

func TestHealthzReportsUnavailableAfterIntakeStops(t *testing.T) {
	server := newServer(t, nil, nil)
	server.manager.StopIntake()
	response := server.do(t, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"status":"unavailable"`) {
		t.Errorf("body = %s", response.Body.String())
	}
}

func TestDiagnosticsIsLocalOnlyByDefault(t *testing.T) {
	server := newServer(t, nil, nil)

	local := httptest.NewRequest(http.MethodGet, "/diagnostics", nil)
	local.RemoteAddr = "127.0.0.1:54321"
	if code := server.do(t, local).Code; code != http.StatusOK {
		t.Fatalf("loopback status = %d; want 200", code)
	}

	remote := httptest.NewRequest(http.MethodGet, "/diagnostics", nil)
	remote.RemoteAddr = "203.0.113.7:54321"
	if code := server.do(t, remote).Code; code != http.StatusNotFound {
		t.Fatalf("remote status = %d; want 404", code)
	}
}

func TestDiagnosticsCanBeDisabled(t *testing.T) {
	server := newServer(t, func(settings *config.Config) {
		settings.Diagnostics = config.DiagnosticsDisabled
	}, nil)
	request := httptest.NewRequest(http.MethodGet, "/diagnostics", nil)
	request.RemoteAddr = "127.0.0.1:54321"
	if code := server.do(t, request).Code; code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", code)
	}
}

func TestDiagnosticsExposesNoSensitiveDetail(t *testing.T) {
	server := newServer(t, func(settings *config.Config) {
		settings.Diagnostics = config.DiagnosticsEnabled
	}, nil)
	manifest := server.upload(t, []string{"secret-holiday.jpg"})

	request := httptest.NewRequest(http.MethodGet, "/diagnostics", nil)
	request.Header.Set("Accept", "application/json")
	response := server.do(t, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	for _, want := range []string{"ffmpeg", "ffprobe", "vips", "queue", "free_space"} {
		if !strings.Contains(body, want) {
			t.Errorf("diagnostics is missing %q", want)
		}
	}
	dataDir := server.manager.Store().Root()
	for _, forbidden := range []string{dataDir, manifest.ID, "secret-holiday", "/data", "VAAPI_DEVICE"} {
		if forbidden == "" {
			continue
		}
		if strings.Contains(body, forbidden) {
			t.Errorf("diagnostics leaked %q\n%s", forbidden, body)
		}
	}
}

func TestWebRootPrefixIsHonoured(t *testing.T) {
	server := newServer(t, func(settings *config.Config) {
		settings.WebRoot = "/convert"
	}, nil)
	if server.base != "/convert" {
		t.Fatalf("base = %q", server.base)
	}

	response := server.do(t, httptest.NewRequest(http.MethodGet, "/convert/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, `action="/convert/jobs"`) {
		t.Errorf("form action is not prefixed\n%s", body)
	}
	if !strings.Contains(body, `/convert/static/app.css`) {
		t.Error("assets are not prefixed")
	}

	if code := server.do(t, httptest.NewRequest(http.MethodGet, "/", nil)).Code; code == http.StatusOK {
		t.Error("the unprefixed root should not be served when WEBROOT is set")
	}
	redirect := server.do(t, httptest.NewRequest(http.MethodGet, "/convert", nil))
	if redirect.Code != http.StatusMovedPermanently {
		t.Errorf("prefix redirect status = %d", redirect.Code)
	}

	manifest := server.upload(t, []string{"a.jpg"})
	start := httptest.NewRequest(http.MethodPost, "/convert/jobs/"+manifest.ID+"/start", nil)
	if code := server.do(t, start).Code; code != http.StatusOK {
		t.Fatalf("prefixed start status = %d", code)
	}
	final := server.waitForState(t, manifest.ID, storage.JobCompleted)
	download := server.do(t, httptest.NewRequest(
		http.MethodGet,
		"/convert/jobs/"+manifest.ID+"/files/"+final.Files[0].ID,
		nil,
	))
	if download.Code != http.StatusOK {
		t.Fatalf("prefixed download status = %d", download.Code)
	}
}

func TestNewAppRejectsMissingManager(t *testing.T) {
	if _, err := web.NewApp(web.Options{}); err == nil {
		t.Fatal("NewApp = nil; want rejection")
	}
}
