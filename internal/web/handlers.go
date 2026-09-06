package web

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"strings"

	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/jobs"
	"github.com/guigui42/filetwist/internal/jobs/storage"
)

// maxSelectionFormBytes bounds the urlencoded body of a start request.
const maxSelectionFormBytes = 64 << 10

// uploadFramingAllowance is the multipart framing budget granted on top of the
// aggregate file byte ceiling. It covers part boundaries, part headers, and the
// handful of non-file form fields a client may send, so the total bytes one
// upload request may read stay bounded even when the body is chunked and
// declares no length.
const uploadFramingAllowance = 16 << 20

func (app *App) handleIndex(writer http.ResponseWriter, request *http.Request) {
	data := app.newPageData("Filetwist", "index")
	manifests, err := app.manager.List()
	if err != nil {
		app.logger.Warn("job listing failed", slog.String("error", err.Error()))
	}
	now := app.now()
	for index, manifest := range manifests {
		if index >= 10 {
			break
		}
		data.Jobs = append(data.Jobs, jobSummary{
			ID:           manifest.ID,
			ShortID:      shortID(manifest.ID),
			StateLabel:   jobStateLabel(manifest.State),
			BadgeClass:   jobBadgeClass(manifest.State),
			FileCount:    len(manifest.Files),
			CreatedLabel: humanSince(manifest.CreatedAt, now),
		})
	}
	app.renderPage(writer, http.StatusOK, data)
	_ = request
}

func (app *App) handleUpload(writer http.ResponseWriter, request *http.Request) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		app.renderNotice(writer, http.StatusUnsupportedMediaType, "error",
			"The upload must be sent as a multipart form.")
		return
	}
	bodyLimit := app.manager.MaxUploadSize() + uploadFramingAllowance
	if request.ContentLength > 0 && request.ContentLength > bodyLimit {
		app.renderNotice(writer, http.StatusRequestEntityTooLarge, "error",
			fmt.Sprintf("The upload exceeds the %s limit.", humanBytes(app.manager.MaxUploadSize())))
		return
	}
	// The unwrapped writer is passed on purpose: net/http recognizes only its
	// own response value when MaxBytesReader reports an oversized body.
	request.Body = http.MaxBytesReader(baseWriter(writer), request.Body, bodyLimit)

	reader, err := request.MultipartReader()
	if err != nil {
		app.renderNotice(writer, http.StatusBadRequest, "error",
			"The upload could not be read as a multipart stream.")
		return
	}

	manifest, err := app.manager.Accept(request.Context(), reader)
	if err != nil {
		status, message := uploadFailure(err, app.manager.MaxUploadSize(), app.manager.MaxFilesPerJob())
		if requestStalled(request) {
			// The transport cut the body, so whatever the multipart parser
			// made of the truncated stream is not the real reason.
			status = http.StatusRequestTimeout
			message = "The upload stopped making progress and was canceled."
		}
		app.renderNotice(writer, status, "error", message)
		return
	}
	if request.Header.Get("HX-Request") == "" {
		http.Redirect(
			writer,
			request,
			joinURL(app.base, fmt.Sprintf("/jobs/%s", manifest.ID)),
			http.StatusSeeOther,
		)
		return
	}

	data := app.newPageData("Filetwist", "index")
	data.Job = app.buildJobView(manifest, app.now())
	app.renderFragment(writer, http.StatusOK, "job", data)
}

func uploadFailure(err error, maxSize int64, maxFiles int) (int, string) {
	switch {
	case errors.Is(err, jobs.ErrNoFiles):
		return http.StatusBadRequest, "Select at least one file before uploading."
	case errors.Is(err, jobs.ErrTooManyFiles):
		return http.StatusRequestEntityTooLarge, fmt.Sprintf(
			"Upload at most %d files in one job.", maxFiles)
	case errors.Is(err, jobs.ErrUploadTooLarge):
		return http.StatusRequestEntityTooLarge, fmt.Sprintf(
			"The upload exceeds the %s limit.", humanBytes(maxSize))
	case errors.Is(err, jobs.ErrInsufficientSpace):
		return http.StatusInsufficientStorage, "The server does not have enough free disk space."
	case errors.Is(err, jobs.ErrShuttingDown):
		return http.StatusServiceUnavailable, "The service is shutting down and is not accepting uploads."
	case errors.Is(err, jobs.ErrInvalidUpload):
		return http.StatusBadRequest, "The upload was malformed and was discarded."
	case errors.Is(err, jobs.ErrUploadStalled):
		return http.StatusRequestTimeout,
			"The upload stopped making progress and was canceled."
	case errors.Is(err, context.Canceled):
		// The client is gone, so the status only reaches the access log.
		return http.StatusRequestTimeout, "The upload was canceled."
	default:
		return http.StatusInternalServerError, "The upload could not be stored."
	}
}

