package media_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/guigui42/filetwist/internal/media"
)

func TestDecodeProbeAndSelectStreams(t *testing.T) {
	tests := []struct {
		name             string
		fixture          string
		wantVideo        int
		wantAudio        int
		wantRotation     int
		wantAudioCodec   string
		wantWarningCodes []media.WarningCode
	}{
		{
			name:             "iPhone AAC plus unsupported spatial audio",
			fixture:          "iphone-spatial.json",
			wantVideo:        0,
			wantAudio:        1,
			wantRotation:     90,
			wantAudioCodec:   "aac",
			wantWarningCodes: []media.WarningCode{media.WarningUnsupportedAudioCodec},
		},
		{
			name:             "video with no supported audio",
			fixture:          "video-no-supported-audio.json",
			wantVideo:        5,
			wantAudio:        -1,
			wantWarningCodes: []media.WarningCode{media.WarningUnsupportedAudioCodec},
		},
		{
			name:             "common audio codecs select first",
			fixture:          "common-audio.json",
			wantVideo:        -1,
			wantAudio:        0,
			wantAudioCodec:   "flac",
			wantWarningCodes: []media.WarningCode{media.WarningAdditionalAudioStream, media.WarningAdditionalAudioStream},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe := readProbeFixture(t, tt.fixture)
			selection := media.SelectStreams(probe)

			if got := streamIndex(selection.Video); got != tt.wantVideo {
				t.Errorf("video index = %d; want %d", got, tt.wantVideo)
			}
			if got := streamIndex(selection.Audio); got != tt.wantAudio {
				t.Errorf("audio index = %d; want %d", got, tt.wantAudio)
			}
			if selection.Video != nil && selection.Video.Rotation() != tt.wantRotation {
				t.Errorf("rotation = %d; want %d", selection.Video.Rotation(), tt.wantRotation)
			}
			if selection.Audio != nil && selection.Audio.CodecName != tt.wantAudioCodec {
				t.Errorf("audio codec = %q; want %q", selection.Audio.CodecName, tt.wantAudioCodec)
			}

			gotCodes := make([]media.WarningCode, 0, len(selection.Warnings))
			for _, warning := range selection.Warnings {
				gotCodes = append(gotCodes, warning.Code)
				if warning.StreamIndex < 0 {
					t.Errorf("warning stream index = %d; want non-negative", warning.StreamIndex)
				}
			}
			if !reflect.DeepEqual(gotCodes, tt.wantWarningCodes) {
				t.Errorf("warning codes = %v; want %v", gotCodes, tt.wantWarningCodes)
			}
		})
	}
}

func TestSupportedAudioCodec(t *testing.T) {
	tests := []struct {
		codec string
		want  bool
	}{
		{codec: "aac", want: true},
		{codec: "mp3", want: true},
		{codec: "ac3", want: true},
		{codec: "eac3", want: true},
		{codec: "alac", want: true},
		{codec: "flac", want: true},
		{codec: "opus", want: true},
		{codec: "vorbis", want: true},
		{codec: "pcm_s16le", want: true},
		{codec: "pcm_f64be", want: true},
		{codec: "wmav2", want: true},
		{codec: "wmapro", want: true},
		{codec: "apac", want: false},
		{codec: "unknown", want: false},
		{codec: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.codec, func(t *testing.T) {
			if got := media.SupportedAudioCodec(tt.codec); got != tt.want {
				t.Errorf("SupportedAudioCodec(%q) = %t; want %t", tt.codec, got, tt.want)
			}
		})
	}
}

func readProbeFixture(t *testing.T, name string) media.Probe {
	t.Helper()

	file, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("close fixture: %v", err)
		}
	}()

	probe, err := media.DecodeProbe(file)
	if err != nil {
		t.Fatalf("DecodeProbe() error = %v", err)
	}
	return probe
}

func streamIndex(stream *media.Stream) int {
	if stream == nil {
		return -1
	}
	return stream.Index
}
