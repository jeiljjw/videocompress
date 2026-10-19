package engine

import (
	"testing"

	"videocompress/internal/media"
)

func TestVerifySingle(t *testing.T) {
	src := &media.Info{Duration: 20, Width: 1280, Height: 720}
	ok := &media.Info{SizeBytes: 4_900_000, Duration: 20.1, Width: 1280, Height: 720}
	if err := VerifySingle(src, ok, 5_000_000); err != nil {
		t.Fatalf("want ok: %v", err)
	}
	tooBig := &media.Info{SizeBytes: 5_100_000, Duration: 20, Width: 1280, Height: 720}
	if err := VerifySingle(src, tooBig, 5_000_000); err == nil {
		t.Fatal("want size error")
	}
	wrongDur := &media.Info{SizeBytes: 100, Duration: 40, Width: 1280, Height: 720}
	if err := VerifySingle(src, wrongDur, 5_000_000); err == nil {
		t.Fatal("want duration error")
	}
	noVideo := &media.Info{SizeBytes: 100, Duration: 20}
	if err := VerifySingle(src, noVideo, 5_000_000); err == nil {
		t.Fatal("want video error")
	}
}

func TestVerifyParts(t *testing.T) {
	// Source duration must equal the sum of the parts for the happy path.
	src := &media.Info{Duration: 90, Width: 1920, Height: 1080}
	parts := []*media.Info{
		{SizeBytes: 3_900_000, Duration: 30, Width: 1280, Height: 720, StartTime: 0},
		{SizeBytes: 3_900_000, Duration: 30, Width: 1280, Height: 720, StartTime: 0},
		{SizeBytes: 3_900_000, Duration: 30, Width: 1280, Height: 720, StartTime: 0},
	}
	if err := VerifyParts(src, parts, 4_000_000); err != nil {
		t.Fatalf("want ok: %v", err)
	}
	big := append([]*media.Info{}, parts...)
	big[1] = &media.Info{SizeBytes: 4_100_000, Duration: 30, Width: 1280, Height: 720}
	if err := VerifyParts(src, big, 4_000_000); err == nil {
		t.Fatal("want part size error")
	}
	short := append([]*media.Info{}, parts...)
	short[2] = &media.Info{SizeBytes: 100, Duration: 5, Width: 1280, Height: 720}
	if err := VerifyParts(src, short, 4_000_000); err == nil {
		t.Fatal("want duration sum error")
	}
	noVideo := append([]*media.Info{}, parts...)
	noVideo[0] = &media.Info{SizeBytes: 100, Duration: 30}
	if err := VerifyParts(src, noVideo, 4_000_000); err == nil {
		t.Fatal("want video error")
	}
}

func TestDurationToleranceGrowsWithParts(t *testing.T) {
	if DurationTolerance(6) < 2.0 {
		t.Fatal("tolerance must be at least 2s")
	}
	if DurationTolerance(200) <= DurationTolerance(6) {
		t.Fatal("tolerance must grow with part count")
	}
}
