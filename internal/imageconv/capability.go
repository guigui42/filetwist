package imageconv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/guigui42/filetwist/internal/probe"
)

const (
	capabilityInvalid Capability = iota
	// CapabilityDecodeHEIF records successful decoding of the current HEIF input.
	CapabilityDecodeHEIF
	// CapabilityDecodeAVIF records successful decoding of the current AVIF input.
	CapabilityDecodeAVIF
	capabilityCount
)

// NewCapabilitySet constructs an immutable capability bitset.
func NewCapabilitySet(capabilities ...Capability) CapabilitySet {
	var set CapabilitySet
	for _, capability := range capabilities {
		if capability > capabilityInvalid && capability < capabilityCount {
			set |= 1 << capability
		}
	}
	return set
}

// Has reports whether capability is present in the set.
func (set CapabilitySet) Has(capability Capability) bool {
	return capability > capabilityInvalid &&
		capability < capabilityCount &&
		set&(1<<capability) != 0
}

// RequiredDecodeCapability returns the optional runtime capability required by format.
func RequiredDecodeCapability(format Format) (Capability, bool) {
	switch format {
	case FormatHEIF:
		return CapabilityDecodeHEIF, true
	case FormatAVIF:
		return CapabilityDecodeAVIF, true
	default:
		return capabilityInvalid, false
	}
}

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
func ProbeHEIF(ctx context.Context, prober *probe.Prober, executable, inputPath, tempDir string) (CapabilitySet, error) {
	return probeDecodeWithFS(ctx, prober, executable, inputPath, tempDir, FormatHEIF, os.MkdirTemp, os.RemoveAll)
}

// ProbeAVIF performs a real AVIF decode through the shared functional prober.
func ProbeAVIF(ctx context.Context, prober *probe.Prober, executable, inputPath, tempDir string) (CapabilitySet, error) {
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
) (capabilities CapabilitySet, returnErr error) {
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
	if _, err := prober.Functional(ctx, spec); err != nil {
		return capabilities, err
	}
	switch format {
	case FormatHEIF:
		capabilities = NewCapabilitySet(CapabilityDecodeHEIF)
	case FormatAVIF:
		capabilities = NewCapabilitySet(CapabilityDecodeAVIF)
	}
	return capabilities, nil
}
