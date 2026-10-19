package ffmpeg

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestParseProgressLine(t *testing.T) {
	var p Progress
	if done := ParseProgressLine("out_time_us=1234567", &p); done {
		t.Fatal("not done yet")
	}
	if p.Seconds < 1.23 || p.Seconds > 1.24 {
		t.Fatalf("seconds = %v", p.Seconds)
	}
	ParseProgressLine("speed=2.5x", &p)
	if p.Speed != 2.5 {
		t.Fatalf("speed = %v", p.Speed)
	}
	if done := ParseProgressLine("progress=end", &p); !done {
		t.Fatal("want done")
	}
	// out_time_ms is microseconds in FFmpeg's progress output.
	var q Progress
	ParseProgressLine("out_time_ms=2500000", &q)
	if q.Seconds != 2.5 {
		t.Fatalf("seconds = %v", q.Seconds)
	}
	// N/A values are ignored.
	var r Progress
	ParseProgressLine("out_time_us=N/A", &r)
	ParseProgressLine("speed=N/A", &r)
	if r.Seconds != 0 || r.Speed != 0 {
		t.Fatalf("got %+v", r)
	}
}

func TestClientRunUnknownBinaryFails(t *testing.T) {
	c := &Client{Tools: Tools{FFmpeg: "definitely-not-ffmpeg"}}
	err := c.Run(context.Background(), []string{"-version"}, 0, nil)
	if err == nil {
		t.Fatal("want error")
	}
}

// --verbose shows the ffmpeg command and the tail of its stderr.
func TestClientLogsCommandAndStderrTail(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	c := &Client{
		Tools: Tools{FFmpeg: "definitely-not-ffmpeg"},
		Logf: func(format string, args ...any) {
			mu.Lock()
			lines = append(lines, fmt.Sprintf(format, args...))
			mu.Unlock()
		},
	}
	if err := c.Run(context.Background(), []string{"-i", "in.mp4"}, 0, nil); err == nil {
		t.Fatal("want error")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) == 0 {
		t.Fatal("the logger was never called")
	}
	if !strings.Contains(lines[0], "in.mp4") || !strings.Contains(lines[0], "definitely-not-ffmpeg") {
		t.Fatalf("first log line = %q", lines[0])
	}
}

func TestLastLines(t *testing.T) {
	got := lastLines("one\n\n two \r\nthree\n", 2)
	if len(got) != 2 || got[0] != " two " || got[1] != "three" {
		t.Fatalf("got %q", got)
	}
	if got := lastLines("solo", 20); len(got) != 1 || got[0] != "solo" {
		t.Fatalf("got %q", got)
	}
	if got := lastLines("", 5); len(got) != 0 {
		t.Fatalf("got %q", got)
	}
}
