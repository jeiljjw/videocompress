package engine

import (
	"strings"
	"testing"
)

func join(args []string) string { return strings.Join(args, " ") }

func TestSingleArgsPass1(t *testing.T) {
	spec := EncodeSpec{
		Input: "in.mp4", PassLog: "log", Pass: 1,
		Width: 1280, Height: 720, Scale: true, VideoKbps: 917,
		AudioMode: AudioNone,
	}
	args := SingleArgs(spec)
	got := join(args)
	for _, want := range []string{"-i in.mp4", "-vf scale=1280:720", "-b:v 917k", "-pass 1", "-passlogfile log", "-an", "-f null -"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "-movflags") {
		t.Fatal("pass 1 must not set movflags")
	}
}

func TestSingleArgsPass2CopyWithFaststart(t *testing.T) {
	spec := EncodeSpec{
		Input: "in.mp4", Output: "out.mp4", PassLog: "log", Pass: 2,
		Width: 1920, Height: 1080, Scale: false, VideoKbps: 1804,
		AudioMode: AudioCopy, FastStart: true,
	}
	args := SingleArgs(spec)
	got := join(args)
	for _, want := range []string{"-map 0:a:0", "-c:a copy", "-movflags +faststart", "-f mp4 out.mp4"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "-vf") {
		t.Fatal("must not scale when Scale=false")
	}
	if strings.Contains(got, "-b:a") {
		t.Fatal("copy must not set audio bitrate")
	}
}

func TestSingleArgsPass2EncodeAudio(t *testing.T) {
	spec := EncodeSpec{
		Input: "in.mp4", Output: "out.mp4", PassLog: "log", Pass: 2,
		Width: 854, Height: 480, VideoKbps: 410,
		AudioMode: AudioAAC, AudioKbps: 64, AudioChannels: 1, FastStart: true,
	}
	got := join(SingleArgs(spec))
	for _, want := range []string{"-c:a aac", "-b:a 64k", "-ac 1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
}

func TestSingleArgsNoAudio(t *testing.T) {
	spec := EncodeSpec{Input: "in.mp4", Output: "out.mp4", Pass: 2, VideoKbps: 500, Width: 640, Height: 360}
	got := join(SingleArgs(spec))
	if !strings.Contains(got, "-an") {
		t.Fatalf("missing -an in %s", got)
	}
}

func TestSingleArgsSegmentOutput(t *testing.T) {
	spec := EncodeSpec{
		Input: "in.mp4", PassLog: "log", Pass: 2, VideoKbps: 917,
		Width: 1280, Height: 720, Scale: true,
		AudioMode: AudioAAC, AudioKbps: 96, AudioChannels: 2,
		ForceKeySec: 30, SegmentSeconds: 30, SegmentPattern: "seg_%03d.mp4",
	}
	got := join(SingleArgs(spec))
	for _, want := range []string{
		"-force_key_frames expr:gte(t,n_forced*30)", "-sc_threshold 0",
		"-f segment", "-segment_time 30", "-segment_format mp4",
		"-reset_timestamps 1", "seg_%03d.mp4",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "-movflags") {
		t.Fatal("segment output must not set movflags")
	}
}

// kbpsWant recomputes the retry bitrate without constant folding so the
// expectation stays a real (non-constant) float expression.
func kbpsWant(current int, targetBytes, actualBytes int64) int {
	return int(float64(current) * float64(targetBytes) / float64(actualBytes) * 0.95)
}

func TestRetryKbps(t *testing.T) {
	// 917 kbps produced 4.4MB for a 4.0MB target.
	got := RetryKbps(917, 4_000_000, 4_400_000)
	if got >= 917 || got < 100 {
		t.Fatalf("got %d", got)
	}
	want := kbpsWant(917, 4_000_000, 4_400_000)
	if got != want {
		t.Fatalf("got %d want %d", got, want)
	}
	if RetryKbps(100, 4_000_000, 40_000_000) != 100 {
		t.Fatal("must clamp at 100")
	}
}
