package media

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/runner"
)

const defaultDurationTolerance = time.Second

// ErrorCode identifies a media planning rejection.
type ErrorCode string

const (
	// ErrorInvalidRequest reports missing paths or an unsupported operation.
	ErrorInvalidRequest ErrorCode = "invalid_request"
	// ErrorNoUsableVideo reports that a video profile has no usable video stream.
	ErrorNoUsableVideo ErrorCode = "no_usable_video"
	// ErrorNoSupportedAudio reports that an audio profile has no allowlisted stream.
	ErrorNoSupportedAudio ErrorCode = "no_supported_audio"
	// ErrorUnsupportedHDR identifies HDR inputs that cannot be safely processed.
	ErrorUnsupportedHDR ErrorCode = "unsupported_hdr"
	// ErrorUnsupportedLosslessAudio reports a source that the FLAC profile
	// cannot preserve without quantization.
	ErrorUnsupportedLosslessAudio ErrorCode = "unsupported_lossless_audio"
	// ErrorAccelerationUnavailable reports required VA-API acceleration that
	// did not pass its functional probe.
	ErrorAccelerationUnavailable ErrorCode = "acceleration_unavailable"
)

// PlanError is a structured, user-facing media planning rejection.
type PlanError struct {
	Code    ErrorCode
	Message string
}

// Error implements error.
func (err *PlanError) Error() string {
	return "media: " + err.Message
}

// AudioPresence defines whether validation requires an audio stream.
type AudioPresence string

const (
	// AudioRequired requires exactly one declared audio stream.
	AudioRequired AudioPresence = "required"
	// AudioForbidden requires no audio streams.
	AudioForbidden AudioPresence = "forbidden"
)

// PlanRequest contains the information needed to build one FFmpeg argv plan.
type PlanRequest struct {
	Operation         corpus.Operation
	InputPath         string
	OutputPath        string
	Input             Probe
	Timeout           time.Duration
	DurationTolerance time.Duration
	Acceleration      AccelerationConfig
	VAAPI             *VAAPIReadiness
}

// ExpectedProfile is the complete output contract validated after conversion.
type ExpectedProfile struct {
	Container         string
	VideoCodec        string
	AudioCodec        string
	PixelFormat       string
	ColorRange        string
	ColorSpace        string
	ColorTransfer     string
	ColorPrimaries    string
	Width             int
	Height            int
	Rotation          int
	AudioPresence     AudioPresence
	Channels          int
	ChannelLayout     string
	SampleRate        int
	Duration          time.Duration
	DurationKnown     bool
	DurationTolerance time.Duration
	FastStart         bool
}

// Plan is a direct FFmpeg argv invocation and its output contract.
type Plan struct {
	Operation       corpus.Operation
	InputPath       string
	OutputPath      string
	Args            []string
	Timeout         time.Duration
	Warnings        []Warning
	RequiredFilters []string
	Expected        ExpectedProfile
	// RequestedAcceleration is the normalized acceleration policy.
	RequestedAcceleration AccelerationMode
	// ExecutionPath is the initially planned CPU or VA-API path.
	ExecutionPath ExecutionPath
	// FallbackReason explains a planned automatic CPU selection.
	FallbackReason FallbackReason
	cpuArgs        []string
}

// Command converts the plan to the shared runner contract without a shell.
func (plan Plan) Command(executable string) runner.Command {
	args := make([]string, len(plan.Args))
	copy(args, plan.Args)
	return runner.Command{
		Path:    executable,
		Args:    args,
		Timeout: plan.Timeout,
	}
}

