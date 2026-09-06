package media_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/media"
)

func TestCorpusExpectedMediaProfilesMatchPlanner(t *testing.T) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe unavailable")
	}

	root := filepath.Clean(filepath.Join("..", ".."))
	manifestFile, err := os.Open(filepath.Join(root, "fixtures", "manifest.json"))
	if err != nil {
		t.Fatalf("open fixture manifest: %v", err)
	}
	defer func() {
		if closeErr := manifestFile.Close(); closeErr != nil {
			t.Errorf("close fixture manifest: %v", closeErr)
		}
	}()

	manifest, err := corpus.DecodeManifest(manifestFile)
	if err != nil {
		t.Fatalf("DecodeManifest() error = %v", err)
	}

	for _, fixture := range manifest.Fixtures {
		if fixture.Input.MediaKind == corpus.MediaImage {
			continue
		}
		t.Run(fixture.ID, func(t *testing.T) {
			inputPath := filepath.Join(root, "fixtures", filepath.FromSlash(fixture.InputPath))
			probe := readCorpusProbe(t, ffprobe, fixture.Representation, inputPath)
			plan, err := media.BuildPlan(media.PlanRequest{
				Operation:         fixture.Operation,
				InputPath:         inputPath,
				OutputPath:        filepath.Join(t.TempDir(), "output"),
				Input:             probe,
				Timeout:           time.Minute,
				DurationTolerance: time.Duration(fixture.Tolerances.DurationMillis) * time.Millisecond,
				Acceleration: media.AccelerationConfig{
					Mode: media.AccelerationCPU,
				},
			})
			if err != nil {
				t.Fatalf("BuildPlan() error = %v", err)
			}
			assertPlanMatchesManifest(t, plan.Expected, fixture)
		})
	}
}

func readCorpusProbe(
	t *testing.T,
	ffprobe string,
	representation corpus.FixtureRepresentation,
	path string,
) media.Probe {
	t.Helper()

	var data []byte
	var err error
	if representation == corpus.RepresentationFFprobeJSON {
		data, err = os.ReadFile(path)
	} else {
		data, err = exec.Command(
			ffprobe,
			"-v", "error",
			"-show_streams",
			"-show_format",
			"-print_format", "json",
			path,
		).Output()
	}
	if err != nil {
		t.Fatalf("read probe data: %v", err)
	}

	probe, err := media.DecodeProbe(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode probe data: %v", err)
	}
	return probe
}

func assertPlanMatchesManifest(t *testing.T, plan media.ExpectedProfile, fixture corpus.Fixture) {
	t.Helper()

	expected := fixture.Expected.Properties
	if expected == nil {
		t.Fatal("media fixture has no expected properties")
	}
	if plan.Container != expected.Container {
		t.Errorf("container = %q, want %q", plan.Container, expected.Container)
	}

	video := findCorpusStream(expected.Streams, corpus.StreamVideo)
	if video == nil {
		if plan.VideoCodec != "" {
			t.Errorf("video codec = %q, want no video", plan.VideoCodec)
		}
	} else {
		if plan.VideoCodec != video.Codec {
			t.Errorf("video codec = %q, want %q", plan.VideoCodec, video.Codec)
		}
		if plan.PixelFormat != video.PixelFormat {
			t.Errorf("pixel format = %q, want %q", plan.PixelFormat, video.PixelFormat)
		}
	}

	audio := findCorpusStream(expected.Streams, corpus.StreamAudio)
	if audio == nil {
		if plan.AudioPresence != media.AudioForbidden {
			t.Errorf("audio presence = %q, want %q", plan.AudioPresence, media.AudioForbidden)
		}
	} else {
		if plan.AudioPresence != media.AudioRequired {
			t.Errorf("audio presence = %q, want %q", plan.AudioPresence, media.AudioRequired)
		}
		if plan.AudioCodec != audio.Codec {
			t.Errorf("audio codec = %q, want %q", plan.AudioCodec, audio.Codec)
		}
		if audio.Channels != nil && plan.Channels != *audio.Channels {
			t.Errorf("channels = %d, want %d", plan.Channels, *audio.Channels)
		}
		if audio.SampleRate != nil && plan.SampleRate != *audio.SampleRate {
			t.Errorf("sample rate = %d, want %d", plan.SampleRate, *audio.SampleRate)
		}
	}

	if expected.Dimensions != nil {
		if plan.Width != expected.Dimensions.Width || plan.Height != expected.Dimensions.Height {
			t.Errorf(
				"dimensions = %dx%d, want %dx%d",
				plan.Width,
				plan.Height,
				expected.Dimensions.Width,
				expected.Dimensions.Height,
			)
		}
	}
	if expected.Orientation != nil && plan.Rotation != expected.Orientation.RotationDegrees {
		t.Errorf("rotation = %d, want %d", plan.Rotation, expected.Orientation.RotationDegrees)
	}

	if expected.DurationMillis != nil {
		if !plan.DurationKnown {
			t.Error("planned duration is unknown")
		} else {
			actualMillis := plan.Duration.Milliseconds()
			difference := actualMillis - *expected.DurationMillis
			if difference < 0 {
				difference = -difference
			}
			if difference > fixture.Tolerances.DurationMillis {
				t.Errorf(
					"duration = %dms, want %dms +/- %dms",
					actualMillis,
					*expected.DurationMillis,
					fixture.Tolerances.DurationMillis,
				)
			}
		}
	}
}

func findCorpusStream(streams []corpus.Stream, kind corpus.StreamKind) *corpus.Stream {
	for index := range streams {
		if streams[index].Kind == kind {
			return &streams[index]
		}
	}
	return nil
}
