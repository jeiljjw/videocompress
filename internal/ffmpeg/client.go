package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"videocompress/internal/media"
)

// Progress holds the parsed values of one FFmpeg -progress block.
type Progress struct {
	Seconds float64
	Speed   float64
}

// Client executes ffmpeg/ffprobe.
type Client struct {
	Tools Tools
	Logf  func(format string, args ...any)
}

// Probe runs ffprobe and parses the result. File size comes from os.Stat so it
// is always the real on-disk size.
func (c *Client) Probe(ctx context.Context, path string) (*media.Info, error) {
	if c.Tools.FFprobe == "" {
		return nil, ErrNotFound
	}
	cmd := exec.CommandContext(ctx, c.Tools.FFprobe,
		"-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
	cmd.SysProcAttr = hiddenProcAttr()
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe 실패: %w", err)
	}
	size := int64(0)
	if st, statErr := os.Stat(path); statErr == nil {
		size = st.Size()
	}
	return media.ParseProbeJSON(out, path, size)
}

// Run executes ffmpeg with progress reporting. The args must not include
// -hide_banner/-progress/-nostats; they are added here.
func (c *Client) Run(ctx context.Context, args []string, totalSec float64, onProgress func(percent float64)) error {
	if c.Tools.FFmpeg == "" {
		return ErrNotFound
	}
	full := append([]string{
		"-hide_banner", "-nostdin", "-loglevel", "error",
		"-progress", "pipe:1", "-nostats",
	}, args...)
	cmd := exec.CommandContext(ctx, c.Tools.FFmpeg, full...)
	cmd.SysProcAttr = hiddenProcAttr()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := &tailWriter{max: 32 * 1024}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var p Progress
	for scanner.Scan() {
		done := ParseProgressLine(scanner.Text(), &p)
		if onProgress != nil && totalSec > 0 && p.Seconds > 0 {
			pct := p.Seconds / totalSec * 100
			if pct < 0 {
				pct = 0
			}
			if pct > 99.9 && !done {
				pct = 99.9
			}
			onProgress(pct)
		}
	}
	waitErr := cmd.Wait()
	if waitErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg 실패: %w\n%s", waitErr, stderr.String())
	}
	if onProgress != nil {
		onProgress(100)
	}
	return nil
}

// ParseProgressLine updates p from one key=value line and reports whether the
// block ended (progress=end).
func ParseProgressLine(line string, p *Progress) bool {
	line = strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "out_time_us="):
		if v, err := strconv.ParseInt(strings.TrimPrefix(line, "out_time_us="), 10, 64); err == nil {
			p.Seconds = float64(v) / 1e6
		}
	case strings.HasPrefix(line, "out_time_ms="):
		// Historically this value is microseconds despite the name.
		if v, err := strconv.ParseInt(strings.TrimPrefix(line, "out_time_ms="), 10, 64); err == nil {
			p.Seconds = float64(v) / 1e6
		}
	case strings.HasPrefix(line, "out_time="):
		if sec, ok := parseClock(strings.TrimPrefix(line, "out_time=")); ok {
			p.Seconds = sec
		}
	case strings.HasPrefix(line, "speed="):
		s := strings.TrimSuffix(strings.TrimPrefix(line, "speed="), "x")
		if v, err := strconv.ParseFloat(s, 64); err == nil && v > 0 {
			p.Speed = v
		}
	case line == "progress=end":
		return true
	}
	return false
}

func parseClock(s string) (float64, bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, false
	}
	h, err1 := strconv.ParseFloat(parts[0], 64)
	m, err2 := strconv.ParseFloat(parts[1], 64)
	sec, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, false
	}
	return h*3600 + m*60 + sec, true
}

// tailWriter keeps only the last max bytes written to it.
type tailWriter struct {
	max int
	buf bytes.Buffer
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	if w.buf.Len() > w.max {
		b := w.buf.Bytes()
		rest := b[w.buf.Len()-w.max:]
		kept := make([]byte, len(rest))
		copy(kept, rest)
		w.buf.Reset()
		w.buf.Write(kept)
	}
	return len(p), nil
}

func (w *tailWriter) String() string { return w.buf.String() }
