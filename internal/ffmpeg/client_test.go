package ffmpeg

import (
	"context"
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
