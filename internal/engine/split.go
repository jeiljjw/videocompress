package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"videocompress/internal/media"
)

const (
	maxRepairAttempts = 3
	maxHalves         = 2
)

// processSplit encodes the source into keyframe-aligned segments, then makes
// sure every part is under PartBytes by re-encoding oversized segments and, as
// a last resort, halving them. Nothing is moved into place until every part
// has been probed and the group passes VerifyParts.
func (e *Engine) processSplit(ctx context.Context, req Request, info *media.Info, sp *media.SplitPlan, tmpDir string, rep *progressReporter) (*Result, error) {
	segDir := filepath.Join(tmpDir, "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		return nil, err
	}
	scale := sp.Width != info.Width || sp.Height != info.Height
	audioMode, audioKbps, audioChannels := AudioNone, 0, 0
	if info.HasAudio() {
		audioMode = AudioAAC
		audioKbps = sp.AudioKbps
		audioChannels = info.Audio.Channels
	}
	spec := EncodeSpec{
		Input:          info.Path,
		PassLog:        filepath.Join(tmpDir, "ffpass-split"),
		Width:          sp.Width,
		Height:         sp.Height,
		Scale:          scale,
		VideoKbps:      sp.VideoKbps,
		AudioMode:      audioMode,
		AudioKbps:      audioKbps,
		AudioChannels:  audioChannels,
		ForceKeySec:    sp.SegmentSeconds,
		SegmentSeconds: sp.SegmentSeconds,
		SegmentPattern: filepath.Join(segDir, "seg_%04d.mp4"),
	}
	enc := rep.scaled(0, 70)
	enc.emit("compressing", 0)
	pass1 := spec
	pass1.Pass = 1
	if err := e.FF.Run(ctx, SingleArgs(pass1), info.Duration, nil); err != nil {
		return nil, err
	}
	enc.emit("compressing", 40)
	pass2 := spec
	pass2.Pass = 2
	if err := e.FF.Run(ctx, SingleArgs(pass2), info.Duration, func(pct float64) {
		enc.emit("compressing", 40+pct*0.6)
	}); err != nil {
		return nil, err
	}
	enc.emit("compressing", 100)

	rawPaths, err := listSegments(segDir)
	if err != nil {
		return nil, err
	}
	if len(rawPaths) == 0 {
		return nil, fmt.Errorf("분할 결과를 만들지 못했습니다")
	}

	// Timestamps were reset per segment, so rebuild absolute offsets by
	// summing the probed durations in order.
	type rawSegment struct {
		path  string
		start float64
	}
	raws := make([]rawSegment, 0, len(rawPaths))
	var offset float64
	for _, p := range rawPaths {
		pi, err := e.FF.Probe(ctx, p)
		if err != nil {
			return nil, fmt.Errorf("생성된 조각을 확인할 수 없습니다: %w", err)
		}
		raws = append(raws, rawSegment{path: p, start: offset})
		offset += pi.Duration
	}

	repair := rep.scaled(70, 96)
	var parts []string
	repaired := false
	for i, raw := range raws {
		lo := float64(i) / float64(len(raws)) * 100
		hi := float64(i+1) / float64(len(raws)) * 100
		out, changed, err := e.fitSegment(ctx, req, info, sp, raw.path, raw.start, tmpDir, fmt.Sprintf("s%04d", i+1), 0, repair.scaled(lo, hi))
		if err != nil {
			return nil, fmt.Errorf("%d번째 조각 처리 실패: %w", i+1, err)
		}
		parts = append(parts, out...)
		repaired = repaired || changed
	}

	ver := rep.scaled(96, 99)
	infos := make([]*media.Info, 0, len(parts))
	for i, p := range parts {
		pi, err := e.FF.Probe(ctx, p)
		if err != nil {
			return nil, fmt.Errorf("조각 검증 실패: %w", err)
		}
		infos = append(infos, pi)
		ver.emit("verifying", float64(i+1)/float64(len(parts))*100)
	}
	if err := VerifyParts(info, infos, req.PartBytes); err != nil {
		return nil, err
	}

	finalPaths, err := SplitOutputPaths(info.Path, req.OutDir, len(parts))
	if err != nil {
		return nil, err
	}
	moved := make([]string, 0, len(parts))
	for i, p := range parts {
		if err := MoveFileNoReplace(p, finalPaths[i]); err != nil {
			for _, m := range moved {
				_ = os.Remove(m)
			}
			return nil, err
		}
		moved = append(moved, finalPaths[i])
	}
	outputs := make([]Output, 0, len(parts))
	for i, p := range finalPaths {
		outputs = append(outputs, Output{
			Path: p, Bytes: infos[i].SizeBytes, Duration: infos[i].Duration,
			Width: infos[i].Width, Height: infos[i].Height,
		})
	}
	return &Result{
		Mode:           media.ModeSplit,
		Outputs:        outputs,
		Width:          sp.Width,
		Height:         sp.Height,
		VideoKbps:      sp.VideoKbps,
		AudioKbps:      sp.AudioKbps,
		SegmentSeconds: sp.SegmentSeconds,
		Parts:          len(outputs),
		LowQuality:     sp.LowQuality || repaired,
	}, nil
}

