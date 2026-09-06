//go:build ignore

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/guigui42/filetwist/internal/corpus"
)

const (
	generatorCommand = "go run ./fixtures/generate.go"
	maxFixtureSize   = 64 * 1024
)

var deterministicFiles = []string{
	"animated-2x2.gif",
	"iphone-spatial-apac.ffprobe.json",
	"opaque-3x2.jpg",
	"oriented-gps-3x2.jpg",
	"rgba-2x2.png",
	"tone-8khz-mono.wav",
}

var ffmpegFiles = []string{
	"tone-44khz-mono.flac",
	"tone-44khz-stereo.mp3",
	"tone-48khz-mono.opus",
	"tone-48khz-stereo.m4a",
	"h264-aac-32x24.mp4",
	"h264-silent-32x24.mp4",
	"h264-rotated-32x24.mp4",
	"h264-odd-17x15.mkv",
	"h264-aac-subtitle.mkv",
	"vp9-opus-32x24.webm",
	"hevc-aac-32x24.mp4",
}

func main() {
	output := flag.String("output", filepath.Join("fixtures", "generated"), "generated fixture directory")
	manifestPath := flag.String("manifest", filepath.Join("fixtures", "manifest.json"), "fixture manifest path")
	ffmpeg := flag.String("ffmpeg", "ffmpeg", "FFmpeg executable")
	ffprobe := flag.String("ffprobe", "ffprobe", "ffprobe executable")
	check := flag.Bool("check", false, "verify committed fixtures can be reproduced")
	flag.Parse()

	var err error
	if *check {
		err = checkFixtures(*output, *manifestPath, *ffmpeg, *ffprobe)
	} else {
		err = generate(*output, *manifestPath, *ffmpeg)
	}
	if err != nil {
		fatal(err)
	}
}

func generate(output, manifestPath, ffmpeg string) error {
	if err := generateArtifacts(output, ffmpeg); err != nil {
		return err
	}
	return writeManifest(manifestPath)
}

