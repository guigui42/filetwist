// Package storage persists filetwist conversion jobs on the local
// filesystem. Every job owns one isolated directory that holds the uploaded
// inputs, the produced outputs, and an atomically rewritten JSON manifest.
package storage

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/profiles"
)

// ManifestSchemaVersion is the stable on-disk manifest schema version.
const ManifestSchemaVersion = "1.0"

// IDBytes is the number of cryptographically random bytes in a job identifier.
const IDBytes = 16

// JobState is the lifecycle state of a whole job.
type JobState string

const (
	// JobPending means uploads completed and the job awaits an explicit start.
	JobPending JobState = "pending"
	// JobQueued means the job was started and waits for a worker slot.
	JobQueued JobState = "queued"
	// JobRunning means at least one file is converting.
	JobRunning JobState = "running"
	// JobCompleted means every file reached a terminal state and at least one
	// output was produced.
	JobCompleted JobState = "completed"
	// JobFailed means every file failed.
	JobFailed JobState = "failed"
	// JobCanceled means the caller canceled the job.
	JobCanceled JobState = "canceled"
	// JobInterrupted means the process restarted while the job was not
	// terminal. Interrupted jobs are never resumed.
	JobInterrupted JobState = "interrupted"
)

// Terminal reports whether no further work will change the job state.
func (state JobState) Terminal() bool {
	switch state {
	case JobCompleted, JobFailed, JobCanceled, JobInterrupted:
		return true
	default:
		return false
	}
}

// Active reports whether the job is started but not finished.
func (state JobState) Active() bool {
	return state == JobQueued || state == JobRunning
}

// FileState is the lifecycle state of one uploaded file.
type FileState string

const (
	// FileUploaded means the bytes are stored but the file was not probed.
	FileUploaded FileState = "uploaded"
	// FileInspected means probing succeeded and an operation was recommended.
	FileInspected FileState = "inspected"
	// FileQueued means the file waits for a converter slot.
	FileQueued FileState = "queued"
	// FileRunning means a converter process is active for this file.
	FileRunning FileState = "running"
	// FileCompleted means an output was produced and validated.
	FileCompleted FileState = "completed"
	// FileFailed means probing or conversion failed.
	FileFailed FileState = "failed"
	// FileCanceled means the file was canceled before completion.
	FileCanceled FileState = "canceled"
	// FileInterrupted means the process restarted mid-conversion.
	FileInterrupted FileState = "interrupted"
)

// Terminal reports whether no further work will change the file state.
func (state FileState) Terminal() bool {
	switch state {
	case FileCompleted, FileFailed, FileCanceled, FileInterrupted:
		return true
	default:
		return false
	}
}

// Failure is a privacy-safe error record persisted in a manifest. It never
// carries filesystem paths or converter command output.
type Failure struct {
	// Kind is the conversion failure classification.
	Kind string `json:"kind"`
	// Code is the stable machine-readable failure code.
	Code string `json:"code"`
	// Message is a short human-readable explanation.
	Message string `json:"message"`
}

// Output describes one produced artifact inside the job directory.
type Output struct {
	// Name is the stored output file name relative to the job output
	// directory.
	Name string `json:"name"`
	// Size is the output size in bytes.
	Size int64 `json:"size"`
	// MIMEType is the served content type.
	MIMEType string `json:"mime_type"`
}

