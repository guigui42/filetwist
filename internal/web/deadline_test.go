package web_test

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/config"
	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/jobs/storage"
	"github.com/guigui42/filetwist/internal/profiles"
)

// live starts a real TCP server. Deadlines are enforced by the connection, so
// they cannot be observed through an in-memory response recorder.
func live(t *testing.T, server *testServer) *httptest.Server {
	t.Helper()
	live := httptest.NewServer(server.handler)
	t.Cleanup(live.Close)
	return live
}

// multipartBody builds one complete multipart upload body.
func multipartBody(t *testing.T, name string, size int) ([]byte, string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	part, err := writer.CreateFormFile("files", name)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(bytes.Repeat([]byte("u"), size)); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return buffer.Bytes(), writer.FormDataContentType()
}

// trickle feeds a body in fixed chunks separated by a pause, so the request
// never stalls but also never arrives at once.
type trickle struct {
	payload []byte
	chunk   int
	pause   time.Duration
	offset  int
}

func (reader *trickle) Read(buffer []byte) (int, error) {
	if reader.offset >= len(reader.payload) {
		return 0, io.EOF
	}
	if reader.offset > 0 {
		time.Sleep(reader.pause)
	}
	end := min(reader.offset+reader.chunk, len(reader.payload))
	count := copy(buffer, reader.payload[reader.offset:end])
	reader.offset += count
	return count, nil
}

// TestStalledUploadIsTerminated proves a body that goes quiet is cut inside a
// bounded window rather than holding the connection and its upload
// reservation until the client gives up.
func TestStalledUploadIsTerminated(t *testing.T) {
	server := newServer(t, func(settings *config.Config) {
		settings.ReadStallTimeout = 300 * time.Millisecond
		settings.MinUploadRate = 0
	}, nil)
	endpoint := live(t, server)

	connection, err := net.Dial("tcp", strings.TrimPrefix(endpoint.URL, "http://"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() {
		_ = connection.Close()
	}()

	body, contentType := multipartBody(t, "slow.jpg", 512)
	preamble := body[:64]
	request := fmt.Sprintf(
		"POST /jobs HTTP/1.1\r\nHost: %s\r\nContent-Type: %s\r\nContent-Length: %d\r\n\r\n",
		endpoint.Listener.Addr(),
		contentType,
		len(body),
	)
	if _, err := connection.Write([]byte(request)); err != nil {
		t.Fatalf("write headers: %v", err)
	}
	if _, err := connection.Write(preamble); err != nil {
		t.Fatalf("write preamble: %v", err)
	}

	// The rest of the declared body is never sent.
	started := time.Now()
	if err := connection.SetReadDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		t.Fatalf("the stalled upload was not terminated: %v", err)
	}
	defer func() {
		_ = response.Body.Close()
	}()
	elapsed := time.Since(started)

	if response.StatusCode != http.StatusRequestTimeout {
		t.Errorf("status = %d; want 408", response.StatusCode)
	}
	if elapsed > 10*time.Second {
		t.Errorf("termination took %s; want a bounded idle window", elapsed)
	}
	manifests, err := server.manager.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(manifests) != 0 {
		t.Errorf("rejected upload left %d job(s) behind", len(manifests))
	}
}

// TestSlowButProgressingUploadCompletes proves the read deadline follows
// progress instead of bounding the whole request, so a large upload on a slow
// link is not rejected for taking longer than the idle window.
func TestSlowButProgressingUploadCompletes(t *testing.T) {
	server := newServer(t, func(settings *config.Config) {
		settings.ReadStallTimeout = 300 * time.Millisecond
		settings.MinUploadRate = 0
	}, nil)
	endpoint := live(t, server)

	const payload = 4096
	body, contentType := multipartBody(t, "slow.jpg", payload)
	source := &trickle{payload: body, chunk: len(body) / 8, pause: 120 * time.Millisecond}

	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		endpoint.URL+"/jobs",
		source,
	)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("HX-Request", "true")

	started := time.Now()
	response, err := endpoint.Client().Do(request)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200", response.StatusCode)
	}
	if elapsed := time.Since(started); elapsed < 500*time.Millisecond {
		t.Fatalf("upload finished in %s; the trickle did not exercise the window", elapsed)
	}

	manifests, err := server.manager.List()
	if err != nil || len(manifests) != 1 {
		t.Fatalf("jobs = %d, err = %v; want one stored job", len(manifests), err)
	}
	if manifests[0].TotalBytes != payload {
		t.Errorf("stored bytes = %d; want %d", manifests[0].TotalBytes, payload)
	}
}

