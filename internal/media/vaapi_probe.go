package media

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	capabilityprobe "github.com/guigui42/filetwist/internal/probe"
	"github.com/guigui42/filetwist/internal/runner"
)

// VAAPIProbeResult contains the functional encode, re-probe, validation, and
// optional hardware-decode results.
type VAAPIProbeResult struct {
	Readiness    VAAPIReadiness
	Encode       capabilityprobe.FunctionalResult
	ProbeCommand runner.Result
	Output       Probe
	Decode       capabilityprobe.FunctionalResult
}

// VAAPIProbeError reports a failed functional readiness stage.
type VAAPIProbeError struct {
	Reason FallbackReason
	Cause  error
}

// Error implements error.
func (err *VAAPIProbeError) Error() string {
	return "media: VA-API functional probe failed"
}

// Unwrap returns the device, runner, probe, or validation error.
func (err *VAAPIProbeError) Unwrap() error {
	return err.Cause
}

type vaapiProbeOptions struct {
	checkDevice func(string) error
}

// VAAPIProbeOption configures the functional VA-API probe.
type VAAPIProbeOption func(*vaapiProbeOptions) error

// WithVAAPIDeviceCheck replaces the render-device access check for controlled
// tests.
func WithVAAPIDeviceCheck(check func(string) error) VAAPIProbeOption {
	return func(options *vaapiProbeOptions) error {
		if check == nil {
			return errors.New("media: VA-API device check must not be nil")
		}
		options.checkDevice = check
		return nil
	}
}

// ProbeVAAPI verifies readiness with a real h264_vaapi encode, output
// re-probe, common compatibility validation, and a hardware decode attempt.
func ProbeVAAPI(
	ctx context.Context,
	run RunFunc,
	ffmpegPath string,
	ffprobePath string,
	config AccelerationConfig,
	timeout time.Duration,
	options ...VAAPIProbeOption,
) (VAAPIProbeResult, error) {
	result := VAAPIProbeResult{}
	if ctx == nil {
		return result, errors.New("media: context must not be nil")
	}
	if run == nil {
		return result, errors.New("media: run function must not be nil")
	}
	if ffmpegPath == "" || ffprobePath == "" {
		return result, errors.New("media: FFmpeg and ffprobe paths must not be empty")
	}
	if timeout < 0 {
		return result, errors.New("media: VA-API probe timeout must not be negative")
	}

	normalized, err := normalizeAccelerationConfig(config)
	if err != nil {
		return result, err
	}
	probeOptions := vaapiProbeOptions{checkDevice: checkVAAPIDevice}
	for _, option := range options {
		if option == nil {
			return result, errors.New("media: VA-API probe option must not be nil")
		}
		if err := option(&probeOptions); err != nil {
			return result, err
		}
	}

	result.Readiness.Device = normalized.Device
	result.Readiness.HardwareDecodeCodecs = make(map[string]bool)
	if err := probeOptions.checkDevice(normalized.Device); err != nil {
		result.Readiness.UnavailableReason = FallbackVAAPIDevice
		return result, &VAAPIProbeError{
			Reason: FallbackVAAPIDevice,
			Cause:  err,
		}
	}

	tempDir, err := os.MkdirTemp("", "filetwist-vaapi-probe-*")
	if err != nil {
		return result, &VAAPIProbeError{
			Reason: FallbackVAAPIProbeUnavailable,
			Cause:  fmt.Errorf("create VA-API probe directory: %w", err),
		}
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()
	outputPath := filepath.Join(tempDir, "probe.mp4")

	prober, err := capabilityprobe.New(capabilityprobe.RunFunc(run), capabilityprobe.WithLookPath(func(path string) (string, error) {
		return path, nil
	}))
	if err != nil {
		return result, err
	}
	result.Encode, err = prober.Functional(ctx, capabilityprobe.FunctionalSpec{
		Name:       "VA-API H.264 encode",
		Executable: ffmpegPath,
		Args: []string{
			"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
			"-init_hw_device", "vaapi=filetwist:" + normalized.Device,
			"-filter_hw_device", "filetwist",
			"-f", "lavfi",
			"-i", "testsrc2=size=320x180:rate=10",
			"-t", "0.4",
			"-an",
			"-vf", "format=nv12,hwupload",
			"-c:v", "h264_vaapi",
			"-qp", "24",
			"-movflags", "+faststart",
			"-f", "mp4",
			outputPath,
		},
		Dir:     tempDir,
		Timeout: timeout,
	})
	if err != nil {
		reason := classifyVAAPIStderr(result.Encode.Command.Stderr.Bytes)
		if reason == FallbackNone {
			reason = FallbackVAAPIProbeUnavailable
		}
		result.Readiness.UnavailableReason = reason
		return result, &VAAPIProbeError{Reason: reason, Cause: err}
	}

	result.Output, result.ProbeCommand, err = ProbeFile(
		ctx,
		run,
		ffprobePath,
		outputPath,
		timeout,
	)
	if err != nil {
		result.Readiness.UnavailableReason = FallbackVAAPIProbeValidation
		return result, &VAAPIProbeError{
			Reason: FallbackVAAPIProbeValidation,
			Cause:  err,
		}
	}
	probePlan := Plan{
		OutputPath: outputPath,
		Expected: ExpectedProfile{
			Container:         "mp4",
			VideoCodec:        "h264",
			PixelFormat:       "yuv420p",
			Width:             320,
			Height:            180,
			Rotation:          0,
			AudioPresence:     AudioForbidden,
			DurationTolerance: defaultDurationTolerance,
			FastStart:         true,
		},
	}
	if err := ValidateOutput(probePlan, result.Output); err != nil {
		result.Readiness.UnavailableReason = FallbackVAAPIProbeValidation
		return result, &VAAPIProbeError{
			Reason: FallbackVAAPIProbeValidation,
			Cause:  err,
		}
	}

	result.Readiness.Ready = true
	result.Readiness.FunctionalProbeComplete = true
	result.Decode, err = prober.Functional(ctx, capabilityprobe.FunctionalSpec{
		Name:       "VA-API H.264 decode",
		Executable: ffmpegPath,
		Args: []string{
			"-hide_banner", "-loglevel", "error", "-nostdin",
			"-init_hw_device", "vaapi=filetwist:" + normalized.Device,
			"-hwaccel", "vaapi",
			"-hwaccel_device", "filetwist",
			"-hwaccel_output_format", "vaapi",
			"-i", outputPath,
			"-map", "0:v:0",
			"-an",
			"-f", "null",
			"-",
		},
		Dir:     tempDir,
		Timeout: timeout,
	})
	if err == nil {
		result.Readiness.HardwareDecodeCodecs["h264"] = true
	} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		result.Readiness.Ready = false
		result.Readiness.UnavailableReason = FallbackVAAPIProbeUnavailable
		return result, &VAAPIProbeError{
			Reason: FallbackVAAPIProbeUnavailable,
			Cause:  err,
		}
	}
	return result, nil
}

