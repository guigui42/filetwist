package corpus

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func closeTestFile(t *testing.T, file *os.File) {
	t.Helper()
	if err := file.Close(); err != nil {
		t.Errorf("close %q: %v", file.Name(), err)
	}
}

func TestManifestValidate(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Manifest)
		wantField string
	}{
		{
			name: "valid",
		},
		{
			name: "unsupported schema version",
			mutate: func(manifest *Manifest) {
				manifest.SchemaVersion = "2.0"
			},
			wantField: "schema_version",
		},
		{
			name: "duplicate fixture ID",
			mutate: func(manifest *Manifest) {
				manifest.Fixtures = append(manifest.Fixtures, manifest.Fixtures[0])
			},
			wantField: "fixtures[1].id",
		},
		{
			name: "unsafe input path",
			mutate: func(manifest *Manifest) {
				manifest.Fixtures[0].InputPath = "../private/photo.png"
			},
			wantField: "fixtures[0].input_path",
		},
		{
			name: "unsupported representation",
			mutate: func(manifest *Manifest) {
				manifest.Fixtures[0].Representation = "archive"
			},
			wantField: "fixtures[0].representation",
		},
		{
			name: "ffprobe JSON must be generated",
			mutate: func(manifest *Manifest) {
				manifest.Fixtures[0].Representation = RepresentationFFprobeJSON
				manifest.Fixtures[0].InputPath = "generated/test.ffprobe.json"
				manifest.Fixtures[0].Source.Category = SourcePublicDomain
			},
			wantField: "fixtures[0].representation",
		},
		{
			name: "ffprobe JSON needs distinct path",
			mutate: func(manifest *Manifest) {
				manifest.Fixtures[0].Representation = RepresentationFFprobeJSON
			},
			wantField: "fixtures[0].input_path",
		},
		{
			name: "generated source needs generator",
			mutate: func(manifest *Manifest) {
				manifest.Fixtures[0].Source.Generator = ""
			},
			wantField: "fixtures[0].source.generator",
		},
		{
			name: "private source cannot be redistributed",
			mutate: func(manifest *Manifest) {
				manifest.Fixtures[0].Source.Category = SourcePrivateCapture
				manifest.Fixtures[0].Source.Privacy = PrivacyPrivate
			},
			wantField: "fixtures[0].source.redistribution",
		},
		{
			name: "success needs expected properties",
			mutate: func(manifest *Manifest) {
				manifest.Fixtures[0].Expected.Properties = nil
			},
			wantField: "fixtures[0].expected.properties",
		},
		{
			name: "rejection needs code",
			mutate: func(manifest *Manifest) {
				manifest.Fixtures[0].Expected = ExpectedResult{
					Outcome: ExpectedRejection,
				}
			},
			wantField: "fixtures[0].expected.rejection_code",
		},
		{
			name: "invalid stream count",
			mutate: func(manifest *Manifest) {
				manifest.Fixtures[0].Expected.Properties.Streams[0].Count = 0
			},
			wantField: "fixtures[0].expected.properties.streams[0].count",
		},
		{
			name: "invalid orientation",
			mutate: func(manifest *Manifest) {
				manifest.Fixtures[0].Expected.Properties.Orientation.RotationDegrees = 45
			},
			wantField: "fixtures[0].expected.properties.orientation.rotation_degrees",
		},
		{
			name: "negative tolerance",
			mutate: func(manifest *Manifest) {
				manifest.Fixtures[0].Tolerances.DurationMillis = -1
			},
			wantField: "fixtures[0].tolerances.duration_millis",
		},
		{
			name: "operation must match input kind",
			mutate: func(manifest *Manifest) {
				manifest.Fixtures[0].Operation = OperationCompatibleAudio
			},
			wantField: "fixtures[0].operation",
		},
		{
			name: "duplicate trait",
			mutate: func(manifest *Manifest) {
				manifest.Fixtures[0].Traits = []string{"transparency", "transparency"}
			},
			wantField: "fixtures[0].traits[1]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := validManifest()
			if tt.mutate != nil {
				tt.mutate(&manifest)
			}

			err := manifest.Validate()
			if tt.wantField == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}

			var validationErrors ValidationErrors
			if !errors.As(err, &validationErrors) {
				t.Fatalf("Validate() error type = %T, want ValidationErrors", err)
			}
			if !hasValidationField(validationErrors, tt.wantField) {
				t.Fatalf("Validate() fields = %v, want %q", validationErrors, tt.wantField)
			}
		})
	}
}

