package media

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Progress is one FFmpeg -progress update.
type Progress struct {
	Frame   int64
	FPS     float64
	OutTime time.Duration
	Speed   float64
	Done    bool
}

// ParseProgress parses all complete FFmpeg key-value progress blocks.
func ParseProgress(reader io.Reader) ([]Progress, error) {
	if reader == nil {
		return nil, fmt.Errorf("media: progress reader must not be nil")
	}

	var (
		current Progress
		updates []Progress
	)
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		switch key {
		case "frame":
			current.Frame, _ = strconv.ParseInt(value, 10, 64)
		case "fps":
			current.FPS, _ = strconv.ParseFloat(value, 64)
		case "out_time_us":
			microseconds, err := strconv.ParseInt(value, 10, 64)
			if err == nil {
				current.OutTime = time.Duration(microseconds) * time.Microsecond
			}
		case "out_time":
			if duration, err := parseClock(value); err == nil {
				current.OutTime = duration
			}
		case "speed":
			current.Speed, _ = strconv.ParseFloat(strings.TrimSuffix(value, "x"), 64)
		case "progress":
			current.Done = value == "end"
			updates = append(updates, current)
			current = Progress{}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("media: read FFmpeg progress: %w", err)
	}
	return updates, nil
}

func parseClock(value string) (time.Duration, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid FFmpeg timestamp %q", value)
	}
	hours, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, err
	}
	minutes, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, err
	}
	seconds, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return 0, err
	}
	return time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds*float64(time.Second)), nil
}
