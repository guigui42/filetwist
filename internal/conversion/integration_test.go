package conversion_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/media"
	"github.com/guigui42/filetwist/internal/probe"
	"github.com/guigui42/filetwist/internal/runner"
)

func TestLocalGeneratedFixturesIntegration(t *testing.T) {
	processRunner, err := runner.New(runner.Config{
		Timeout:     2 * time.Minute,
		StdoutLimit: 256 * 1024,
		StderrLimit: 256 * 1024,
	})
	if err != nil {
		t.Fatalf("runner.New() error = %v", err)
	}
	prober, err := probe.New(processRunner.Run)
	if err != nil {
		t.Fatalf("probe.New() error = %v", err)
	}
	service, err := conversion.NewLocal(conversion.LocalConfig{
		Run:          processRunner.Run,
		Prober:       prober,
		Acceleration: media.AccelerationConfig{Mode: media.AccelerationCPU},
	})
	if err != nil {
		t.Fatalf("NewLocal() error = %v", err)
	}

	root := filepath.Clean(filepath.Join("..", ".."))
	tests := []struct {
		name       string
		required   []string
		input      string
		operation  corpus.Operation
		wantKind   corpus.MediaKind
		wantSuffix string
	}{
		{
			name:       "generated PNG",
			required:   []string{"vips", "vipsheader"},
			input:      filepath.Join(root, "fixtures", "generated", "rgba-2x2.png"),
			operation:  corpus.OperationLosslessImage,
			wantKind:   corpus.MediaImage,
			wantSuffix: "-lossless.png",
		},
		{
			name:       "generated WAV",
			required:   []string{"ffmpeg", "ffprobe"},
			input:      filepath.Join(root, "fixtures", "generated", "tone-8khz-mono.wav"),
			operation:  corpus.OperationCompatibleAudio,
			wantKind:   corpus.MediaAudio,
			wantSuffix: "-compatible.mp3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, executable := range tt.required {
				if _, err := exec.LookPath(executable); err != nil {
					t.Skipf("%s integration skipped: executable unavailable", executable)
				}
			}
			result, err := service.Convert(context.Background(), conversion.Request{
				InputPath:    tt.input,
				Output:       t.TempDir(),
				Operation:    tt.operation,
				OperationSet: true,
			})
			if err != nil {
				t.Fatalf("Convert() error = %v", err)
			}
			if result.DetectedMedia == nil || result.DetectedMedia.Kind != tt.wantKind {
				t.Errorf("detected media = %+v; want %s", result.DetectedMedia, tt.wantKind)
			}
			if result.Validation.Status != "passed" {
				t.Errorf("validation = %+v; want passed", result.Validation)
			}
			if !strings.HasSuffix(result.OutputPath, tt.wantSuffix) {
				t.Errorf("output = %q; want suffix %q", result.OutputPath, tt.wantSuffix)
			}
			if _, err := os.Stat(result.OutputPath); err != nil {
				t.Errorf("output stat error = %v", err)
			}
		})
	}
}
