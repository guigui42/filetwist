package media

import "strings"

// WarningCode identifies a non-fatal stream-selection decision.
type WarningCode string

const (
	// WarningUnsupportedAudioCodec reports an audio stream outside the allowlist.
	WarningUnsupportedAudioCodec WarningCode = "unsupported_audio_codec"
	// WarningAdditionalAudioStream reports an allowlisted stream dropped after
	// the first usable audio stream was selected.
	WarningAdditionalAudioStream WarningCode = "additional_audio_stream"
)

// Warning describes one dropped audio stream.
type Warning struct {
	Code        WarningCode
	StreamIndex int
	Codec       string
	Message     string
}

// Selection contains the first usable video stream, the first allowlisted
// audio stream, and one warning for every dropped audio stream.
type Selection struct {
	Video    *Stream
	Audio    *Stream
	Warnings []Warning
}

var supportedAudioCodecs = map[string]struct{}{
	"aac":         {},
	"mp3":         {},
	"ac3":         {},
	"eac3":        {},
	"alac":        {},
	"flac":        {},
	"opus":        {},
	"vorbis":      {},
	"wmav1":       {},
	"wmav2":       {},
	"wmapro":      {},
	"wmalossless": {},
}

// SupportedAudioCodec reports whether codec is in the positive audio allowlist.
func SupportedAudioCodec(codec string) bool {
	codec = strings.ToLower(strings.TrimSpace(codec))
	if strings.HasPrefix(codec, "pcm_") && len(codec) > len("pcm_") {
		return true
	}
	_, ok := supportedAudioCodecs[codec]
	return ok
}

// SelectStreams applies the media stream policy without relying on metadata,
// attachment, data, subtitle, or unknown streams.
func SelectStreams(probe Probe) Selection {
	var selection Selection

	for index := range probe.Streams {
		stream := &probe.Streams[index]
		switch stream.CodecType {
		case "video":
			if selection.Video == nil && usableVideo(*stream) {
				selection.Video = stream
			}
		case "audio":
			if SupportedAudioCodec(stream.CodecName) && selection.Audio == nil {
				selection.Audio = stream
				continue
			}

			code := WarningUnsupportedAudioCodec
			message := "audio stream dropped because its codec is not supported"
			if SupportedAudioCodec(stream.CodecName) {
				code = WarningAdditionalAudioStream
				message = "audio stream dropped because another usable audio stream was selected first"
			}
			selection.Warnings = append(selection.Warnings, Warning{
				Code:        code,
				StreamIndex: stream.Index,
				Codec:       stream.CodecName,
				Message:     message,
			})
		}
	}

	return selection
}

func usableVideo(stream Stream) bool {
	return stream.Disposition.AttachedPic == 0 &&
		stream.Width > 0 &&
		stream.Height > 0 &&
		stream.CodecName != "" &&
		stream.CodecName != "unknown"
}
