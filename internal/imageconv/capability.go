package imageconv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/guigui42/filetwist/internal/probe"
)

// HEIFProbeSpec returns a real HEVC-based HEIF decode operation for probe.Prober.
func HEIFProbeSpec(executable, inputPath, outputPath string) (probe.FunctionalSpec, error) {
	return decodeProbeSpec("libvips HEIF decode", executable, inputPath, outputPath)
}

// AVIFProbeSpec returns a real AV1-based AVIF decode operation for probe.Prober.
func AVIFProbeSpec(executable, inputPath, outputPath string) (probe.FunctionalSpec, error) {
	return decodeProbeSpec("libvips AVIF decode", executable, inputPath, outputPath)
}

func decodeProbeSpec(name, executable, inputPath, outputPath string) (probe.FunctionalSpec, error) {
	if executable == "" || inputPath == "" || outputPath == "" {
		return probe.FunctionalSpec{}, imageError(CodeInvalidRequest, "build image decode probe", errors.New("executable, input, and output paths are required"))
	}
	return probe.FunctionalSpec{
		Name:       name,
		Executable: executable,
		Args:       []string{"copy", inputPath, outputPath},
		Timeout:    30 * time.Second,
	}, nil
}

// ProbeHEIF performs a real HEIF decode through the shared functional prober.
func ProbeHEIF(ctx context.Context, prober *probe.Prober, executable, inputPath, tempDir string) (Capabilities, error) {
	return probeDecodeWithFS(ctx, prober, executable, inputPath, tempDir, FormatHEIF, os.MkdirTemp, os.RemoveAll)
}

// ProbeAVIF performs a real AVIF decode through the shared functional prober.
func ProbeAVIF(ctx context.Context, prober *probe.Prober, executable, inputPath, tempDir string) (Capabilities, error) {
	return probeDecodeWithFS(ctx, prober, executable, inputPath, tempDir, FormatAVIF, os.MkdirTemp, os.RemoveAll)
}

func probeDecodeWithFS(
	ctx context.Context,
	prober *probe.Prober,
	executable string,
	inputPath string,
	tempDir string,
	format Format,
	mkdirTemp func(string, string) (string, error),
	removeAll func(string) error,
) (capabilities Capabilities, returnErr error) {
	if prober == nil {
		return capabilities, imageError(CodeInvalidRequest, "probe image decode", errors.New("prober must not be nil"))
	}
	if tempDir == "" {
		return capabilities, imageError(CodeInvalidRequest, "probe image decode", errors.New("temporary directory is required"))
	}
	if mkdirTemp == nil || removeAll == nil {
		return capabilities, imageError(CodeInvalidRequest, "probe image decode", errors.New("filesystem helpers are required"))
	}
	workDir, err := mkdirTemp(tempDir, ".filetwist-image-probe-")
	if err != nil {
		return capabilities, imageError(CodeProbeFailed, "create image decode probe directory", err)
	}
	outputPath := filepath.Join(workDir, "decoded.v")
	defer func() {
		if cleanupErr := removeAll(workDir); cleanupErr != nil {
			err := imageError(CodeCleanupFailed, "clean image decode probe directory", cleanupErr)
			if returnErr != nil {
				returnErr = errors.Join(returnErr, err)
			}
		}
	}()

	var spec probe.FunctionalSpec
	switch format {
	case FormatHEIF:
		spec, err = HEIFProbeSpec(executable, inputPath, outputPath)
	case FormatAVIF:
		spec, err = AVIFProbeSpec(executable, inputPath, outputPath)
	default:
		err = errors.New("unsupported functional probe format")
	}
	if err != nil {
		return capabilities, err
	}
	result, err := prober.Functional(ctx, spec)
	if err != nil {
		return capabilities, err
	}
	switch format {
	case FormatHEIF:
		capabilities.HEIFDecode = result.Passed
	case FormatAVIF:
		capabilities.AVIFDecode = result.Passed
	}
	return capabilities, nil
}
