package ffmpeg

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// DefaultDownloadURL points at the gyan.dev essentials build (~115MB).
	DefaultDownloadURL = "https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip"
	DefaultMaxBytes    = int64(400 << 20)
	maxExtractBytes    = int64(600 << 20)
)

// Downloader fetches and installs FFmpeg binaries.
type Downloader struct {
	URL      string
	Client   *http.Client
	Verify   func(ctx context.Context, ffmpegPath string) error
	MaxBytes int64
}

// Download fetches the ZIP, extracts ffmpeg/ffprobe into destBinDir, and
// verifies the result. Existing files are only replaced after a successful
// download because extraction happens in a temp directory first.
func (d Downloader) Download(ctx context.Context, destBinDir string, progress func(done, total int64)) (Tools, error) {
	url := d.URL
	if url == "" {
		url = DefaultDownloadURL
	}
	maxBytes := d.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Tools{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return Tools{}, fmt.Errorf("FFmpeg 다운로드 실패: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Tools{}, fmt.Errorf("FFmpeg 다운로드 실패: HTTP %d", resp.StatusCode)
	}

	parent := filepath.Dir(destBinDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return Tools{}, err
	}
	tmpDir, err := os.MkdirTemp(parent, "ffmpeg-dl-")
	if err != nil {
		return Tools{}, err
	}
	defer os.RemoveAll(tmpDir)

	zipPath := filepath.Join(tmpDir, "ffmpeg.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		return Tools{}, err
	}
	total := resp.ContentLength
	var done int64
	buf := make([]byte, 256*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			done += int64(n)
			if done > maxBytes {
				f.Close()
				return Tools{}, fmt.Errorf("다운로드 용량이 제한(%dMB)을 초과했습니다", maxBytes>>20)
			}
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return Tools{}, werr
			}
			if progress != nil {
				progress(done, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			f.Close()
			return Tools{}, fmt.Errorf("FFmpeg 다운로드 실패: %w", readErr)
		}
	}
	if err := f.Close(); err != nil {
		return Tools{}, err
	}

	extractDir := filepath.Join(tmpDir, "bin")
	tools, err := ExtractTools(zipPath, extractDir)
	if err != nil {
		return Tools{}, err
	}

	verify := d.Verify
	if verify == nil {
		verify = DefaultVerify
	}
	if err := verify(ctx, tools.FFmpeg); err != nil {
		return Tools{}, fmt.Errorf("다운로드한 FFmpeg 검증 실패: %w", err)
	}

	if err := os.MkdirAll(destBinDir, 0o755); err != nil {
		return Tools{}, err
	}
	final := Tools{
		FFmpeg:  filepath.Join(destBinDir, filepath.Base(tools.FFmpeg)),
		FFprobe: filepath.Join(destBinDir, filepath.Base(tools.FFprobe)),
	}
	for _, pair := range [][2]string{{tools.FFmpeg, final.FFmpeg}, {tools.FFprobe, final.FFprobe}} {
		if _, err := os.Stat(pair[1]); err == nil {
			continue // never clobber a working install
		}
		if err := os.Rename(pair[0], pair[1]); err != nil {
			return Tools{}, err
		}
	}
	return final, nil
}

// ExtractTools copies the first ffmpeg/ffprobe entries (matched by base name)
// from the ZIP into destDir. Other entries, including absolute or traversal
// paths, are ignored.
func ExtractTools(zipPath, destDir string) (Tools, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return Tools{}, err
	}
	defer r.Close()
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return Tools{}, err
	}
	var found Tools
	var total int64
	for _, f := range r.File {
		base := strings.ToLower(filepath.Base(f.Name))
		if (base != "ffmpeg.exe" && base != "ffprobe.exe") || f.FileInfo().IsDir() {
			continue
		}
		target := filepath.Join(destDir, base)
		if err := copyZipEntry(f, target, &total); err != nil {
			return Tools{}, err
		}
		if base == "ffmpeg.exe" {
			found.FFmpeg = target
		} else {
			found.FFprobe = target
		}
	}
	if found.FFmpeg == "" || found.FFprobe == "" {
		return Tools{}, errors.New("ZIP에서 ffmpeg.exe/ffprobe.exe를 찾지 못했습니다")
	}
	return found, nil
}

func copyZipEntry(f *zip.File, target string, total *int64) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	w, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer w.Close()
	n, err := io.Copy(w, io.LimitReader(rc, maxExtractBytes-*total+1))
	if err != nil {
		return err
	}
	*total += n
	if *total > maxExtractBytes {
		return errors.New("압축 해제 용량 제한을 초과했습니다")
	}
	return nil
}
