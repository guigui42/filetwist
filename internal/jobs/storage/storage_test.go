package storage_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/jobs/storage"
)

func newStore(t *testing.T) *storage.Store {
	t.Helper()
	store, err := storage.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func TestNewIDIsRandomBase64URL(t *testing.T) {
	seen := make(map[string]bool)
	for index := 0; index < 64; index++ {
		id, err := storage.NewID()
		if err != nil {
			t.Fatalf("NewID: %v", err)
		}
		if len(id) != 22 {
			t.Fatalf("id %q length = %d; want 22 characters for 128 bits", id, len(id))
		}
		if err := storage.ValidateID(id); err != nil {
			t.Fatalf("ValidateID(%q): %v", id, err)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestValidateIDRejectsTraversalAndShortValues(t *testing.T) {
	for _, id := range []string{
		"", "..", "../../etc/passwd", "short", strings.Repeat("a", 23),
		"AAAAAAAAAAAAAAAAAAAA/A", "AAAAAAAAAAAAAAAAAAAA.A", "AAAAAAAAAAAAAAAAAAAA=A",
	} {
		if err := storage.ValidateID(id); err == nil {
			t.Errorf("ValidateID(%q) = nil; want rejection", id)
		}
	}
}

func TestValidateFileIDRejectsTraversal(t *testing.T) {
	if err := storage.ValidateFileID("f001"); err != nil {
		t.Fatalf("ValidateFileID(f001): %v", err)
	}
	for _, id := range []string{"", "..", "../f001", "f001/x", "F001", strings.Repeat("f", 25)} {
		if err := storage.ValidateFileID(id); err == nil {
			t.Errorf("ValidateFileID(%q) = nil; want rejection", id)
		}
	}
}

func TestSafeFileNameStripsPathsAndControlCharacters(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain", input: "photo.HEIC", want: "photo.heic"},
		{name: "posix traversal", input: "../../etc/passwd", want: "passwd"},
		{name: "windows traversal", input: `..\..\windows\system32\cmd.exe`, want: "cmd.exe"},
		{name: "absolute", input: "/etc/shadow", want: "shadow"},
		{name: "dot only", input: "...", want: "upload"},
		{name: "empty", input: "", want: "upload"},
		{name: "control characters", input: "a\x00b\nc.jpg", want: "abc.jpg"},
		{name: "spaces", input: "my holiday photo.jpg", want: "my-holiday-photo.jpg"},
		{name: "hidden", input: ".bashrc", want: "bashrc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := storage.SafeFileName(tt.input, "upload")
			if got != tt.want {
				t.Fatalf("SafeFileName(%q) = %q; want %q", tt.input, got, tt.want)
			}
			if strings.ContainsAny(got, `/\`) {
				t.Fatalf("SafeFileName(%q) = %q; must not contain separators", tt.input, got)
			}
		})
	}
}

func TestSafeFileNameBoundsLength(t *testing.T) {
	got := storage.SafeFileName(strings.Repeat("a", 500)+".jpeg", "upload")
	if len(got) > storage.MaxFileNameLength {
		t.Fatalf("length = %d; want at most %d", len(got), storage.MaxFileNameLength)
	}
	if !strings.HasSuffix(got, ".jpeg") {
		t.Fatalf("name = %q; want preserved extension", got)
	}
}

func TestUniqueFileNameDisambiguates(t *testing.T) {
	taken := make(map[string]bool)
	first := storage.UniqueFileName(taken, "photo.jpg")
	second := storage.UniqueFileName(taken, "photo.jpg")
	third := storage.UniqueFileName(taken, "photo.jpg")
	if first != "photo.jpg" || second != "photo-2.jpg" || third != "photo-3.jpg" {
		t.Fatalf("names = %q, %q, %q", first, second, third)
	}
}

func TestResolveRejectsEscapes(t *testing.T) {
	dir := t.TempDir()
	if _, err := storage.Resolve(dir, "photo.jpg"); err != nil {
		t.Fatalf("Resolve valid name: %v", err)
	}
	for _, name := range []string{"", ".", "..", "../escape", "sub/photo.jpg", `..\escape`, "/etc/passwd"} {
		if _, err := storage.Resolve(dir, name); err == nil {
			t.Errorf("Resolve(%q) = nil; want rejection", name)
		}
	}
}

func TestContains(t *testing.T) {
	if !storage.Contains("/data/jobs/a", "/data/jobs/a/input/x.jpg") {
		t.Error("Contains should accept a descendant")
	}
	if storage.Contains("/data/jobs/a", "/data/jobs/b/input/x.jpg") {
		t.Error("Contains should reject a sibling")
	}
	if storage.Contains("/data/jobs/a", "/data/jobs/a/../b") {
		t.Error("Contains should reject traversal")
	}
}

func TestCreateIsolatesJobDirectories(t *testing.T) {
	store := newStore(t)
	first, err := store.Create(time.Now(), time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	second, err := store.Create(time.Now(), time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if first.ID == second.ID {
		t.Fatal("two jobs shared one identifier")
	}
	for _, id := range []string{first.ID, second.ID} {
		dir, err := store.JobDir(id)
		if err != nil {
			t.Fatalf("JobDir: %v", err)
		}
		for _, name := range []string{"input", "output", "tmp", "manifest.json"} {
			if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
				t.Fatalf("missing %s in job directory: %v", name, err)
			}
		}

	}
}

func TestCreateRemovesDirectoryWhenManifestCannotBeSaved(t *testing.T) {
	store := newStore(t)
	_, err := store.Create(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), time.Hour)
	if err == nil {
		t.Fatal("Create accepted a timestamp that JSON cannot encode")
	}
	entries, err := os.ReadDir(filepath.Join(store.Root(), "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed Create left orphan directories: %v", entries)
	}
}

func TestSaveIsAtomicAndLeavesNoTemporaryFiles(t *testing.T) {
	store := newStore(t)
	manifest, err := store.Create(time.Now(), time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	manifest.State = storage.JobRunning
	manifest.Files = append(manifest.Files, storage.File{ID: "f001", Name: "a.jpg", State: storage.FileQueued})
	if err := store.Save(manifest); err != nil {
		t.Fatalf("Save: %v", err)
	}

	dir, err := store.JobDir(manifest.ID)
	if err != nil {
		t.Fatalf("JobDir: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".manifest-") {
			t.Fatalf("temporary manifest %q was left behind", entry.Name())
		}
	}

	payload, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var decoded storage.Manifest
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if decoded.State != storage.JobRunning || len(decoded.Files) != 1 {
		t.Fatalf("decoded = %+v; want the saved state", decoded)
	}
	if decoded.SchemaVersion != storage.ManifestSchemaVersion {
		t.Fatalf("schema version = %q; want %q", decoded.SchemaVersion, storage.ManifestSchemaVersion)
	}
}

func TestUpdateStampsAndPersists(t *testing.T) {
	store := newStore(t)
	created := time.Now().UTC()
	manifest, err := store.Create(created, time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	later := created.Add(time.Minute)
	updated, err := store.Update(manifest.ID, later, func(current *storage.Manifest) error {
		current.State = storage.JobCompleted
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !updated.UpdatedAt.Equal(later.UTC()) {
		t.Fatalf("UpdatedAt = %s; want %s", updated.UpdatedAt, later.UTC())
	}
	reloaded, err := store.Load(manifest.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.State != storage.JobCompleted {
		t.Fatalf("state = %q; want completed", reloaded.State)
	}
}

func TestLoadAndDeleteReportMissingJobs(t *testing.T) {
	store := newStore(t)
	id, err := storage.NewID()
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	if _, err := store.Load(id); err != storage.ErrNotFound {
		t.Fatalf("Load = %v; want ErrNotFound", err)
	}
	if err := store.Delete(id); err != storage.ErrNotFound {
		t.Fatalf("Delete = %v; want ErrNotFound", err)
	}
	if _, err := store.Load("../../etc"); err == nil {
		t.Fatal("Load accepted a traversal identifier")
	}
}

func TestListSortsNewestFirstAndSkipsForeignDirectories(t *testing.T) {
	store := newStore(t)
	older, err := store.Create(time.Now().Add(-time.Hour), time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	newer, err := store.Create(time.Now(), time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.Mkdir(filepath.Join(store.Root(), "jobs", "not-a-job"), 0o750); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	manifests, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(manifests) != 2 {
		t.Fatalf("len = %d; want 2", len(manifests))
	}
	if manifests[0].ID != newer.ID || manifests[1].ID != older.ID {
		t.Fatalf("order = %q, %q; want newest first", manifests[0].ID, manifests[1].ID)
	}
}

func TestContentTypeNeverReturnsActiveTypes(t *testing.T) {
	tests := map[string]string{
		"photo.jpg":     "image/jpeg",
		"clip.mp4":      "video/mp4",
		"track.mp3":     "audio/mpeg",
		"bundle.zip":    "application/zip",
		"payload.html":  "application/octet-stream",
		"script.js":     "application/octet-stream",
		"document.svg":  "application/octet-stream",
		"unknown.xyzzy": "application/octet-stream",
	}
	for name, want := range tests {
		if got := storage.ContentType(name); got != want {
			t.Errorf("ContentType(%q) = %q; want %q", name, got, want)
		}
	}
}

func TestExpiredUsesRetentionWindow(t *testing.T) {
	now := time.Now()
	manifest := storage.Manifest{ExpiresAt: now.Add(time.Hour)}
	if manifest.Expired(now) {
		t.Error("job expired before its window closed")
	}
	if !manifest.Expired(now.Add(2 * time.Hour)) {
		t.Error("job did not expire after its window closed")
	}
}

func TestFreeSpaceReportsPositiveValue(t *testing.T) {
	free, err := storage.FreeSpace(t.TempDir())
	if err != nil {
		t.Fatalf("FreeSpace: %v", err)
	}
	if free <= 0 {
		t.Fatalf("free = %d; want positive", free)
	}
}
