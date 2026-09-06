package web_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guigui42/filetwist/internal/config"
	"github.com/guigui42/filetwist/internal/jobs/storage"
)

// endlessFieldBody streams non-file multipart fields forever, standing in for a
// client that never declares a length and never sends a file.
type endlessFieldBody struct {
	boundary string
	pending  string
	read     int64
}

func (body *endlessFieldBody) Read(buffer []byte) (int, error) {
	if body.pending == "" {
		body.pending = fmt.Sprintf(
			"--%s\r\nContent-Disposition: form-data; name=%q\r\n\r\n%s\r\n",
			body.boundary,
			"noise",
			strings.Repeat("x", 4096),
		)
	}
	count := copy(buffer, body.pending)
	body.pending = body.pending[count:]
	body.read += int64(count)
	return count, nil
}

func TestUploadStopsReadingAnEndlessBody(t *testing.T) {
	server := newServer(t, nil, nil)
	body := &endlessFieldBody{boundary: "filetwistboundary"}
	request := httptest.NewRequest(http.MethodPost, server.path("/jobs"), body)
	request.Header.Set("Content-Type", "multipart/form-data; boundary="+body.boundary)
	request.Header.Set("HX-Request", "true")
	request.ContentLength = -1

	response := server.do(t, request)
	if response.Code != http.StatusBadRequest && response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d; want 400 or 413", response.Code)
	}
	if body.read > 32<<20 {
		t.Errorf("handler read %d bytes from an endless body; want a bounded prefix", body.read)
	}
	manifests, err := server.manager.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(manifests) != 0 {
		t.Errorf("jobs = %d; want the rejected upload to leave nothing behind", len(manifests))
	}
}

func TestUploadRejectsABodyBeyondTheDeclaredLimit(t *testing.T) {
	server := newServer(t, func(settings *config.Config) {
		settings.MaxUploadSize = 1 << 10
	}, nil)
	request := uploadRequest(t, server.path("/jobs"), []string{"a.jpg"})
	request.ContentLength = (1 << 30)

	response := server.do(t, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d; want 413", response.Code)
	}
}

// completedArchiveJob uploads, converts, and returns the finished manifest.
func completedArchiveJob(t *testing.T, server *testServer, names []string) storage.Manifest {
	t.Helper()
	manifest := server.upload(t, names)
	server.do(t, httptest.NewRequest(http.MethodPost, server.path("/jobs/"+manifest.ID+"/start"), nil))
	return server.waitForState(t, manifest.ID, storage.JobCompleted)
}

func TestArchiveIsRefusedWhenAnOutputIsMissing(t *testing.T) {
	server := newServer(t, nil, nil)
	final := completedArchiveJob(t, server, []string{"a.jpg", "b.jpg"})
	outputs := final.CompletedOutputs()
	if len(outputs) != 2 {
		t.Fatalf("outputs = %d; want 2", len(outputs))
	}

	outputRoot, err := server.manager.Store().OutputDir(final.ID)
	if err != nil {
		t.Fatalf("OutputDir: %v", err)
	}
	missing := filepath.Join(outputRoot, outputs[1].ID, outputs[1].Output.Name)
	if err := os.Remove(missing); err != nil {
		t.Fatalf("remove output: %v", err)
	}

	response := server.do(t, httptest.NewRequest(http.MethodGet, server.path("/jobs/"+final.ID+"/download"), nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", response.Code)
	}
	payload := response.Body.Bytes()
	if _, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload))); err == nil {
		t.Fatal("a readable archive was served even though an output was missing")
	}
}

func TestArchiveEntryNamesAreAlwaysPathFree(t *testing.T) {
	server := newServer(t, nil, nil)
	final := completedArchiveJob(t, server, []string{"a.jpg"})
	outputs := final.CompletedOutputs()
	if len(outputs) != 1 {
		t.Fatalf("outputs = %d; want 1", len(outputs))
	}

	outputRoot, err := server.manager.Store().OutputDir(final.ID)
	if err != nil {
		t.Fatalf("OutputDir: %v", err)
	}
	// A manifest is data on disk, so an archive entry name must stay path-free
	// even when the recorded output name looks like a path on some platform.
	hostile := `C:evil.jpg`
	fileDir := filepath.Join(outputRoot, outputs[0].ID)
	if err := os.Rename(
		filepath.Join(fileDir, outputs[0].Output.Name),
		filepath.Join(fileDir, hostile),
	); err != nil {
		t.Fatalf("rename output: %v", err)
	}
	stored, err := server.manager.Get(final.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	stored.FileByID(outputs[0].ID).Output.Name = hostile
	if err := server.manager.Store().Save(stored); err != nil {
		t.Fatalf("Save: %v", err)
	}

	response := server.do(t, httptest.NewRequest(http.MethodGet, server.path("/jobs/"+final.ID+"/download"), nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", response.Code)
	}
	payload := response.Body.Bytes()
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	if len(archive.File) != 1 {
		t.Fatalf("entries = %d; want 1", len(archive.File))
	}
	name := archive.File[0].Name
	if strings.ContainsAny(name, `/\:`) {
		t.Fatalf("entry name %q carries a path separator or drive prefix", name)
	}
	entry, err := archive.File[0].Open()
	if err != nil {
		t.Fatalf("open entry: %v", err)
	}
	content, err := io.ReadAll(entry)
	if err != nil {
		t.Fatalf("read entry: %v", err)
	}
	if err := entry.Close(); err != nil {
		t.Fatalf("close entry: %v", err)
	}
	if string(content) != "converted-image-bytes" {
		t.Fatalf("entry content = %q", content)
	}
}
