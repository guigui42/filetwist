package media_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/media"
	"github.com/guigui42/filetwist/internal/runner"
)

func TestConvertRejectsVisibleSensitiveMetadataBeforePublication(t *testing.T) {
	for _, profile := range []struct {
		operation corpus.Operation
		container string
		codec     string
	}{
		{corpus.OperationExtractAudio, "mp4", "aac"},
		{corpus.OperationCompatibleAudio, "mp3", "mp3"},
		{corpus.OperationLosslessAudio, "flac", "flac"},
	} {
		for _, tag := range []struct {
			key      string
			category string
		}{
			{"GPSLatitude", "gps"},
			{"LOCATION", "gps"},
			{"com.apple.quicktime.location.ISO6709", "gps"},
			{"ISO6709", "gps"},
			{"EXIF", "exif"},
			{"XMP", "xmp"},
			{"GainMap", "gain_map"},
			{"gain_map", "gain_map"},
		} {
			for _, scope := range []string{"format", "stream"} {
				t.Run(profile.container+"/"+tag.key+"/"+scope, func(t *testing.T) {
					dir := t.TempDir()
					path := filepath.Join(dir, "output."+profile.container)
					input := media.Probe{
						Streams: []media.Stream{{Index: 0, CodecType: "audio", CodecName: "flac",
							SampleRate: "48000", Channels: 2, ChannelLayout: "stereo"}},
						Format: media.Format{DurationText: "0.4"},
					}
					plan, err := media.BuildPlan(media.PlanRequest{
						Operation: profile.operation, InputPath: "input.flac", OutputPath: path, Input: input,
					})
					if err != nil {
						t.Fatal(err)
					}
					output := input
					output.Streams = append([]media.Stream(nil), input.Streams...)
					output.Streams[0].CodecName = profile.codec
					output.Format.FormatName = profile.container
					const privateValue = "synthetic-private-metadata"
					tags := map[string]string{tag.key: privateValue}
					if scope == "format" {
						output.Format.Tags = tags
					} else {
						output.Streams[0].Tags = tags
					}
					probeJSON, err := json.Marshal(output)
					if err != nil {
						t.Fatal(err)
					}
					run := func(_ context.Context, command runner.Command) (runner.Result, error) {
						if command.Path == "ffprobe" {
							return runner.Result{Stdout: runner.Output{Bytes: probeJSON}}, nil
						}
						data := append(mp4Box("ftyp"), append(mp4Box("moov"), mp4Box("mdat")...)...)
						if err := os.WriteFile(command.Args[len(command.Args)-1], data, 0o600); err != nil {
							t.Fatal(err)
						}
						return runner.Result{}, nil
					}
					_, err = media.Convert(context.Background(), run, "ffmpeg", "ffprobe", plan)
					var validationErr *media.ValidationError
					if !errors.As(err, &validationErr) {
						t.Fatalf("Convert() error = %v; want metadata validation failure", err)
					}
					if len(validationErr.Issues) != 1 ||
						validationErr.Issues[0].Code != "metadata" ||
						validationErr.Issues[0].Field != "metadata."+tag.category {
						t.Errorf("validation issues = %+v", validationErr.Issues)
					}
					serialized, err := json.Marshal(validationErr)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(serialized), privateValue) {
						t.Errorf("validation exposed a metadata value: %s", serialized)
					}
					entries, err := os.ReadDir(dir)
					if err != nil {
						t.Fatal(err)
					}
					if len(entries) != 0 {
						t.Errorf("rejected output left files: %v", entries)
					}
				})
			}
		}
	}
}

func TestValidateProbeAllowsRuntimeMetadata(t *testing.T) {
	plan := media.Plan{Expected: media.ExpectedProfile{
		Container: "mp4", AudioPresence: media.AudioRequired, AudioCodec: "aac",
		Duration: time.Second, DurationKnown: true,
	}}
	output := media.Probe{
		Streams: []media.Stream{{CodecType: "audio", CodecName: "aac", Tags: map[string]string{
			"language": "und", "handler_name": "SoundHandler", "encoder": "Lavc aac", "vendor_id": "[0][0][0][0]",
		}}},
		Format: media.Format{FormatName: "mp4", DurationText: "1", Tags: map[string]string{
			"major_brand": "isom", "minor_version": "512", "compatible_brands": "isomiso2mp41", "encoder": "Lavf",
		}},
	}
	if err := media.ValidateProbe(plan, output); err != nil {
		t.Fatalf("runtime metadata rejected: %v", err)
	}
}

func TestPlansDisableStreamMetadataCopy(t *testing.T) {
	input := readProbeFixture(t, "iphone-spatial.json")
	readiness := media.VAAPIReadiness{
		Device: media.DefaultVAAPIDevice, Ready: true, FunctionalProbeComplete: true,
		HardwareDecodeCodecs: map[string]bool{"h264": true, "hevc": true},
	}
	for _, operation := range []corpus.Operation{
		corpus.OperationCompatibleVideo, corpus.OperationSmallerVideo,
		corpus.OperationExtractAudio, corpus.OperationCompatibleAudio, corpus.OperationLosslessAudio,
	} {
		for _, mode := range []media.AccelerationMode{media.AccelerationCPU, media.AccelerationVAAPI} {
			t.Run(string(operation)+"/"+string(mode), func(t *testing.T) {
				plan, err := media.BuildPlan(media.PlanRequest{
					Operation: operation, InputPath: "input.mov", OutputPath: "output.mp4", Input: input,
					Acceleration: media.AccelerationConfig{Mode: mode}, VAAPI: &readiness,
				})
				if err != nil {
					t.Fatal(err)
				}
				if !containsPair(plan.Args, "-map_metadata", "-1") ||
					!containsPair(plan.Args, "-map_metadata:s", "-1") {
					t.Errorf("plan does not explicitly disable global and stream metadata: %v", plan.Args)
				}
			})
		}
	}
}