// TestUploadBelowMinimumRateIsRejected covers the gap the idle window leaves:
// a client that sends a few bytes before every deadline never stalls, yet it
// still holds a connection and an upload reservation indefinitely.
func TestUploadBelowMinimumRateIsRejected(t *testing.T) {
	server := newServer(t, func(settings *config.Config) {
		settings.ReadStallTimeout = 200 * time.Millisecond
		settings.MinUploadRate = 1 << 20
	}, nil)
	endpoint := live(t, server)

	body, contentType := multipartBody(t, "trickle.jpg", 4096)
	source := &trickle{payload: body, chunk: 16, pause: 50 * time.Millisecond}

	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		endpoint.URL+"/jobs",
		source,
	)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("HX-Request", "true")

	response, err := endpoint.Client().Do(request)
	if err != nil {
		// A refused body can also surface as a broken write on the client.
		return
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode != http.StatusRequestTimeout {
		t.Errorf("status = %d; want 408", response.StatusCode)
	}
}

// bigOutputConverter writes an output large enough that the response cannot
// fit in the socket buffers of either end.
func bigOutputConverter(t *testing.T, size int64) *stubConverter {
	t.Helper()
	return &stubConverter{
		convert: func(_ context.Context, request conversion.Request) (conversion.Result, error) {
			name, err := profiles.OutputName(request.InputPath, request.Operation)
			if err != nil {
				return conversion.Result{}, err
			}
			outputPath := filepath.Join(request.Output, name)
			handle, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
			if err != nil {
				return conversion.Result{}, err
			}
			if _, err := io.CopyN(handle, newFiller(), size); err != nil {
				_ = handle.Close()
				return conversion.Result{}, err
			}
			if err := handle.Close(); err != nil {
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
		},
	}
}

func newFiller() io.Reader {
	return &filler{block: bytes.Repeat([]byte("filetwist-output-"), 4096)}
}

type filler struct {
	block  []byte
	offset int
}

func (reader *filler) Read(buffer []byte) (int, error) {
	count := copy(buffer, reader.block[reader.offset:])
	reader.offset = (reader.offset + count) % len(reader.block)
	return count, nil
}

// downloadedJob uploads, converts, and returns the completed job identifier.
func downloadedJob(t *testing.T, server *testServer) string {
	t.Helper()
	manifest := server.upload(t, []string{"big.jpg"})
	start := httptest.NewRequest(http.MethodPost, server.path("/jobs/"+manifest.ID+"/start"), nil)
	if code := server.do(t, start).Code; code != http.StatusOK {
		t.Fatalf("start status = %d", code)
	}
	server.waitForState(t, manifest.ID, storage.JobCompleted)
	return manifest.ID
}

// TestLongDownloadSurvivesASlowReader proves the write deadline follows write
// progress, so a large streamed archive is never cut for taking longer than
// the idle window. A fixed WriteTimeout on the server would fail this.
//
// The reader is deliberately slower than the transfer but much faster than the
// window: a socket only reports write progress when the receiver advertises a
// window update, which lags a very slow reader by whole buffers.
func TestLongDownloadSurvivesASlowReader(t *testing.T) {
	const outputSize = 16 << 20
	server := newServer(t, func(settings *config.Config) {
		settings.WriteStallTimeout = time.Second
		settings.MaxUploadSize = 1 << 20
	}, bigOutputConverter(t, outputSize))
	endpoint := live(t, server)
	id := downloadedJob(t, server)

	response, err := endpoint.Client().Get(endpoint.URL + server.path("/jobs/"+id+"/download"))
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}

	started := time.Now()
	buffer := make([]byte, 64<<10)
	var total int64
	for {
		count, err := response.Body.Read(buffer)
		total += int64(count)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("slow read failed after %d bytes: %v", total, err)
		}
		time.Sleep(4 * time.Millisecond)
	}
	if elapsed := time.Since(started); elapsed < 2*time.Second {
		t.Fatalf("download finished in %s; the reader did not exercise the window", elapsed)
	}
	if total < outputSize {
		t.Errorf("archive was %d bytes; want at least the %d byte payload", total, outputSize)
	}
}

// TestStalledDownloadIsTerminated proves a client that stops reading cannot
// pin a response goroutine and its job lease forever.
func TestStalledDownloadIsTerminated(t *testing.T) {
	const outputSize = 48 << 20
	server := newServer(t, func(settings *config.Config) {
		settings.WriteStallTimeout = 200 * time.Millisecond
		settings.MaxUploadSize = 1 << 20
	}, bigOutputConverter(t, outputSize))
	endpoint := live(t, server)
	id := downloadedJob(t, server)

	manifest, err := server.manager.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	target := fmt.Sprintf("/jobs/%s/files/%s", id, manifest.Files[0].ID)

	connection, err := net.Dial("tcp", strings.TrimPrefix(endpoint.URL, "http://"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() {
		_ = connection.Close()
	}()
	request := fmt.Sprintf(
		"GET %s HTTP/1.1\r\nHost: %s\r\n\r\n",
		server.path(target),
		endpoint.Listener.Addr(),
	)
	if _, err := connection.Write([]byte(request)); err != nil {
		t.Fatalf("write request: %v", err)
	}

	// Read nothing at all for well beyond the write window, then drain.
	time.Sleep(2 * time.Second)
	if err := connection.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	received, err := io.Copy(io.Discard, connection)
	if err == nil && received >= outputSize {
		t.Fatalf("the stalled download delivered %d bytes; want a terminated response", received)
	}
}
