package conversion

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishNoReplaceTreatsLinkAsCommit(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "staged.jpg")
	target := filepath.Join(dir, "final.jpg")
	if err := os.WriteFile(source, []byte("converted"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := publishNoReplaceWith(source, target, os.Link, func(string) error { return errors.New("unlink failed") }); err != nil {
		t.Fatalf("publishNoReplace() error = %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(content) != "converted" {
		t.Fatalf("target content = %q; want converted", content)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("staged output should remain when unlink fails: %v", err)
	}
}
