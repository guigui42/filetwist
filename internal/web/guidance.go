package web

import "github.com/guigui42/filetwist/internal/profiles"

// operationOption supplies the same profile explanation to selectors and help.
func operationOption(operation profiles.Operation) optionView {
	option := optionView{Value: string(operation), Label: string(operation)}
	if spec, ok := profiles.Lookup(operation); ok {
		option.Label = spec.Label
		option.Format = spec.Output.FormatLabel
	}
	switch operation {
	case profiles.OperationCompatiblePhoto:
		option.Description = "JPEG with lossy compression. Applies orientation and flattens transparency onto white."
	case profiles.OperationSmallerPhoto:
		option.Description = "WebP with stronger compression and supported transparency. A smaller file is not guaranteed."
	case profiles.OperationLosslessImage:
		option.Description = "PNG preserves supported transparency and decoded sample precision. Color and orientation can change; capture metadata is removed. This is not an original-file copy."
	case profiles.OperationCompatibleVideo:
		option.Description = "H.264 MP4 with selected audio converted to stereo AAC. Normalizes rotation; supported HDR becomes SDR. Extra tracks and subtitles are dropped."
	case profiles.OperationSmallerVideo:
		option.Description = "H.264 MP4 fitted within 1280 x 720, with stronger compression and stereo AAC when supported audio is present. Supported HDR becomes SDR; extra tracks and subtitles are dropped. A smaller file is not guaranteed."
	case profiles.OperationExtractAudio:
		option.Description = "M4A containing the selected audio track re-encoded to stereo 48 kHz AAC. This is not a bit-for-bit extraction; video is not included."
	case profiles.OperationCompatibleAudio:
		option.Description = "MP3 re-encoded to stereo 48 kHz at 192 kb/s. This changes the audio and can reduce quality."
	case profiles.OperationLosslessAudio:
		option.Description = "FLAC preserves the selected track's channel count and sample rate. Floating-point and greater-than-24-bit PCM are unsupported. It cannot restore quality already lost."
	}
	return option
}
