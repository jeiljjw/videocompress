package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// toolsForTest returns Tools from VC_TEST_FFMPEG or the app cache, skipping
// the test when no FFmpeg is available.
func toolsForTest(t *testing.T) Tools {
	t.Helper()
	if p := os.Getenv("VC_TEST_FFMPEG"); p != "" {
		return Tools{FFmpeg: p, FFprobe: siblingFFprobe(p)}
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("no user cache dir: %v", err)
	}
	bin := filepath.Join(cache, "VideoCompress", "ffmpeg", "bin")
	tools := Tools{
		FFmpeg:  filepath.Join(bin, "ffmpeg"+exeSuffix()),
		FFprobe: filepath.Join(bin, "ffprobe"+exeSuffix()),
	}
	if _, err := os.Stat(tools.FFmpeg); err != nil {
		t.Skipf("no ffmpeg at %s (set VC_TEST_FFMPEG)", tools.FFmpeg)
	}
	return tools
}

// TestClientRunAndProbeRealFFmpeg exercises the real binary end to end:
// encode a short synthetic clip while receiving progress, then probe it.
func TestClientRunAndProbeRealFFmpeg(t *testing.T) {
	tools := toolsForTest(t)
	c := &Client{Tools: tools}
	ctx := context.Background()

	out := filepath.Join(t.TempDir(), "clip.mp4")
	args := []string{
		"-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=10",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "1", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "64k", "-shortest", out,
	}
	var last float64
	if err := c.Run(ctx, args, 1, func(pct float64) { last = pct }); err != nil {
		t.Fatalf("run: %v", err)
	}
	if last != 100 {
		t.Fatalf("final progress = %v, want 100", last)
	}

	info, err := c.Probe(ctx, out)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if info.Width != 320 || info.Height != 240 {
		t.Fatalf("dims = %dx%d", info.Width, info.Height)
	}
	if info.Duration < 0.9 || info.Duration > 1.2 {
		t.Fatalf("duration = %v", info.Duration)
	}
	if info.SizeBytes <= 0 {
		t.Fatalf("size = %d", info.SizeBytes)
	}
	if info.Audio == nil || info.Audio.Codec != "aac" {
		t.Fatalf("audio = %+v", info.Audio)
	}
}
