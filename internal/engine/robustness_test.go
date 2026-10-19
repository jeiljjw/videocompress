package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"videocompress/internal/media"
)

// scriptedFF answers probes from a function and records (and materialises) the
// files ffmpeg would have produced.
type scriptedFF struct {
	src   string
	probe func(path string) (*media.Info, error)
	runs  [][]string
	nRuns int
	onRun func(args []string)
}

func (s *scriptedFF) Probe(_ context.Context, path string) (*media.Info, error) {
	return s.probe(path)
}

func (s *scriptedFF) Run(_ context.Context, args []string, _ float64, _ func(float64)) error {
	s.nRuns++
	s.runs = append(s.runs, args)
	if s.onRun != nil {
		s.onRun(args)
	}
	for _, a := range args {
		// The segment muxer takes a pattern, not a file name.
		if strings.Contains(a, "%04d") {
			continue
		}
		if strings.HasSuffix(a, ".mp4") && filepath.IsAbs(a) && a != s.src {
			if err := os.MkdirAll(filepath.Dir(a), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(a, []byte("x"), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *scriptedFF) ranAny(substr string) bool {
	for _, args := range s.runs {
		for _, a := range args {
			if strings.Contains(a, substr) {
				return true
			}
		}
	}
	return false
}

func sourceInfo(path string) *media.Info {
	return &media.Info{
		Path: path, SizeBytes: 50_000_000, Duration: 20, Width: 1920, Height: 1080,
		VideoCodec: "h264", Audio: &media.AudioInfo{Codec: "aac", BitrateKbps: 96, Channels: 2},
	}
}

// A verified-but-structurally-broken output (right size, wrong duration) cannot
// be fixed by trying a lower resolution, so the engine must fail with the real
// reason instead of spending two more passes per ladder rung.
func TestProcessSingleFailsFastOnStructuralVerifyFailure(t *testing.T) {
	src := filepath.Join(t.TempDir(), "a.mp4")
	ff := &scriptedFF{src: src, probe: func(path string) (*media.Info, error) {
		if path == src {
			return sourceInfo(src), nil
		}
		// Small enough, but only 3s instead of 20s.
		return &media.Info{Path: path, SizeBytes: 1_000_000, Duration: 3, Width: 1280, Height: 720, VideoCodec: "h264"}, nil
	}}
	eng := New(ff, t.TempDir())

	_, err := eng.Process(context.Background(), Request{
		InputPath: src, OutDir: t.TempDir(), TargetBytes: 5_000_000, PartBytes: 4_000_000,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "출력 검증 실패") {
		t.Fatalf("err = %v, want a verification failure", err)
	}
	if ff.nRuns != 2 {
		t.Fatalf("ran %d encodes, want exactly one 2-pass encode", ff.nRuns)
	}
}

// The remux that makes a segment faststart can push it back over the part size.
// Verification would then fail the whole job, so the part must be re-encoded.
func TestSplitReencodesWhenRemuxGrowsPastLimit(t *testing.T) {
	src := filepath.Join(t.TempDir(), "movie.mp4")
	partBytes := int64(1_000_000)

	ff := &scriptedFF{src: src}
	ff.probe = func(path string) (*media.Info, error) {
		base := filepath.Base(path)
		switch {
		case path == src:
			info := sourceInfo(src)
			info.Duration = 60
			return info, nil
		case strings.Contains(base, "seg_"):
			// Under the limit before the container rewrite.
			return &media.Info{Path: path, SizeBytes: 900_000, Duration: 60, Width: 1280, Height: 720, VideoCodec: "h264", Audio: &media.AudioInfo{Codec: "aac", Channels: 2}}, nil
		case strings.Contains(base, "remux-"):
			// faststart pushed it over the limit.
			return &media.Info{Path: path, SizeBytes: partBytes + 1, Duration: 60, Width: 1280, Height: 720, VideoCodec: "h264", Audio: &media.AudioInfo{Codec: "aac", Channels: 2}}, nil
		default:
			return &media.Info{Path: path, SizeBytes: 800_000, Duration: 60, Width: 1280, Height: 720, VideoCodec: "h264", Audio: &media.AudioInfo{Codec: "aac", Channels: 2}}, nil
		}
	}
	// One raw segment is enough to exercise fitSegment.
	ff.onRun = func(args []string) {
		for _, a := range args {
			if strings.Contains(a, "seg_%04d") {
				p := strings.Replace(a, "seg_%04d", "seg_0001", 1)
				if err := os.WriteFile(p, []byte("x"), 0o644); err == nil {
					return
				}
			}
		}
	}
	eng := New(ff, t.TempDir())

	// target 2MB / part 1MB cannot be met by a single file, so this splits.
	res, err := eng.Process(context.Background(), Request{
		InputPath: src, OutDir: t.TempDir(), TargetBytes: 2_000_000, PartBytes: partBytes,
	}, nil)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if res.Mode != media.ModeSplit || len(res.Outputs) == 0 {
		t.Fatalf("result = %+v", res)
	}
	if res.Outputs[0].Bytes > partBytes {
		t.Fatalf("part bytes = %d, over the limit", res.Outputs[0].Bytes)
	}
	if !ff.ranAny("repair-") {
		t.Fatalf("the oversized remux was not re-encoded; runs = %v", ff.runs)
	}
}
