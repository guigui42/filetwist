package web

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/jobs"
	"github.com/guigui42/filetwist/internal/jobs/storage"
)

// pageData is the root template context for every rendered page and fragment.
type pageData struct {
	Title          string
	Page           string
	Base           string
	RetentionLabel string
	MaxFiles       int
	MaxUploadLabel string
	MaxUploadBytes int64
	Operations     []optionView
	Job            *jobView
	Jobs           []jobSummary
	Diagnostics    *diagnosticsView
}

// noticeData renders a standalone message fragment.
type noticeData struct {
	Class   string
	Message string
}

// jobSummary is the compact recent-jobs list entry.
type jobSummary struct {
	ID           string
	Title        string
	ShortID      string
	StateLabel   string
	BadgeClass   string
	FileCount    int
	CreatedLabel string
}

// jobView is the full job fragment context.
type jobView struct {
	ID               string
	Heading          string
	StatusMessage    string
	CompletedCount   int
	FinishedCount    int
	ShortID          string
	State            string
	StateLabel       string
	BadgeClass       string
	FileCount        int
	TotalBytesLabel  string
	ExpiresLabel     string
	ErrorMessage     string
	Poll             bool
	CanStart         bool
	CanCancel        bool
	HasOutputs       bool
	ConvertibleCount int
	Files            []fileView
	BatchOptions     []optionView
}

// fileView is one uploaded file inside a job fragment.
type fileView struct {
	ID               string
	Name             string
	SizeLabel        string
	State            string
	StateLabel       string
	BadgeClass       string
	MediaSummary     string
	CanSelect        bool
	CanRemove        bool
	OperationHelp    string
	ProbeFormat      string
	Options          []optionView
	RecommendedLabel string
	SelectedLabel    string
	ValidationLabel  string
	ExecutionLabel   string
	Warnings         []string
	ErrorMessage     string
	DownloadURL      string
	OutputName       string
	OutputSizeLabel  string
}

// optionView is one selectable named operation.
type optionView struct {
	Value       string
	Label       string
	Format      string
	Description string
	Selected    bool
}

// diagnosticsView is the privacy-safe diagnostics payload.
type diagnosticsView struct {
	Tools        []toolStatus       `json:"tools"`
	Queue        jobs.Stats         `json:"queue"`
	Storage      storageStatus      `json:"storage"`
	Acceleration accelerationStatus `json:"acceleration"`
}

// toolStatus reports whether one converter executable is present.
type toolStatus struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
}

// storageStatus reports disk headroom without exposing any path.
type storageStatus struct {
	Jobs              int    `json:"jobs"`
	FreeSpaceBytes    int64  `json:"free_space_bytes"`
	FreeSpaceLabel    string `json:"free_space"`
	MinFreeSpaceBytes int64  `json:"min_free_space_bytes"`
	MinFreeSpaceLabel string `json:"min_free_space"`
	BelowFloor        bool   `json:"below_floor"`
}

// accelerationStatus reports the configured video acceleration policy.
type accelerationStatus struct {
	Mode          string `json:"mode"`
	DevicePresent bool   `json:"device_present"`
}

func (app *App) newPageData(title, page string) pageData {
	return pageData{
		Title:          title,
		Page:           page,
		Base:           app.base,
		RetentionLabel: humanDuration(app.retention),
		MaxFiles:       app.manager.MaxFilesPerJob(),
		MaxUploadLabel: humanBytes(app.manager.MaxUploadSize()),
		MaxUploadBytes: app.manager.MaxUploadSize(),
	}
}

