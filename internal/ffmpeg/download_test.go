package ffmpeg

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func makeZip(t *testing.T, entries map[string][]byte) string {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, data := range entries {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "in.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractToolsFindsNestedBinaries(t *testing.T) {
	zipPath := makeZip(t, map[string][]byte{
		"ffmpeg-8.0-essentials_build/bin/ffmpeg.exe":  []byte("ff"),
		"ffmpeg-8.0-essentials_build/bin/ffprobe.exe": []byte("fp"),
		"ffmpeg-8.0-essentials_build/README.txt":      []byte("readme"),
	})
	dest := t.TempDir()
	tools, err := ExtractTools(zipPath, dest)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if tools.FFmpeg != filepath.Join(dest, "ffmpeg.exe") {
		t.Fatalf("ffmpeg path = %q", tools.FFmpeg)
	}
	if tools.FFprobe != filepath.Join(dest, "ffprobe.exe") {
		t.Fatalf("ffprobe path = %q", tools.FFprobe)
	}
}

func TestExtractToolsIgnoresPathTraversal(t *testing.T) {
	zipPath := makeZip(t, map[string][]byte{
		"../../evil.exe":  []byte("evil"),
		"bin/ffmpeg.exe":  []byte("ff"),
		"bin/ffprobe.exe": []byte("fp"),
	})
	dest := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractTools(zipPath, dest); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "evil.exe")); err == nil {
		t.Fatal("evil.exe should not have been extracted")
	}
}

func TestExtractToolsMissingBinary(t *testing.T) {
	zipPath := makeZip(t, map[string][]byte{"bin/ffmpeg.exe": []byte("ff")})
	if _, err := ExtractTools(zipPath, t.TempDir()); err == nil {
		t.Fatal("want error when ffprobe is missing")
	}
}

func TestDownloadInstallsAndVerifies(t *testing.T) {
	zipPath := makeZip(t, map[string][]byte{
		"ffmpeg-8.0-essentials_build/bin/ffmpeg.exe":  []byte("ff"),
		"ffmpeg-8.0-essentials_build/bin/ffprobe.exe": []byte("fp"),
	})
	data, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	var lastDone, lastTotal int64
	dest := filepath.Join(t.TempDir(), "ffmpeg", "bin")
	d := Downloader{
		URL:    srv.URL,
		Client: srv.Client(),
		Verify: func(_ context.Context, ffmpegPath string) error {
			if _, err := os.Stat(ffmpegPath); err != nil {
				return err
			}
			return nil
		},
	}
	tools, err := d.Download(context.Background(), dest, func(done, total int64) {
		lastDone, lastTotal = done, total
	})
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if lastDone != int64(len(data)) || lastTotal != int64(len(data)) {
		t.Fatalf("progress = %d/%d", lastDone, lastTotal)
	}
	for _, p := range []string{tools.FFmpeg, tools.FFprobe} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing %s: %v", p, err)
		}
	}
}

func TestDownloadRejectsTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 1024))
	}))
	defer srv.Close()
	d := Downloader{URL: srv.URL, Client: srv.Client(), MaxBytes: 100}
	if _, err := d.Download(context.Background(), filepath.Join(t.TempDir(), "bin"), nil); err == nil {
		t.Fatal("want size limit error")
	}
}

func TestDownloadRejectsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	d := Downloader{URL: srv.URL, Client: srv.Client()}
	if _, err := d.Download(context.Background(), filepath.Join(t.TempDir(), "bin"), nil); err == nil {
		t.Fatal("want HTTP error")
	}
}
