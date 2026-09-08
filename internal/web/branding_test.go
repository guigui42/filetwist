package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/guigui42/filetwist/internal/config"
)

func TestApplicationPagesUseFiletwistBranding(t *testing.T) {
	server := newServer(t, func(settings *config.Config) {
		settings.Diagnostics = config.DiagnosticsEnabled
	}, nil)
	manifest := server.upload(t, []string{"photo.jpg"})
	for _, route := range []string{"/", "/jobs/" + manifest.ID, "/diagnostics"} {
		t.Run(route, func(t *testing.T) {
			response := server.do(t, httptest.NewRequest(http.MethodGet, route, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200", response.Code)
			}
			body := response.Body.String()
			hasTitle := strings.Contains(body, "<title>Filetwist")
			hasBrandName := strings.Contains(body, `class="brand-name">Filetwist</span>`)
			if !hasTitle || !hasBrandName {
				t.Error("page is missing the Filetwist title or header")
			}
			if strings.Contains(strings.ToLower(body), "convertx") {
				t.Error("page still contains the former product name")
			}
		})
	}
}
