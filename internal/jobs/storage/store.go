package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	jobsDirName     = "jobs"
	inputDirName    = "input"
	outputDirName   = "output"
	tempDirName     = "tmp"
	manifestName    = "manifest.json"
	dirPermissions  = 0o750
	filePermissions = 0o640
)

// ErrNotFound reports a job directory or manifest that does not exist.
var ErrNotFound = errors.New("storage: job not found")

// Store owns the on-disk job tree rooted at a data directory.
type Store struct {
	root string
}

// NewStore prepares the job tree under root and returns a Store. root is
// created when missing so tests and local runs can use any writable directory.
func NewStore(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("storage: data directory must not be empty")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("storage: resolve data directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(absolute, jobsDirName), dirPermissions); err != nil {
		return nil, fmt.Errorf("storage: create job directory: %w", err)
	}
	return &Store{root: absolute}, nil
}

// Root returns the absolute data directory.
func (store *Store) Root() string {
	return store.root
}

// JobDir returns the absolute directory for a validated job identifier.
func (store *Store) JobDir(id string) (string, error) {
	if err := ValidateID(id); err != nil {
		return "", err
	}
	return filepath.Join(store.root, jobsDirName, id), nil
}

// InputDir returns the absolute uploaded-input directory for a job.
func (store *Store) InputDir(id string) (string, error) {
	dir, err := store.JobDir(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, inputDirName), nil
}

// OutputDir returns the absolute produced-output directory for a job.
func (store *Store) OutputDir(id string) (string, error) {
	dir, err := store.JobDir(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, outputDirName), nil
}

// TempDir returns the absolute in-progress upload directory for a job.
func (store *Store) TempDir(id string) (string, error) {
	dir, err := store.JobDir(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, tempDirName), nil
}

// Create allocates a new isolated job directory and writes its initial
// manifest. The returned manifest carries the generated identifier.
func (store *Store) Create(now time.Time, ttl time.Duration) (Manifest, error) {
	id, err := NewID()
	if err != nil {
		return Manifest{}, err
	}
	jobDir := filepath.Join(store.root, jobsDirName, id)
	if err := os.Mkdir(jobDir, dirPermissions); err != nil {
		return Manifest{}, fmt.Errorf("storage: create job directory: %w", err)
	}
	created := false
	defer func() {
		if !created {
			_ = os.RemoveAll(jobDir)
		}
	}()
	for _, name := range []string{inputDirName, outputDirName, tempDirName} {
		if err := os.Mkdir(filepath.Join(jobDir, name), dirPermissions); err != nil {
			return Manifest{}, fmt.Errorf("storage: create job subdirectory: %w", err)
		}
	}

	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion,
		ID:            id,
		State:         JobPending,
		CreatedAt:     now.UTC(),
		UpdatedAt:     now.UTC(),
		ExpiresAt:     now.UTC().Add(ttl),
		Files:         []File{},
	}
	if err := store.Save(manifest); err != nil {
		return Manifest{}, err
	}
	created = true
	return manifest, nil
}

// Load reads one manifest. It returns ErrNotFound when the job is absent.
func (store *Store) Load(id string) (Manifest, error) {
	dir, err := store.JobDir(id)
	if err != nil {
		return Manifest{}, err
	}
	payload, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Manifest{}, ErrNotFound
		}
		return Manifest{}, fmt.Errorf("storage: read manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("storage: decode manifest: %w", err)
	}
	if manifest.ID != id {
		return Manifest{}, fmt.Errorf("storage: manifest id mismatch for %q", id)
	}
	return manifest, nil
}

