package ffmpeg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fakeVerify succeeds when both ffmpeg and ffprobe files exist.
func fakeVerify(t *testing.T) func(context.Context, string) error {
	t.Helper()
	return func(_ context.Context, ffmpegPath string) error {
		if _, err := os.Stat(ffmpegPath); err != nil {
			return err
		}
		probe := filepath.Join(filepath.Dir(ffmpegPath), "ffprobe"+exeSuffix())
		if _, err := os.Stat(probe); err != nil {
			return errors.New("ffprobe missing")
		}
		return nil
	}
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestLocatePrefersOverride(t *testing.T) {
	dir := t.TempDir()
	overrideDir := filepath.Join(dir, "override")
	configuredDir := filepath.Join(dir, "configured")
	touch(t, filepath.Join(overrideDir, "ffmpeg"+exeSuffix()))
	touch(t, filepath.Join(overrideDir, "ffprobe"+exeSuffix()))
	touch(t, filepath.Join(configuredDir, "ffmpeg"+exeSuffix()))
	touch(t, filepath.Join(configuredDir, "ffprobe"+exeSuffix()))

	loc := Locator{
		Override:   filepath.Join(overrideDir, "ffmpeg"+exeSuffix()),
		Configured: filepath.Join(configuredDir, "ffmpeg"+exeSuffix()),
		Verify:     fakeVerify(t),
	}
	got, err := loc.Locate(context.Background())
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if got.FFmpeg != filepath.Join(overrideDir, "ffmpeg"+exeSuffix()) {
		t.Fatalf("got %q", got.FFmpeg)
	}
}

func TestLocateOrderAndCache(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	touch(t, filepath.Join(cacheDir, "ffmpeg", "bin", "ffmpeg"+exeSuffix()))
	touch(t, filepath.Join(cacheDir, "ffmpeg", "bin", "ffprobe"+exeSuffix()))

	loc := Locator{
		CacheDir: cacheDir,
		AppDir:   filepath.Join(dir, "app"),
		LookPath: func(string) (string, error) { return "", errors.New("not on path") },
		Verify:   fakeVerify(t),
	}
	got, err := loc.Locate(context.Background())
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if got.FFmpeg != filepath.Join(cacheDir, "ffmpeg", "bin", "ffmpeg"+exeSuffix()) {
		t.Fatalf("got %q", got.FFmpeg)
	}
	if got.FFprobe != filepath.Join(cacheDir, "ffmpeg", "bin", "ffprobe"+exeSuffix()) {
		t.Fatalf("ffprobe = %q", got.FFprobe)
	}
}

func TestLocateFallsBackToPath(t *testing.T) {
	dir := t.TempDir()
	pathDir := filepath.Join(dir, "pathbin")
	touch(t, filepath.Join(pathDir, "ffmpeg"+exeSuffix()))
	touch(t, filepath.Join(pathDir, "ffprobe"+exeSuffix()))

	loc := Locator{
		LookPath: func(name string) (string, error) { return filepath.Join(pathDir, name+exeSuffix()), nil },
		Verify:   fakeVerify(t),
	}
	got, err := loc.Locate(context.Background())
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if filepath.Dir(got.FFmpeg) != pathDir {
		t.Fatalf("got %q", got.FFmpeg)
	}
}

func TestLocateNotFound(t *testing.T) {
	loc := Locator{
		LookPath: func(string) (string, error) { return "", errors.New("no") },
		Verify:   func(context.Context, string) error { return errors.New("nope") },
	}
	_, err := loc.Locate(context.Background())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}
