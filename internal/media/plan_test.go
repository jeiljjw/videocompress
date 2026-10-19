package media

import (
	"errors"
	"testing"
)

func videoInfo(w, h int, dur float64, size int64, audio *AudioInfo) *Info {
	return &Info{Path: "a.mp4", SizeBytes: size, Duration: dur, Width: w, Height: h, VideoCodec: "h264", Audio: audio}
}

func TestFitBox(t *testing.T) {
	cases := []struct{ w, h, bw, bh, wantW, wantH int }{
		{1920, 1080, 1280, 720, 1280, 720},
		{3840, 2160, 1920, 1080, 1920, 1080},
		{1080, 1920, 1280, 720, 404, 720},
		{640, 360, 854, 480, 640, 360},       // no upscale
		{640, 480, 854, 480, 640, 480},       // no upscale, already fits
		{1000, 1000, 1920, 1080, 1000, 1000}, // already fits, never upscale
		{1920, 800, 1280, 720, 1280, 532},    // 533.3 → even 532
		{1919, 1079, 1920, 1080, 1918, 1078},
	}
	for _, c := range cases {
		gotW, gotH := FitBox(c.w, c.h, c.bw, c.bh)
		if gotW != c.wantW || gotH != c.wantH {
			t.Errorf("FitBox(%d,%d,%d,%d) = %d,%d want %d,%d", c.w, c.h, c.bw, c.bh, gotW, gotH, c.wantW, c.wantH)
		}
	}
}

func TestFloorKbps(t *testing.T) {
	cases := []struct{ w, h, want int }{
		{1920, 1080, 1500},
		{1080, 1920, 1500},
		{1280, 720, 800},
		{720, 1280, 800},
		{854, 480, 400},
		{640, 360, 400},
	}
	for _, c := range cases {
		if got := FloorKbps(c.w, c.h); got != c.want {
			t.Errorf("FloorKbps(%d,%d) = %d want %d", c.w, c.h, got, c.want)
		}
	}
}

func TestVideoKbpsFor(t *testing.T) {
	// 5MB / 60s, audio 128: floor(666.66*0.95)=633; 633-128=505
	if got := VideoKbpsFor(5_000_000, 60, 128); got != 505 {
		t.Fatalf("got %d want 505", got)
	}
	// 5MB / 10s, audio 96: 4000*0.95-96 = 3704
	if got := VideoKbpsFor(5_000_000, 10, 96); got != 3704 {
		t.Fatalf("got %d want 3704", got)
	}
	// 20MB / 1s, audio 96: 160000*0.95-96 → capped at 12000
	if got := VideoKbpsFor(20_000_000, 1, 96); got != 12000 {
		t.Fatalf("got %d want 12000", got)
	}
	// 1MB / 3600s, audio 96: clamped to 100
	if got := VideoKbpsFor(1_000_000, 3600, 96); got != 100 {
		t.Fatalf("got %d want 100", got)
	}
}

func TestAudioPlan(t *testing.T) {
	cases := []struct {
		name     string
		audio    *AudioInfo
		force    bool
		wantKbps int
		wantCopy bool
	}{
		{"none", nil, false, 0, false},
		{"aac 128 copy", &AudioInfo{Codec: "aac", BitrateKbps: 128, Channels: 2}, false, 128, true},
		{"aac 96 copy", &AudioInfo{Codec: "aac", BitrateKbps: 96, Channels: 2}, false, 96, true},
		{"aac 129 encode", &AudioInfo{Codec: "aac", BitrateKbps: 129, Channels: 2}, false, 96, false},
		{"aac unknown encode", &AudioInfo{Codec: "aac", BitrateKbps: 0, Channels: 2}, false, 96, false},
		{"mp3 stereo", &AudioInfo{Codec: "mp3", BitrateKbps: 128, Channels: 2}, false, 96, false},
		{"mono", &AudioInfo{Codec: "opus", BitrateKbps: 96, Channels: 1}, false, 64, false},
		{"force encode even if small aac", &AudioInfo{Codec: "aac", BitrateKbps: 64, Channels: 2}, true, 96, false},
		{"zero channels defaults stereo", &AudioInfo{Codec: "aac", BitrateKbps: 0, Channels: 0}, false, 96, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kbps, copyOK := AudioPlan(&Info{Audio: c.audio}, c.force)
			if kbps != c.wantKbps || copyOK != c.wantCopy {
				t.Fatalf("got kbps=%d copy=%v want kbps=%d copy=%v", kbps, copyOK, c.wantKbps, c.wantCopy)
			}
		})
	}
}