// fitSegment guarantees one segment is at or below PartBytes and returns one
// or more faststart MP4 paths in playback order, plus whether the segment had
// to be re-encoded. start is the segment's offset in the source, used when
// re-encoding a range. tag identifies the originating segment and keeps every
// scratch file unique, otherwise re-encoded segments would overwrite each
// other and a part could be moved twice.
func (e *Engine) fitSegment(ctx context.Context, req Request, info *media.Info, sp *media.SplitPlan, path string, start float64, tmpDir, tag string, depth int, rep *progressReporter) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	cur, err := e.FF.Probe(ctx, path)
	if err != nil {
		return nil, false, err
	}
	if cur.SizeBytes <= req.PartBytes {
		out, err := e.remuxFastStart(ctx, path, tmpDir, depth)
		if err != nil {
			return nil, false, err
		}
		rep.emit("repairing", 100)
		return []string{out}, false, nil
	}

	kbps := RetryKbps(sp.VideoKbps, req.PartBytes, cur.SizeBytes)
	var lastPath string
	var lastInfo *media.Info
	for attempt := 1; attempt <= maxRepairAttempts; attempt++ {
		outPath := filepath.Join(tmpDir, fmt.Sprintf("repair-%s-d%d-a%d.mp4", tag, depth, attempt))
		spec := RangeSpec{
			Input: info.Path, Output: outPath,
			Start: start, Duration: cur.Duration,
			Width: sp.Width, Height: sp.Height,
			Scale:         sp.Width != info.Width || sp.Height != info.Height,
			VideoKbps:     kbps,
			AudioKbps:     sp.AudioKbps,
			AudioChannels: audioChannelsOf(info),
			HasAudio:      info.HasAudio(),
		}
		if err := e.FF.Run(ctx, RangeArgs(spec), cur.Duration, func(pct float64) {
			rep.emit("repairing", (float64(attempt-1)+pct/100)/float64(maxRepairAttempts)*100)
		}); err != nil {
			return nil, false, err
		}
		pi, err := e.FF.Probe(ctx, outPath)
		if err != nil {
			return nil, false, err
		}
		lastPath, lastInfo = outPath, pi
		if pi.SizeBytes <= req.PartBytes {
			rep.emit("repairing", 100)
			return []string{lastPath}, true, nil
		}
		next := RetryKbps(kbps, req.PartBytes, pi.SizeBytes)
		if next >= kbps {
			break // the bitrate is already at its floor; halving is the only option
		}
		kbps = next
	}

	if depth >= maxHalves {
		return nil, false, fmt.Errorf("조각을 조각 크기(%d bytes) 이하로 줄이지 못했습니다 (마지막 %d bytes)", req.PartBytes, lastInfo.SizeBytes)
	}
	half := lastInfo.Duration / 2
	if half < 1 {
		return nil, false, fmt.Errorf("조각을 더 나눌 수 없습니다 (길이 %.2fs)", lastInfo.Duration)
	}

	// Halving keeps the bitrate, so each half is roughly half the size.
	var out []string
	for i, seg := range []struct{ start, dur float64 }{
		{start, half},
		{start + half, lastInfo.Duration - half},
	} {
		sub := rep.scaled(float64(i)*50, float64(i+1)*50)
		halfPath := filepath.Join(tmpDir, fmt.Sprintf("half-%s-d%d-%d.mp4", tag, depth, i+1))
		spec := RangeSpec{
			Input: info.Path, Output: halfPath,
			Start: seg.start, Duration: seg.dur,
			Width: sp.Width, Height: sp.Height,
			Scale:         sp.Width != info.Width || sp.Height != info.Height,
			VideoKbps:     kbps,
			AudioKbps:     sp.AudioKbps,
			AudioChannels: audioChannelsOf(info),
			HasAudio:      info.HasAudio(),
		}
		if err := e.FF.Run(ctx, RangeArgs(spec), seg.dur, nil); err != nil {
			return nil, false, err
		}
		parts, _, err := e.fitSegment(ctx, req, info, sp, halfPath, seg.start, tmpDir, tag, depth+1, sub)
		if err != nil {
			return nil, false, err
		}
		out = append(out, parts...)
	}
	return out, true, nil
}

// remuxFastStart stream-copies a segment into a faststart MP4 so it can play
// without a full download. The temp file reserves a unique name.
func (e *Engine) remuxFastStart(ctx context.Context, src, tmpDir string, depth int) (string, error) {
	f, err := os.CreateTemp(tmpDir, fmt.Sprintf("remux-d%d-*.mp4", depth))
	if err != nil {
		return "", err
	}
	out := f.Name()
	if err := f.Close(); err != nil {
		return "", err
	}
	args := []string{"-y", "-i", src, "-map", "0", "-c", "copy", "-movflags", "+faststart", "-f", "mp4", out}
	if err := e.FF.Run(ctx, args, 0, nil); err != nil {
		return "", err
	}
	return out, nil
}

// listSegments returns segment files in ascending name order.
func listSegments(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, en := range entries {
		if en.IsDir() || !strings.HasSuffix(strings.ToLower(en.Name()), ".mp4") {
			continue
		}
		out = append(out, filepath.Join(dir, en.Name()))
	}
	sort.Strings(out)
	return out, nil
}

func audioChannelsOf(info *media.Info) int {
	if info.HasAudio() {
		return info.Audio.Channels
	}
	return 0
}