func (app *App) handleJobPage(writer http.ResponseWriter, request *http.Request) {
	data := app.newPageData("Filetwist job", "job")
	manifest, err := app.loadJob(request.PathValue("id"))
	if err != nil {
		app.renderPage(writer, http.StatusNotFound, data)
		return
	}
	data.Job = app.buildJobView(manifest, app.now())
	app.renderPage(writer, http.StatusOK, data)
}

func (app *App) handleJobStatus(writer http.ResponseWriter, request *http.Request) {
	manifest, err := app.loadJob(request.PathValue("id"))
	if err != nil {
		app.renderNotice(writer, http.StatusNotFound, "hint", "This job is no longer available.")
		return
	}
	data := app.newPageData("Filetwist", "index")
	data.Job = app.buildJobView(manifest, app.now())
	app.renderFragment(writer, http.StatusOK, "job", data)
}

func (app *App) handleStart(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if err := storage.ValidateID(id); err != nil {
		app.renderNotice(writer, http.StatusNotFound, "error", "This job is no longer available.")
		return
	}
	selections, err := parseSelections(request)
	if err != nil {
		app.renderNotice(writer, http.StatusBadRequest, "error",
			"The selected operations could not be read.")
		return
	}

	manifest, err := app.manager.Start(id, selections)
	if err != nil {
		switch {
		case errors.Is(err, jobs.ErrNotFound):
			app.renderNotice(writer, http.StatusNotFound, "error", "This job is no longer available.")
		case errors.Is(err, jobs.ErrIncompatibleOperation):
			app.renderNotice(writer, http.StatusBadRequest, "error",
				"The chosen operation is not compatible with the detected media.")
		case errors.Is(err, jobs.ErrNoConvertibleFiles):
			app.renderNotice(writer, http.StatusBadRequest, "error",
				"None of the uploaded files supports a conversion operation.")
		case errors.Is(err, jobs.ErrNotStartable):
			app.renderNotice(writer, http.StatusConflict, "error", "This job was already started.")
		case errors.Is(err, jobs.ErrQueueFull):
			app.renderNotice(writer, http.StatusServiceUnavailable, "error",
				"The conversion queue is full. Try again shortly.")
		case errors.Is(err, jobs.ErrShuttingDown):
			app.renderNotice(writer, http.StatusServiceUnavailable, "error",
				"The service is shutting down and is not starting new work.")
		default:
			app.renderNotice(writer, http.StatusInternalServerError, "error",
				"The job could not be started.")
		}
		return
	}
	data := app.newPageData("Filetwist", "index")
	data.Job = app.buildJobView(manifest, app.now())
	app.renderFragment(writer, http.StatusOK, "job", data)
}

func (app *App) handleCancel(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if err := storage.ValidateID(id); err != nil {
		app.renderNotice(writer, http.StatusNotFound, "error", "This job is no longer available.")
		return
	}
	manifest, err := app.manager.Cancel(id)
	if err != nil {
		if errors.Is(err, jobs.ErrNotFound) {
			app.renderNotice(writer, http.StatusNotFound, "error", "This job is no longer available.")
			return
		}
		app.renderNotice(writer, http.StatusInternalServerError, "error",
			"The job could not be canceled.")
		return
	}
	data := app.newPageData("Filetwist", "index")
	data.Job = app.buildJobView(manifest, app.now())
	app.renderFragment(writer, http.StatusOK, "job", data)
}

func (app *App) handleDelete(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if err := storage.ValidateID(id); err != nil {
		app.renderNotice(writer, http.StatusNotFound, "hint", "This job is no longer available.")
		return
	}
	switch err := app.manager.Delete(id); {
	case err == nil:
		app.renderNotice(writer, http.StatusOK, "hint", "The job and all of its files were deleted.")
	case errors.Is(err, jobs.ErrNotFound):
		app.renderNotice(writer, http.StatusNotFound, "hint", "This job is no longer available.")
	case errors.Is(err, jobs.ErrLeased):
		app.renderNotice(writer, http.StatusConflict, "error",
			"The job is still in use. Try again in a moment.")
	default:
		app.renderNotice(writer, http.StatusInternalServerError, "error",
			"The job could not be deleted.")
	}
}