func (app *App) buildJobView(manifest storage.Manifest, now time.Time) *jobView {
	view := &jobView{
		ID:              manifest.ID,
		ShortID:         shortID(manifest.ID),
		State:           string(manifest.State),
		StateLabel:      jobStateLabel(manifest.State),
		BadgeClass:      jobBadgeClass(manifest.State),
		FileCount:       len(manifest.Files),
		TotalBytesLabel: humanBytes(manifest.TotalBytes),
		ExpiresLabel:    humanUntil(manifest.ExpiresAt, now),
		Poll:            manifest.State.Active(),
		CanCancel:       manifest.State.Active(),
		Files:           make([]fileView, 0, len(manifest.Files)),
	}
	if manifest.Error != nil {
		view.ErrorMessage = manifest.Error.Message
	}

	for _, file := range manifest.Files {
		fileView := app.buildFileView(manifest.ID, file)
		fileView.CanRemove = manifest.State == storage.JobPending
		fileView.CanSelect = fileView.CanSelect && manifest.State == storage.JobPending
		view.Files = append(view.Files, fileView)
		if file.State == storage.FileInspected {
			view.ConvertibleCount++
		}
		if file.State == storage.FileCompleted && file.Output != nil {
			view.HasOutputs = true
			view.CompletedCount++
		}
		if file.State.Terminal() {
			view.FinishedCount++
		}
	}
	view.CanStart = manifest.State == storage.JobPending && view.ConvertibleCount > 0
	if view.CanStart {
		for _, operation := range conversion.AllOperations() {
			for _, file := range manifest.Files {
				if file.State == storage.FileInspected && slices.Contains(file.Compatible, operation) {
					view.BatchOptions = append(view.BatchOptions, operationOption(operation))
					break
				}
			}
		}
	}
	switch {
	case view.CanStart:
		view.Heading = "Review your files"
		view.StatusMessage = "Choose your outputs, then convert. You can remove files before starting."
	case view.Poll:
		view.Heading = "Converting your files"
		view.StatusMessage = fmt.Sprintf("%d of %d file%s finished. %s. You can return to this job while it runs.", view.FinishedCount, view.FileCount, plural(view.FileCount), view.StateLabel)
	case view.HasOutputs:
		view.Heading = "Your downloads are ready"
		if view.CompletedCount < view.FileCount {
			view.Heading = "Some downloads are ready"
		}
		view.StatusMessage = fmt.Sprintf("%d of %d file%s converted. Download the results you want to keep before they expire.", view.CompletedCount, view.FileCount, plural(view.FileCount))
	default:
		view.Heading = "This job needs your attention"
		view.StatusMessage = "No downloads are available. Review the messages below and upload your originals again to retry."
	}
	return view
}

func (app *App) buildFileView(jobID string, file storage.File) fileView {
	view := fileView{
		ID:         file.ID,
		Name:       file.OriginalName,
		SizeLabel:  humanBytes(file.Size),
		State:      string(file.State),
		StateLabel: fileStateLabel(file.State),
		BadgeClass: fileBadgeClass(file.State),
		CanSelect:  file.State == storage.FileInspected,
	}
	if view.Name == "" {
		view.Name = file.Name
	}
	if file.Media != nil {
		view.MediaSummary = mediaSummary(*file.Media)
		view.ProbeFormat = file.Media.Format
	}
	if file.Recommended != "" {
		view.RecommendedLabel = conversion.OperationLabel(file.Recommended)
	}
	if file.Selected != "" {
		view.SelectedLabel = conversion.OperationLabel(file.Selected)
	}
	for _, operation := range file.Compatible {
		option := operationOption(operation)
		option.Selected = operation == selectedOrRecommended(file)
		view.Options = append(view.Options, option)
	}
	view.OperationHelp = operationOption(selectedOrRecommended(file)).Description
	if file.Validation.Status != "" && file.Validation.Status != "not_run" {
		view.ValidationLabel = file.Validation.Status
		for _, issue := range file.Validation.Issues {
			view.Warnings = append(view.Warnings, fmt.Sprintf(
				"validation %s: expected %s, observed %s",
				issue.Code,
				issue.Expected,
				issue.Actual,
			))
		}
	}
	if file.Execution.Final != "" {
		if file.Execution.Initial != file.Execution.Final || file.FallbackReason != "" {
			view.ExecutionLabel = fmt.Sprintf(
				"%s to %s (%s)",
				file.Execution.Initial,
				file.Execution.Final,
				file.FallbackReason,
			)
		} else {
			view.ExecutionLabel = file.Execution.Final
		}
	}
	for _, warning := range file.Warnings {
		view.Warnings = append(view.Warnings, warning.Message)
	}
	if file.Error != nil {
		view.ErrorMessage = file.Error.Message
	}
	if file.State == storage.FileCompleted && file.Output != nil {
		view.DownloadURL = joinURL(app.base, fmt.Sprintf("/jobs/%s/files/%s", jobID, file.ID))
		view.OutputName = file.Output.Name
		view.OutputSizeLabel = humanBytes(file.Output.Size)
	}
	return view
}

func selectedOrRecommended(file storage.File) corpus.Operation {
	if file.Selected != "" {
		return file.Selected
	}
	return file.Recommended
}

