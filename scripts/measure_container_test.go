package scripts

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMeasureContainerCompressionBudget(t *testing.T) {
	for _, tc := range []struct {
		name        string
		fastSize    string
		bestSize    string
		wantLevels  string
		wantExit    int
		wantMessage string
	}{
		{"fast pass", "1024", "512", "-1\n", 0, "PASS: compressed image"},
		{"exact budget", "1048576", "512", "-1\n", 0, "PASS: compressed image"},
		{"best compression fallback", "2097152", "1024", "-1\n-9\n", 0, "PASS: compressed image"},
		{"over budget", "2097152", "2097152", "-1\n-9\n", 1, "ERROR: compressed image exceeds"},
		{"compression failure", "fail", "1024", "-1\n", 42, "simulated gzip failure"},
		{"fallback failure", "2097152", "fail", "-1\n-9\n", 42, "simulated gzip failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			temp := t.TempDir()
			bin := filepath.Join(temp, "bin")
			work := filepath.Join(temp, "work")
			for _, dir := range []string{bin, work} {
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			commands := map[string]string{
				"docker": `#!/bin/sh
set -eu
case "$1" in
  info|run|history|rm) exit 0 ;;
  image)
    case "$2" in
      inspect) exit 0 ;;
      save) printf 'synthetic image\n' > "$4"; exit 0 ;;
    esac
    ;;
  create) echo synthetic-container; exit 0 ;;
  export) printf 'synthetic rootfs\n' > "$3"; exit 0 ;;
esac
echo "unexpected docker invocation: $*" >&2
exit 1
`,
				"gzip": `#!/bin/sh
set -eu
for arg do
  case "$arg" in
    -1) level=$arg; size=$FAST_SIZE ;;
    -9) level=$arg; size=$BEST_SIZE ;;
  esac
done
printf '%s\n' "$level" >> "$LEVEL_LOG"
if test "$size" = fail; then
  echo "simulated gzip failure" >&2
  exit 42
fi
head -c "$size" /dev/zero
`,
			}
			for name, body := range commands {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			log := filepath.Join(temp, "levels")
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("TMPDIR", work)
			t.Setenv("MAX_COMPRESSED_MIB", "1")
			t.Setenv("FAST_SIZE", tc.fastSize)
			t.Setenv("BEST_SIZE", tc.bestSize)
			t.Setenv("LEVEL_LOG", log)
			cmd := exec.CommandContext(t.Context(), "sh", "measure-container.sh", "filetwist:measure-test")
			output, err := cmd.CombinedOutput()
			if tc.wantExit == 0 {
				if err != nil {
					t.Fatalf("measurement failed: %v\n%s", err, output)
				}
			} else {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != tc.wantExit {
					t.Fatalf("expected exit %d, got %v:\n%s", tc.wantExit, err, output)
				}
			}
			if !strings.Contains(string(output), tc.wantMessage) {
				t.Fatalf("missing %q:\n%s", tc.wantMessage, output)
			}
			levels, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if string(levels) != tc.wantLevels {
				t.Errorf("compression levels = %q, want %q", levels, tc.wantLevels)
			}
			remaining, err := os.ReadDir(work)
			if err != nil {
				t.Fatal(err)
			}
			if len(remaining) != 0 {
				t.Fatalf("measurement work directories survived cleanup: %v", remaining)
			}
		})
	}
}
