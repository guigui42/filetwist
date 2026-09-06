package deploy

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestMakeDev(t *testing.T) {
	for _, tt := range []struct {
		name  string
		args  []string
		image string
		port  string
	}{
		{"defaults", nil, "filetwist:dev", "18769"},
		{"overrides", []string{"IMAGE=filetwist:custom-dev", "DEV_PORT=18080"}, "filetwist:custom-dev", "18080"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"--no-print-directory", "--dry-run", "dev", "VERSION=test", "REVISION=test"}, tt.args...)
			command := exec.Command("make", args...)
			command.Dir = ".."
			command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("make dev: %v\n%s", err, output)
			}
			recipe := string(output)
			for _, want := range []string{
				"docker build", "--tag " + tt.image, "docker run --rm --init",
				"--platform linux/amd64", "127.0.0.1:" + tt.port + ":8080",
				"--env ACCELERATION=cpu", "http://127.0.0.1:" + tt.port,
			} {
				if !strings.Contains(recipe, want) {
					t.Errorf("recipe missing %q:\n%s", want, recipe)
				}
			}
			run := strings.Index(recipe, "docker run")
			if run < 0 {
				t.Fatal("dev must start a container")
			}
			if strings.Index(recipe, "docker build") > run {
				t.Error("dev must build before starting the container")
			}
			if !strings.Contains(recipe[run:], tt.image) {
				t.Error("dev must run the image it just built")
			}
			for _, forbidden := range []string{"--detach", "--volume", "docker push", "0.0.0.0:"} {
				if strings.Contains(recipe, forbidden) {
					t.Errorf("disposable localhost dev recipe contains %q", forbidden)
				}
			}
		})
	}
}
