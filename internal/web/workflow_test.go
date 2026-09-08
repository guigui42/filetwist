package web_test

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/config"
	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/corpus"
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
		`aria-describedby="help-`, `data-format="JPEG"`, "data-output-format",
		"/remove", "Conversion details",
		`aria-label="Conversion progress"`, "route-profile", "route-output",
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
		"Download all files", "data-download-all",
		`<details`, "Conversion details", "Upload more files",
		"Output passed profile validation", `aria-current="step"`,
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

func TestWorkflowWarningsPrecedeValidation(t *testing.T) {
	defaultConverter := &stubConverter{}
	converter := &stubConverter{convert: func(
		ctx context.Context,
		request conversion.Request,
	) (conversion.Result, error) {
		result, err := defaultConverter.Convert(ctx, request)
		if err != nil {
			return conversion.Result{}, err
		}
		result.Warnings = []conversion.Warning{{
			Code:    "test_warning",
			Message: "A secondary stream was dropped.",
		}}
		return result, nil
	}}
	server := newServer(t, nil, converter)
	manifest := server.upload(t, []string{"photo.jpg"})
	if _, err := server.manager.Start(manifest.ID, nil); err != nil {
		t.Fatal(err)
	}
	server.waitForState(t, manifest.ID, storage.JobCompleted)
	body := server.do(t, httptest.NewRequest(http.MethodGet, "/jobs/"+manifest.ID, nil)).Body.String()
	warningIndex := strings.Index(body, "Processing note:")
	validationIndex := strings.Index(body, "Output passed profile validation")
	if warningIndex < 0 || validationIndex < 0 {
		t.Fatalf("completed page missing warning or validation result")
	}
	if warningIndex > validationIndex {
		t.Error("validation appears before the processing warning")
	}
}

func TestWorkflowTerminalJobPresentation(t *testing.T) {
	tests := []struct {
		name             string
		jobState         storage.JobState
		fileState        storage.FileState
		validationStatus string
		errorMessage     string
		wantFailedStage  string
	}{
		{
			name:            "conversion failure",
			jobState:        storage.JobFailed,
			fileState:       storage.FileFailed,
			errorMessage:    "The converter could not decode this file.",
			wantFailedStage: "Convert",
		},
		{
			name:             "validation failure",
			jobState:         storage.JobFailed,
			fileState:        storage.FileFailed,
			validationStatus: "failed",
			errorMessage:     "The output did not satisfy the selected profile.",
			wantFailedStage:  "Validate",
		},
		{
			name:            "canceled",
			jobState:        storage.JobCanceled,
			fileState:       storage.FileCanceled,
			wantFailedStage: "Convert",
		},
		{
			name:            "interrupted",
			jobState:        storage.JobInterrupted,
			fileState:       storage.FileInterrupted,
			wantFailedStage: "Convert",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newServer(t, nil, nil)
			manifest := server.upload(t, []string{"photo.jpg"})
			if _, err := server.manager.Store().Update(
				manifest.ID,
				time.Now(),
				func(current *storage.Manifest) error {
					current.State = tt.jobState
					file := &current.Files[0]
					file.State = tt.fileState
					file.Selected = corpus.OperationCompatiblePhoto
					file.Validation.Status = tt.validationStatus
					if tt.errorMessage != "" {
						file.Error = &storage.Failure{
							Kind:    "test",
							Code:    "test_failure",
							Message: tt.errorMessage,
						}
					}
					return nil
				},
			); err != nil {
				t.Fatal(err)
			}

			body := server.do(t, httptest.NewRequest(
				http.MethodGet,
				"/jobs/"+manifest.ID,
				nil,
			)).Body.String()
			if strings.Contains(body, "Created after conversion") {
				t.Error("terminal job promises a future output")
			}
			if !strings.Contains(body, "No output available") {
				t.Error("terminal job does not explain that no output is available")
			}
			failedIndex := strings.Index(body, `class="stage--error"`)
			if failedIndex < 0 {
				t.Fatal("terminal job has no failed stage")
			}
			stageIndex := strings.Index(body[failedIndex:], tt.wantFailedStage)
			if stageIndex < 0 || stageIndex > 300 {
				t.Errorf("failed stage does not identify %q", tt.wantFailedStage)
			}
			if tt.errorMessage != "" {
				if !strings.Contains(body, tt.errorMessage) {
					t.Errorf("terminal job is missing error %q", tt.errorMessage)
				}
				if strings.Contains(body, "Conversion failed:") {
					t.Error("terminal error has an inaccurate conversion-only prefix")
				}
			}
		})
	}
}

