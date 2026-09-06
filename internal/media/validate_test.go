package media_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/media"
)

func TestValidateProbe(t *testing.T) {
	input := readProbeFixture(t, "iphone-spatial.json")
	plan, err := media.BuildPlan(media.PlanRequest{
		Operation:  corpus.OperationCompatibleVideo,
		InputPath:  "input.mov",
		OutputPath: "output.mp4",
		Input:      input,
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}

	valid := media.Probe{
		Streams: []media.Stream{
			{
				Index:         0,
				CodecName:     "h264",
				CodecType:     "video",
				Width:         1080,
				Height:        1920,
				PixelFormat:   "yuv420p",
				DurationText:  "12.340000",
				Disposition:   media.Disposition{},
				SideDataList:  nil,
				ChannelLayout: "",
			},
			{
				Index:         1,
				CodecName:     "aac",
				CodecType:     "audio",
				SampleRate:    "48000",
				Channels:      2,
				ChannelLayout: "stereo",
				DurationText:  "12.340000",
			},
		},
		Format: media.Format{
			FormatName:   "mov,mp4,m4a,3gp,3g2,mj2",
			DurationText: "12.340000",
		},
	}

	if err := media.ValidateProbe(plan, valid); err != nil {
		t.Fatalf("ValidateProbe(valid) error = %v", err)
	}

	invalid := valid
	invalid.Streams = append(append([]media.Stream(nil), valid.Streams...), media.Stream{
		Index:     2,
		CodecName: "mov_text",
		CodecType: "subtitle",
	})
	invalid.Streams[0].PixelFormat = "yuv444p"
	invalid.Streams[1].SampleRate = "44100"

	err = media.ValidateProbe(plan, invalid)
	var validationErr *media.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("ValidateProbe(invalid) error = %T %v; want *media.ValidationError", err, err)
	}
	for _, code := range []media.ValidationCode{
		media.ValidationPixelFormat,
		media.ValidationSampleRate,
		media.ValidationUndeclaredStream,
	} {
		if !hasValidationCode(validationErr.Issues, code) {
			t.Errorf("validation issues %v do not include %q", validationErr.Issues, code)
		}
	}
}

func TestFastStart(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{
			name: "moov before mdat",
			data: append(mp4Box("ftyp"), append(mp4Box("moov"), mp4Box("mdat")...)...),
			want: true,
		},
		{
			name: "moov after mdat",
			data: append(mp4Box("ftyp"), append(mp4Box("mdat"), mp4Box("moov")...)...),
		},
		{
			name: "missing moov",
			data: append(mp4Box("ftyp"), mp4Box("mdat")...),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := media.HasFastStart(bytes.NewReader(tt.data), int64(len(tt.data)))
			if err != nil {
				t.Fatalf("HasFastStart() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("HasFastStart() = %t; want %t", got, tt.want)
			}
		})
	}
}

func TestValidationDurationTolerance(t *testing.T) {
	input := readProbeFixture(t, "common-audio.json")
	plan, err := media.BuildPlan(media.PlanRequest{
		Operation:         corpus.OperationLosslessAudio,
		InputPath:         "input.flac",
		OutputPath:        "output.flac",
		Input:             input,
		DurationTolerance: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	output := media.Probe{
		Streams: []media.Stream{{
			Index:         0,
			CodecName:     "flac",
			CodecType:     "audio",
			SampleRate:    "96000",
			Channels:      2,
			ChannelLayout: "stereo",
		}},
		Format: media.Format{FormatName: "flac", DurationText: "3.600000"},
	}

	err = media.ValidateProbe(plan, output)
	var validationErr *media.ValidationError
	if !errors.As(err, &validationErr) || !hasValidationCode(validationErr.Issues, media.ValidationDuration) {
		t.Fatalf("ValidateProbe() error = %v; want duration validation issue", err)
	}
}

func TestValidateProbeChecksToneMappedColorSignaling(t *testing.T) {
	plan := media.Plan{
		Expected: media.ExpectedProfile{
			Container:      "mp4",
			VideoCodec:     "h264",
			PixelFormat:    "yuv420p",
			ColorRange:     "tv",
			ColorSpace:     "bt709",
			ColorTransfer:  "bt709",
			ColorPrimaries: "bt709",
			Width:          1920,
			Height:         1080,
			AudioPresence:  media.AudioForbidden,
		},
	}
	output := media.Probe{
		Streams: []media.Stream{{
			CodecName:      "h264",
			CodecType:      "video",
			PixelFormat:    "yuv420p",
			ColorRange:     "tv",
			ColorSpace:     "bt2020nc",
			ColorTransfer:  "arib-std-b67",
			ColorPrimaries: "bt2020",
			Width:          1920,
			Height:         1080,
		}},
		Format: media.Format{
			FormatName:   "mov,mp4,m4a,3gp,3g2,mj2",
			DurationText: "1.000000",
		},
	}

	err := media.ValidateProbe(plan, output)
	var validationErr *media.ValidationError
	if !errors.As(err, &validationErr) || !hasValidationCode(validationErr.Issues, media.ValidationColor) {
		t.Fatalf("ValidateProbe() error = %v; want color validation issue", err)
	}
}

func hasValidationCode(issues []media.ValidationIssue, code media.ValidationCode) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func mp4Box(kind string) []byte {
	return []byte{0, 0, 0, 8, kind[0], kind[1], kind[2], kind[3]}
}