func generateArtifacts(output, ffmpeg string) error {
	if err := os.MkdirAll(output, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	files := map[string][]byte{
		"rgba-2x2.png":                     encodePNG(),
		"opaque-3x2.jpg":                   encodeJPEG(false),
		"oriented-gps-3x2.jpg":             encodeJPEG(true),
		"animated-2x2.gif":                 encodeGIF(),
		"tone-8khz-mono.wav":               encodeWAV(),
		"iphone-spatial-apac.ffprobe.json": []byte(iphoneSpatialProbe),
	}
	for name, data := range files {
		if err := write(filepath.Join(output, name), data); err != nil {
			return err
		}
	}

	temp, err := os.MkdirTemp("", "filetwist-fixtures-*")
	if err != nil {
		return fmt.Errorf("create generator work directory: %w", err)
	}
	defer os.RemoveAll(temp)

	subtitlePath := filepath.Join(temp, "synthetic.srt")
	if err := write(subtitlePath, []byte("1\n00:00:00,000 --> 00:00:00,400\nsynthetic subtitle\n")); err != nil {
		return err
	}

	if err := generateAudioFixtures(output, ffmpeg); err != nil {
		return err
	}
	return generateVideoFixtures(output, subtitlePath, ffmpeg)
}

func generateAudioFixtures(output, ffmpeg string) error {
	commands := []struct {
		name string
		args []string
	}{
		{
			name: "tone-44khz-mono.flac",
			args: []string{
				"-f", "lavfi", "-i", "sine=frequency=550:sample_rate=44100:duration=0.25",
				"-map", "0:a:0", "-c:a", "flac", "-compression_level", "8",
				"-sample_fmt", "s16", "-map_metadata", "-1", "-map_chapters", "-1",
				"-fflags", "+bitexact", "-flags:a", "+bitexact",
			},
		},
		{
			name: "tone-44khz-stereo.mp3",
			args: []string{
				"-f", "lavfi", "-i", "sine=frequency=660:sample_rate=44100:duration=0.25",
				"-map", "0:a:0", "-c:a", "libmp3lame", "-b:a", "64k", "-ac", "2",
				"-ar", "44100", "-map_metadata", "-1", "-map_chapters", "-1",
				"-fflags", "+bitexact", "-flags:a", "+bitexact",
			},
		},
		{
			name: "tone-48khz-mono.opus",
			args: []string{
				"-f", "lavfi", "-i", "sine=frequency=770:sample_rate=48000:duration=0.25",
				"-map", "0:a:0", "-c:a", "libopus", "-b:a", "32k", "-ac", "1",
				"-ar", "48000", "-map_metadata", "-1", "-map_chapters", "-1",
				"-fflags", "+bitexact", "-flags:a", "+bitexact",
			},
		},
		{
			name: "tone-48khz-stereo.m4a",
			args: []string{
				"-f", "lavfi", "-i", "sine=frequency=880:sample_rate=48000:duration=0.25",
				"-map", "0:a:0", "-c:a", "aac", "-b:a", "48k", "-ac", "2",
				"-ar", "48000", "-map_metadata", "-1", "-map_chapters", "-1",
				"-movflags", "+faststart", "-fflags", "+bitexact", "-flags:a", "+bitexact",
			},
		},
	}

	for _, command := range commands {
		if err := runFFmpeg(ffmpeg, command.args, filepath.Join(output, command.name)); err != nil {
			return fmt.Errorf("generate %s: %w", command.name, err)
		}
	}
	return nil
}

func generateVideoFixtures(output, subtitlePath, ffmpeg string) error {
	videoInput := []string{
		"-f", "lavfi", "-i", "testsrc2=size=32x24:rate=8:duration=0.5",
	}
	audioInput := []string{
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=0.5",
	}
	h264 := []string{
		"-c:v", "libx264", "-preset", "veryslow", "-crf", "28",
		"-pix_fmt", "yuv420p", "-threads", "1", "-flags:v", "+bitexact",
	}
	aac := []string{
		"-c:a", "aac", "-b:a", "32k", "-ac", "1", "-ar", "48000",
		"-flags:a", "+bitexact",
	}
	clean := []string{
		"-map_metadata", "-1", "-map_chapters", "-1", "-fflags", "+bitexact",
	}

	h264AACPath := filepath.Join(output, "h264-aac-32x24.mp4")
	args := append([]string{}, videoInput...)
	args = append(args, audioInput...)
	args = append(args, "-map", "0:v:0", "-map", "1:a:0")
	args = append(args, h264...)
	args = append(args, aac...)
	args = append(args, "-shortest")
	args = append(args, clean...)
	args = append(args, "-movflags", "+faststart")
	if err := runFFmpeg(ffmpeg, args, h264AACPath); err != nil {
		return fmt.Errorf("generate h264-aac-32x24.mp4: %w", err)
	}

	args = append([]string{}, videoInput...)
	args = append(args, "-map", "0:v:0")
	args = append(args, h264...)
	args = append(args, "-an")
	args = append(args, clean...)
	args = append(args, "-movflags", "+faststart")
	if err := runFFmpeg(ffmpeg, args, filepath.Join(output, "h264-silent-32x24.mp4")); err != nil {
		return fmt.Errorf("generate h264-silent-32x24.mp4: %w", err)
	}

	args = []string{
		"-display_rotation:v:0", "90",
		"-i", h264AACPath,
		"-map", "0", "-c", "copy",
		"-map_metadata", "-1", "-map_chapters", "-1",
		"-movflags", "+faststart", "-fflags", "+bitexact",
	}
	if err := runFFmpeg(ffmpeg, args, filepath.Join(output, "h264-rotated-32x24.mp4")); err != nil {
		return fmt.Errorf("generate h264-rotated-32x24.mp4: %w", err)
	}

	args = []string{
		"-f", "lavfi", "-i", "testsrc=size=17x15:rate=8:duration=0.5",
		"-map", "0:v:0", "-c:v", "libx264", "-preset", "veryslow", "-crf", "28",
		"-pix_fmt", "yuv444p", "-threads", "1", "-an",
		"-map_metadata", "-1", "-map_chapters", "-1",
		"-fflags", "+bitexact", "-flags:v", "+bitexact",
	}
	if err := runFFmpeg(ffmpeg, args, filepath.Join(output, "h264-odd-17x15.mkv")); err != nil {
		return fmt.Errorf("generate h264-odd-17x15.mkv: %w", err)
	}

	args = append([]string{}, videoInput...)
	args = append(args, audioInput...)
	args = append(args, "-f", "srt", "-i", subtitlePath)
	args = append(args, "-map", "0:v:0", "-map", "1:a:0", "-map", "2:s:0")
	args = append(args, h264...)
	args = append(args, aac...)
	args = append(args,
		"-c:s", "srt", "-shortest",
		"-map_metadata", "-1", "-map_chapters", "-1",
		"-fflags", "+bitexact",
	)
	if err := runFFmpeg(ffmpeg, args, filepath.Join(output, "h264-aac-subtitle.mkv")); err != nil {
		return fmt.Errorf("generate h264-aac-subtitle.mkv: %w", err)
	}

	args = append([]string{}, videoInput...)
	args = append(args, []string{
		"-f", "lavfi", "-i", "sine=frequency=660:sample_rate=48000:duration=0.5",
		"-map", "0:v:0", "-map", "1:a:0",
		"-c:v", "libvpx-vp9", "-deadline", "best", "-cpu-used", "0",
		"-row-mt", "0", "-threads", "1", "-b:v", "40k", "-pix_fmt", "yuv420p",
		"-c:a", "libopus", "-b:a", "24k", "-ac", "1", "-ar", "48000",
		"-shortest", "-map_metadata", "-1", "-map_chapters", "-1",
		"-fflags", "+bitexact", "-flags:v", "+bitexact", "-flags:a", "+bitexact",
	}...)
	if err := runFFmpeg(ffmpeg, args, filepath.Join(output, "vp9-opus-32x24.webm")); err != nil {
		return fmt.Errorf("generate vp9-opus-32x24.webm: %w", err)
	}

	args = append([]string{}, videoInput...)
	args = append(args, []string{
		"-f", "lavfi", "-i", "sine=frequency=770:sample_rate=48000:duration=0.5",
		"-map", "0:v:0", "-map", "1:a:0",
		"-c:v", "libx265", "-preset", "slower",
		"-x265-params", "pools=none:frame-threads=1:log-level=error",
		"-crf", "30", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "32k", "-ac", "1", "-ar", "48000",
		"-shortest", "-map_metadata", "-1", "-map_chapters", "-1",
		"-movflags", "+faststart", "-fflags", "+bitexact",
		"-flags:v", "+bitexact", "-flags:a", "+bitexact",
	}...)
	if err := runFFmpeg(ffmpeg, args, filepath.Join(output, "hevc-aac-32x24.mp4")); err != nil {
		return fmt.Errorf("generate hevc-aac-32x24.mp4: %w", err)
	}

	return nil
}

func runFFmpeg(executable string, args []string, output string) error {
	fullArgs := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}
	fullArgs = append(fullArgs, args...)
	fullArgs = append(fullArgs, output)
	command := exec.Command(executable, fullArgs...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func writeManifest(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create manifest: %w", err)
	}
	if err := corpus.EncodeManifest(file, manifest()); err != nil {
		_ = file.Close()
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close manifest: %w", err)
	}
	return nil
}

func manifest() corpus.Manifest {
	return corpus.Manifest{
		SchemaVersion: corpus.ManifestVersion,
		Fixtures: []corpus.Fixture{
			imageFixture(
				"generated-rgba-png",
				"Generated 2x2 transparent RGBA PNG",
				"generated/rgba-2x2.png",
				"png",
				"png",
				2,
				2,
				corpus.PresenceRequired,
				corpus.OperationLosslessImage,
				"png",
				"png",
				2,
				2,
				corpus.PresenceRequired,
				[]string{"transparency"},
			),
			imageFixture(
				"generated-opaque-jpeg",
				"Generated 3x2 opaque JPEG",
				"generated/opaque-3x2.jpg",
				"jpeg",
				"mjpeg",
				3,
				2,
				corpus.PresenceForbidden,
				corpus.OperationCompatiblePhoto,
				"jpeg",
				"mjpeg",
				3,
				2,
				corpus.PresenceForbidden,
				[]string{"odd-dimensions", "opaque"},
			),
			orientedJPEGFixture(),
			animatedGIFFixture(),
			audioFixture(
				"generated-mono-wav",
				"Generated 100ms mono PCM WAV",
				"generated/tone-8khz-mono.wav",
				".wav",
				"audio/wav",
				"wav",
				"pcm_s16le",
				1,
				8000,
				100,
				corpus.OperationLosslessAudio,
				"flac",
				"flac",
				1,
				8000,
				5,
				[]string{"low-sample-rate", "mono-audio"},
			),
			audioFixture(
				"generated-mono-flac",
				"Generated 250ms mono FLAC",
				"generated/tone-44khz-mono.flac",
				".flac",
				"audio/flac",
				"flac",
				"flac",
				1,
				44100,
				250,
				corpus.OperationCompatibleAudio,
				"mp3",
				"mp3",
				2,
				48000,
				80,
				[]string{"flac-input", "mono-audio"},
			),
			audioFixture(
				"generated-stereo-mp3",
				"Generated 250ms stereo MP3",
				"generated/tone-44khz-stereo.mp3",
				".mp3",
				"audio/mpeg",
				"mp3",
				"mp3",
				2,
				44100,
				250,
				corpus.OperationCompatibleAudio,
				"mp3",
				"mp3",
				2,
				48000,
				100,
				[]string{"lossy-audio", "mp3-input"},
			),
			audioFixture(
				"generated-mono-opus",
				"Generated 250ms mono Opus",
				"generated/tone-48khz-mono.opus",
				".opus",
				"audio/ogg",
				"ogg",
				"opus",
				1,
				48000,
				256,
				corpus.OperationCompatibleAudio,
				"mp3",
				"mp3",
				2,
				48000,
				100,
				[]string{"mono-audio", "opus-input"},
			),
			audioFixture(
				"generated-stereo-aac",
				"Generated 250ms stereo AAC in M4A",
				"generated/tone-48khz-stereo.m4a",
				".m4a",
				"audio/mp4",
				"mp4",
				"aac",
				2,
				48000,
				250,
				corpus.OperationLosslessAudio,
				"flac",
				"flac",
				2,
				48000,
				80,
				[]string{"aac-input", "lossy-audio"},
			),
			videoFixture(
				"generated-h264-aac-mp4",
				"Generated H.264 MP4 with AAC",
				"generated/h264-aac-32x24.mp4",
				".mp4",
				"video/mp4",
				"mp4",
				"h264",
				"yuv420p",
				32,
				24,
				0,
				"aac",
				500,
				corpus.OperationCompatibleVideo,
				32,
				24,
				true,
				[]string{"aac-audio", "cpu-vaapi-input", "h264-input"},
				nil,
			),
			videoFixture(
				"generated-h264-silent-mp4",
				"Generated silent H.264 MP4",
				"generated/h264-silent-32x24.mp4",
				".mp4",
				"video/mp4",
				"mp4",
				"h264",
				"yuv420p",
				32,
				24,
				0,
				"",
				500,
				corpus.OperationCompatibleVideo,
				32,
				24,
				false,
				[]string{"h264-input", "no-audio"},
				nil,
			),
			videoFixture(
				"generated-h264-rotated-mp4",
				"Generated H.264 MP4 with 90-degree display rotation",
				"generated/h264-rotated-32x24.mp4",
				".mp4",
				"video/mp4",
				"mp4",
				"h264",
				"yuv420p",
				32,
				24,
				90,
				"aac",
				500,
				corpus.OperationCompatibleVideo,
				24,
				32,
				true,
				[]string{"display-matrix", "rotation"},
				nil,
			),
			videoFixture(
				"generated-h264-odd-mkv",
				"Generated odd-dimension H.264 Matroska video",
				"generated/h264-odd-17x15.mkv",
				".mkv",
				"video/x-matroska",
				"matroska",
				"h264",
				"yuv444p",
				17,
				15,
				0,
				"",
				500,
				corpus.OperationCompatibleVideo,
				16,
				14,
				false,
				[]string{"h264-input", "matroska", "odd-dimensions"},
				nil,
			),
			videoFixture(
				"generated-h264-aac-subtitle-mkv",
				"Generated H.264 Matroska with AAC and an extra subtitle stream",
				"generated/h264-aac-subtitle.mkv",
				".mkv",
				"video/x-matroska",
				"matroska",
				"h264",
				"yuv420p",
				32,
				24,
				0,
				"aac",
				405,
				corpus.OperationCompatibleVideo,
				32,
				24,
				true,
				[]string{"additional-track", "matroska", "subtitle-drop"},
				[]corpus.Stream{{
					Kind:  corpus.StreamSubtitle,
					Codec: "subrip",
					Count: 1,
				}},
			),
			videoFixture(
				"generated-vp9-opus-webm",
				"Generated VP9 WebM with Opus",
				"generated/vp9-opus-32x24.webm",
				".webm",
				"video/webm",
				"webm",
				"vp9",
				"yuv420p",
				32,
				24,
				0,
				"opus",
				508,
				corpus.OperationSmallerVideo,
				32,
				24,
				true,
				[]string{"opus-audio", "vp9-input", "webm"},
				nil,
			),
			videoFixture(
				"generated-hevc-aac-mp4",
				"Generated HEVC MP4 with AAC",
				"generated/hevc-aac-32x24.mp4",
				".mp4",
				"video/mp4",
				"mp4",
				"hevc",
				"yuv420p",
				32,
				24,
				0,
				"aac",
				500,
				corpus.OperationCompatibleVideo,
				32,
				24,
				true,
				[]string{"aac-audio", "cpu-vaapi-input", "hevc-input"},
				nil,
			),
			iphoneSpatialProbeFixture(),
		},
	}
}

func imageFixture(
	id, title, path, inputContainer, inputCodec string,
	inputWidth, inputHeight int,
	inputAlpha corpus.Presence,
	operation corpus.Operation,
	outputContainer, outputCodec string,
	outputWidth, outputHeight int,
	outputAlpha corpus.Presence,
	traits []string,
) corpus.Fixture {
	return corpus.Fixture{
		ID:             id,
		Title:          title,
		InputPath:      path,
		Representation: corpus.RepresentationMedia,
		Source:         generatedSource(),
		Input: corpus.InputCharacteristics{
			MediaKind: corpus.MediaImage,
			Extension: filepath.Ext(path),
			MIMEType:  imageMIME(filepath.Ext(path)),
			Properties: visualProperties(
				inputContainer,
				corpus.Stream{Kind: corpus.StreamImage, Codec: inputCodec, Count: 1},
				inputWidth,
				inputHeight,
				0,
				true,
				inputAlpha,
				emptyMetadata(),
			),
		},
		Operation: operation,
		Expected: corpus.ExpectedResult{
			Outcome: corpus.ExpectedSuccess,
			Properties: ptr(visualProperties(
				outputContainer,
				corpus.Stream{Kind: corpus.StreamImage, Codec: outputCodec, Count: 1},
				outputWidth,
				outputHeight,
				0,
				true,
				outputAlpha,
				emptyMetadata(),
			)),
		},
		Tolerances: corpus.Tolerances{},
		Traits:     traits,
	}
}

func orientedJPEGFixture() corpus.Fixture {
	inputMetadata := emptyMetadata()
	inputMetadata.GPS = corpus.PresenceRequired
	inputMetadata.EXIF = corpus.PresenceRequired
	return corpus.Fixture{
		ID:             "generated-oriented-gps-jpeg",
		Title:          "Generated 3x2 JPEG with EXIF orientation and synthetic GPS tags",
		InputPath:      "generated/oriented-gps-3x2.jpg",
		Representation: corpus.RepresentationMedia,
		Source:         generatedSource(),
		Input: corpus.InputCharacteristics{
			MediaKind: corpus.MediaImage,
			Extension: ".jpg",
			MIMEType:  "image/jpeg",
			Properties: visualProperties(
				"jpeg",
				corpus.Stream{Kind: corpus.StreamImage, Codec: "mjpeg", Count: 1},
				3,
				2,
				90,
				false,
				corpus.PresenceForbidden,
				inputMetadata,
			),
		},
		Operation: corpus.OperationCompatiblePhoto,
		Expected: corpus.ExpectedResult{
			Outcome: corpus.ExpectedSuccess,
			Properties: ptr(visualProperties(
				"jpeg",
				corpus.Stream{Kind: corpus.StreamImage, Codec: "mjpeg", Count: 1},
				2,
				3,
				0,
				true,
				corpus.PresenceForbidden,
				emptyMetadata(),
			)),
		},
		Tolerances: corpus.Tolerances{},
		Traits:     []string{"exif-orientation", "gps-metadata", "metadata-removal"},
	}
}

func animatedGIFFixture() corpus.Fixture {
	duration := int64(200)
	properties := visualProperties(
		"gif",
		corpus.Stream{Kind: corpus.StreamImage, Codec: "gif", Count: 1, PixelFormat: "bgra"},
		2,
		2,
		0,
		true,
		corpus.PresenceRequired,
		emptyMetadata(),
	)
	properties.DurationMillis = &duration
	return corpus.Fixture{
		ID:             "generated-animated-gif",
		Title:          "Generated two-frame transparent GIF",
		InputPath:      "generated/animated-2x2.gif",
		Representation: corpus.RepresentationMedia,
		Source:         generatedSource(),
		Input: corpus.InputCharacteristics{
			MediaKind:  corpus.MediaImage,
			Extension:  ".gif",
			MIMEType:   "image/gif",
			Properties: properties,
		},
		Operation: corpus.OperationCompatiblePhoto,
		Expected: corpus.ExpectedResult{
			Outcome:       corpus.ExpectedRejection,
			RejectionCode: "animated-image-unsupported",
		},
		Tolerances: corpus.Tolerances{},
		Traits:     []string{"animation", "transparency"},
	}
}

func audioFixture(
	id, title, path, extension, mimeType, container, inputCodec string,
	inputChannels, inputSampleRate int,
	durationMillis int64,
	operation corpus.Operation,
	outputContainer, outputCodec string,
	outputChannels, outputSampleRate int,
	durationTolerance int64,
	traits []string,
) corpus.Fixture {
	duration := durationMillis
	outputDuration := durationMillis
	return corpus.Fixture{
		ID:             id,
		Title:          title,
		InputPath:      path,
		Representation: corpus.RepresentationMedia,
		Source:         generatedSource(),
		Input: corpus.InputCharacteristics{
			MediaKind: corpus.MediaAudio,
			Extension: extension,
			MIMEType:  mimeType,
			Properties: corpus.MediaProperties{
				Container: container,
				Streams: []corpus.Stream{{
					Kind:       corpus.StreamAudio,
					Codec:      inputCodec,
					Count:      1,
					Channels:   ptr(inputChannels),
					SampleRate: ptr(inputSampleRate),
				}},
				DurationMillis: &duration,
				Alpha:          corpus.PresenceIgnored,
				Metadata:       emptyMetadata(),
			},
		},
		Operation: operation,
		Expected: corpus.ExpectedResult{
			Outcome: corpus.ExpectedSuccess,
			Properties: &corpus.MediaProperties{
				Container: outputContainer,
				Streams: []corpus.Stream{{
					Kind:       corpus.StreamAudio,
					Codec:      outputCodec,
					Count:      1,
					Channels:   ptr(outputChannels),
					SampleRate: ptr(outputSampleRate),
				}},
				DurationMillis: &outputDuration,
				Alpha:          corpus.PresenceIgnored,
				Metadata:       emptyMetadata(),
			},
		},
		Tolerances: corpus.Tolerances{
			DurationMillis: durationTolerance,
		},
		Traits: traits,
	}
}

func videoFixture(
	id, title, path, extension, mimeType, container, videoCodec, pixelFormat string,
	width, height, rotation int,
	audioCodec string,
	durationMillis int64,
	operation corpus.Operation,
	outputWidth, outputHeight int,
	outputHasAudio bool,
	traits []string,
	additionalStreams []corpus.Stream,
) corpus.Fixture {
	duration := durationMillis
	outputDuration := durationMillis
	streams := []corpus.Stream{{
		Kind:        corpus.StreamVideo,
		Codec:       videoCodec,
		Count:       1,
		PixelFormat: pixelFormat,
	}}
	if audioCodec != "" {
		streams = append(streams, corpus.Stream{
			Kind:       corpus.StreamAudio,
			Codec:      audioCodec,
			Count:      1,
			Channels:   ptr(1),
			SampleRate: ptr(48000),
		})
	}
	streams = append(streams, additionalStreams...)

	outputStreams := []corpus.Stream{{
		Kind:        corpus.StreamVideo,
		Codec:       "h264",
		Count:       1,
		PixelFormat: "yuv420p",
	}}
	if outputHasAudio {
		outputStreams = append(outputStreams, corpus.Stream{
			Kind:       corpus.StreamAudio,
			Codec:      "aac",
			Count:      1,
			Channels:   ptr(2),
			SampleRate: ptr(48000),
		})
	}

	return corpus.Fixture{
		ID:             id,
		Title:          title,
		InputPath:      path,
		Representation: corpus.RepresentationMedia,
		Source:         generatedSource(),
		Input: corpus.InputCharacteristics{
			MediaKind: corpus.MediaVideo,
			Extension: extension,
			MIMEType:  mimeType,
			Properties: corpus.MediaProperties{
				Container: container,
				Streams:   streams,
				Dimensions: &corpus.Dimensions{
					Width:  width,
					Height: height,
				},
				DurationMillis: &duration,
				Orientation: &corpus.Orientation{
					RotationDegrees: rotation,
					PixelNormalized: rotation == 0,
				},
				Alpha:    corpus.PresenceIgnored,
				Metadata: emptyMetadata(),
			},
		},
		Operation: operation,
		Expected: corpus.ExpectedResult{
			Outcome: corpus.ExpectedSuccess,
			Properties: &corpus.MediaProperties{
				Container: "mp4",
				Streams:   outputStreams,
				Dimensions: &corpus.Dimensions{
					Width:  outputWidth,
					Height: outputHeight,
				},
				DurationMillis: &outputDuration,
				Orientation: &corpus.Orientation{
					RotationDegrees: 0,
					PixelNormalized: true,
				},
				Alpha:    corpus.PresenceIgnored,
				Metadata: emptyMetadata(),
			},
		},
		Tolerances: corpus.Tolerances{
			DurationMillis:    100,
			DimensionPixels:   0,
			FrameRateMilliFPS: 0,
			BitratePercent:    0,
		},
		Traits: traits,
	}
}

func iphoneSpatialProbeFixture() corpus.Fixture {
	duration := int64(12345)
	return corpus.Fixture{
		ID:             "simulated-iphone-spatial-apac-probe",
		Title:          "Simulated ffprobe JSON for iPhone HEVC, AAC, and unsupported APAC",
		InputPath:      "generated/iphone-spatial-apac.ffprobe.json",
		Representation: corpus.RepresentationFFprobeJSON,
		Source:         generatedSource(),
		Input: corpus.InputCharacteristics{
			MediaKind: corpus.MediaVideo,
			Extension: ".mov",
			MIMEType:  "video/quicktime",
			Properties: corpus.MediaProperties{
				Container: "mov",
				Streams: []corpus.Stream{
					{
						Kind:        corpus.StreamVideo,
						Codec:       "hevc",
						Count:       1,
						PixelFormat: "yuv420p10le",
					},
					{
						Kind:       corpus.StreamAudio,
						Codec:      "aac",
						Count:      1,
						Channels:   ptr(2),
						SampleRate: ptr(48000),
					},
					{
						Kind:       corpus.StreamAudio,
						Codec:      "apac",
						Count:      1,
						Channels:   ptr(4),
						SampleRate: ptr(48000),
					},
					{Kind: corpus.StreamImage, Codec: "mjpeg", Count: 1},
					{Kind: corpus.StreamData, Codec: "bin_data", Count: 1},
				},
				Dimensions:     &corpus.Dimensions{Width: 1921, Height: 1081},
				DurationMillis: &duration,
				Orientation: &corpus.Orientation{
					RotationDegrees: 90,
					PixelNormalized: false,
				},
				Alpha:    corpus.PresenceIgnored,
				Metadata: emptyMetadata(),
			},
		},
		Operation: corpus.OperationCompatibleVideo,
		Expected: corpus.ExpectedResult{
			Outcome: corpus.ExpectedSuccess,
			Properties: &corpus.MediaProperties{
				Container: "mp4",
				Streams: []corpus.Stream{
					{
						Kind:        corpus.StreamVideo,
						Codec:       "h264",
						Count:       1,
						PixelFormat: "yuv420p",
					},
					{
						Kind:       corpus.StreamAudio,
						Codec:      "aac",
						Count:      1,
						Channels:   ptr(2),
						SampleRate: ptr(48000),
					},
				},
				Dimensions:     &corpus.Dimensions{Width: 1080, Height: 1920},
				DurationMillis: &duration,
				Orientation: &corpus.Orientation{
					RotationDegrees: 0,
					PixelNormalized: true,
				},
				Alpha:    corpus.PresenceIgnored,
				Metadata: emptyMetadata(),
			},
		},
		Tolerances: corpus.Tolerances{
			DurationMillis:    1000,
			DimensionPixels:   0,
			FrameRateMilliFPS: 0,
			BitratePercent:    0,
		},
		Traits: []string{
			"apac-simulated",
			"attached-picture",
			"data-stream",
			"ffprobe-json",
			"rotation",
			"unsupported-audio",
		},
	}
}

func generatedSource() corpus.Source {
	return corpus.Source{
		Category:       corpus.SourceGenerated,
		Privacy:        corpus.PrivacySynthetic,
		Redistribution: corpus.RedistributionAllowed,
		Generator:      generatorCommand,
		License:        "CC0-1.0",
	}
}

func visualProperties(
	container string,
	stream corpus.Stream,
	width, height, rotation int,
	pixelNormalized bool,
	alpha corpus.Presence,
	metadata corpus.Metadata,
) corpus.MediaProperties {
	return corpus.MediaProperties{
		Container:  container,
		Streams:    []corpus.Stream{stream},
		Dimensions: &corpus.Dimensions{Width: width, Height: height},
		Orientation: &corpus.Orientation{
			RotationDegrees: rotation,
			PixelNormalized: pixelNormalized,
		},
		Alpha:    alpha,
		Metadata: metadata,
	}
}

func emptyMetadata() corpus.Metadata {
	return corpus.Metadata{
		GPS:          corpus.PresenceForbidden,
		ColorProfile: corpus.PresenceIgnored,
		EXIF:         corpus.PresenceForbidden,
		XMP:          corpus.PresenceForbidden,
		GainMap:      corpus.PresenceForbidden,
	}
}

func imageMIME(extension string) string {
	switch extension {
	case ".png":
		return "image/png"
	case ".jpg":
		return "image/jpeg"
	default:
		return ""
	}
}

func checkFixtures(output, manifestPath, ffmpeg, ffprobe string) error {
	first, err := os.MkdirTemp("", "filetwist-fixtures-check-first-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(first)
	second, err := os.MkdirTemp("", "filetwist-fixtures-check-second-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(second)

	firstManifest := filepath.Join(first, "manifest.json")
	secondManifest := filepath.Join(second, "manifest.json")
	firstOutput := filepath.Join(first, "generated")
	secondOutput := filepath.Join(second, "generated")
	if err := generate(firstOutput, firstManifest, ffmpeg); err != nil {
		return err
	}
	if err := generate(secondOutput, secondManifest, ffmpeg); err != nil {
		return err
	}

	allFiles := append(slices.Clone(deterministicFiles), ffmpegFiles...)
	for _, name := range allFiles {
		if err := compareFiles(
			filepath.Join(firstOutput, name),
			filepath.Join(secondOutput, name),
		); err != nil {
			return fmt.Errorf("generator is not byte-reproducible for %s: %w", name, err)
		}
	}
	if err := compareFiles(firstManifest, secondManifest); err != nil {
		return fmt.Errorf("manifest generator is not reproducible: %w", err)
	}
	if err := compareFiles(firstManifest, manifestPath); err != nil {
		return fmt.Errorf("committed manifest differs from generated manifest: %w", err)
	}

	for _, name := range deterministicFiles {
		if err := compareFiles(filepath.Join(firstOutput, name), filepath.Join(output, name)); err != nil {
			return fmt.Errorf("committed deterministic fixture %s differs: %w", name, err)
		}
	}
	for _, name := range ffmpegFiles {
		generatedSignature, err := probeSignature(ffprobe, filepath.Join(firstOutput, name))
		if err != nil {
			return err
		}
		committedSignature, err := probeSignature(ffprobe, filepath.Join(output, name))
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(generatedSignature, committedSignature) {
			return fmt.Errorf("committed FFmpeg fixture %s has a different probe signature", name)
		}
	}

	entries, err := os.ReadDir(output)
	if err != nil {
		return fmt.Errorf("read generated fixture directory: %w", err)
	}
	expected := make(map[string]struct{}, len(allFiles))
	for _, name := range allFiles {
		expected[name] = struct{}{}
		info, statErr := os.Stat(filepath.Join(output, name))
		if statErr != nil {
			return fmt.Errorf("stat committed fixture %s: %w", name, statErr)
		}
		if info.Size() > maxFixtureSize {
			return fmt.Errorf("committed fixture %s is %d bytes, limit is %d", name, info.Size(), maxFixtureSize)
		}
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return fmt.Errorf("unexpected directory in generated fixtures: %s", entry.Name())
		}
		if _, ok := expected[entry.Name()]; !ok {
			return fmt.Errorf("unexpected generated fixture: %s", entry.Name())
		}
	}
	return nil
}

type probeDocument struct {
	Streams []probeStream `json:"streams"`
	Format  probeFormat   `json:"format"`
}

type probeStream struct {
	Index         int              `json:"index"`
	CodecName     string           `json:"codec_name"`
	CodecType     string           `json:"codec_type"`
	Width         int              `json:"width"`
	Height        int              `json:"height"`
	PixelFormat   string           `json:"pix_fmt"`
	SampleRate    string           `json:"sample_rate"`
	Channels      int              `json:"channels"`
	ChannelLayout string           `json:"channel_layout"`
	Duration      string           `json:"duration"`
	Disposition   probeDisposition `json:"disposition"`
	SideData      []probeSideData  `json:"side_data_list"`
}

type probeDisposition struct {
	Default     int `json:"default"`
	AttachedPic int `json:"attached_pic"`
}

type probeSideData struct {
	Type     string `json:"side_data_type"`
	Rotation int    `json:"rotation"`
}

type probeFormat struct {
	Name     string `json:"format_name"`
	Duration string `json:"duration"`
}

func probeSignature(ffprobe, path string) (probeDocument, error) {
	command := exec.Command(
		ffprobe,
		"-v", "error",
		"-show_streams",
		"-show_format",
		"-print_format", "json",
		path,
	)
	output, err := command.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return probeDocument{}, fmt.Errorf("ffprobe %s: %s", path, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return probeDocument{}, fmt.Errorf("ffprobe %s: %w", path, err)
	}
	var probe probeDocument
	if err := json.Unmarshal(output, &probe); err != nil {
		return probeDocument{}, fmt.Errorf("decode ffprobe output for %s: %w", path, err)
	}
	return probe, nil
}

func compareFiles(first, second string) error {
	firstData, err := os.ReadFile(first)
	if err != nil {
		return err
	}
	secondData, err := os.ReadFile(second)
	if err != nil {
		return err
	}
	if !bytes.Equal(firstData, secondData) {
		return errors.New("contents differ")
	}
	return nil
}

func encodePNG() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	img.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 128})
	img.SetNRGBA(0, 1, color.NRGBA{B: 255, A: 64})
	img.SetNRGBA(1, 1, color.NRGBA{R: 255, G: 255, B: 255, A: 0})

	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		panic(err)
	}
	return buffer.Bytes()
}

