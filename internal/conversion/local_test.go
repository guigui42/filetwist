package conversion_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/imageconv"
	"github.com/guigui42/filetwist/internal/media"
	"github.com/guigui42/filetwist/internal/probe"
	"github.com/guigui42/filetwist/internal/runner"
)

func TestLocalInspectionDefersRuntimeCapabilities(t *testing.T) {
	for _, kind := range []string{"video", "heif", "avif"} {
		t.Run(kind, func(t *testing.T) {
			input := writeTemporaryFile(t)
			calls := 0
			run := func(_ context.Context, command runner.Command) (runner.Result, error) {
				calls++
				switch command.Path {
				case "vipsheader":
					if kind == "video" {
						return runner.Result{}, errors.New("not an image")
					}
					compression := "hevc"
					if kind == "avif" {
						compression = "av1"
					}
					return runner.Result{Stdout: runner.Output{Bytes: []byte(
						"width: 2\nheight: 2\nbands: 3\nformat: uchar\ninterpretation: srgb\n" +
							"vips-loader: heifload\nheif-compression: " + compression,
					)}}, nil
				case "ffprobe":
					return runner.Result{Stdout: runner.Output{Bytes: []byte(
						`{"format":{"format_name":"mp4"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":640,"height":480}]}`,
					)}}, nil
				default:
					t.Errorf("inspection ran a non-content probe: %+v", command)
					return runner.Result{}, errors.New("runtime probes must not run")
				}
			}
			prober, err := probe.New(run, probe.WithLookPath(func(name string) (string, error) {
				if name != "vipsheader" && name != "ffprobe" {
					t.Errorf("inspection checked runtime executable %s", name)
					return "", errors.New("converter unavailable")
				}
				return name, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			service, err := conversion.NewLocal(conversion.LocalConfig{
				Run: run, Prober: prober,
				TemporaryRoot: filepath.Join(filepath.Dir(input), "unused"),
				Acceleration: media.AccelerationConfig{
					Mode: media.AccelerationVAAPI, Device: "/dev/dri/renderD128",
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Inspect(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			wantCalls := 1
			if kind == "video" {
				wantCalls = 2
			}
			if calls != wantCalls {
				t.Errorf("probe calls = %d; want %d", calls, wantCalls)
			}
			entries, err := os.ReadDir(filepath.Dir(input))
			if err != nil || len(entries) != 1 {
				t.Fatalf("inspection wrote files: %v, %v", entries, err)
			}
		})
	}
}

func TestLocalRejectsUnsafeImagesBeforeFunctionalDecode(t *testing.T) {
	for _, compression := range []string{"hevc", "av1"} {
		for _, test := range []struct {
			name   string
			fields string
			code   string
		}{
			{"oversized", "width: 32769\n", imageconv.CodeDimensionsExceeded},
			{"multipage", "n-pages: 2\n", imageconv.CodeAnimatedUnsupported},
			{"HDR", "cicp-transfer-characteristics: 16\n", imageconv.CodeHDRUnsupported},
		} {
			t.Run(compression+"/"+test.name, func(t *testing.T) {
				dir := t.TempDir()
				input := filepath.Join(dir, "input.bin")
				if err := os.WriteFile(input, []byte("fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
				decodeCalls := 0
				run := func(_ context.Context, command runner.Command) (runner.Result, error) {
					if command.Path == "vipsheader" {
						return runner.Result{Stdout: runner.Output{Bytes: []byte(
							"width: 2\nheight: 2\nbands: 3\nformat: uchar\ninterpretation: srgb\n" +
								"vips-loader: heifload\nheif-compression: " + compression + "\n" + test.fields,
						)}}, nil
					}
					decodeCalls++
					return runner.Result{}, errors.New("pixel decoding should not be reached")
				}
				service := localTestService(t, run, dir, time.Second)
				_, err := service.Convert(context.Background(), conversion.Request{
					InputPath: input,
					Output:    filepath.Join(dir, "output"),
				})
				var classified *conversion.Error
				if !errors.As(err, &classified) || classified.Code != test.code {
					t.Errorf("error = %v; want %s", err, test.code)
				}
				if decodeCalls != 0 {
					t.Errorf("pixel decode calls = %d; want zero", decodeCalls)
				}
			})
		}
	}
}

func TestLocalImageHeadersHonorProbeTimeout(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.png")
	if err := os.WriteFile(input, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	const timeout = 5 * time.Second
	headers := 0
	run := func(ctx context.Context, command runner.Command) (runner.Result, error) {
		if command.Path == "vipsheader" {
			headers++
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > timeout {
				t.Errorf("header %d deadline = %v, present = %t; want configured probe timeout", headers, deadline, ok)
			}
			loader := "jpegload"
			if command.Args[len(command.Args)-1] == input {
				loader = "pngload"
			}
			return runner.Result{Stdout: runner.Output{Bytes: []byte(
				"width: 2\nheight: 2\nbands: 3\nformat: uchar\ninterpretation: srgb\nvips-loader: " + loader,
			)}}, nil
		}
		if command.Path == "vips" && command.Args[0] == "jpegsave" {
			if err := os.WriteFile(command.Args[2], []byte("converted"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return runner.Result{}, nil
	}
	service := localTestService(t, run, dir, timeout)
	if _, err := service.Convert(context.Background(), conversion.Request{
		InputPath: input,
		Output:    filepath.Join(dir, "output"),
	}); err != nil {
		t.Fatal(err)
	}
	if headers != 3 {
		t.Errorf("headers = %d; want detection, input and output probes", headers)
	}
}

func TestLocalOptionalDecodeHonorsProbeTimeout(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.heic")
	if err := os.WriteFile(input, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	const timeout = time.Second
	decoded := false
	run := func(ctx context.Context, command runner.Command) (runner.Result, error) {
		if command.Path == "vipsheader" {
			return runner.Result{Stdout: runner.Output{Bytes: []byte(
				"width: 2\nheight: 2\nbands: 3\nformat: uchar\ninterpretation: srgb\nvips-loader: heifload",
			)}}, nil
		}
		decoded = true
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > timeout {
			t.Errorf("decode deadline = %v, present = %t; want configured probe timeout", deadline, ok)
		}
		return runner.Result{}, errors.New("decode unavailable")
	}
	service := localTestService(t, run, dir, timeout)
	_, err := service.Convert(context.Background(), conversion.Request{InputPath: input, Output: dir})
	if err == nil || !decoded {
		t.Errorf("optional decode error = %v, decoded = %t", err, decoded)
	}
}

func TestLocalOptionalDecodeScratchRootFailureIsInvalidOutput(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.heic")
	if err := os.WriteFile(input, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(dir, "job-output")
	scratchRoot := filepath.Join(dir, "scratch-root")
	if err := os.WriteFile(scratchRoot, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(_ context.Context, command runner.Command) (runner.Result, error) {
		if command.Path == "vipsheader" {
			return runner.Result{Stdout: runner.Output{Bytes: []byte(
				"width: 2\nheight: 2\nbands: 3\nformat: uchar\ninterpretation: srgb\nvips-loader: heifload\nheif-compression: hevc",
			)}}, nil
		}
		t.Fatalf("unexpected runtime command: %+v", command)
		return runner.Result{}, nil
	}

	prober, err := probe.New(run, probe.WithLookPath(func(name string) (string, error) {
		if !strings.HasPrefix(name, "vips") {
			return "", fmt.Errorf("unexpected executable %s", name)
		}
		return name, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	service, err := conversion.NewLocal(conversion.LocalConfig{
		Run:           run,
		Prober:        prober,
		ProbeTimeout:  time.Second,
		TemporaryRoot: scratchRoot,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Convert(context.Background(), conversion.Request{
		InputPath: input,
		Output:    outputDir,
	})
	var classified *conversion.Error
	if !errors.As(err, &classified) || classified.Kind != conversion.FailureConfiguration ||
		classified.Code != "invalid_output" {
		t.Fatalf("error = %v; want invalid_output configuration failure", err)
	}
}

func localTestService(t *testing.T, run probe.RunFunc, dir string, timeout time.Duration) *conversion.Service {
	t.Helper()
	prober, err := probe.New(run, probe.WithLookPath(func(name string) (string, error) {
		if !strings.HasPrefix(name, "vips") {
			return "", fmt.Errorf("unexpected executable %s", name)
		}
		return name, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	service, err := conversion.NewLocal(conversion.LocalConfig{
		Run: run, Prober: prober, TemporaryRoot: dir, ProbeTimeout: timeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestLocalOptionalDecodeDefaultsScratchToOutputDir(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.heic")
	if err := os.WriteFile(input, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(dir, "job-output")
	var decodeOutput string

	run := func(_ context.Context, command runner.Command) (runner.Result, error) {
		switch command.Path {
		case "vipsheader":
			if command.Args[len(command.Args)-1] == input {
				return runner.Result{Stdout: runner.Output{Bytes: []byte(
					"width: 2\nheight: 2\nbands: 3\nformat: uchar\ninterpretation: srgb\nvips-loader: heifload\nheif-compression: hevc",
				)}}, nil
			}
			return runner.Result{Stdout: runner.Output{Bytes: []byte(
				"width: 2\nheight: 2\nbands: 3\nformat: uchar\ninterpretation: srgb\nvips-loader: jpegload",
			)}}, nil
		case "vips":
			switch command.Args[0] {
			case "copy":
				decodeOutput = command.Args[2]
				if !strings.HasPrefix(decodeOutput, outputDir+string(os.PathSeparator)) {
					t.Fatalf("decode output path = %q; want prefix %q", decodeOutput, outputDir+string(os.PathSeparator))
				}
			}
			outputPath := command.Args[2]
			if err := os.WriteFile(outputPath, []byte("converted"), 0o600); err != nil {
				t.Fatal(err)
			}
			return runner.Result{}, nil
		default:
			return runner.Result{}, fmt.Errorf("unexpected executable %s", command.Path)
		}
	}

	prober, err := probe.New(run, probe.WithLookPath(func(name string) (string, error) {
		if !strings.HasPrefix(name, "vips") {
			return "", fmt.Errorf("unexpected executable %s", name)
		}
		return name, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	service, err := conversion.NewLocal(conversion.LocalConfig{
		Run: run, Prober: prober, ProbeTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Convert(context.Background(), conversion.Request{
		InputPath: input,
		Output:    outputDir,
	})
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if decodeOutput == "" {
		t.Fatal("functional decode probe did not run")
	}
	if _, err := os.Stat(result.OutputPath); err != nil {
		t.Fatalf("published output missing: %v", err)
	}
}

func TestLocalOptionalDecodeFailureRemainsLoaderUnavailable(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.heic")
	if err := os.WriteFile(input, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	decoded := false
	run := func(_ context.Context, command runner.Command) (runner.Result, error) {
		switch command.Path {
		case "vipsheader":
			return runner.Result{Stdout: runner.Output{Bytes: []byte(
				"width: 2\nheight: 2\nbands: 3\nformat: uchar\ninterpretation: srgb\nvips-loader: heifload\nheif-compression: hevc",
			)}}, nil
		case "vips":
			decoded = true
			return runner.Result{}, errors.New("decode unavailable")
		default:
			return runner.Result{}, fmt.Errorf("unexpected executable %s", command.Path)
		}
	}

	service := localTestService(t, run, dir, time.Second)
	_, err := service.Convert(context.Background(), conversion.Request{
		InputPath: input,
		Output:    filepath.Join(dir, "output"),
	})
	var classified *conversion.Error
	if !errors.As(err, &classified) || classified.Kind != conversion.FailureRejection ||
		classified.Code != imageconv.CodeHEIFUnavailable {
		t.Fatalf("error = %v; want heif unavailable rejection", err)
	}
	if !decoded {
		t.Fatal("functional decode probe did not run")
	}
}
