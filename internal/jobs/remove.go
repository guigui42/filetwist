package jobs

import (
	"errors"
	"fmt"
	"os"

	"github.com/guigui42/filetwist/internal/jobs/storage"
)

// RemoveFile removes one uploaded input from an unleased pending job. Invalid
// identifiers return storage.ErrInvalidID, unknown jobs or files return
// ErrNotFound, non-pending jobs return ErrNotStartable, and leases return
// ErrLeased. Removing the last file deletes the job and returns a zero Manifest
// with a nil error.
func (manager *Manager) RemoveFile(id, fileID string) (storage.Manifest, error) {
	if err := storage.ValidateID(id); err != nil {
		return storage.Manifest{}, err
	}
	if err := storage.ValidateFileID(fileID); err != nil {
		return storage.Manifest{}, err
	}
	unlock := manager.lockJob(id)
	defer unlock()

	manifest, err := manager.store.Load(id)
	if err != nil {
		return storage.Manifest{}, err
	}
	if manifest.State != storage.JobPending {
		return storage.Manifest{}, ErrNotStartable
	}
	if manager.Leased(id) {
		return storage.Manifest{}, ErrLeased
	}
	index := -1
	for candidate := range manifest.Files {
		if manifest.Files[candidate].ID == fileID {
			index = candidate
			break
		}
	}
	if index < 0 {
		return storage.Manifest{}, fmt.Errorf("jobs: uploaded file not found: %w", ErrNotFound)
	}
	file := manifest.Files[index]
	inputDir, err := manager.store.InputDir(id)
	if err != nil {
		return storage.Manifest{}, err
	}
	inputPath, err := storage.Resolve(inputDir, file.Name)
	if err != nil {
		return storage.Manifest{}, err
	}

	updated := manifest
	updated.Files = make([]storage.File, 0, len(manifest.Files)-1)
	updated.Files = append(updated.Files, manifest.Files[:index]...)
	updated.Files = append(updated.Files, manifest.Files[index+1:]...)
	updated.TotalBytes -= file.Size
	updated.UpdatedAt = manager.now().UTC()

	rollback := func(cause error) (storage.Manifest, error) {
		if err := manager.store.Save(manifest); err != nil {
			cause = errors.Join(cause, fmt.Errorf("jobs: restore manifest after failed file removal: %w", err))
		}
		return storage.Manifest{}, cause
	}
	// Persist the exclusion before unlinking the input, so a failed save or a
	// crash cannot leave a newly published manifest referencing removed bytes.
	if err := manager.store.Save(updated); err != nil {
		// Save can fail during directory sync after publishing the manifest.
		return rollback(err)
	}
	if err := os.Remove(inputPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			err = errors.Join(ErrNotFound, err)
		}
		return rollback(fmt.Errorf("jobs: remove uploaded file: %w", err))
	}
	if len(updated.Files) == 0 {
		// Do not restore the original manifest if directory deletion fails:
		// its input is already gone. Report the failure for whole-job cleanup.
		if err := manager.store.Delete(id); err != nil {
			return storage.Manifest{}, err
		}
		return storage.Manifest{}, nil
	}
	return updated, nil
}