func TestManifestJSONRoundTrip(t *testing.T) {
	want := validManifest()

	var encoded bytes.Buffer
	if err := EncodeManifest(&encoded, want); err != nil {
		t.Fatalf("EncodeManifest() error = %v", err)
	}

	got, err := DecodeManifest(&encoded)
	if err != nil {
		t.Fatalf("DecodeManifest() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch:\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestDecodeManifestRejectsNonStrictJSON(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{
			name: "unknown field",
			data: `{"schema_version":"1.0","fixtures":[],"unexpected":true}`,
		},
		{
			name: "trailing document",
			data: `{"schema_version":"1.0","fixtures":[]} {}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeManifest(strings.NewReader(tt.data)); err == nil {
				t.Fatal("DecodeManifest() error = nil, want strict JSON error")
			}
		})
	}
}

func TestManifestAllowsPartiallySpecifiedRepeatedStreams(t *testing.T) {
	t.Parallel()
	manifest := validManifest()
	generic := manifest.Fixtures[0].Expected.Properties.Streams[0]
	constrained := generic
	constrained.PixelFormat = "rgba"
	manifest.Fixtures[0].Expected.Properties.Streams = []Stream{generic, constrained}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("repeated stream shapes unexpectedly prohibited: %v", err)
	}
}

func TestTestdataManifests(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		file, err := os.Open(filepath.Join("testdata", "valid-manifest.json"))
		if err != nil {
			t.Fatalf("open valid manifest: %v", err)
		}
		defer closeTestFile(t, file)

		if _, err := DecodeManifest(file); err != nil {
			t.Fatalf("DecodeManifest() error = %v", err)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join("testdata", "invalid-manifest.json"))
		if err != nil {
			t.Fatalf("read invalid manifest: %v", err)
		}

		var manifest Manifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if err := manifest.Validate(); err == nil {
			t.Fatal("Validate() error = nil, want invalid testdata error")
		}
	})
}

func TestCommittedFixtureManifest(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	file, err := os.Open(filepath.Join(root, "fixtures", "manifest.json"))
	if err != nil {
		t.Fatalf("open fixture manifest: %v", err)
	}
	defer closeTestFile(t, file)

	manifest, err := DecodeManifest(file)
	if err != nil {
		t.Fatalf("DecodeManifest() error = %v", err)
	}

	referenced := make(map[string]struct{}, len(manifest.Fixtures))
	for _, fixture := range manifest.Fixtures {
		if fixture.Source.Privacy == PrivacyPrivate {
			t.Errorf("fixture %q is private and must not be committed", fixture.ID)
		}
		if fixture.Source.Redistribution != RedistributionAllowed {
			t.Errorf("fixture %q redistribution = %q, want %q", fixture.ID, fixture.Source.Redistribution, RedistributionAllowed)
		}
		if fixture.Source.License == "" {
			t.Errorf("fixture %q has no redistribution license", fixture.ID)
		}
		if _, exists := referenced[fixture.InputPath]; exists {
			t.Errorf("fixture path %q is referenced more than once", fixture.InputPath)
		}
		referenced[fixture.InputPath] = struct{}{}

		switch fixture.Representation {
		case RepresentationMedia:
			if strings.HasSuffix(fixture.InputPath, ".json") {
				t.Errorf("media fixture %q points to JSON", fixture.ID)
			}
		case RepresentationFFprobeJSON:
			if !strings.HasSuffix(fixture.InputPath, ".ffprobe.json") {
				t.Errorf("ffprobe fixture %q path = %q, want .ffprobe.json suffix", fixture.ID, fixture.InputPath)
			}
			if !slicesContains(fixture.Traits, "ffprobe-json") {
				t.Errorf("ffprobe fixture %q must include the ffprobe-json trait", fixture.ID)
			}
		}

		path := filepath.Join(root, "fixtures", filepath.FromSlash(fixture.InputPath))
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("fixture %q path %q: %v", fixture.ID, fixture.InputPath, err)
			continue
		}
		if info.Size() > 64*1024 {
			t.Errorf("fixture %q is %d bytes, want <= 64 KiB", fixture.ID, info.Size())
		}
	}

	entries, err := os.ReadDir(filepath.Join(root, "fixtures", "generated"))
	if err != nil {
		t.Fatalf("read generated fixtures: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			t.Errorf("unexpected generated fixture directory %q", entry.Name())
			continue
		}
		path := filepath.ToSlash(filepath.Join("generated", entry.Name()))
		if _, exists := referenced[path]; !exists {
			t.Errorf("generated fixture %q is not referenced by the manifest", path)
		}
	}
}

func TestGeneratedFixturesAreReadable(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", "fixtures", "generated"))

	for _, name := range []string{
		"rgba-2x2.png",
		"opaque-3x2.jpg",
		"oriented-gps-3x2.jpg",
		"animated-2x2.gif",
	} {
		t.Run(name, func(t *testing.T) {
			file, err := os.Open(filepath.Join(root, name))
			if err != nil {
				t.Fatalf("open image: %v", err)
			}
			defer closeTestFile(t, file)

			config, format, err := image.DecodeConfig(file)
			if err != nil {
				t.Fatalf("DecodeConfig() error = %v", err)
			}
			if format == "" || config.Width <= 0 || config.Height <= 0 {
				t.Fatalf("invalid decoded image config: format=%q size=%dx%d", format, config.Width, config.Height)
			}
		})
	}

	t.Run("tone-8khz-mono.wav", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join(root, "tone-8khz-mono.wav"))
		if err != nil {
			t.Fatalf("read WAV: %v", err)
		}
		if len(data) < 44 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
			t.Fatal("generated WAV has an invalid header")
		}
		if got := binary.LittleEndian.Uint32(data[24:28]); got != 8000 {
			t.Fatalf("sample rate = %d, want 8000", got)
		}
	})
}

func TestGeneratedFixtureProbePropertiesMatchManifest(t *testing.T) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe unavailable")
	}

	root := filepath.Clean(filepath.Join("..", ".."))
	file, err := os.Open(filepath.Join(root, "fixtures", "manifest.json"))
	if err != nil {
		t.Fatalf("open fixture manifest: %v", err)
	}
	defer closeTestFile(t, file)

	manifest, err := DecodeManifest(file)
	if err != nil {
		t.Fatalf("DecodeManifest() error = %v", err)
	}

	for _, fixture := range manifest.Fixtures {
		if fixture.Input.MediaKind == MediaImage && fixture.Representation == RepresentationMedia {
			continue
		}
		t.Run(fixture.ID, func(t *testing.T) {
			path := filepath.Join(root, "fixtures", filepath.FromSlash(fixture.InputPath))
			var data []byte
			if fixture.Representation == RepresentationFFprobeJSON {
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

			var probe fixtureProbe
			if err := json.Unmarshal(data, &probe); err != nil {
				t.Fatalf("decode probe data: %v", err)
			}
			assertProbeMatchesProperties(t, probe, fixture.Input.Properties, fixture.Tolerances)
		})
	}
}

func TestGeneratedFixtureRegeneration(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe unavailable")
	}

	root := filepath.Clean(filepath.Join("..", ".."))
	command := exec.Command("go", "run", "./fixtures/generate.go", "-check")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture regeneration check failed: %v\n%s", err, output)
	}
}

type fixtureProbe struct {
	Streams []fixtureProbeStream `json:"streams"`
	Format  fixtureProbeFormat   `json:"format"`
}

type fixtureProbeStream struct {
	CodecName    string                  `json:"codec_name"`
	CodecType    string                  `json:"codec_type"`
	Width        int                     `json:"width"`
	Height       int                     `json:"height"`
	PixelFormat  string                  `json:"pix_fmt"`
	SampleRate   string                  `json:"sample_rate"`
	Channels     int                     `json:"channels"`
	Disposition  fixtureProbeDisposition `json:"disposition"`
	SideDataList []fixtureProbeSideData  `json:"side_data_list"`
}

type fixtureProbeDisposition struct {
	AttachedPic int `json:"attached_pic"`
}

type fixtureProbeSideData struct {
	Type     string `json:"side_data_type"`
	Rotation int    `json:"rotation"`
}

type fixtureProbeFormat struct {
	Name     string `json:"format_name"`
	Duration string `json:"duration"`
}

func assertProbeMatchesProperties(
	t *testing.T,
	probe fixtureProbe,
	properties MediaProperties,
	tolerances Tolerances,
) {
	t.Helper()

	if !probeContainerMatches(properties.Container, probe.Format.Name) {
		t.Errorf("container = %q, want %q", probe.Format.Name, properties.Container)
	}

	actualStreams := append([]fixtureProbeStream(nil), probe.Streams...)
	for _, expected := range properties.Streams {
		for count := 0; count < expected.Count; count++ {
			index := matchingProbeStream(actualStreams, expected)
			if index < 0 {
				t.Errorf("missing stream %s:%s", expected.Kind, expected.Codec)
				break
			}
			actual := actualStreams[index]
			actualStreams = append(actualStreams[:index], actualStreams[index+1:]...)
			if expected.PixelFormat != "" && actual.PixelFormat != expected.PixelFormat {
				t.Errorf("%s pixel format = %q, want %q", expected.Codec, actual.PixelFormat, expected.PixelFormat)
			}
			if expected.Channels != nil && actual.Channels != *expected.Channels {
				t.Errorf("%s channels = %d, want %d", expected.Codec, actual.Channels, *expected.Channels)
			}
			if expected.SampleRate != nil {
				sampleRate, parseErr := strconv.Atoi(actual.SampleRate)
				if parseErr != nil || sampleRate != *expected.SampleRate {
					t.Errorf("%s sample rate = %q, want %d", expected.Codec, actual.SampleRate, *expected.SampleRate)
				}
			}
		}
	}
	if len(actualStreams) != 0 {
		t.Errorf("manifest does not declare %d probed streams", len(actualStreams))
	}

	if properties.Dimensions != nil {
		video := primaryProbeVideo(probe.Streams)
		if video == nil {
			t.Error("probe has no primary video stream")
		} else {
			if video.Width != properties.Dimensions.Width || video.Height != properties.Dimensions.Height {
				t.Errorf(
					"dimensions = %dx%d, want %dx%d",
					video.Width,
					video.Height,
					properties.Dimensions.Width,
					properties.Dimensions.Height,
				)
			}
			if properties.Orientation != nil && probeRotation(*video) != properties.Orientation.RotationDegrees {
				t.Errorf("rotation = %d, want %d", probeRotation(*video), properties.Orientation.RotationDegrees)
			}
		}
	}

	if properties.DurationMillis != nil {
		seconds, parseErr := strconv.ParseFloat(probe.Format.Duration, 64)
		if parseErr != nil {
			t.Errorf("duration = %q: %v", probe.Format.Duration, parseErr)
		} else {
			actualMillis := int64(math.Round(seconds * 1000))
			difference := actualMillis - *properties.DurationMillis
			if difference < 0 {
				difference = -difference
			}
			if difference > tolerances.DurationMillis {
				t.Errorf(
					"duration = %dms, want %dms +/- %dms",
					actualMillis,
					*properties.DurationMillis,
					tolerances.DurationMillis,
				)
			}
		}
	}
}

func matchingProbeStream(streams []fixtureProbeStream, expected Stream) int {
	for index, stream := range streams {
		if probeStreamKind(stream) == expected.Kind && stream.CodecName == expected.Codec {
			return index
		}
	}
	return -1
}

func probeStreamKind(stream fixtureProbeStream) StreamKind {
	switch stream.CodecType {
	case "audio":
		return StreamAudio
	case "subtitle":
		return StreamSubtitle
	case "data":
		return StreamData
	case "video":
		if stream.Disposition.AttachedPic != 0 {
			return StreamImage
		}
		return StreamVideo
	default:
		return StreamKind(stream.CodecType)
	}
}

func primaryProbeVideo(streams []fixtureProbeStream) *fixtureProbeStream {
	for index := range streams {
		if probeStreamKind(streams[index]) == StreamVideo {
			return &streams[index]
		}
	}
	return nil
}

func probeRotation(stream fixtureProbeStream) int {
	for _, sideData := range stream.SideDataList {
		if strings.EqualFold(sideData.Type, "Display Matrix") {
			rotation := sideData.Rotation % 360
			if rotation < 0 {
				rotation += 360
			}
			return rotation
		}
	}
	return 0
}

func probeContainerMatches(expected, actual string) bool {
	for _, name := range strings.Split(actual, ",") {
		name = strings.TrimSpace(name)
		if name == expected {
			return true
		}
		if expected == "mp4" && name == "mov" {
			return true
		}
		if expected == "matroska" && name == "matroska" {
			return true
		}
		if expected == "webm" && name == "webm" {
			return true
		}
	}
	return false
}

func slicesContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func validManifest() Manifest {
	return Manifest{
		SchemaVersion: ManifestVersion,
		Fixtures: []Fixture{
			{
				ID:             "generated-rgba-png",
				Title:          "Generated 2x2 RGBA PNG",
				InputPath:      "generated/rgba-2x2.png",
				Representation: RepresentationMedia,
				Source: Source{
					Category:       SourceGenerated,
					Privacy:        PrivacySynthetic,
					Redistribution: RedistributionAllowed,
					Generator:      "go run ./fixtures/generate.go",
				},
				Input: InputCharacteristics{
					MediaKind: MediaImage,
					Extension: ".png",
					MIMEType:  "image/png",
					Properties: MediaProperties{
						Container: "png",
						Streams: []Stream{
							{
								Kind:  StreamImage,
								Codec: "png",
								Count: 1,
							},
						},
						Dimensions: &Dimensions{Width: 2, Height: 2},
						Orientation: &Orientation{
							RotationDegrees: 0,
							PixelNormalized: true,
						},
						Alpha: PresenceRequired,
						Metadata: Metadata{
							GPS:          PresenceForbidden,
							ColorProfile: PresenceIgnored,
							EXIF:         PresenceForbidden,
							XMP:          PresenceForbidden,
							GainMap:      PresenceForbidden,
						},
					},
				},
				Operation: OperationLosslessImage,
				Expected: ExpectedResult{
					Outcome: ExpectedSuccess,
					Properties: &MediaProperties{
						Container: "png",
						Streams: []Stream{
							{
								Kind:  StreamImage,
								Codec: "png",
								Count: 1,
							},
						},
						Dimensions: &Dimensions{Width: 2, Height: 2},
						Orientation: &Orientation{
							RotationDegrees: 0,
							PixelNormalized: true,
						},
						Alpha: PresenceRequired,
						Metadata: Metadata{
							GPS:          PresenceForbidden,
							ColorProfile: PresenceIgnored,
							EXIF:         PresenceForbidden,
							XMP:          PresenceForbidden,
							GainMap:      PresenceForbidden,
						},
					},
				},
				Tolerances: Tolerances{
					DurationMillis:    2,
					DimensionPixels:   0,
					FrameRateMilliFPS: 0,
					BitratePercent:    0,
				},
				Traits: []string{"transparency"},
			},
		},
	}
}

func hasValidationField(errs ValidationErrors, field string) bool {
	for _, err := range errs {
		if err.Field == field {
			return true
		}
	}
	return false
}
