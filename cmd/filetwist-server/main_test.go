package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/config"
)

func newTestApplication(t *testing.T) *application {
	t.Helper()
	dataDir := t.TempDir()
	settings, err := config.Load(func(name string) (string, bool) {
		if name == config.DataDirEnv {
			return dataDir, true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	application, err := newApplication(settings, discard)
	if err != nil {
		t.Fatalf("newApplication: %v", err)
	}
	t.Cleanup(func() {
		if err := application.manager.Shutdown(context.Background()); err != nil {
			t.Errorf("manager shutdown: %v", err)
		}
	})
	return application
}

func TestHealthHandler(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		wantStatus int
		wantBody   string
	}{
		{name: "get", method: http.MethodGet, wantStatus: http.StatusOK, wantBody: `"status":"ok"`},
		{name: "head", method: http.MethodHead, wantStatus: http.StatusOK},
		{name: "post", method: http.MethodPost, wantStatus: http.StatusMethodNotAllowed},
	}

	handler := newTestApplication(t).handler
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, "/healthz", nil)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d; want %d", recorder.Code, tt.wantStatus)
			}
			if tt.wantBody != "" && !strings.Contains(recorder.Body.String(), tt.wantBody) {
				t.Errorf("body = %q; want substring %q", recorder.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestRunHealthcheck(t *testing.T) {
	server := httptest.NewServer(newTestApplication(t).handler)
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runHealthcheck(
		context.Background(),
		[]string{"--url", server.URL + "/healthz"},
		&stdout,
		&stderr,
		server.Client(),
	)
	if code != 0 {
		t.Fatalf("exit code = %d; stderr = %s", code, stderr.String())
	}
	if stdout.String() != "healthy\n" {
		t.Errorf("stdout = %q; want healthy", stdout.String())
	}
}

func TestRunVersionAndInvalidCommand(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		want     string
	}{
		{name: "version", args: []string{"version"}, wantCode: 0, want: "filetwist-server dev"},
		{name: "invalid", args: []string{"unknown"}, wantCode: 2, want: "unknown command"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(context.Background(), tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Fatalf("exit code = %d; want %d", code, tt.wantCode)
			}
			output := stdout.String() + stderr.String()
			if !strings.Contains(output, tt.want) {
				t.Errorf("output = %q; want substring %q", output, tt.want)
			}
		})
	}
}

func TestServeRejectsInvalidConfiguration(t *testing.T) {
	t.Setenv(config.JobTTLEnv, "not-a-duration")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), []string{"serve"}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d; want 1", code)
	}
	if !strings.Contains(stderr.String(), config.JobTTLEnv) {
		t.Errorf("stderr = %q; want %s", stderr.String(), config.JobTTLEnv)
	}
}

// TestHealthcheckFollowsListenAddress covers the packaging failure this
// replaces: the container healthcheck used a hardcoded port, so any
// LISTEN_ADDRESS other than :8080 reported unhealthy while serving correctly.
func TestHealthcheckFollowsListenAddress(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := httptest.NewUnstartedServer(newTestApplication(t).handler)
	if err := server.Listener.Close(); err != nil {
		t.Fatalf("close default listener: %v", err)
	}
	server.Listener = listener
	server.Start()
	defer server.Close()

	address, ok := server.Listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address = %T", server.Listener.Addr())
	}
	port := strconv.Itoa(address.Port)
	if port == "8080" {
		t.Skip("the ephemeral port collided with the default")
	}

	t.Setenv(config.DataDirEnv, t.TempDir())
	t.Setenv(config.ListenAddressEnv, "0.0.0.0:"+port)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := runHealthcheck(
		context.Background(), nil, &stdout, &stderr, server.Client(),
	); code != 0 {
		t.Fatalf("exit code = %d; stderr = %s", code, stderr.String())
	}
	if stdout.String() != "healthy\n" {
		t.Errorf("stdout = %q; want healthy", stdout.String())
	}
}

// TestHealthcheckPrefersExplicitOverrides confirms the flag still wins over
// the derived URL, and that HEALTHCHECK_URL wins over LISTEN_ADDRESS.
func TestHealthcheckPrefersExplicitOverrides(t *testing.T) {
	server := httptest.NewServer(newTestApplication(t).handler)
	defer server.Close()

	t.Setenv(config.DataDirEnv, t.TempDir())
	// Both point at a port nothing is listening on, so only an honoured
	// override can succeed.
	t.Setenv(config.ListenAddressEnv, "127.0.0.1:1")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := runHealthcheck(
		context.Background(),
		[]string{"--url", server.URL + "/healthz"},
		&stdout,
		&stderr,
		server.Client(),
	); code != 0 {
		t.Fatalf("flag override: exit code = %d; stderr = %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	t.Setenv(config.HealthcheckURLEnv, server.URL+"/healthz")
	if code := runHealthcheck(
		context.Background(), nil, &stdout, &stderr, server.Client(),
	); code != 0 {
		t.Fatalf("env override: exit code = %d; stderr = %s", code, stderr.String())
	}
	if stdout.String() != "healthy\n" {
		t.Errorf("stdout = %q; want healthy", stdout.String())
	}
}

func TestHealthcheckRejectsInvalidConfiguration(t *testing.T) {
	t.Setenv(config.ListenAddressEnv, "no-port")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runHealthcheck(context.Background(), nil, &stdout, &stderr, http.DefaultClient)
	if code != 1 {
		t.Fatalf("exit code = %d; want 1", code)
	}
	if !strings.Contains(stderr.String(), config.ListenAddressEnv) {
		t.Errorf("stderr = %q; want %s", stderr.String(), config.ListenAddressEnv)
	}
}

func TestHTTPShutdownClosesStalledRequest(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(started)
		_, err := io.Copy(io.Discard, request.Body)
		finished <- err
	}))
	defer server.Close()
	connection, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close connection: %v", err)
		}
	}()
	if _, err := fmt.Fprint(connection, "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1024\r\n\r\npartial"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := shutdownHTTPServer(ctx, server.Config); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown = %v; want expired grace period", err)
	}
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("truncated request was read successfully")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown left the request body blocked")
	}
}

func TestHTTPShutdownAllowsGracefulCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := shutdownHTTPServer(ctx, server.Config); err != nil {
		t.Fatalf("graceful shutdown = %v", err)
	}
}
