package media

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	// AccelerationEnv selects the video acceleration policy.
	AccelerationEnv = "ACCELERATION"
	// VAAPIDeviceEnv selects the VA-API render device.
	VAAPIDeviceEnv = "VAAPI_DEVICE"
	// DefaultVAAPIDevice is the default Intel render node.
	DefaultVAAPIDevice = "/dev/dri/renderD128"
)

// AccelerationMode controls whether video encoding uses the CPU or VA-API.
type AccelerationMode string

const (
	// AccelerationAuto uses VA-API after a successful functional probe and
	// otherwise uses the CPU engine.
	AccelerationAuto AccelerationMode = "auto"
	// AccelerationCPU disables hardware acceleration.
	AccelerationCPU AccelerationMode = "cpu"
	// AccelerationVAAPI requires a successful VA-API functional probe.
	AccelerationVAAPI AccelerationMode = "vaapi"
)

// AccelerationConfig contains the validated video acceleration settings.
type AccelerationConfig struct {
	Mode   AccelerationMode
	Device string
}

// ExecutionPath identifies the video processing path used by FFmpeg.
type ExecutionPath string

const (
	// ExecutionPathCPU uses software decode and libx264 encode.
	ExecutionPathCPU ExecutionPath = "cpu"
	// ExecutionPathVAAPISoftwareDecode uses software decode, hwupload, and
	// h264_vaapi encode.
	ExecutionPathVAAPISoftwareDecode ExecutionPath = "vaapi_software_decode"
	// ExecutionPathVAAPIHardwareDecode uses VA-API decode and encode.
	ExecutionPathVAAPIHardwareDecode ExecutionPath = "vaapi_hardware_decode"
)

// FallbackReason identifies why VA-API was unavailable or retried on CPU.
type FallbackReason string

const (
	// FallbackNone means no CPU fallback occurred.
	FallbackNone FallbackReason = ""
	// FallbackVAAPIProbeUnavailable means no successful functional probe was
	// supplied to automatic planning.
	FallbackVAAPIProbeUnavailable FallbackReason = "vaapi_probe_unavailable"
	// FallbackVAAPIDevice means the render device was missing or inaccessible.
	FallbackVAAPIDevice FallbackReason = "vaapi_device"
	// FallbackVAAPIInitialization means FFmpeg could not initialize VA-API.
	FallbackVAAPIInitialization FallbackReason = "vaapi_initialization"
	// FallbackVAAPIUpload means a software-decoded frame could not be uploaded.
	FallbackVAAPIUpload FallbackReason = "vaapi_upload"
	// FallbackVAAPIEncoder means h264_vaapi could not encode the stream.
	FallbackVAAPIEncoder FallbackReason = "vaapi_encoder"
	// FallbackVAAPIProbeValidation means the probe output failed the common
	// compatibility checks.
	FallbackVAAPIProbeValidation FallbackReason = "vaapi_probe_validation"
)

// VAAPIReadiness is the functional capability report consumed by planning.
type VAAPIReadiness struct {
	Device                  string
	Ready                   bool
	HardwareDecodeCodecs    map[string]bool
	UnavailableReason       FallbackReason
	FunctionalProbeComplete bool
}

// SupportsHardwareDecode reports whether the device passed a real hardware
// decode test for codec.
func (readiness VAAPIReadiness) SupportsHardwareDecode(codec string) bool {
	return readiness.HardwareDecodeCodecs[strings.ToLower(strings.TrimSpace(codec))]
}

// AccelerationConfigFromEnv loads and strictly validates ACCELERATION and
// VAAPI_DEVICE.
func AccelerationConfigFromEnv() (AccelerationConfig, error) {
	return LoadAccelerationConfig(os.LookupEnv)
}

// LoadAccelerationConfig loads acceleration settings through lookup. Unset
// values default to auto and /dev/dri/renderD128.
func LoadAccelerationConfig(lookup func(string) (string, bool)) (AccelerationConfig, error) {
	if lookup == nil {
		return AccelerationConfig{}, errors.New("media: environment lookup must not be nil")
	}

	mode := string(AccelerationAuto)
	if value, ok := lookup(AccelerationEnv); ok {
		if value == "" {
			return AccelerationConfig{}, errors.New("media: ACCELERATION must not be empty")
		}
		mode = value
	}
	device := DefaultVAAPIDevice
	if value, ok := lookup(VAAPIDeviceEnv); ok {
		if value == "" {
			return AccelerationConfig{}, errors.New("media: VAAPI_DEVICE must not be empty")
		}
		device = value
	}
	return normalizeAccelerationConfig(AccelerationConfig{
		Mode:   AccelerationMode(mode),
		Device: device,
	})
}

func normalizeAccelerationConfig(config AccelerationConfig) (AccelerationConfig, error) {
	if config.Mode == "" {
		config.Mode = AccelerationAuto
	}
	if config.Device == "" {
		config.Device = DefaultVAAPIDevice
	}

	if string(config.Mode) != strings.TrimSpace(string(config.Mode)) {
		return AccelerationConfig{}, errors.New("media: acceleration mode must not contain surrounding whitespace")
	}
	switch config.Mode {
	case AccelerationAuto, AccelerationCPU, AccelerationVAAPI:
	default:
		return AccelerationConfig{}, errors.New("media: acceleration mode must be auto, cpu, or vaapi")
	}

	if config.Device != strings.TrimSpace(config.Device) {
		return AccelerationConfig{}, errors.New("media: VA-API device must not contain surrounding whitespace")
	}
	if strings.ContainsRune(config.Device, 0) {
		return AccelerationConfig{}, errors.New("media: VA-API device contains a null byte")
	}
	if !filepath.IsAbs(config.Device) {
		return AccelerationConfig{}, errors.New("media: VA-API device must be an absolute path")
	}
	if filepath.Clean(config.Device) != config.Device {
		return AccelerationConfig{}, errors.New("media: VA-API device must be a clean path")
	}
	if err := validateLocalPath(config.Device); err != nil {
		return AccelerationConfig{}, errors.New("media: VA-API device must identify a local path")
	}
	return config, nil
}