// BuildPlan creates a CPU or probed VA-API FFmpeg profile with explicit stream
// maps and one shared output compatibility contract.
func BuildPlan(request PlanRequest) (Plan, error) {
	if request.InputPath == "" || request.OutputPath == "" {
		return Plan{}, &PlanError{
			Code:    ErrorInvalidRequest,
			Message: "input and output paths must not be empty",
		}
	}
	if err := validateLocalPath(request.InputPath); err != nil {
		return Plan{}, &PlanError{
			Code:    ErrorInvalidRequest,
			Message: "input path must identify a local file",
		}
	}
	if err := validateLocalPath(request.OutputPath); err != nil {
		return Plan{}, &PlanError{
			Code:    ErrorInvalidRequest,
			Message: "output path must identify a local file",
		}
	}
	if request.Timeout < 0 || request.DurationTolerance < 0 {
		return Plan{}, &PlanError{
			Code:    ErrorInvalidRequest,
			Message: "timeouts and tolerances must not be negative",
		}
	}
	acceleration, err := normalizeAccelerationConfig(request.Acceleration)
	if err != nil {
		return Plan{}, &PlanError{
			Code:    ErrorInvalidRequest,
			Message: strings.TrimPrefix(err.Error(), "media: "),
		}
	}

	selection := SelectStreams(request.Input)
	tolerance := request.DurationTolerance
	if tolerance == 0 {
		tolerance = defaultDurationTolerance
	}
	plan := Plan{
		Operation:  request.Operation,
		InputPath:  request.InputPath,
		OutputPath: request.OutputPath,
		Timeout:    request.Timeout,
		Warnings:   append([]Warning(nil), selection.Warnings...),
		Expected: ExpectedProfile{
			DurationTolerance: tolerance,
		},
		RequestedAcceleration: acceleration.Mode,
		ExecutionPath:         ExecutionPathCPU,
	}

	switch request.Operation {
	case corpus.OperationCompatibleVideo:
		if err := requireVideo(selection); err != nil {
			return Plan{}, err
		}
		if isHDR(*selection.Video) && !supportsHDRToneMapping(*selection.Video) {
			return Plan{}, &PlanError{
				Code:    ErrorUnsupportedHDR,
				Message: "HDR video uses color signaling that the tone-mapping profile does not support",
			}
		}
		if isHDR(*selection.Video) {
			plan.RequiredFilters = []string{"zscale", "tonemap"}
		}
		plan.Expected.Duration, plan.Expected.DurationKnown = videoDuration(request.Input, selection)
		plan.buildVideo(selection, false)
		if err := plan.applyVideoAcceleration(acceleration, request.VAAPI, selection, false); err != nil {
			return Plan{}, err
		}
	case corpus.OperationSmallerVideo:
		if err := requireVideo(selection); err != nil {
			return Plan{}, err
		}
		if isHDR(*selection.Video) && !supportsHDRToneMapping(*selection.Video) {
			return Plan{}, &PlanError{
				Code:    ErrorUnsupportedHDR,
				Message: "HDR video uses color signaling that the tone-mapping profile does not support",
			}
		}
		if isHDR(*selection.Video) {
			plan.RequiredFilters = []string{"zscale", "tonemap"}
		}
		plan.Expected.Duration, plan.Expected.DurationKnown = videoDuration(request.Input, selection)
		plan.buildVideo(selection, true)
		if err := plan.applyVideoAcceleration(acceleration, request.VAAPI, selection, true); err != nil {
			return Plan{}, err
		}
	case corpus.OperationExtractAudio:
		if err := requireAudio(selection); err != nil {
			return Plan{}, err
		}
		plan.Expected.Duration, plan.Expected.DurationKnown = audioDuration(request.Input, selection)
		plan.buildAACAudio(selection)
	case corpus.OperationCompatibleAudio:
		if err := requireAudio(selection); err != nil {
			return Plan{}, err
		}
		plan.Expected.Duration, plan.Expected.DurationKnown = audioDuration(request.Input, selection)
		plan.buildMP3Audio(selection)
	case corpus.OperationLosslessAudio:
		if err := requireAudio(selection); err != nil {
			return Plan{}, err
		}
		if lossyFLACSource(*selection.Audio) {
			return Plan{}, &PlanError{
				Code:    ErrorUnsupportedLosslessAudio,
				Message: "the FLAC profile cannot preserve floating-point PCM or samples above 24 bits exactly",
			}
		}
		plan.Expected.Duration, plan.Expected.DurationKnown = audioDuration(request.Input, selection)
		plan.buildFLACAudio(selection)
	default:
		return Plan{}, &PlanError{
			Code:    ErrorInvalidRequest,
			Message: fmt.Sprintf("operation %q is not an audio or video CPU profile", request.Operation),
		}
	}

	return plan, nil
}

