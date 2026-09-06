package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/guigui42/filetwist/internal/config"
)

// trustedProxies parses a list the way TRUSTED_PROXIES is parsed at startup.
func trustedProxies(t *testing.T, value string) []netip.Prefix {
	t.Helper()
	settings, err := config.Load(func(name string) (string, bool) {
		switch name {
		case config.DataDirEnv:
			return t.TempDir(), true
		case config.TrustedProxiesEnv:
			return value, true
		default:
			return "", false
		}
	})
	if err != nil {
		t.Fatalf("config.Load(%q): %v", value, err)
	}
	return settings.TrustedProxies
}

// TestDiagnosticsLocalHonoursOnlyTrustedProxies covers the gap a same-host
// reverse proxy opens: every proxied client arrives from 127.0.0.1, so the
// peer address alone cannot decide who is local.
func TestDiagnosticsLocalHonoursOnlyTrustedProxies(t *testing.T) {
	tests := []struct {
		name       string
		proxies    string
		remoteAddr string
		headers    map[string]string
		wantStatus int
	}{
		{
			name:       "direct loopback",
			remoteAddr: "127.0.0.1:54321",
			wantStatus: http.StatusOK,
		},
		{
			name:       "direct loopback ipv6",
			remoteAddr: "[::1]:54321",
			wantStatus: http.StatusOK,
		},
		{
			name:       "direct remote",
			remoteAddr: "203.0.113.7:54321",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "same-host proxy with external client",
			proxies:    "loopback",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.7"},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "same-host proxy with local client",
			proxies:    "loopback",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string]string{"X-Forwarded-For": "127.0.0.1"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "same-host proxy chain ending outside",
			proxies:    "loopback, 10.0.0.0/8",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.7, 10.0.0.5"},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "spoofed header from untrusted loopback peer",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string]string{"X-Forwarded-For": "127.0.0.1"},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "spoofed header from untrusted remote peer",
			remoteAddr: "203.0.113.7:54321",
			headers:    map[string]string{"X-Forwarded-For": "127.0.0.1"},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "rfc7239 header from untrusted peer",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string]string{"Forwarded": `for="203.0.113.7"`},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "rfc7239 header from trusted proxy is not parsed",
			proxies:    "loopback",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string]string{"Forwarded": `for="203.0.113.7"`},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "unparsable chain from trusted proxy",
			proxies:    "loopback",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string]string{"X-Forwarded-For": "unknown"},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "trusted proxy on another host with local client claim",
			proxies:    "198.51.100.4",
			remoteAddr: "198.51.100.4:54321",
			headers:    map[string]string{"X-Forwarded-For": "127.0.0.1"},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var prefixes []netip.Prefix
			if tt.proxies != "" {
				prefixes = trustedProxies(t, tt.proxies)
			}
			server := newServer(t, func(settings *config.Config) {
				settings.TrustedProxies = prefixes
			}, nil)

			request := httptest.NewRequest(http.MethodGet, "/diagnostics", nil)
			request.RemoteAddr = tt.remoteAddr
			for name, value := range tt.headers {
				request.Header.Set(name, value)
			}
			if code := server.do(t, request).Code; code != tt.wantStatus {
				t.Fatalf("status = %d; want %d", code, tt.wantStatus)
			}
		})
	}
}

// TestDiagnosticsModesIgnoreForwardedHeaders confirms that the explicit modes
// never consult the proxy chain at all.
func TestDiagnosticsModesIgnoreForwardedHeaders(t *testing.T) {
	tests := []struct {
		name       string
		mode       config.Diagnostics
		wantStatus int
	}{
		{name: "enabled", mode: config.DiagnosticsEnabled, wantStatus: http.StatusOK},
		{name: "disabled", mode: config.DiagnosticsDisabled, wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newServer(t, func(settings *config.Config) {
				settings.Diagnostics = tt.mode
			}, nil)
			request := httptest.NewRequest(http.MethodGet, "/diagnostics", nil)
			request.RemoteAddr = "203.0.113.7:54321"
			request.Header.Set("X-Forwarded-For", "127.0.0.1")
			if code := server.do(t, request).Code; code != tt.wantStatus {
				t.Fatalf("status = %d; want %d", code, tt.wantStatus)
			}
		})
	}
}
