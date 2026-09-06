package jobs_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type observedUploadBody struct {
	io.ReadCloser
	reading chan struct{}
	once    sync.Once
}

func (body *observedUploadBody) Read(buffer []byte) (int, error) {
	body.once.Do(func() { close(body.reading) })
	return body.ReadCloser.Read(buffer)
}

func TestShutdownNeedsTransportToUnblockStalledUpload(t *testing.T) {
	manager := newManager(t, &fakeConverter{})
	reading := make(chan struct{})
	accepted := make(chan error, 1)
	requestContext := make(chan context.Context, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.Body = &observedUploadBody{ReadCloser: request.Body, reading: reading}
		requestContext <- request.Context()
		reader, err := request.MultipartReader()
		if err == nil {
			_, err = manager.Accept(request.Context(), reader)
		}
		accepted <- err
	}))
	defer server.Close()
	connection, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close upload connection: %v", err)
		}
	}()
	if _, err := fmt.Fprint(connection,
		"POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1048576\r\n"+
			"Content-Type: multipart/form-data; boundary=stalled\r\n\r\n"+
			"--stalled\r\nContent-Disposition: form-data; name=\"files\"; filename=\"a.jpg\"\r\n\r\n"+
			"unfinished upload"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reading:
	case <-time.After(5 * time.Second):
		t.Fatal("the upload never started reading its body")
	}
	requestCtx := <-requestContext

	httpCtx, cancelHTTP := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelHTTP()
	if err := server.Config.Shutdown(httpCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("HTTP shutdown = %v; want deadline exceeded", err)
	}
	if err := requestCtx.Err(); err != nil {
		t.Fatalf("HTTP Shutdown unexpectedly canceled its active request: %v", err)
	}

	drainCtx, cancelDrain := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelDrain()
	if err := manager.Shutdown(drainCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("manager shutdown = %v; want deadline exceeded while the body is blocked", err)
	}
	if stats := manager.Stats(); stats.Leases != 1 {
		t.Fatalf("blocked upload lost its cleanup protection: %+v", stats)
	}

	if err := server.Config.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-accepted:
		if err == nil {
			t.Fatal("the truncated upload was accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("closing the HTTP transport did not release the upload")
	}
	doneCtx, cancelDone := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelDone()
	if err := manager.Shutdown(doneCtx); err != nil {
		t.Fatal(err)
	}
	manifests, err := manager.List()
	if err != nil || len(manifests) != 0 {
		t.Fatalf("the interrupted upload was not removed: %v, %v", manifests, err)
	}
	if stats := manager.Stats(); stats.Leases != 0 {
		t.Fatalf("the interrupted upload retained its lease: %+v", stats)
	}
}