func (plan *Plan) applyVideoAcceleration(
	config AccelerationConfig,
	readiness *VAAPIReadiness,
	selection Selection,
	smaller bool,
) error {
	if config.Mode == AccelerationCPU {
		return nil
	}
	if readiness == nil || !readiness.Ready || !readiness.FunctionalProbeComplete ||
		readiness.Device != config.Device {
		reason := FallbackVAAPIProbeUnavailable
		if readiness != nil && readiness.UnavailableReason != FallbackNone {
			reason = readiness.UnavailableReason
		}
		if config.Mode == AccelerationVAAPI {
			return &PlanError{
				Code:    ErrorAccelerationUnavailable,
				Message: "VA-API acceleration requires a successful functional probe for the configured device",
			}
		}
		plan.FallbackReason = reason
		return nil
	}

	plan.cpuArgs = append([]string(nil), plan.Args...)
	hardwareDecode := selection.Video != nil &&
		!isHDR(*selection.Video) &&
		selection.Video.Rotation() == 0 &&
		selection.Video.PixelFormat == "yuv420p" &&
		readiness.SupportsHardwareDecode(selection.Video.CodecName)
	plan.Args = plan.buildVAAPIVideoArgs(selection, smaller, config.Device, hardwareDecode)
	if hardwareDecode {
		plan.ExecutionPath = ExecutionPathVAAPIHardwareDecode
	} else {
		plan.ExecutionPath = ExecutionPathVAAPISoftwareDecode
	}
	return nil
}

func (plan *Plan) buildVAAPIVideoArgs(
	selection Selection,
	smaller bool,
	device string,
	hardwareDecode bool,
) []string {
	video := selection.Video
	width, height := displayedDimensions(*video)
	qp := "20"
	audioBitrate := "192k"
	if smaller {
		width, height = fitDimensions(width, height, 1280, 720)
		qp = "28"
		audioBitrate = "128k"
	} else {
		width = evenFloor(width)
		height = evenFloor(height)
	}

	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-nostdin",
		"-y",
		"-progress", "pipe:1",
		"-nostats",
		"-init_hw_device", "vaapi=filetwist:" + device,
		"-filter_hw_device", "filetwist",
	}
	if hardwareDecode {
		args = append(args,
			"-hwaccel", "vaapi",
			"-hwaccel_device", "filetwist",
			"-hwaccel_output_format", "vaapi",
		)
	}
	args = append(args,
		"-protocol_whitelist", "file,pipe",
		"-i", plan.InputPath,
		"-map", streamMap(video.Index),
	)
	if selection.Audio != nil {
		args = append(args, "-map", streamMap(selection.Audio.Index))
	}
	args = append(args,
		"-map_metadata", "-1",
		"-map_metadata:s", "-1",
		"-map_chapters", "-1",
		"-sn",
		"-dn",
		"-c:v", "h264_vaapi",
		"-qp", qp,
	)
	hdr := isHDR(*video)
	if hardwareDecode {
		args = append(args, "-vf", fmt.Sprintf(
			"scale_vaapi=w=%d:h=%d:format=nv12",
			width,
			height,
		))
	} else {
		args = append(args, "-vf", softwareVideoFilter(width, height, "nv12", hdr)+",hwupload")
	}
	if hdr {
		args = appendSDRColorMetadata(args)
	}
	if selection.Audio != nil {
		args = append(args,
			"-c:a", "aac",
			"-b:a", audioBitrate,
			"-ac", "2",
			"-ar", "48000",
		)
	}
	return append(args,
		"-movflags", "+faststart",
		"-f", "mp4",
		plan.OutputPath,
	)
}

