package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ErrNotFound is returned when no usable FFmpeg installation is available.
var ErrNotFound = errors.New("FFmpeg를 찾을 수 없습니다")

// Tools are the absolute paths of the ffmpeg and ffprobe executables.
type Tools struct {
	FFmpeg  string
	FFprobe string
}

// Locator searches for FFmpeg in a deterministic order.
type Locator struct {
	Override   string // --ffmpeg flag
	Configured string // settings.json ffmpegPath
	AppDir     string // directory of the running executable
	CacheDir   string // <UserCacheDir>/VideoCompress
	LookPath   func(string) (string, error)
	Verify     func(ctx context.Context, ffmpegPath string) error
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func (l Locator) verify() func(context.Context, string) error {
	if l.Verify != nil {
		return l.Verify
	}
	return DefaultVerify
}

// Candidates returns the search order, dropping empty entries.
func (l Locator) Candidates() []string {
	var out []string
	add := func(p string) {
		if strings.TrimSpace(p) == "" {
			return
		}
		for _, existing := range out {
			if existing == p {
				return
			}
		}
		out = append(out, p)
	}
	add(l.Override)
	add(l.Configured)
	if l.AppDir != "" {
		add(filepath.Join(l.AppDir, "bin", "ffmpeg"+exeSuffix()))
	}
	if l.CacheDir != "" {
		add(filepath.Join(l.CacheDir, "ffmpeg", "bin", "ffmpeg"+exeSuffix()))
	}
	if l.LookPath != nil {
		if p, err := l.LookPath("ffmpeg"); err == nil {
			add(p)
		}
	}
	return out
}

// Locate returns the first candidate that passes verification.
func (l Locator) Locate(ctx context.Context) (Tools, error) {
	verify := l.verify()
	var errs []string
	for _, candidate := range l.Candidates() {
		if err := verify(ctx, candidate); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", candidate, err))
			continue
		}
		return Tools{FFmpeg: candidate, FFprobe: siblingFFprobe(candidate)}, nil
	}
	if len(errs) == 0 {
		return Tools{}, ErrNotFound
	}
	return Tools{}, fmt.Errorf("%w (%s)", ErrNotFound, strings.Join(errs, "; "))
}

func siblingFFprobe(ffmpegPath string) string {
	return filepath.Join(filepath.Dir(ffmpegPath), "ffprobe"+exeSuffix())
}

// DefaultVerify runs `ffmpeg -version` and checks ffprobe exists.
func DefaultVerify(ctx context.Context, ffmpegPath string) error {
	probe := siblingFFprobe(ffmpegPath)
	if err := runVersion(ctx, probe); err != nil {
		return fmt.Errorf("ffprobe: %w", err)
	}
	if err := runVersion(ctx, ffmpegPath); err != nil {
		return err
	}
	return nil
}

func runVersion(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "-version")
	cmd.SysProcAttr = hiddenProcAttr()
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	if !strings.Contains(string(out), "version") {
		return fmt.Errorf("예상한 버전 출력이 아닙니다: %s", path)
	}
	return nil
}