func encodeJPEG(withEXIF bool) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	pixels := []color.RGBA{
		{R: 255, A: 255},
		{G: 255, A: 255},
		{B: 255, A: 255},
		{R: 255, G: 255, A: 255},
		{G: 255, B: 255, A: 255},
		{R: 255, B: 255, A: 255},
	}
	for index, pixel := range pixels {
		img.SetRGBA(index%3, index/3, pixel)
	}

	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, img, &jpeg.Options{Quality: 90}); err != nil {
		panic(err)
	}
	if !withEXIF {
		return buffer.Bytes()
	}
	return injectEXIF(buffer.Bytes(), exifOrientationAndGPS())
}

func injectEXIF(jpegData, exif []byte) []byte {
	if len(jpegData) < 2 || jpegData[0] != 0xff || jpegData[1] != 0xd8 {
		panic("generated JPEG is missing its SOI marker")
	}
	payload := append([]byte("Exif\x00\x00"), exif...)
	if len(payload)+2 > math.MaxUint16 {
		panic("EXIF payload is too large")
	}

	var output bytes.Buffer
	output.Write(jpegData[:2])
	output.Write([]byte{0xff, 0xe1})
	writeBinary(&output, uint16(len(payload)+2), binary.BigEndian)
	output.Write(payload)
	output.Write(jpegData[2:])
	return output.Bytes()
}

