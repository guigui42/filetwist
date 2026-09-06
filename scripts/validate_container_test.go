package scripts

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateContainerCodecDirectoryCleanup(t *testing.T) {
	temp := t.TempDir()
	bin := filepath.Join(temp, "bin")
	work := filepath.Join(temp, "work")
	for _, dir := range []string{bin, work} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Stop at the codec probe so this regression runs without Docker or codecs.
	docker := `#!/bin/sh
set -eu
case "$*" in
  "image inspect filetwist:cleanup-test") exit 0 ;;
  *" probe capabilities") exit 0 ;;
  *" probe image-codecs "*)
    for arg do
      case "$arg" in
        *:/data) output=${arg%:/data} ;;
      esac
    done
    test -d "$output/codecs" || {
      echo "codec directory must be created by the host" >&2
      exit 1
    }
    mode=$(LC_ALL=C ls -ld "$output/codecs")
    case "$mode" in
      drwxrwxrwx*) ;;
      *) echo "codec directory must be writable by the runtime UID" >&2; exit 1 ;;
    esac
    printf 'synthetic output\n' > "$output/codecs/probe.heic"
    echo "simulated codec failure" >&2
    exit 42
    ;;
  *) echo "unexpected docker invocation: $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(docker), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMPDIR", work)

	cmd := exec.CommandContext(t.Context(), "sh", "validate-container.sh", "filetwist:cleanup-test")
	output, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 42 {
		t.Fatalf("expected codec failure to survive cleanup, got %v:\n%s", err, output)
	}
	if !strings.Contains(string(output), "simulated codec failure") {
		t.Fatalf("codec probe did not run:\n%s", output)
	}
	remaining, err := os.ReadDir(work)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("smoke-test work directories survived cleanup: %v", remaining)
	}
}