func (app *App) handleFileDownload(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	fileID := request.PathValue("fileID")
	if storage.ValidateID(id) != nil || storage.ValidateFileID(fileID) != nil {
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}
	release, err := app.manager.Lease(id)
	if err != nil {
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}
	defer release()

	manifest, err := app.manager.Get(id)
	if err != nil {
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}
	file := manifest.FileByID(fileID)
	if file == nil || file.State != storage.FileCompleted || file.Output == nil {
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}
	path, err := app.manager.OutputPath(id, *file)
	if err != nil {
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}
	handle, err := os.Open(path)
	if err != nil {
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}
	defer func() {
		if closeErr := handle.Close(); closeErr != nil {
			app.logger.Warn("download handle could not be closed")
		}
	}()
	info, err := handle.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}

	attachmentHeaders(writer, file.Output.MIMEType, file.Output.Name)
	writer.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	if _, err := io.Copy(writer, handle); err != nil {
		app.logger.Warn("download stream ended early", slog.String("job", id))
	}
	_ = request
}

func (app *App) handleArchiveDownload(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if storage.ValidateID(id) != nil {
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}
	release, err := app.manager.Lease(id)
	if err != nil {
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}
	defer release()

	manifest, err := app.manager.Get(id)
	if err != nil {
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}
	outputs := manifest.CompletedOutputs()
	if len(outputs) == 0 {
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}
	// Every entry is checked before a single response byte is written. Once the
	// archive is streaming the status is already committed, so a late failure
	// could only be reported as a valid but silently incomplete download.
	paths := make([]string, 0, len(outputs))
	for _, file := range outputs {
		path, err := app.manager.OutputPath(id, file)
		if err != nil {
			http.Error(writer, "not found", http.StatusNotFound)
			return
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			http.Error(writer, "not found", http.StatusNotFound)
			return
		}
		paths = append(paths, path)
	}

	attachmentHeaders(writer, "application/zip", fmt.Sprintf("filetwist-%s.zip", shortID(id)))
	archive := zip.NewWriter(writer)
	taken := make(map[string]bool)
	for index, file := range outputs {
		if err := app.appendArchiveEntry(archive, paths[index], file, taken); err != nil {
			// The central directory is deliberately not written, so every
			// extractor rejects the truncated archive instead of presenting it
			// as a complete set of results.
			app.logger.Warn("archive entry failed", slog.String("job", id))
			return
		}
	}
	if err := archive.Close(); err != nil {
		app.logger.Warn("archive could not be finalized", slog.String("job", id))
	}
	_ = request
}

// appendArchiveEntry streams one output into the archive. Entries use
// zip.Store so nothing is compressed twice and nothing is materialized on disk.
func (app *App) appendArchiveEntry(
	archive *zip.Writer,
	path string,
	file storage.File,
	taken map[string]bool,
) error {
	handle, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		_ = handle.Close()
	}()
	info, err := handle.Stat()
	if err != nil {
		return err
	}

	header := &zip.FileHeader{
		Name:   storage.UniqueFileName(taken, archiveEntryName(file.Output.Name, file.ID)),
		Method: zip.Store,
	}
	header.Modified = info.ModTime()
	header.SetMode(0o640)
	entry, err := archive.CreateHeader(header)
	if err != nil {
		return err
	}
	if _, err := io.Copy(entry, handle); err != nil {
		return err
	}
	return nil
}

// archiveEntryName reduces a stored output name to a path-free archive entry
// name. Extractors treat both "/" and "\" as separators and some resolve drive
// prefixes, so none of them may reach an entry name regardless of what a
// manifest on disk claims.
func archiveEntryName(name, fallback string) string {
	cleaned := strings.Map(func(character rune) rune {
		switch {
		case character < 0x20 || character == 0x7f:
			return -1
		case character == '/' || character == '\\' || character == ':':
			return '_'
		default:
			return character
		}
	}, name)
	cleaned = strings.Trim(cleaned, ". ")
	if cleaned == "" {
		return fallback
	}
	return cleaned
}

