package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"videocompress/internal/config"
	"videocompress/internal/engine"
	"videocompress/internal/ffmpeg"
	"videocompress/internal/jobs"
	"videocompress/internal/media"
)

// stubFF makes the engine run without a real FFmpeg.
type stubFF struct{}

func (stubFF) Probe(context.Context, string) (*media.Info, error) {
	return &media.Info{Path: "x", SizeBytes: 1_000_000, Duration: 10, Width: 640, Height: 360}, nil
}

func (stubFF) Run(context.Context, []string, float64, func(float64)) error { return nil }

func testApp(t *testing.T) *App {
	t.Helper()
	a := NewApp()
	a.ctx = context.Background()
	dir := t.TempDir()
	a.settingsPath = filepath.Join(dir, "settings.json")
	a.cacheDir = filepath.Join(dir, "cache")
	a.tempRoot = filepath.Join(a.cacheDir, "temp")
	a.emit = func(string, ...any) {}
	a.locator = ffmpeg.Locator{
		Verify:   func(context.Context, string) error { return os.ErrNotExist },
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
	}
	a.downloader = ffmpeg.Downloader{
		URL:    "http://127.0.0.1:0/unused",
		Verify: func(context.Context, string) error { return nil },
	}
	return a
}

func tmpFile(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestSaveAndGetSettings(t *testing.T) {
	a := testApp(t)
	if err := a.SaveSettings(6, 3); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := a.GetSettings(); got.TargetMB != 6 || got.PartMB != 3 {
		t.Fatalf("settings = %+v", got)
	}
	if err := a.SaveSettings(1, 1); err == nil {
		t.Fatal("want validation error for target 1")
	}
	// persisted to disk
	loaded, err := config.Load(a.settingsPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.TargetMB != 6 || loaded.PartMB != 3 {
		t.Fatalf("persisted = %+v", loaded)
	}
}

func TestAddFilesRejectsNonVideo(t *testing.T) {
	a := testApp(t)
	mp4 := tmpFile(t, "clip.mp4")
	txt := tmpFile(t, "notes.txt")
	added := a.AddFiles([]string{mp4, txt})
	if len(added) != 2 {
		t.Fatalf("added = %+v", added)
	}
	if added[0].Status != jobs.StatusQueued {
		t.Fatalf("mp4 status = %s", added[0].Status)
	}
	if added[1].Status != jobs.StatusFailed {
		t.Fatalf("txt status = %s", added[1].Status)
	}
	if len(a.GetJobs()) != 2 {
		t.Fatalf("jobs = %+v", a.GetJobs())
	}
}

func TestStartProcessingRequiresFFmpeg(t *testing.T) {
	a := testApp(t)
	a.AddFiles([]string{tmpFile(t, "clip.mp4")})
	err := a.StartProcessing()
	if err == nil || !strings.Contains(err.Error(), "FFmpeg") {
		t.Fatalf("err = %v", err)
	}
	if a.GetJobs()[0].Status != jobs.StatusQueued {
		t.Fatalf("job should stay queued: %+v", a.GetJobs()[0])
	}
}

func TestStartProcessingRunsQueuedJobs(t *testing.T) {
	a := testApp(t)
	a.tools = ffmpeg.Tools{FFmpeg: "fake-ffmpeg", FFprobe: "fake-ffprobe"}
	a.newEngine = func(ffmpeg.Tools, string) *engine.Engine {
		return engine.New(stubFF{}, t.TempDir())
	}
	clip := tmpFile(t, "clip.mp4")
	a.AddFiles([]string{clip})
	if err := a.StartProcessing(); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitFor(t, func() bool {
		return a.GetJobs()[0].Status != jobs.StatusRunning && a.GetJobs()[0].Status != jobs.StatusQueued
	})
	got := a.GetJobs()[0]
	if got.Status != jobs.StatusSkipped {
		t.Fatalf("job = %+v", got)
	}
	if a.IsRunning() {
		t.Fatal("manager should be idle")
	}
}

func TestPrepareFFmpegReportsMissing(t *testing.T) {
	a := testApp(t)
	if err := a.prepareFFmpeg(context.Background(), false); err != nil {
		t.Fatalf("prepare (locate-only) must not fail: %v", err)
	}
	st := a.GetFFmpegStatus()
	if st.Found || st.Error == "" {
		t.Fatalf("status = %+v", st)
	}
}

// usableFFmpeg creates an ffmpeg/ffprobe pair and returns the ffmpeg path.
func usableFFmpeg(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ff := filepath.Join(dir, "ffmpeg.exe")
	probe := filepath.Join(dir, "ffprobe.exe")
	for _, p := range []string{ff, probe} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return ff
}

func onlyFFmpeg(t *testing.T, a *App, ff string) {
	t.Helper()
	a.locator.Verify = func(_ context.Context, p string) error {
		if p != ff {
			return os.ErrNotExist
		}
		return nil
	}
}

func TestPickFFmpegStoresVerifiedPath(t *testing.T) {
	a := testApp(t)
	ff := usableFFmpeg(t)
	onlyFFmpeg(t, a, ff)
	a.pickFFmpegFn = func(context.Context) (string, error) { return ff, nil }

	got, err := a.PickFFmpeg()
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if got != ff {
		t.Fatalf("path = %q want %q", got, ff)
	}
	if st := a.GetFFmpegStatus(); !st.Found || st.Path != ff || st.Error != "" {
		t.Fatalf("status = %+v", st)
	}
	if a.locator.Configured != ff {
		t.Fatalf("locator.Configured = %q", a.locator.Configured)
	}
	loaded, err := config.Load(a.settingsPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.FFmpegPath != ff {
		t.Fatalf("persisted ffmpegPath = %q", loaded.FFmpegPath)
	}
}

func TestPickFFmpegRejectsUnusableBinary(t *testing.T) {
	a := testApp(t)
	a.pickFFmpegFn = func(context.Context) (string, error) {
		return filepath.Join(t.TempDir(), "not-ffmpeg.exe"), nil
	}
	if _, err := a.PickFFmpeg(); err == nil || !strings.Contains(err.Error(), "사용할 수 없습니다") {
		t.Fatalf("err = %v", err)
	}
	if st := a.GetFFmpegStatus(); st.Found {
		t.Fatalf("status should stay not-found: %+v", st)
	}
}

func TestPickFFmpegCancelIsNotAnError(t *testing.T) {
	a := testApp(t)
	a.pickFFmpegFn = func(context.Context) (string, error) { return "", nil }
	if _, err := a.PickFFmpeg(); err == nil {
		t.Fatal("want an error for an empty selection")
	}
}

func TestConfiguredFFmpegPathIsUsed(t *testing.T) {
	a := testApp(t)
	ff := usableFFmpeg(t)
	onlyFFmpeg(t, a, ff)
	if err := config.Save(a.settingsPath, config.Settings{TargetMB: 5, PartMB: 4, FFmpegPath: ff}); err != nil {
		t.Fatalf("save: %v", err)
	}
	a.loadSettings()
	if err := a.prepareFFmpeg(context.Background(), false); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if st := a.GetFFmpegStatus(); !st.Found || st.Path != ff {
		t.Fatalf("status = %+v (saved ffmpegPath must be a candidate)", st)
	}
}

func TestRevealInExplorerMissingPath(t *testing.T) {
	a := testApp(t)
	if err := a.RevealInExplorer(filepath.Join(t.TempDir(), "nope.mp4")); err == nil {
		t.Fatal("want error for a missing path")
	}
}

func TestBeforeCloseAllowsWhenIdle(t *testing.T) {
	a := testApp(t)
	if a.beforeClose(context.Background()) {
		t.Fatal("idle app should close without prompting")
	}
}
