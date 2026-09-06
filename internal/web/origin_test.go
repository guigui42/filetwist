package web_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/guigui42/filetwist/internal/config"
)

func TestCrossOriginMutationsAreRejected(t *testing.T) {
	for _, base := range []string{"", "/convert"} {
		t.Run("webroot="+base, func(t *testing.T) {
			server := newServer(t, func(settings *config.Config) {
				settings.WebRoot = base
			}, nil)
			manifest := server.upload(t, []string{"photo.jpg"})
			for _, action := range []string{"/jobs", "/jobs/" + manifest.ID + "/start",
				"/jobs/" + manifest.ID + "/cancel", "/jobs/" + manifest.ID + "/delete"} {
				t.Run(action, func(t *testing.T) {
					request := uploadRequest(t, server.path(action), []string{"unexpected.jpg"})
					request.Header.Set("Origin", "https://untrusted.example")
					response := server.do(t, request)
					if response.Code != http.StatusForbidden {
						t.Errorf("cross-origin POST status = %d; want 403", response.Code)
					}
				})
			}
			got, err := server.manager.Get(manifest.ID)
			if err != nil || got.State != manifest.State {
				t.Errorf("cross-origin requests changed job: state=%s, err=%v", got.State, err)
			}
			manifests, err := server.manager.List()
			if err != nil || len(manifests) != 1 {
				t.Errorf("cross-origin upload created jobs: count=%d, err=%v", len(manifests), err)
			}
		})
	}
}

func TestOriginPolicyAllowsSameOriginAndCLI(t *testing.T) {
	for _, origin := range []string{"", "http://example.com"} {
		t.Run(origin, func(t *testing.T) {
			server := newServer(t, nil, nil)
			request := uploadRequest(t, "/jobs", []string{"photo.jpg"})
			if origin != "" {
				request.Header.Set("Origin", origin)
			}
			if response := server.do(t, request); response.Code != http.StatusOK {
				t.Errorf("same-origin/CLI upload status = %d", response.Code)
			}
		})
	}
}

func TestFetchMetadataBlocksCrossSiteWithoutOrigin(t *testing.T) {
	server := newServer(t, nil, nil)
	request := uploadRequest(t, "/jobs", []string{"photo.jpg"})
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	if response := server.do(t, request); response.Code != http.StatusForbidden {
		t.Errorf("cross-site upload status = %d; want 403", response.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set("Origin", "https://untrusted.example")
	if response := server.do(t, request); response.Code != http.StatusOK {
		t.Errorf("safe health request status = %d", response.Code)
	}
}