func (plan *Plan) buildVideo(selection Selection, smaller bool) {
	video := selection.Video
	width, height := displayedDimensions(*video)
	crf := "20"
	audioBitrate := "192k"
	if smaller {
		width, height = fitDimensions(width, height, 1280, 720)
		crf = "28"
		audioBitrate = "128k"
	} else {
		width = evenFloor(width)
		height = evenFloor(height)
	}

	hdr := isHDR(*video)
	args := inputArgs(plan.InputPath)
	args = append(args, "-map", streamMap(video.Index))
	audioPresence := AudioForbidden
	audioCodec := ""
	if selection.Audio != nil {
		args = append(args, "-map", streamMap(selection.Audio.Index))
		audioPresence = AudioRequired
		audioCodec = "aac"
	}
	args = append(args,
		"-map_metadata", "-1",
		"-map_metadata:s", "-1",
		"-map_chapters", "-1",
		"-sn",
		"-dn",
		"-c:v", "libx264",
		"-preset", "medium",
		"-crf", crf,
		"-vf", softwareVideoFilter(width, height, "yuv420p", hdr),
		"-pix_fmt", "yuv420p",
	)
	if hdr {
		args = appendSDRColorMetadata(args)
	}
	if selection.Audio != nil {
		args = append(args,
			"-c:a", "aac",
			"-b:a", audioBitrate,
			"-ac", "2",
			"-ar", "48000",
		)
	}
	args = append(args,
		"-movflags", "+faststart",
		"-f", "mp4",
		plan.OutputPath,
	)

	plan.Args = args
	plan.Expected = ExpectedProfile{
		Container:         "mp4",
		VideoCodec:        "h264",
		AudioCodec:        audioCodec,
		PixelFormat:       "yuv420p",
		Width:             width,
		Height:            height,
		Rotation:          0,
		AudioPresence:     audioPresence,
		Channels:          conditionalInt(selection.Audio != nil, 2),
		ChannelLayout:     conditionalString(selection.Audio != nil, "stereo"),
		SampleRate:        conditionalInt(selection.Audio != nil, 48000),
		Duration:          plan.Expected.Duration,
		DurationKnown:     plan.Expected.DurationKnown,
		DurationTolerance: plan.Expected.DurationTolerance,
		FastStart:         true,
	}
	if hdr {
		setExpectedSDRColor(&plan.Expected)
	}
}

func softwareVideoFilter(width, height int, pixelFormat string, hdr bool) string {
	scale := fmt.Sprintf("scale=%d:%d", width, height)
	if !hdr {
		if pixelFormat == "yuv420p" {
			return scale
		}
		return scale + ",format=" + pixelFormat
	}
	return "zscale=t=linear:npl=100,format=gbrpf32le," +
		"tonemap=hable:desat=0," +
		"zscale=p=bt709:t=bt709:m=bt709:r=tv," +
		scale + ",format=" + pixelFormat
}

func appendSDRColorMetadata(args []string) []string {
	return append(args,
		"-color_primaries", "bt709",
		"-color_trc", "bt709",
		"-colorspace", "bt709",
		"-color_range", "tv",
	)
}

func setExpectedSDRColor(expected *ExpectedProfile) {
	expected.ColorRange = "tv"
	expected.ColorSpace = "bt709"
	expected.ColorTransfer = "bt709"
	expected.ColorPrimaries = "bt709"
}

func (plan *Plan) buildAACAudio(selection Selection) {
	plan.Args = append(inputArgs(plan.InputPath),
		"-map", streamMap(selection.Audio.Index),
		"-map_metadata", "-1",
		"-map_metadata:s", "-1",
		"-map_chapters", "-1",
		"-vn",
		"-sn",
		"-dn",
		"-c:a", "aac",
		"-b:a", "192k",
		"-ac", "2",
		"-ar", "48000",
		"-movflags", "+faststart",
		"-f", "mp4",
		plan.OutputPath,
	)
	plan.Expected = ExpectedProfile{
		Container:         "mp4",
		AudioCodec:        "aac",
		AudioPresence:     AudioRequired,
		Channels:          2,
		ChannelLayout:     "stereo",
		SampleRate:        48000,
		Duration:          plan.Expected.Duration,
		DurationKnown:     plan.Expected.DurationKnown,
		DurationTolerance: plan.Expected.DurationTolerance,
		FastStart:         true,
	}
}

func (plan *Plan) buildMP3Audio(selection Selection) {
	plan.Args = append(inputArgs(plan.InputPath),
		"-map", streamMap(selection.Audio.Index),
		"-map_metadata", "-1",
		"-map_metadata:s", "-1",
		"-map_chapters", "-1",
		"-vn",
		"-sn",
		"-dn",
		"-c:a", "libmp3lame",
		"-b:a", "192k",
		"-ac", "2",
		"-ar", "48000",
		"-f", "mp3",
		plan.OutputPath,
	)
	plan.Expected = ExpectedProfile{
		Container:         "mp3",
		AudioCodec:        "mp3",
		AudioPresence:     AudioRequired,
		Channels:          2,
		ChannelLayout:     "stereo",
		SampleRate:        48000,
		Duration:          plan.Expected.Duration,
		DurationKnown:     plan.Expected.DurationKnown,
		DurationTolerance: plan.Expected.DurationTolerance,
	}
}