func exifOrientationAndGPS() []byte {
	var data bytes.Buffer
	data.WriteString("II")
	writeBinary(&data, uint16(42), binary.LittleEndian)
	writeBinary(&data, uint32(8), binary.LittleEndian)

	writeBinary(&data, uint16(2), binary.LittleEndian)
	writeIFDEntry(&data, 0x0112, 3, 1, 6)
	writeIFDEntry(&data, 0x8825, 4, 1, 38)
	writeBinary(&data, uint32(0), binary.LittleEndian)

	writeBinary(&data, uint16(3), binary.LittleEndian)
	writeIFDEntry(&data, 0x0000, 1, 4, 0x00000302)
	writeIFDEntry(&data, 0x0001, 2, 2, uint32('N'))
	writeIFDEntry(&data, 0x0003, 2, 2, uint32('E'))
	writeBinary(&data, uint32(0), binary.LittleEndian)
	return data.Bytes()
}

func writeIFDEntry(buffer *bytes.Buffer, tag, fieldType uint16, count, value uint32) {
	writeBinary(buffer, tag, binary.LittleEndian)
	writeBinary(buffer, fieldType, binary.LittleEndian)
	writeBinary(buffer, count, binary.LittleEndian)
	writeBinary(buffer, value, binary.LittleEndian)
}

