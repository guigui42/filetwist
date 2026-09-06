package web_test

import (
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/guigui42/filetwist/internal/config"
	"github.com/guigui42/filetwist/internal/jobs"
	"github.com/guigui42/filetwist/internal/jobs/storage"
)

func TestWorkflowGuidance(t *testing.T) {
	for _, base := range []string{"", "/convert"} {
		t.Run(base, func(t *testing.T) {
			server := newServer(t, func(settings *config.Config) {
				settings.WebRoot = base
			}, nil)
			for _, route := range []string{"/", "/help"} {
				response := server.do(t, httptest.NewRequest(http.MethodGet, server.path(route), nil))
				if response.Code != http.StatusOK {
					t.Fatalf("%s status = %d", route, response.Code)
				}
				body := response.Body.String()
				for _, want := range []string{
					"Shared workspace", "download", "delete", server.path("/help"),
				} {
					if !strings.Contains(body, want) {
						t.Errorf("%s missing %q", route, want)
					}
				}
			}
			help := server.do(t, httptest.NewRequest(http.MethodGet, server.path("/help"), nil)).Body.String()
			for _, want := range []string{"JPEG", "WebP", "PNG", "H.264", "M4A", "MP3", "FLAC", "not guaranteed", "not an original-file copy", "1 day", "Keep your originals"} {
				if !strings.Contains(help, want) {
					t.Errorf("help missing %q", want)
				}
			}
		})
	}
}

func TestWorkflowReviewAndRecentJobs(t *testing.T) {
	server := newServer(t, nil, nil)
	manifest := server.upload(t, []string{"holiday.jpg", "second.jpg"})
	response := server.do(t, httptest.NewRequest(http.MethodGet, "/jobs/"+manifest.ID, nil))
	body := response.Body.String()
	for _, want := range []string{
		"Review your files", "Convert 2 files", "JPEG", "flattens transparency",
		"Apply to compatible files", "Keep individual choices", "data-operation",
		`aria-describedby="help-`, "/remove", "Conversion details",
		`data-job-url="/jobs/` + manifest.ID,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("review missing %q", want)
		}
	}
	if strings.Contains(body, "file(s)") {
		t.Error("review uses placeholder pluralization")
	}
	index := html.UnescapeString(server.do(t, httptest.NewRequest(http.MethodGet, "/", nil)).Body.String())
	if !strings.Contains(index, "holiday.jpg + 1 more") {
		t.Error("recent job has no recognizable content label")
	}
}

func TestWorkflowCompletedDownloadIsPrimary(t *testing.T) {
	server := newServer(t, nil, nil)
	manifest := server.upload(t, []string{"photo.jpg"})
	if _, err := server.manager.Start(manifest.ID, nil); err != nil {
		t.Fatal(err)
	}
	server.waitForState(t, manifest.ID, storage.JobCompleted)
	body := server.do(t, httptest.NewRequest(http.MethodGet, "/jobs/"+manifest.ID, nil)).Body.String()
	for _, want := range []string{
		"Your downloads are ready", "1 of 1 file converted",
		`class="button primary-download"`, "Download all as ZIP",
		`<details`, "Conversion details", "Upload more files",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("completed page missing %q", want)
		}
	}
	if strings.Contains(body, "/remove") {
		t.Error("completed job exposes pre-conversion removal")
	}
	if strings.Index(body, "Download all as ZIP") > strings.Index(body, "Conversion details") {
		t.Error("primary download appears after technical details")
	}
}

func TestRemoveFileHTTP(t *testing.T) {
	for _, base := range []string{"", "/convert"} {
		t.Run(base, func(t *testing.T) {
			server := newServer(t, func(settings *config.Config) {
				settings.WebRoot = base
			}, nil)
			manifest := server.upload(t, []string{"first.jpg", "second.jpg"})
			path := server.path("/jobs/" + manifest.ID + "/files/" + manifest.Files[0].ID + "/remove")
			response := server.do(t, httptest.NewRequest(http.MethodPost, path, nil))
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Convert 1 file") {
				t.Fatalf("remove response = %d %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "first.jpg") {
				t.Error("removed file still appears in the job")
			}
			missing := server.do(t, httptest.NewRequest(http.MethodPost, path, nil))
			if missing.Code != http.StatusNotFound {
				t.Errorf("repeat removal status = %d", missing.Code)
			}
			path = server.path("/jobs/" + manifest.ID + "/files/" + manifest.Files[1].ID + "/remove")
			response = server.do(t, httptest.NewRequest(http.MethodPost, path, nil))
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "empty job was deleted") {
				t.Fatalf("last removal response = %d %s", response.Code, response.Body.String())
			}
			if _, err := server.manager.Get(manifest.ID); !errors.Is(err, jobs.ErrNotFound) {
				t.Errorf("empty job still exists: %v", err)
			}
		})
	}
}

func TestRemoveFileHTTPRejectsStartedJob(t *testing.T) {
	server := newServer(t, nil, nil)
	manifest := server.upload(t, []string{"photo.jpg"})
	if _, err := server.manager.Start(manifest.ID, nil); err != nil {
		t.Fatal(err)
	}
	path := "/jobs/" + manifest.ID + "/files/" + manifest.Files[0].ID + "/remove"
	response := server.do(t, httptest.NewRequest(http.MethodPost, path, nil))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "before conversion starts") {
		t.Fatalf("started removal response = %d %s", response.Code, response.Body.String())
	}
}