func (app *App) handleHealth(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Cache-Control", "no-store")
	status := http.StatusOK
	body := "ok"
	if !app.manager.Stats().Accepting {
		status = http.StatusServiceUnavailable
		body = "unavailable"
	} else if free, err := storage.FreeSpace(app.manager.Store().Root()); err != nil || free < app.minFree {
		status = http.StatusServiceUnavailable
		body = "unavailable"
	}
	if request.Method == http.MethodHead {
		writer.WriteHeader(status)
		return
	}
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(map[string]string{"status": body}); err != nil {
		app.logger.Warn("health response could not be written")
	}
}

func (app *App) handleDiagnostics(writer http.ResponseWriter, request *http.Request) {
	if !app.diagnosticsAllowed(request) {
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}
	view := app.collectDiagnostics()
	if strings.Contains(request.Header.Get("Accept"), "application/json") {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(writer).Encode(view); err != nil {
			app.logger.Warn("diagnostics response could not be written")
		}
		return
	}
	data := app.newPageData("Filetwist diagnostics", "diagnostics")
	data.Diagnostics = &view
	app.renderPage(writer, http.StatusOK, data)
}

// collectDiagnostics reports tool availability, queue state, and disk
// headroom. It never reports filesystem paths, environment values, or command
// output.
func (app *App) collectDiagnostics() diagnosticsView {
	view := diagnosticsView{
		Queue: app.manager.Stats(),
		Acceleration: accelerationStatus{
			Mode:          app.accelMode,
			DevicePresent: app.renderDevicePresent(),
		},
	}
	for _, tool := range []string{"vips", "vipsheader", "ffmpeg", "ffprobe"} {
		view.Tools = append(view.Tools, toolStatus{Name: tool, Available: app.lookupTool(tool)})
	}
	manifests, err := app.manager.List()
	if err == nil {
		view.Storage.Jobs = len(manifests)
	}
	free, err := storage.FreeSpace(app.manager.Store().Root())
	if err == nil {
		view.Storage.FreeSpaceBytes = free
		view.Storage.FreeSpaceLabel = humanBytes(free)
		view.Storage.BelowFloor = free < app.minFree
	}
	view.Storage.MinFreeSpaceBytes = app.minFree
	view.Storage.MinFreeSpaceLabel = humanBytes(app.minFree)
	return view
}

func (app *App) renderDevicePresent() bool {
	if app.accelDevice == "" {
		return false
	}
	info, err := os.Stat(app.accelDevice)
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func (app *App) loadJob(id string) (storage.Manifest, error) {
	if err := storage.ValidateID(id); err != nil {
		return storage.Manifest{}, err
	}
	return app.manager.Get(id)
}

// parseSelections reads the per-file operation choices from a bounded
// urlencoded body. Keys use the "operation.<fileID>" form.
func parseSelections(request *http.Request) (map[string]corpus.Operation, error) {
	request.Body = http.MaxBytesReader(nil, request.Body, maxSelectionFormBytes)
	if err := request.ParseForm(); err != nil {
		return nil, err
	}
	selections := make(map[string]corpus.Operation)
	for key, values := range request.PostForm {
		fileID, ok := strings.CutPrefix(key, "operation.")
		if !ok || len(values) == 0 || values[0] == "" {
			continue
		}
		if storage.ValidateFileID(fileID) != nil {
			continue
		}
		operation, err := conversion.ParseOperation(values[0])
		if err != nil {
			return nil, err
		}
		selections[fileID] = operation
	}
	return selections, nil
}

// contentDisposition builds a safe attachment header for a stored file name.
// The ASCII fallback is quoted and stripped of characters that could terminate
// the header, and the UTF-8 form carries the original name.
func contentDisposition(name string) string {
	ascii := strings.Map(func(character rune) rune {
		if character < 0x20 || character > 0x7e {
			return '_'
		}
		switch character {
		case '"', '\\', ';', ',':
			return '_'
		default:
			return character
		}
	}, name)
	if ascii == "" {
		ascii = "download"
	}
	return fmt.Sprintf(
		"attachment; filename=%q; filename*=UTF-8''%s",
		ascii,
		urlEncodePath(name),
	)
}

func urlEncodePath(value string) string {
	var builder strings.Builder
	for _, octet := range []byte(value) {
		switch {
		case octet >= 'a' && octet <= 'z',
			octet >= 'A' && octet <= 'Z',
			octet >= '0' && octet <= '9',
			octet == '-', octet == '.', octet == '_', octet == '~':
			builder.WriteByte(octet)
		default:
			_, _ = fmt.Fprintf(&builder, "%%%02X", octet)
		}
	}
	return builder.String()
}
