package media_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/media"
	"github.com/guigui42/filetwist/internal/runner"
)

func TestPlanUsesSelectedMatroskaStreamDurations(t *testing.T) {
	input, err := media.DecodeProbe(strings.NewReader(`{
		"streams":[
			{"index":0,"codec_type":"video","codec_name":"h264","width":64,"height":48,
				"tags":{"DURATION":"00:00:00.400000000"}},
			{"index":1,"codec_type":"audio","codec_name":"aac",
				"tags":{"DURATION":"00:00:00.423000000"}},
			{"index":2,"codec_type":"audio","codec_name":"aac",
				"tags":{"DURATION":"00:00:03.023000000"}}
		],
		"format":{"format_name":"matroska,webm","duration":"3.023"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []corpus.Operation{corpus.OperationCompatibleVideo, corpus.OperationExtractAudio} {
		t.Run(string(operation), func(t *testing.T) {
			plan, err := media.BuildPlan(media.PlanRequest{
				Operation: operation, InputPath: "input.mkv", OutputPath: "output.mp4", Input: input,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !plan.Expected.DurationKnown || plan.Expected.Duration != 423*time.Millisecond {
				t.Errorf("duration = %s, known = %t; want selected streams' 423ms", plan.Expected.Duration, plan.Expected.DurationKnown)
			}
		})
	}
}

func TestStreamDurationTagFallback(t *testing.T) {
	for _, test := range []struct {
		tag   string
		want  time.Duration
		known bool
	}{
		{"01:02:03.5", time.Hour + 2*time.Minute + 3500*time.Millisecond, true},
		{"00:00:00", 0, true},
		{"-01:00:00", 0, false},
		{"NaN", 0, false},
		{"99:99:NaN", 0, false},
		{"999999999999999:00:00", 0, false},
	} {
		t.Run(test.tag, func(t *testing.T) {
			stream := media.Stream{Tags: map[string]string{"DURATION": test.tag}}
			if got, known := stream.Duration(); got != test.want || known != test.known {
				t.Errorf("Duration() = %s, %t; want %s, %t", got, known, test.want, test.known)
			}
			stream.DurationText = "2"
			if got, known := stream.Duration(); !known || got != 2*time.Second {
				t.Errorf("numeric stream duration should take precedence: %s, %t", got, known)
			}
		})
	}
}

func TestProbeDurationRejectsNonfiniteAndOverflow(t *testing.T) {
	for _, value := range []string{"NaN", "Inf", "+Inf", "-Inf", "9223372037", "1e100", "-1"} {
		t.Run(value, func(t *testing.T) {
			if duration, known := (media.Format{DurationText: value}).Duration(); known {
				t.Errorf("Format.Duration(%q) = %s, known; want unknown", value, duration)
			}
			if duration, known := (media.Stream{DurationText: value}).Duration(); known {
				t.Errorf("Stream.Duration(%q) = %s, known; want unknown", value, duration)
			}
		})
	}
	if duration, known := (media.Format{DurationText: "1.25"}).Duration(); !known || duration != 1250*time.Millisecond {
		t.Errorf("valid duration = %s, %t", duration, known)
	}
}

func TestProbeFileAllowsLiteralPercentInLocalPaths(t *testing.T) {
	for _, path := range []string{"100% finished.wav", "/media/100%/tone.wav", "./tone%zz.wav"} {
		t.Run(path, func(t *testing.T) {
			called := false
			_, _, err := media.ProbeFile(context.Background(),
				func(_ context.Context, command runner.Command) (runner.Result, error) {
					called = true
					if command.Args[len(command.Args)-1] != path {
						t.Errorf("path changed: %v", command.Args)
					}
					return runner.Result{Stdout: runner.Output{Bytes: []byte(`{"streams":[],"format":{}}`)}}, nil
				}, "ffprobe", path, time.Second)
			if err != nil || !called {
				t.Errorf("ProbeFile(%q) error = %v; called = %t", path, err, called)
			}
		})
	}
}

func TestValidateProbeRejectsNonfiniteDuration(t *testing.T) {
	output, err := media.DecodeProbe(strings.NewReader(`{
		"streams":[{"index":0,"codec_type":"audio","codec_name":"mp3"}],
		"format":{"format_name":"mp3","duration":"NaN"}
	}`))
	if err != nil {
		t.Fatal(err)
	}

	plan := media.Plan{Expected: media.ExpectedProfile{
		Container: "mp3", AudioPresence: media.AudioRequired, AudioCodec: "mp3",
		Duration: time.Second, DurationKnown: true, DurationTolerance: time.Second,
	}}
	if err := media.ValidateProbe(plan, output); err == nil {
		t.Fatal("nonfinite duration passed validation")
	}
}

func TestValidateProbeRejectsZeroDurationForShortInput(t *testing.T) {
	plan := media.Plan{Expected: media.ExpectedProfile{
		Container: "mp3", AudioPresence: media.AudioRequired, AudioCodec: "mp3",
		Duration: 400 * time.Millisecond, DurationKnown: true, DurationTolerance: time.Second,
	}}
	output := media.Probe{
		Streams: []media.Stream{{CodecType: "audio", CodecName: "mp3"}},
		Format:  media.Format{FormatName: "mp3", DurationText: "0"},
	}
	if err := media.ValidateProbe(plan, output); err == nil {
		t.Fatal("zero-length output passed the short input's duration tolerance")
	}
}

func TestProbeFileRejectsProtocolPathsBeforeExecution(t *testing.T) {
	for _, path := range []string{
		"https://example.invalid/video.mp4",
		"https:%2f%2fexample.invalid/video.mp4",
		"file:/private/input.mp4",
		"pipe:0",
		"concat:one.mp4|two.mp4",
	} {
		t.Run(path, func(t *testing.T) {
			run := func(context.Context, runner.Command) (runner.Result, error) {
				t.Fatal("protocol path reached the executable")
				return runner.Result{}, nil
			}
			if _, _, err := media.ProbeFile(context.Background(), run, "ffprobe", path, time.Second); err == nil {
				t.Fatal("protocol path accepted")
			}
		})
	}
}