func checkVAAPIDevice(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat VA-API device: %w", err)
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return errors.New("VA-API device is not a character device")
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open VA-API device read-write: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close VA-API device: %w", err)
	}
	return nil
}

func classifyVAAPIStderr(stderr []byte) FallbackReason {
	text := strings.ToLower(string(stderr))
	for _, phrase := range []string{
		"no va display found",
		"cannot open drm render node",
		"failed to open vaapi device",
		"failed to open /dev/dri/",
	} {
		if strings.Contains(text, phrase) {
			return FallbackVAAPIDevice
		}
	}
	if strings.Contains(text, "/dev/dri/") && strings.Contains(text, "permission denied") {
		return FallbackVAAPIDevice
	}
	for _, phrase := range []string{
		"failed to initialise vaapi connection",
		"failed to initialize vaapi connection",
		"failed to create a vaapi device",
		"device creation failed",
		"failed to set value 'vaapi=",
	} {
		if strings.Contains(text, phrase) {
			return FallbackVAAPIInitialization
		}
	}
	for _, phrase := range []string{
		"no usable encoding entrypoint",
		"failed to initialise vaapi encoder",
		"failed to initialize vaapi encoder",
		"error while opening encoder",
		"driver does not support any rc mode",
	} {
		if strings.Contains(text, phrase) {
			return FallbackVAAPIEncoder
		}
	}
	for _, phrase := range []string{
		"a hardware device reference is required to upload frames",
		"failed to upload frame",
		"failed to map frame",
		"failed to transfer data to surface",
	} {
		if strings.Contains(text, phrase) {
			return FallbackVAAPIUpload
		}
	}
	if strings.Contains(text, "hwupload") &&
		(strings.Contains(text, "failed") || strings.Contains(text, "error")) {
		return FallbackVAAPIUpload
	}
	return FallbackNone
}
