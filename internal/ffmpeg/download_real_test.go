package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestInstallRealFFmpeg downloads FFmpeg into the app cache. It is opt-in via
// VC_INSTALL_FFMPEG to avoid 115MB downloads during normal test runs.
func TestInstallRealFFmpeg(t *testing.T) {
	if os.Getenv("VC_INSTALL_FFMPEG") != "1" {
		t.Skip("set VC_INSTALL_FFMPEG=1 to run")
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(cache, "VideoCompress", "ffmpeg", "bin")
	d := Downloader{}
	tools, err := d.Download(context.Background(), dest, func(done, total int64) {
		if done%(20<<20) < 256*1024 {
			t.Logf("downloaded %d/%d MB", done>>20, total>>20)
		}
	})
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	t.Logf("installed: %s", tools.FFmpeg)
}