// File is one uploaded input and everything known about it.
type File struct {
	// ID is the stable per-job file identifier used in URLs.
	ID string `json:"id"`
	// Name is the sanitized stored input file name.
	Name string `json:"name"`
	// OriginalName is the sanitized display name shown to the user.
	OriginalName string `json:"original_name"`
	// Size is the uploaded size in bytes.
	Size int64 `json:"size"`
	// State is the file lifecycle state.
	State FileState `json:"state"`
	// Media is the privacy-safe probe summary, when probing succeeded.
	Media *conversion.DetectedMedia `json:"media,omitempty"`
	// Recommended is the single suggested operation.
	Recommended profiles.Operation `json:"recommended,omitempty"`
	// Compatible lists every operation the user may select.
	Compatible []profiles.Operation `json:"compatible,omitempty"`
	// Selected is the operation chosen for this file.
	Selected profiles.Operation `json:"selected,omitempty"`
	// OperationSource records whether the selection was recommended or
	// requested.
	OperationSource string `json:"operation_source,omitempty"`
	// Validation reports whether output validation ran and passed.
	Validation conversion.ValidationResult `json:"validation"`
	// Warnings lists non-fatal policy decisions.
	Warnings []conversion.Warning `json:"warnings,omitempty"`
	// Execution reports the requested, initial, and final engine paths.
	Execution conversion.ExecutionResult `json:"execution"`
	// FallbackReason repeats the execution fallback reason for display.
	FallbackReason string `json:"fallback_reason,omitempty"`
	// Error is the terminal failure, when the file failed.
	Error *Failure `json:"error,omitempty"`
	// Output is the produced artifact, when the file completed.
	Output *Output `json:"output,omitempty"`
	// StartedAt is when conversion began.
	StartedAt *time.Time `json:"started_at,omitempty"`
	// FinishedAt is when the file reached a terminal state.
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Manifest is the complete persisted state of one job.
type Manifest struct {
	// SchemaVersion is the manifest schema version.
	SchemaVersion string `json:"schema_version"`
	// ID is the 128-bit base64url job identifier.
	ID string `json:"id"`
	// State is the job lifecycle state.
	State JobState `json:"state"`
	// CreatedAt is when the job directory was created.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when the manifest was last written.
	UpdatedAt time.Time `json:"updated_at"`
	// ExpiresAt is when cleanup may remove the job.
	ExpiresAt time.Time `json:"expires_at"`
	// TotalBytes is the aggregate uploaded size.
	TotalBytes int64 `json:"total_bytes"`
	// Files are the uploaded inputs in upload order.
	Files []File `json:"files"`
	// Error is a job-level failure, when one applies.
	Error *Failure `json:"error,omitempty"`
}

// FileByID returns a pointer to the file with the supplied identifier.
func (manifest *Manifest) FileByID(id string) *File {
	for index := range manifest.Files {
		if manifest.Files[index].ID == id {
			return &manifest.Files[index]
		}
	}
	return nil
}

// CompletedOutputs returns every file that produced a downloadable output.
func (manifest *Manifest) CompletedOutputs() []File {
	outputs := make([]File, 0, len(manifest.Files))
	for _, file := range manifest.Files {
		if file.State == FileCompleted && file.Output != nil {
			outputs = append(outputs, file)
		}
	}
	return outputs
}

// Expired reports whether the retention window closed at the supplied time.
func (manifest *Manifest) Expired(now time.Time) bool {
	return !manifest.ExpiresAt.IsZero() && now.After(manifest.ExpiresAt)
}

// NewID returns a 128-bit cryptographically random base64url job identifier
// with no padding.
func NewID() (string, error) {
	buffer := make([]byte, IDBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("storage: generate job id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

// ErrInvalidID reports a job identifier that was not produced by NewID.
var ErrInvalidID = errors.New("storage: invalid job id")

// ValidateID rejects any identifier that is not an unpadded base64url encoding
// of exactly IDBytes bytes. It is the only gate between a URL path segment and
// the filesystem.
func ValidateID(id string) error {
	if len(id) != base64.RawURLEncoding.EncodedLen(IDBytes) {
		return ErrInvalidID
	}
	for _, character := range id {
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9',
			character == '-', character == '_':
		default:
			return ErrInvalidID
		}
	}
	decoded, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil || len(decoded) != IDBytes {
		return ErrInvalidID
	}
	return nil
}

// ValidateFileID rejects any per-job file identifier that is not a short
// lowercase alphanumeric token.
func ValidateFileID(id string) error {
	if id == "" || len(id) > 24 {
		return ErrInvalidID
	}
	for _, character := range id {
		switch {
		case character >= 'a' && character <= 'z',
			character >= '0' && character <= '9',
			character == '-':
		default:
			return ErrInvalidID
		}
	}
	if strings.Contains(id, "..") {
		return ErrInvalidID
	}
	return nil
}
