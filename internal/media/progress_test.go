package media_test

import (
	"strings"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/media"
)

func TestParseProgress(t *testing.T) {
	input := strings.NewReader("frame=12\nfps=24.5\nout_time_us=500000\nspeed=1.25x\nprogress=continue\n" +
		"frame=24\nfps=25\nout_time=00:00:01.000000\nspeed=N/A\nprogress=end\n")

	updates, err := media.ParseProgress(input)
	if err != nil {
		t.Fatalf("ParseProgress() error = %v", err)
	}
	if len(updates) != 2 {
		t.Fatalf("updates = %d; want 2", len(updates))
	}
	if updates[0].Frame != 12 || updates[0].FPS != 24.5 || updates[0].OutTime != 500*time.Millisecond ||
		updates[0].Speed != 1.25 || updates[0].Done {
		t.Errorf("first update = %+v", updates[0])
	}
	if updates[1].Frame != 24 || updates[1].OutTime != time.Second || !updates[1].Done {
		t.Errorf("second update = %+v", updates[1])
	}
}