func encodeGIF() []byte {
	palette := color.Palette{
		color.NRGBA{A: 0},
		color.NRGBA{R: 255, A: 255},
		color.NRGBA{B: 255, A: 255},
	}
	first := image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
	first.Pix = []byte{1, 0, 0, 1}
	second := image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
	second.Pix = []byte{0, 2, 2, 0}

	animation := &gif.GIF{
		Image:     []*image.Paletted{first, second},
		Delay:     []int{10, 10},
		LoopCount: 0,
	}

	var buffer bytes.Buffer
	if err := gif.EncodeAll(&buffer, animation); err != nil {
		panic(err)
	}
	return buffer.Bytes()
}

func encodeWAV() []byte {
	const (
		sampleRate = 8000
		samples    = 800
		frequency  = 440
		amplitude  = 12000
	)

	var pcm bytes.Buffer
	for index := 0; index < samples; index++ {
		value := int16(amplitude * math.Sin(2*math.Pi*frequency*float64(index)/sampleRate))
		writeBinary(&pcm, value, binary.LittleEndian)
	}

	var wav bytes.Buffer
	wav.WriteString("RIFF")
	writeBinary(&wav, uint32(36+pcm.Len()), binary.LittleEndian)
	wav.WriteString("WAVE")
	wav.WriteString("fmt ")
	writeBinary(&wav, uint32(16), binary.LittleEndian)
	writeBinary(&wav, uint16(1), binary.LittleEndian)
	writeBinary(&wav, uint16(1), binary.LittleEndian)
	writeBinary(&wav, uint32(sampleRate), binary.LittleEndian)
	writeBinary(&wav, uint32(sampleRate*2), binary.LittleEndian)
	writeBinary(&wav, uint16(2), binary.LittleEndian)
	writeBinary(&wav, uint16(16), binary.LittleEndian)
	wav.WriteString("data")
	writeBinary(&wav, uint32(pcm.Len()), binary.LittleEndian)
	wav.Write(pcm.Bytes())
	return wav.Bytes()
}