func mediaSummary(media conversion.DetectedMedia) string {
	parts := []string{strings.TrimSpace(friendlyFormat(media.Format) + " " + string(media.Kind))}
	if media.Width > 0 && media.Height > 0 {
		parts = append(parts, fmt.Sprintf("%dx%d", media.Width, media.Height))
	}
	if media.DurationMillis > 0 {
		parts = append(parts, humanDuration(time.Duration(media.DurationMillis)*time.Millisecond))
	}
	return strings.Join(parts, ", ")
}

func friendlyFormat(format string) string {
	switch strings.ToLower(strings.Split(format, ",")[0]) {
	case "jpeg", "mjpeg":
		return "JPEG"
	case "png":
		return "PNG"
	case "webp":
		return "WebP"
	case "heif", "heic":
		return "HEIF / HEIC"
	case "avif":
		return "AVIF"
	case "mov", "mp4", "m4a":
		return "QuickTime / MP4"
	case "matroska", "webm":
		return "Matroska / WebM"
	default:
		return strings.ToUpper(strings.Split(format, ",")[0])
	}
}

func jobTitle(manifest storage.Manifest) string {
	if len(manifest.Files) == 0 {
		return "Empty job"
	}
	title := manifest.Files[0].OriginalName
	if title == "" {
		title = manifest.Files[0].Name
	}
	if len(manifest.Files) > 1 {
		title += fmt.Sprintf(" + %d more", len(manifest.Files)-1)
	}
	return title
}

func jobStateLabel(state storage.JobState) string {
	switch state {
	case storage.JobPending:
		return "Ready to convert"
	case storage.JobQueued:
		return "Queued"
	case storage.JobRunning:
		return "Converting"
	case storage.JobCompleted:
		return "Completed"
	case storage.JobFailed:
		return "Failed"
	case storage.JobCanceled:
		return "Canceled"
	case storage.JobInterrupted:
		return "Interrupted"
	default:
		return string(state)
	}
}

func jobBadgeClass(state storage.JobState) string {
	switch state {
	case storage.JobCompleted:
		return "badge--ok"
	case storage.JobFailed, storage.JobInterrupted:
		return "badge--error"
	case storage.JobQueued, storage.JobRunning:
		return "badge--busy"
	default:
		return ""
	}
}

func fileStateLabel(state storage.FileState) string {
	switch state {
	case storage.FileUploaded:
		return "Uploaded"
	case storage.FileInspected:
		return "Ready"
	case storage.FileQueued:
		return "Queued"
	case storage.FileRunning:
		return "Converting"
	case storage.FileCompleted:
		return "Converted"
	case storage.FileFailed:
		return "Failed"
	case storage.FileCanceled:
		return "Canceled"
	case storage.FileInterrupted:
		return "Interrupted"
	default:
		return string(state)
	}
}

func fileBadgeClass(state storage.FileState) string {
	switch state {
	case storage.FileCompleted:
		return "badge--ok"
	case storage.FileFailed, storage.FileInterrupted:
		return "badge--error"
	case storage.FileQueued, storage.FileRunning:
		return "badge--busy"
	default:
		return ""
	}
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func humanBytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	size := float64(value)
	index := -1
	for size >= 1024 && index < len(units)-1 {
		size /= 1024
		index++
	}
	return fmt.Sprintf("%.1f %s", size, units[index])
}

func humanDuration(value time.Duration) string {
	switch {
	case value >= 24*time.Hour:
		days := int(value.Hours() / 24)
		return fmt.Sprintf("%d day%s", days, plural(days))
	case value >= time.Hour:
		hours := int(value.Hours())
		return fmt.Sprintf("%d hour%s", hours, plural(hours))
	case value >= time.Minute:
		minutes := int(value.Minutes())
		return fmt.Sprintf("%d minute%s", minutes, plural(minutes))
	default:
		return fmt.Sprintf("%.1f seconds", value.Seconds())
	}
}

func humanUntil(instant, now time.Time) string {
	if instant.IsZero() {
		return "unknown"
	}
	remaining := instant.Sub(now)
	if remaining <= 0 {
		return "expired"
	}
	return "in " + humanDuration(remaining)
}

func humanSince(instant, now time.Time) string {
	if instant.IsZero() {
		return "unknown"
	}
	elapsed := now.Sub(instant)
	if elapsed < time.Minute {
		return "just now"
	}
	return humanDuration(elapsed) + " ago"
}

func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