func TestWorkflowCompletedBatchDownloadsAreSeparate(t *testing.T) {
	tests := []struct {
		name string
		base string
	}{
		{name: "root", base: ""},
		{name: "webroot", base: "/convert"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defaultConverter := &stubConverter{}
			converter := &stubConverter{convert: func(
				ctx context.Context,
				request conversion.Request,
			) (conversion.Result, error) {
				if strings.Contains(filepath.Base(request.InputPath), "failed") {
					return conversion.Result{}, errors.New("test conversion failure")
				}
				return defaultConverter.Convert(ctx, request)
			}}
			server := newServer(t, func(settings *config.Config) {
				settings.WebRoot = tt.base
			}, converter)
			manifest := server.upload(t, []string{"first.jpg", "failed.jpg", "second.jpg"})
			if _, err := server.manager.Start(manifest.ID, nil); err != nil {
				t.Fatal(err)
			}
			final := server.waitForState(t, manifest.ID, storage.JobCompleted)
			body := server.do(t, httptest.NewRequest(
				http.MethodGet,
				server.path("/jobs/"+manifest.ID),
				nil,
			)).Body.String()

			for _, want := range []string{
				"Some downloads are ready",
				"2 of 3 files converted",
				"Download all as ZIP",
				"Download all files",
				"data-download-all",
			} {
				if !strings.Contains(body, want) {
					t.Errorf("completed page missing %q", want)
				}
			}
			if got := strings.Count(body, "data-download-file"); got != 2 {
				t.Errorf("batch download targets = %d; want 2", got)
			}
			if got := strings.Count(body, `download="`); got != 2 {
				t.Errorf("download attributes = %d; want 2", got)
			}
			if strings.Index(body, "Download all as ZIP") > strings.Index(body, "Download all files") {
				t.Error("separate downloads appear before the primary ZIP action")
			}
			for _, file := range final.Files {
				url := server.path("/jobs/" + final.ID + "/files/" + file.ID)
				hasDownload := strings.Contains(body, `href="`+url+`"`)
				if file.State == storage.FileCompleted && !hasDownload {
					t.Errorf("completed file %q has no download URL", file.OriginalName)
				}
				if file.State != storage.FileCompleted && hasDownload {
					t.Errorf("non-completed file %q has a download URL", file.OriginalName)
				}
			}
		})
	}
}

func TestWorkflowActiveJobHidesBatchDownloads(t *testing.T) {
	server := newServer(t, nil, nil)
	manifest := server.upload(t, []string{"first.jpg", "second.jpg", "third.jpg"})
	if _, err := server.manager.Store().Update(manifest.ID, time.Now(), func(current *storage.Manifest) error {
		current.State = storage.JobRunning
		for index := range 2 {
			current.Files[index].State = storage.FileCompleted
			current.Files[index].Output = &storage.Output{
				Name:     current.Files[index].Name,
				Size:     5,
				MIMEType: "image/jpeg",
			}
		}
		current.Files[2].State = storage.FileRunning
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	body := server.do(t, httptest.NewRequest(
		http.MethodGet,
		"/jobs/"+manifest.ID+"/status",
		nil,
	)).Body.String()
	if strings.Contains(body, "Download all") {
		t.Error("active job exposes batch downloads while polling")
	}
	if got := strings.Count(body, "data-download-file"); got != 2 {
		t.Errorf("completed file links = %d; want 2", got)
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

func TestWorkflowRunningStatusPluralization(t *testing.T) {
	for _, tt := range []struct {
		name  string
		files []string
		want  string
	}{
		{"one file", []string{"first.jpg"}, "0 of 1 file finished."},
		{"two files", []string{"first.jpg", "second.jpg"}, "0 of 2 files finished."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newServer(t, nil, nil)
			manifest := server.upload(t, tt.files)
			_, err := server.manager.Store().Update(manifest.ID, time.Now(), func(current *storage.Manifest) error {
				current.State = storage.JobRunning
				current.Files[0].State = storage.FileRunning
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			response := server.do(t, httptest.NewRequest(http.MethodGet, "/jobs/"+manifest.ID, nil))
			if !strings.Contains(response.Body.String(), tt.want) {
				t.Errorf("running status missing %q", tt.want)
			}
		})
	}
}
