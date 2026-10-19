package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"videocompress/internal/ffmpeg"
	"videocompress/internal/media"
)

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// testClient returns a real ffmpeg client, skipping when none is installed.
// Set VC_TEST_FFMPEG to point at an ffmpeg binary to override.
func testClient(t *testing.T) *ffmpeg.Client {
	t.Helper()
	ffmpegPath := os.Getenv("VC_TEST_FFMPEG")
	if ffmpegPath == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			t.Fatal(err)
		}
		ffmpegPath = filepath.Join(cache, "VideoCompress", "ffmpeg", "bin", "ffmpeg"+exeSuffix())
	}
	if _, err := os.Stat(ffmpegPath); err != nil {
		t.Skipf("FFmpeg not found at %s; set VC_TEST_FFMPEG to run integration tests", ffmpegPath)
	}
	return &ffmpeg.Client{Tools: ffmpeg.Tools{
		FFmpeg:  ffmpegPath,
		FFprobe: filepath.Join(filepath.Dir(ffmpegPath), "ffprobe"+exeSuffix()),
	}}
}

func runFF(t *testing.T, bin string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append([]string{"-hide_banner", "-loglevel", "error", "-nostdin"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
	}
}

// makeVideo synthesizes a deterministic clip with optional audio.
func makeVideo(t *testing.T, ff *ffmpeg.Client, path string, w, h, seconds, kbps int, audio bool) {
	t.Helper()
	args := []string{"-y", "-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=%dx%d:rate=30", w, h)}
	if audio {
		args = append(args, "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000")
	}
	args = append(args, "-t", strconv.Itoa(seconds),
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-b:v", fmt.Sprintf("%dk", kbps))
	if audio {
		args = append(args, "-c:a", "aac", "-b:a", "128k", "-shortest")
	}
	args = append(args, "-movflags", "+faststart", path)
	runFF(t, ff.Tools.FFmpeg, args...)
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return st.Size()
}

func TestProcessSingleIntegration(t *testing.T) {
	ff := testClient(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "한글 영상.mp4")
	makeVideo(t, ff, src, 640, 360, 4, 1500, true)

	eng := New(ff, filepath.Join(dir, "tmp"))
	var stages []string
	res, err := eng.Process(context.Background(), Request{
		InputPath: src, TargetBytes: 400_000, PartBytes: 300_000,
	}, func(p Progress) { stages = append(stages, p.Stage) })
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if res.Mode != media.ModeSingle || len(res.Outputs) != 1 {
		t.Fatalf("result = %+v", res)
	}
	out := res.Outputs[0]
	if out.Bytes > 400_000 {
		t.Fatalf("output size %d exceeds target", out.Bytes)
	}
	if filepath.Base(out.Path) != "한글 영상_compressed.mp4" {
		t.Fatalf("output name = %q", out.Path)
	}
	if out.Bytes >= fileSize(t, src) {
		t.Fatalf("output (%d) not smaller than source (%d)", out.Bytes, fileSize(t, src))
	}
	if _, err := os.Stat(out.Path); err != nil {
		t.Fatalf("output missing: %v", err)
	}
	if len(stages) == 0 {
		t.Fatal("no progress reports")
	}
}

func TestProcessSingleFallsBackToSplitIntegration(t *testing.T) {
	ff := testClient(t)
	dir := t.TempDir()
	// A long clip cannot meet a tiny single-file target even at 480p, so the
	// engine must switch to splitting.
	src := filepath.Join(dir, "long.mp4")
	makeVideo(t, ff, src, 640, 360, 60, 700, true)

	eng := New(ff, filepath.Join(dir, "tmp"))
	res, err := eng.Process(context.Background(), Request{
		InputPath: src, TargetBytes: 700_000, PartBytes: 500_000,
	}, nil)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if res.Mode != media.ModeSplit || len(res.Outputs) < 2 {
		t.Fatalf("result = %+v", res)
	}
	for _, o := range res.Outputs {
		if o.Bytes > 500_000 {
			t.Fatalf("part %s is %d bytes", o.Path, o.Bytes)
		}
	}
}

func TestProcessSkipIntegration(t *testing.T) {
	ff := testClient(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "tiny.mp4")
	makeVideo(t, ff, src, 320, 240, 2, 200, false)

	eng := New(ff, filepath.Join(dir, "tmp"))
	res, err := eng.Process(context.Background(), Request{
		InputPath: src, TargetBytes: 5_000_000, PartBytes: 4_000_000,
	}, nil)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if !res.Skipped || res.Mode != media.ModeSkipped {
		t.Fatalf("result = %+v", res)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, en := range entries {
		if strings.Contains(en.Name(), "_compressed") {
			t.Fatalf("skip must not create outputs, found %s", en.Name())
		}
	}
}

func TestProcessSplitIntegration(t *testing.T) {
	ff := testClient(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "series.mp4")
	makeVideo(t, ff, src, 640, 360, 45, 600, true)

	eng := New(ff, filepath.Join(dir, "tmp"))
	res, err := eng.Process(context.Background(), Request{
		InputPath: src, TargetBytes: 1_500_000, PartBytes: 1_200_000,
	}, nil)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if res.Mode != media.ModeSplit {
		t.Fatalf("mode = %s (result %+v)", res.Mode, res)
	}
	if len(res.Outputs) < 2 {
		t.Fatalf("expected multiple parts, got %d", len(res.Outputs))
	}
	var sum float64
	for i, o := range res.Outputs {
		if o.Bytes > 1_200_000 {
			t.Fatalf("part %d size %d exceeds part target", i+1, o.Bytes)
		}
		if _, err := os.Stat(o.Path); err != nil {
			t.Fatalf("part %d missing: %v", i+1, err)
		}
		if !strings.Contains(filepath.Base(o.Path), "_part") {
			t.Fatalf("part %d name = %q", i+1, o.Path)
		}
		sum += o.Duration
	}
	if sum < 40 || sum > 50 {
		t.Fatalf("part durations sum to %.1fs", sum)
	}
}

// The part limit here is below what a single-quality part can produce, so the
// engine must repair and/or halve segments. The assertions are path-agnostic:
// whatever route it takes, every part must fit and the mode must be split.
func TestProcessSplitRepairAndHalveIntegration(t *testing.T) {
	ff := testClient(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "tough.mp4")
	makeVideo(t, ff, src, 640, 360, 30, 600, true)

	eng := New(ff, filepath.Join(dir, "tmp"))
	res, err := eng.Process(context.Background(), Request{
		InputPath: src, TargetBytes: 400_000, PartBytes: 260_000,
	}, nil)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if res.Mode != media.ModeSplit {
		t.Fatalf("mode = %s", res.Mode)
	}
	if !res.LowQuality {
		t.Fatal("expected LowQuality to be set for a repaired split")
	}
	if len(res.Outputs) < 2 {
		t.Fatalf("expected at least 2 parts, got %d", len(res.Outputs))
	}
	for i, o := range res.Outputs {
		if o.Bytes > 260_000 {
			t.Fatalf("part %d size %d exceeds 260000", i+1, o.Bytes)
		}
	}
}
