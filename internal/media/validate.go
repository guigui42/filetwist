package media

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ValidationCode identifies one output compatibility failure.
type ValidationCode string

const (
	// ValidationContainer reports an unexpected output container.
	ValidationContainer ValidationCode = "container"
	// ValidationCodec reports an unexpected audio or video codec.
	ValidationCodec ValidationCode = "codec"
	// ValidationStreamCount reports a missing or additional declared stream.
	ValidationStreamCount ValidationCode = "stream_count"
	// ValidationPixelFormat reports an incompatible video pixel format.
	ValidationPixelFormat ValidationCode = "pixel_format"
	// ValidationColor reports incompatible video color signaling.
	ValidationColor ValidationCode = "color"
	// ValidationDuration reports an output duration outside tolerance.
	ValidationDuration ValidationCode = "duration"
	// ValidationDimensions reports unexpected output dimensions.
	ValidationDimensions ValidationCode = "dimensions"
	// ValidationRotation reports unnormalized output rotation.
	ValidationRotation ValidationCode = "rotation"
	// ValidationFastStart reports a missing or late MP4 moov atom.
	ValidationFastStart ValidationCode = "faststart"
	// ValidationAudioPresence reports a violated audio presence policy.
	ValidationAudioPresence ValidationCode = "audio_presence"
	// ValidationChannelLayout reports an unexpected audio channel layout.
	ValidationChannelLayout ValidationCode = "channel_layout"
	// ValidationSampleRate reports an unexpected audio sample rate.
	ValidationSampleRate ValidationCode = "sample_rate"
	// ValidationMetadata reports known sensitive container or stream tags.
	ValidationMetadata ValidationCode = "metadata"
	// ValidationUndeclaredStream reports subtitle, data, attachment, unknown, or
	// surplus audio/video streams.
	ValidationUndeclaredStream ValidationCode = "undeclared_stream"
)

// ValidationIssue is one structured output incompatibility.
type ValidationIssue struct {
	Code     ValidationCode
	Field    string
	Expected string
	Actual   string
}

// ValidationError contains every incompatibility found in an output.
type ValidationError struct {
	Issues []ValidationIssue
}

// Error implements error.
func (err *ValidationError) Error() string {
	return fmt.Sprintf("media: output failed %d compatibility checks", len(err.Issues))
}

// ValidateProbe validates all output properties available through ffprobe.
func ValidateProbe(plan Plan, output Probe) error {
	issues := make([]ValidationIssue, 0)
	expected := plan.Expected
	metadata := DetectSensitiveMetadata(output)
	for _, category := range []struct {
		name    string
		present bool
	}{
		{"gps", metadata.GPS},
		{"exif", metadata.EXIF},
		{"xmp", metadata.XMP},
		{"gain_map", metadata.GainMap},
	} {
		if category.present {
			issues = append(issues, issue(ValidationMetadata, "metadata."+category.name, "absent", "present"))
		}
	}

	if !containerMatches(expected.Container, output.Format.FormatName) {
		issues = append(issues, issue(
			ValidationContainer,
			"format.format_name",
			expected.Container,
			output.Format.FormatName,
		))
	}

	var videoStreams, audioStreams []Stream
	for _, stream := range output.Streams {
		switch stream.CodecType {
		case "video":
			if stream.Disposition.AttachedPic != 0 {
				issues = append(issues, undeclaredIssue(stream))
				continue
			}
			videoStreams = append(videoStreams, stream)
		case "audio":
			audioStreams = append(audioStreams, stream)
		default:
			issues = append(issues, undeclaredIssue(stream))
		}
	}

	expectedVideoCount := 0
	if expected.VideoCodec != "" {
		expectedVideoCount = 1
	}
	if len(videoStreams) != expectedVideoCount {
		issues = append(issues, issue(
			ValidationStreamCount,
			"streams.video",
			strconv.Itoa(expectedVideoCount),
			strconv.Itoa(len(videoStreams)),
		))
	}
	for _, stream := range surplusStreams(videoStreams, expectedVideoCount) {
		issues = append(issues, undeclaredIssue(stream))
	}

	expectedAudioCount := 0
	if expected.AudioPresence == AudioRequired {
		expectedAudioCount = 1
	}
	if len(audioStreams) != expectedAudioCount {
		issues = append(issues, issue(
			ValidationAudioPresence,
			"streams.audio",
			string(expected.AudioPresence),
			strconv.Itoa(len(audioStreams)),
		))
	}
	for _, stream := range surplusStreams(audioStreams, expectedAudioCount) {
		issues = append(issues, undeclaredIssue(stream))
	}

	if expectedVideoCount == 1 && len(videoStreams) > 0 {
		video := videoStreams[0]
		if video.CodecName != expected.VideoCodec {
			issues = append(issues, issue(ValidationCodec, "streams.video.codec", expected.VideoCodec, video.CodecName))
		}
		if video.PixelFormat != expected.PixelFormat {
			issues = append(issues, issue(
				ValidationPixelFormat,
				"streams.video.pixel_format",
				expected.PixelFormat,
				video.PixelFormat,
			))
		}
		for _, color := range []struct {
			field    string
			expected string
			actual   string
		}{
			{field: "color_range", expected: expected.ColorRange, actual: video.ColorRange},
			{field: "color_space", expected: expected.ColorSpace, actual: video.ColorSpace},
			{field: "color_transfer", expected: expected.ColorTransfer, actual: video.ColorTransfer},
			{field: "color_primaries", expected: expected.ColorPrimaries, actual: video.ColorPrimaries},
		} {
			if color.expected != "" && color.actual != color.expected {
				issues = append(issues, issue(
					ValidationColor,
					"streams.video."+color.field,
					color.expected,
					color.actual,
				))
			}
		}
		if video.Width != expected.Width || video.Height != expected.Height {
			issues = append(issues, issue(
				ValidationDimensions,
				"streams.video.dimensions",
				fmt.Sprintf("%dx%d", expected.Width, expected.Height),
				fmt.Sprintf("%dx%d", video.Width, video.Height),
			))
		}
		if rotation := video.Rotation(); rotation != expected.Rotation {
			issues = append(issues, issue(
				ValidationRotation,
				"streams.video.rotation",
				strconv.Itoa(expected.Rotation),
				strconv.Itoa(rotation),
			))
		}
	}

	if expectedAudioCount == 1 && len(audioStreams) > 0 {
		audio := audioStreams[0]
		if audio.CodecName != expected.AudioCodec {
			issues = append(issues, issue(ValidationCodec, "streams.audio.codec", expected.AudioCodec, audio.CodecName))
		}
		if expected.Channels > 0 && audio.Channels != expected.Channels {
			issues = append(issues, issue(
				ValidationChannelLayout,
				"streams.audio.channels",
				strconv.Itoa(expected.Channels),
				strconv.Itoa(audio.Channels),
			))
		}
		if expected.ChannelLayout != "" && audio.ChannelLayout != expected.ChannelLayout {
			issues = append(issues, issue(
				ValidationChannelLayout,
				"streams.audio.channel_layout",
				expected.ChannelLayout,
				audio.ChannelLayout,
			))
		}
		sampleRate, _ := strconv.Atoi(audio.SampleRate)
		if expected.SampleRate > 0 && sampleRate != expected.SampleRate {
			issues = append(issues, issue(
				ValidationSampleRate,
				"streams.audio.sample_rate",
				strconv.Itoa(expected.SampleRate),
				audio.SampleRate,
			))
		}
	}

	actualDuration, actualDurationKnown := output.Format.Duration()
	if expected.DurationKnown {
		if !actualDurationKnown || actualDuration <= 0 ||
			durationDifference(actualDuration, expected.Duration) > expected.DurationTolerance {
			actualText := output.Format.DurationText
			if actualText == "" {
				actualText = "unknown"
			}
			issues = append(issues, issue(
				ValidationDuration,
				"format.duration",
				expected.Duration.String()+" +/- "+expected.DurationTolerance.String(),
				actualText,
			))
		}
	} else if !actualDurationKnown || actualDuration <= 0 {
		issues = append(issues, issue(
			ValidationDuration,
			"format.duration",
			"positive duration",
			output.Format.DurationText,
		))
	}

	if len(issues) > 0 {
		return &ValidationError{Issues: issues}
	}
	return nil
}

