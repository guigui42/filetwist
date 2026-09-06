package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/guigui42/filetwist/internal/runner"
)

// RunFunc is the process execution contract used by ProbeFile.
type RunFunc func(context.Context, runner.Command) (runner.Result, error)

// Probe is the typed subset of ffprobe JSON needed by the media profiles.
type Probe struct {
	Streams []Stream `json:"streams"`
	Format  Format   `json:"format"`
}

// Stream describes one ffprobe stream.
type Stream struct {
	Index            int               `json:"index"`
	CodecName        string            `json:"codec_name"`
	CodecType        string            `json:"codec_type"`
	Width            int               `json:"width"`
	Height           int               `json:"height"`
	PixelFormat      string            `json:"pix_fmt"`
	SampleFormat     string            `json:"sample_fmt"`
	BitsPerRawSample string            `json:"bits_per_raw_sample"`
	SampleRate       string            `json:"sample_rate"`
	Channels         int               `json:"channels"`
	ChannelLayout    string            `json:"channel_layout"`
	ColorRange       string            `json:"color_range"`
	ColorSpace       string            `json:"color_space"`
	ColorTransfer    string            `json:"color_transfer"`
	ColorPrimaries   string            `json:"color_primaries"`
	DurationText     string            `json:"duration"`
	Disposition      Disposition       `json:"disposition"`
	Tags             map[string]string `json:"tags"`
	SideDataList     []SideData        `json:"side_data_list"`
}

// Disposition contains compatibility-relevant ffprobe disposition flags.
type Disposition struct {
	Default     int `json:"default"`
	AttachedPic int `json:"attached_pic"`
}

// SideData contains typed display-matrix rotation metadata.
type SideData struct {
	Type     string `json:"side_data_type"`
	Rotation int    `json:"rotation"`
}

// Format contains compatibility-relevant ffprobe container properties.
type Format struct {
	FormatName   string            `json:"format_name"`
	DurationText string            `json:"duration"`
	Size         string            `json:"size"`
	Tags         map[string]string `json:"tags"`
}

// DecodeProbe parses ffprobe JSON into typed media structures.
func DecodeProbe(reader io.Reader) (Probe, error) {
	if reader == nil {
		return Probe{}, errors.New("media: probe reader must not be nil")
	}

	var probe Probe
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&probe); err != nil {
		return Probe{}, fmt.Errorf("media: decode ffprobe JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Probe{}, errors.New("media: ffprobe JSON contains multiple values")
		}
		return Probe{}, fmt.Errorf("media: decode trailing ffprobe JSON: %w", err)
	}
	return probe, nil
}

// ProbeFile runs ffprobe directly against one local file and parses its JSON.
func ProbeFile(
	ctx context.Context,
	run RunFunc,
	executable string,
	path string,
	timeout time.Duration,
) (Probe, runner.Result, error) {
	if ctx == nil {
		return Probe{}, runner.Result{}, errors.New("media: context must not be nil")
	}
	if run == nil {
		return Probe{}, runner.Result{}, errors.New("media: run function must not be nil")
	}
	if executable == "" {
		return Probe{}, runner.Result{}, errors.New("media: ffprobe path must not be empty")
	}
	if path == "" {
		return Probe{}, runner.Result{}, errors.New("media: input path must not be empty")
	}
	if err := validateLocalPath(path); err != nil {
		return Probe{}, runner.Result{}, err
	}

	result, err := run(ctx, runner.Command{
		Path: executable,
		Args: []string{
			"-v", "error",
			"-show_format",
			"-show_streams",
			"-print_format", "json",
			"-protocol_whitelist", "file,pipe",
			"-i", path,
		},
		Timeout: timeout,
	})
	if err != nil {
		return Probe{}, result, fmt.Errorf("media: ffprobe failed: %w", err)
	}
	if result.Stdout.Truncated {
		return Probe{}, result, errors.New("media: ffprobe JSON exceeded the runner output limit")
	}

	probe, err := DecodeProbe(strings.NewReader(string(result.Stdout.Bytes)))
	if err != nil {
		return Probe{}, result, err
	}
	return probe, result, nil
}

// Rotation returns normalized clockwise stream rotation in degrees.
func (stream Stream) Rotation() int {
	for _, sideData := range stream.SideDataList {
		if strings.EqualFold(sideData.Type, "Display Matrix") {
			return normalizeRotation(sideData.Rotation)
		}
	}
	if stream.Tags != nil {
		if value, ok := stream.Tags["rotate"]; ok {
			rotation, err := strconv.Atoi(strings.TrimSpace(value))
			if err == nil {
				return normalizeRotation(rotation)
			}
		}
	}
	return 0
}

// Duration returns the stream duration when ffprobe supplied a finite value.
func (stream Stream) Duration() (time.Duration, bool) {
	if duration, known := parseSeconds(stream.DurationText); known {
		return duration, true
	}
	// Matroska exposes per-stream durations as clock-formatted tags instead
	// of numeric duration fields. The container can include dropped tracks.
	for key, value := range stream.Tags {
		if !strings.EqualFold(key, "duration") {
			continue
		}
		parts := strings.Split(strings.TrimSpace(value), ":")
		if len(parts) != 3 {
			return 0, false
		}
		duration, err := time.ParseDuration(parts[0] + "h" + parts[1] + "m" + parts[2] + "s")
		if err != nil || duration < 0 {
			return 0, false
		}
		return duration, true
	}
	return 0, false
}

// Duration returns the container duration when ffprobe supplied a finite value.
func (format Format) Duration() (time.Duration, bool) {
	return parseSeconds(format.DurationText)
}

func normalizeRotation(rotation int) int {
	rotation %= 360
	if rotation < 0 {
		rotation += 360
	}
	return rotation
}

func parseSeconds(value string) (time.Duration, bool) {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) ||
		seconds*float64(time.Second) >= float64(math.MaxInt64) {
		return 0, false
	}
	return time.Duration(seconds * float64(time.Second)), true
}

func validateLocalPath(path string) error {
	// FFmpeg treats a colon before any path separator as a protocol prefix.
	// Local filenames are not URLs, so percent signs must remain literal.
	if index := strings.IndexAny(path, ":/\\"); index >= 0 && path[index] == ':' {
		return errors.New("media: path must identify a local file")
	}
	if strings.ContainsRune(path, 0) {
		return errors.New("media: path contains a null byte")
	}
	return nil
}