func writeBinary(buffer *bytes.Buffer, value any, order binary.ByteOrder) {
	if err := binary.Write(buffer, order, value); err != nil {
		panic(err)
	}
}

func write(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func ptr[T any](value T) *T {
	return &value
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

const iphoneSpatialProbe = `{
  "streams": [
    {
      "index": 0,
      "codec_name": "hevc",
      "codec_type": "video",
      "width": 1921,
      "height": 1081,
      "pix_fmt": "yuv420p10le",
      "duration": "12.345000",
      "disposition": {
        "default": 1,
        "attached_pic": 0
      },
      "side_data_list": [
        {
          "side_data_type": "Display Matrix",
          "rotation": 90
        }
      ]
    },
    {
      "index": 1,
      "codec_name": "aac",
      "codec_type": "audio",
      "sample_rate": "48000",
      "channels": 2,
      "channel_layout": "stereo",
      "duration": "12.345000",
      "disposition": {
        "default": 1,
        "attached_pic": 0
      }
    },
    {
      "index": 2,
      "codec_name": "apac",
      "codec_type": "audio",
      "sample_rate": "48000",
      "channels": 4,
      "channel_layout": "4.0",
      "duration": "12.345000",
      "disposition": {
        "default": 0,
        "attached_pic": 0
      }
    },
    {
      "index": 3,
      "codec_name": "mjpeg",
      "codec_type": "video",
      "width": 640,
      "height": 480,
      "disposition": {
        "default": 0,
        "attached_pic": 1
      }
    },
    {
      "index": 4,
      "codec_name": "bin_data",
      "codec_type": "data",
      "disposition": {
        "default": 0,
        "attached_pic": 0
      }
    }
  ],
  "format": {
    "format_name": "mov,mp4,m4a,3gp,3g2,mj2",
    "duration": "12.345000",
    "size": "1234567"
  }
}
`