// ValidateOutput validates ffprobe properties and MP4 fast-start placement.
func ValidateOutput(plan Plan, output Probe) error {
	var issues []ValidationIssue
	if err := ValidateProbe(plan, output); err != nil {
		if validationErr, ok := err.(*ValidationError); ok {
			issues = append(issues, validationErr.Issues...)
		} else {
			return err
		}
	}

	if plan.Expected.FastStart {
		file, err := os.Open(plan.OutputPath)
		if err != nil {
			issues = append(issues, issue(ValidationFastStart, "file", "readable MP4", "unreadable"))
		} else {
			stat, statErr := file.Stat()
			if statErr != nil {
				issues = append(issues, issue(ValidationFastStart, "file", "readable MP4", "stat failed"))
			} else {
				fastStart, fastStartErr := HasFastStart(file, stat.Size())
				if fastStartErr != nil || !fastStart {
					issues = append(issues, issue(ValidationFastStart, "file.moov", "before mdat", "after or missing"))
				}
			}
			if closeErr := file.Close(); closeErr != nil && err == nil {
				issues = append(issues, issue(ValidationFastStart, "file", "clean close", "close failed"))
			}
		}
	}

	if len(issues) > 0 {
		return &ValidationError{Issues: issues}
	}
	return nil
}

func containerMatches(expected, actual string) bool {
	actualNames := strings.Split(actual, ",")
	accepted := map[string]map[string]struct{}{
		"mp4": {
			"mov": {}, "mp4": {}, "m4a": {}, "3gp": {}, "3g2": {}, "mj2": {},
		},
		"mp3":  {"mp3": {}},
		"flac": {"flac": {}},
	}
	for _, name := range actualNames {
		name = strings.TrimSpace(name)
		if name == expected {
			return true
		}
		if aliases, ok := accepted[expected]; ok {
			if _, ok := aliases[name]; ok {
				return true
			}
		}
	}
	return false
}

func surplusStreams(streams []Stream, expected int) []Stream {
	if len(streams) <= expected {
		return nil
	}
	return streams[expected:]
}

func undeclaredIssue(stream Stream) ValidationIssue {
	return issue(
		ValidationUndeclaredStream,
		fmt.Sprintf("streams[%d]", stream.Index),
		"no undeclared stream",
		stream.CodecType+":"+stream.CodecName,
	)
}

func durationDifference(first, second time.Duration) time.Duration {
	if first >= second {
		return first - second
	}
	return second - first
}

func issue(code ValidationCode, field, expected, actual string) ValidationIssue {
	return ValidationIssue{
		Code:     code,
		Field:    field,
		Expected: expected,
		Actual:   actual,
	}
}