// Save writes a manifest atomically: the encoded document is written to a
// temporary file in the same directory, flushed to stable storage, and renamed
// over the previous manifest.
func (store *Store) Save(manifest Manifest) error {
	dir, err := store.JobDir(manifest.ID)
	if err != nil {
		return err
	}
	if manifest.SchemaVersion == "" {
		manifest.SchemaVersion = ManifestSchemaVersion
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("storage: encode manifest: %w", err)
	}
	payload = append(payload, '\n')

	temporary, err := os.CreateTemp(dir, ".manifest-*.json")
	if err != nil {
		return fmt.Errorf("storage: create temporary manifest: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if _, err := temporary.Write(payload); err != nil {
		cleanup()
		return fmt.Errorf("storage: write temporary manifest: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("storage: sync temporary manifest: %w", err)
	}
	if err := temporary.Chmod(filePermissions); err != nil {
		cleanup()
		return fmt.Errorf("storage: set manifest permissions: %w", err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("storage: close temporary manifest: %w", err)
	}
	if err := os.Rename(temporaryPath, filepath.Join(dir, manifestName)); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("storage: publish manifest: %w", err)
	}
	return syncDir(dir)
}

// Update loads a manifest, applies mutate, stamps UpdatedAt, and saves the
// result atomically. Concurrent updates of one job must be serialized by the
// caller.
func (store *Store) Update(id string, now time.Time, mutate func(*Manifest) error) (Manifest, error) {
	manifest, err := store.Load(id)
	if err != nil {
		return Manifest{}, err
	}
	if mutate != nil {
		if err := mutate(&manifest); err != nil {
			return Manifest{}, err
		}
	}
	manifest.UpdatedAt = now.UTC()
	if err := store.Save(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// List returns every readable manifest sorted by creation time, newest first.
// Unreadable job directories are skipped rather than failing the whole listing.
func (store *Store) List() ([]Manifest, error) {
	entries, err := os.ReadDir(filepath.Join(store.root, jobsDirName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: list jobs: %w", err)
	}
	manifests := make([]Manifest, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if ValidateID(entry.Name()) != nil {
			continue
		}
		manifest, err := store.Load(entry.Name())
		if err != nil {
			continue
		}
		manifests = append(manifests, manifest)
	}
	sort.Slice(manifests, func(first, second int) bool {
		return manifests[first].CreatedAt.After(manifests[second].CreatedAt)
	})
	return manifests, nil
}

// Delete removes one job directory and everything inside it.
func (store *Store) Delete(id string) error {
	dir, err := store.JobDir(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return fmt.Errorf("storage: inspect job directory: %w", err)
	}
	manifest, manifestErr := store.Load(id)
	if err := os.RemoveAll(dir); err != nil {
		removeErr := fmt.Errorf("storage: remove job directory: %w", err)
		// Empty manifests are safe to restore after partial deletion and keep
		// the directory discoverable for later cleanup attempts.
		if manifestErr == nil && len(manifest.Files) == 0 {
			if saveErr := store.Save(manifest); saveErr != nil {
				return errors.Join(removeErr, fmt.Errorf("storage: preserve empty manifest after failed removal: %w", saveErr))
			}
		}
		return removeErr
	}
	return nil
}

// Resolve joins name onto a job subdirectory and guarantees the result stays
// inside that directory. It rejects absolute paths, separators, and traversal.
func Resolve(dir, name string) (string, error) {
	if name == "" {
		return "", errors.New("storage: file name must not be empty")
	}
	if strings.ContainsAny(name, `/\`) || filepath.IsAbs(name) {
		return "", fmt.Errorf("storage: file name %q must not contain a path", name)
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("storage: file name %q is not permitted", name)
	}
	joined := filepath.Join(dir, name)
	if !Contains(dir, joined) {
		return "", fmt.Errorf("storage: file name %q escapes its job directory", name)
	}
	return joined, nil
}

// Contains reports whether candidate resolves inside parent.
func Contains(parent, candidate string) bool {
	cleanParent := filepath.Clean(parent)
	cleanCandidate := filepath.Clean(candidate)
	if cleanParent == cleanCandidate {
		return true
	}
	relative, err := filepath.Rel(cleanParent, cleanCandidate)
	if err != nil {
		return false
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(relative)
}

func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("storage: open job directory: %w", err)
	}
	syncErr := handle.Sync()
	closeErr := handle.Close()
	return directorySyncError(syncErr, closeErr)
}

func directorySyncError(syncErr, closeErr error) error {
	// Unsupported directory fsync is harmless, but a PathError can also wrap
	// real I/O failures that must not be reported as a successful save.
	if syncErr != nil && !errors.Is(syncErr, os.ErrInvalid) &&
		!errors.Is(syncErr, syscall.EINVAL) && !errors.Is(syncErr, syscall.ENOTSUP) {
		return fmt.Errorf("storage: sync job directory: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("storage: close job directory: %w", closeErr)
	}
	return nil
}