func (plan *Plan) buildFLACAudio(selection Selection) {
	plan.Args = append(inputArgs(plan.InputPath),
		"-map", streamMap(selection.Audio.Index),
		"-map_metadata", "-1",
		"-map_metadata:s", "-1",
		"-map_chapters", "-1",
		"-vn",
		"-sn",
		"-dn",
		"-c:a", "flac",
		"-compression_level", "8",
		"-f", "flac",
		plan.OutputPath,
	)
	sampleRate, _ := strconv.Atoi(selection.Audio.SampleRate)
	plan.Expected = ExpectedProfile{
		Container:         "flac",
		AudioCodec:        "flac",
		AudioPresence:     AudioRequired,
		Channels:          selection.Audio.Channels,
		ChannelLayout:     selection.Audio.ChannelLayout,
		SampleRate:        sampleRate,
		Duration:          plan.Expected.Duration,
		DurationKnown:     plan.Expected.DurationKnown,
		DurationTolerance: plan.Expected.DurationTolerance,
	}
}

func inputArgs(path string) []string {
	return []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-nostdin",
		"-y",
		"-progress", "pipe:1",
		"-nostats",
		"-protocol_whitelist", "file,pipe",
		"-i", path,
	}
}

func requireVideo(selection Selection) error {
	if selection.Video == nil {
		return &PlanError{
			Code:    ErrorNoUsableVideo,
			Message: "input does not contain a usable video stream",
		}
	}
	return nil
}

func requireAudio(selection Selection) error {
	if selection.Audio == nil {
		return &PlanError{
			Code:    ErrorNoSupportedAudio,
			Message: "input does not contain an allowlisted audio stream",
		}
	}
	return nil
}

func streamMap(index int) string {
	return "0:" + strconv.Itoa(index)
}

func displayedDimensions(video Stream) (int, int) {
	if rotation := video.Rotation(); rotation == 90 || rotation == 270 {
		return video.Height, video.Width
	}
	return video.Width, video.Height
}

func evenFloor(value int) int {
	if value <= 2 {
		return 2
	}
	return value - value%2
}

func fitDimensions(width, height, maxWidth, maxHeight int) (int, int) {
	scale := math.Min(1, math.Min(float64(maxWidth)/float64(width), float64(maxHeight)/float64(height)))
	return nearestEven(float64(width) * scale),
		nearestEven(float64(height) * scale)
}

func nearestEven(value float64) int {
	return max(2, int(math.Round(value/2))*2)
}

func videoDuration(probe Probe, selection Selection) (time.Duration, bool) {
	var (
		duration time.Duration
		known    bool
	)
	if selection.Video != nil {
		if streamDuration, ok := selection.Video.Duration(); ok {
			duration = streamDuration
			known = true
		}
	}
	if selection.Audio != nil {
		if streamDuration, ok := selection.Audio.Duration(); ok && (!known || streamDuration > duration) {
			duration = streamDuration
			known = true
		}
	}
	if known {
		return duration, true
	}
	return probe.Format.Duration()
}

func audioDuration(probe Probe, selection Selection) (time.Duration, bool) {
	if selection.Audio != nil {
		if duration, ok := selection.Audio.Duration(); ok {
			return duration, true
		}
	}
	return probe.Format.Duration()
}

func isHDR(stream Stream) bool {
	switch stream.ColorTransfer {
	case "smpte2084", "arib-std-b67":
		return true
	}
	return stream.ColorPrimaries == "bt2020"
}

func supportsHDRToneMapping(stream Stream) bool {
	switch stream.ColorTransfer {
	case "smpte2084", "arib-std-b67":
		return stream.ColorPrimaries == "bt2020" && stream.ColorSpace == "bt2020nc"
	default:
		return false
	}
}

func lossyFLACSource(stream Stream) bool {
	if strings.HasPrefix(stream.CodecName, "pcm_f") ||
		strings.HasPrefix(stream.CodecName, "pcm_s64") ||
		strings.HasPrefix(stream.CodecName, "pcm_u64") {
		return true
	}
	bits, _ := strconv.Atoi(stream.BitsPerRawSample)
	if bits > 24 {
		return true
	}
	return bits <= 0 && (strings.HasPrefix(stream.CodecName, "pcm_s32") ||
		strings.HasPrefix(stream.CodecName, "pcm_u32"))
}

func conditionalInt(condition bool, value int) int {
	if condition {
		return value
	}
	return 0
}

func conditionalString(condition bool, value string) string {
	if condition {
		return value
	}
	return ""
}
