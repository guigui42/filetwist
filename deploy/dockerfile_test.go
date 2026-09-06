package deploy

import (
	"os"
	"strings"
	"testing"
)

func TestRuntimeDependencyCacheIgnoresReleaseMetadata(t *testing.T) {
	data, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(data)
	start := strings.LastIndex(dockerfile, "\nFROM ")
	if start < 0 {
		t.Fatal("runtime stage not found")
	}
	runtime := dockerfile[start:]
	dependencies := strings.Index(runtime, "apt-get install")
	if dependencies < 0 {
		t.Fatal("runtime dependency installation not found")
	}
	// RUN instructions inherit every ARG in scope, even if not used explicitly.
	for _, name := range []string{"VERSION", "REVISION"} {
		t.Run(name, func(t *testing.T) {
			arg := strings.Index(runtime, "\nARG "+name+"=")
			if arg < 0 {
				t.Fatalf("runtime %s argument not found", name)
			}
			if arg < dependencies {
				t.Fatalf("%s must be declared after dependency installation to preserve its cache", name)
			}
		})
	}
}