func TestSingleCandidatesLadder(t *testing.T) {
	// 20s 1080p source, target 5MB, aac 96 copy:
	// total = 2,000kbps *0.95 = 1900; video = 1900-96 = 1804 → 1080p(1500) ok.
	cands := SingleCandidates(videoInfo(1920, 1080, 20, 50_000_000, &AudioInfo{Codec: "aac", BitrateKbps: 96, Channels: 2}), 5_000_000)
	if len(cands) != 3 {
		t.Fatalf("want 3 candidates, got %d: %+v", len(cands), cands)
	}
	if cands[0].Width != 1920 || cands[0].Height != 1080 {
		t.Fatalf("first candidate = %+v", cands[0])
	}
	// 480p box 854x480 fitted to a 1920x1080 source → 852x480 (even, no upscale).
	if cands[1].Width != 1280 || cands[2].Width != 852 {
		t.Fatalf("ladder order wrong: %+v", cands)
	}
}

func TestSingleCandidatesSkipsFloors(t *testing.T) {
	// 120s 1080p, target 5MB → video = 333-96 = 237 (<400) → no candidates.
	cands := SingleCandidates(videoInfo(1920, 1080, 120, 50_000_000, &AudioInfo{Codec: "aac", BitrateKbps: 96, Channels: 2}), 5_000_000)
	if len(cands) != 0 {
		t.Fatalf("want none, got %+v", cands)
	}
}

func TestSingleCandidatesDedupeSmallSource(t *testing.T) {
	cands := SingleCandidates(videoInfo(640, 360, 10, 8_000_000, nil), 5_000_000)
	if len(cands) != 1 {
		t.Fatalf("want 1 deduped candidate, got %+v", cands)
	}
	if cands[0].Width != 640 || cands[0].Height != 360 {
		t.Fatalf("candidate = %+v", cands[0])
	}
}

func TestSplitPlan720p(t *testing.T) {
	// 180s 1080p, part 4MB, audio 96 → 30s chosen, 917kbps, 6 parts.
	sp, err := SplitPlanFor(videoInfo(1920, 1080, 180, 100_000_000, &AudioInfo{Codec: "aac", BitrateKbps: 96, Channels: 2}), 4_000_000)
	if err != nil {
		t.Fatalf("split plan: %v", err)
	}
	if sp.SegmentSeconds != 30 || sp.VideoKbps != 917 || sp.Parts != 6 {
		t.Fatalf("got %+v", sp)
	}
	if sp.Width != 1280 || sp.Height != 720 || sp.AudioKbps != 96 || sp.LowQuality {
		t.Fatalf("got %+v", sp)
	}
}

func TestSplitPlan480pUses60s(t *testing.T) {
	// 480p source stays 480p; 60s: 4e6*8/60/1000*0.95=506.6→506; 506-96=410 >= 400.
	sp, err := SplitPlanFor(videoInfo(854, 480, 300, 100_000_000, &AudioInfo{Codec: "aac", BitrateKbps: 96, Channels: 2}), 4_000_000)
	if err != nil {
		t.Fatalf("split plan: %v", err)
	}
	if sp.SegmentSeconds != 60 || sp.VideoKbps != 410 {
		t.Fatalf("got %+v", sp)
	}
}

func TestSplitPlanLowQualityFallback(t *testing.T) {
	// 0.6MB parts: even 15s cannot reach the 720p 800kbps floor.
	// 6e5*8/15/1000 = 320; 320*0.95 = 304; 304-96 = 208.
	sp, err := SplitPlanFor(videoInfo(1920, 1080, 180, 100_000_000, &AudioInfo{Codec: "aac", BitrateKbps: 96, Channels: 2}), 600_000)
	if err != nil {
		t.Fatalf("split plan: %v", err)
	}
	if !sp.LowQuality || sp.SegmentSeconds != 15 {
		t.Fatalf("got %+v", sp)
	}
	if sp.VideoKbps != 208 || sp.Parts != 12 {
		t.Fatalf("video kbps = %d parts = %d", sp.VideoKbps, sp.Parts)
	}
}

func TestPlanForSkipAndModes(t *testing.T) {
	small := videoInfo(1280, 720, 20, 3_000_000, nil)
	p, err := PlanFor(small, 5_000_000, 4_000_000)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if p.Mode != ModeSkipped {
		t.Fatalf("mode = %s", p.Mode)
	}

	big := videoInfo(1920, 1080, 20, 50_000_000, &AudioInfo{Codec: "aac", BitrateKbps: 96, Channels: 2})
	p, err = PlanFor(big, 5_000_000, 4_000_000)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if p.Mode != ModeSingle || p.Candidate.Width != 1920 {
		t.Fatalf("plan = %+v", p)
	}

	long := videoInfo(1920, 1080, 180, 100_000_000, &AudioInfo{Codec: "aac", BitrateKbps: 96, Channels: 2})
	p, err = PlanFor(long, 5_000_000, 4_000_000)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if p.Mode != ModeSplit || p.Split == nil || p.Split.Parts != 6 {
		t.Fatalf("plan = %+v", p)
	}

	if _, err := PlanFor(videoInfo(0, 0, 0, 0, nil), 5_000_000, 4_000_000); !errors.Is(err, ErrInvalidInfo) {
		t.Fatalf("want ErrInvalidInfo, got %v", err)
	}
	if _, err := PlanFor(big, 5_000_000, 5_000_000); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("want ErrInvalidSettings, got %v", err)
	}
}
