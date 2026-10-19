package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"videocompress/internal/media"
)

const maxSingleAttempts = 3

// processSingle walks the resolution ladder. Each rung is retried up to
// maxSingleAttempts times with a reduced bitrate when the output overshoots,
// then the next (lower) rung is tried. It returns errSingleExhausted when the
// whole ladder fails so Process can fall back to splitting.
func (e *Engine) processSingle(ctx context.Context, req Request, info *media.Info, plan *media.Plan, tmpDir string, rep *progressReporter) (*Result, error) {
	audioMode, audioKbps, audioChannels := AudioNone, 0, 0
	if info.HasAudio() {
		audioChannels = info.Audio.Channels
		audioKbps = plan.AudioKbps
		if plan.AudioCopy {
			audioMode = AudioCopy
		} else {
			audioMode = AudioAAC
		}
	}
	var lastErr error
	for _, stage := range plan.Stages {
		kbps := stage.VideoKbps
		scale := stage.Width != info.Width || stage.Height != info.Height
		label := fmt.Sprintf("compressing %s", stage.Label)
		rp := newRetryProgress(func(stage string, pct float64) { rep.emit(stage, pct) })
		for attempt := 1; attempt <= maxSingleAttempts; attempt++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			outPath := filepath.Join(tmpDir, fmt.Sprintf("single-%s-%dk-%d.mp4", stage.Label, kbps, attempt))
			pass1 := EncodeSpec{
				Input: info.Path, PassLog: filepath.Join(tmpDir, "ffpass-single"), Pass: 1,
				Width: stage.Width, Height: stage.Height, Scale: scale, VideoKbps: kbps,
			}
			if err := e.FF.Run(ctx, SingleArgs(pass1), info.Duration, nil); err != nil {
				return nil, err
			}
			pass2 := pass1
			pass2.Pass = 2
			pass2.Output = outPath
			pass2.AudioMode = audioMode
			pass2.AudioKbps = audioKbps
			pass2.AudioChannels = audioChannels
			pass2.FastStart = true
			if err := e.FF.Run(ctx, SingleArgs(pass2), info.Duration, func(pct float64) {
				rp.within(label, pct/100)
			}); err != nil {
				return nil, err
			}
			rp.succeed()
			outInfo, err := e.FF.Probe(ctx, outPath)
			if err != nil {
				return nil, fmt.Errorf("출력 검증 실패: %w", err)
			}
			if err := VerifySingle(info, outInfo, req.TargetBytes); err != nil {
				lastErr = err
				e.logf("%s %s attempt %d/%d rejected: %v", req.InputPath, stage.Label, attempt, maxSingleAttempts, err)
				if outInfo.SizeBytes <= req.TargetBytes {
					// The file is already small enough, so the problem is
					// structural (no video stream, or a duration that does not
					// match the source). No ladder rung can fix that, and
					// splitting cannot either: fail with the real reason
					// instead of burning more encodes.
					return nil, fmt.Errorf("출력 검증 실패: %w", err)
				}
				if attempt == maxSingleAttempts {
					break
				}
				next := RetryKbps(kbps, req.TargetBytes, outInfo.SizeBytes)
				if next >= kbps {
					break
				}
				rp.fail()
				kbps = next
				continue
			}
			finalPath, err := SingleOutputPath(info.Path, req.OutDir)
			if err != nil {
				return nil, err
			}
			if err := MoveFileNoReplace(outPath, finalPath); err != nil {
				return nil, err
			}
			rp.finish("saving")
			return &Result{
				Mode: media.ModeSingle,
				Outputs: []Output{{
					Path: finalPath, Bytes: outInfo.SizeBytes, Duration: outInfo.Duration,
					Width: outInfo.Width, Height: outInfo.Height,
				}},
				Width: outInfo.Width, Height: outInfo.Height,
				VideoKbps: kbps, AudioKbps: audioKbps, Parts: 1,
			}, nil
		}
	}
	if lastErr == nil {
		lastErr = errors.New("사용할 수 있는 해상도 단계가 없습니다")
	}
	return nil, fmt.Errorf("%w (%v)", errSingleExhausted, lastErr)
}
