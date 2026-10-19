package engine

import (
	"context"
	"errors"
	"fmt"
	"os"

	"videocompress/internal/media"
)

// FF is the FFmpeg client contract the engine depends on. *ffmpeg.Client
// satisfies it; tests use a fake.
type FF interface {
	Probe(ctx context.Context, path string) (*media.Info, error)
	Run(ctx context.Context, args []string, totalSec float64, onProgress func(percent float64)) error
}

// Progress is one monotonic engine progress report.
type Progress struct {
	Stage   string  `json:"stage"`
	Percent float64 `json:"percent"`
}

// Request is one compression task.
type Request struct {
	JobID       string `json:"jobId"`
	InputPath   string `json:"inputPath"`
	OutDir      string `json:"outDir"`
	TargetBytes int64  `json:"targetBytes"`
	PartBytes   int64  `json:"partBytes"`
	DryRun      bool   `json:"dryRun"`
}

// Output describes one produced file.
type Output struct {
	Path     string  `json:"path"`
	Bytes    int64   `json:"bytes"`
	Duration float64 `json:"duration"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
}

// Result summarizes what the engine did for one input.
type Result struct {
	Mode           string   `json:"mode"`
	Outputs        []Output `json:"outputs"`
	Width          int      `json:"width"`
	Height         int      `json:"height"`
	VideoKbps      int      `json:"videoKbps"`
	AudioKbps      int      `json:"audioKbps"`
	SegmentSeconds int      `json:"segmentSeconds"`
	Parts          int      `json:"parts"`
	LowQuality     bool     `json:"lowQuality"`
	Skipped        bool     `json:"skipped"`
}

// errSingleExhausted triggers the single-to-split fallback.
var errSingleExhausted = errors.New("목표 크기를 맞출 수 없어 분할로 전환합니다")

// Engine implements the compression pipeline.
type Engine struct {
	FF       FF
	TempRoot string
	Logf     func(format string, args ...any)
}

// New returns an engine that writes scratch files under tempRoot.
func New(ff FF, tempRoot string) *Engine {
	return &Engine{FF: ff, TempRoot: tempRoot}
}

func (e *Engine) logf(format string, args ...any) {
	if e.Logf != nil {
		e.Logf(format, args...)
	}
}

// Process runs the full pipeline for one input file: probe, plan, encode,
// verify, and move into place. Nothing is written outside TempRoot until a
// result has passed verification.
func (e *Engine) Process(ctx context.Context, req Request, onProgress func(Progress)) (*Result, error) {
	if e.FF == nil {
		return nil, errors.New("FFmpeg 클라이언트가 준비되지 않았습니다")
	}
	if req.TargetBytes <= 0 || req.PartBytes <= 0 || req.PartBytes >= req.TargetBytes {
		return nil, media.ErrInvalidSettings
	}
	rep := newProgressReporter(onProgress)
	rep.emit("probing", 0)
	info, err := e.FF.Probe(ctx, req.InputPath)
	if err != nil {
		return nil, err
	}
	rep.emit("probing", 2)
	plan, err := media.PlanFor(info, req.TargetBytes, req.PartBytes)
	if err != nil {
		return nil, err
	}
	if plan.Mode == media.ModeSkipped {
		e.logf("%s is already under target (%d bytes); skipping", req.InputPath, info.SizeBytes)
		rep.emit("done", 100)
		return &Result{
			Mode: media.ModeSkipped, Width: info.Width, Height: info.Height, Skipped: true,
		}, nil
	}
	if req.DryRun {
		rep.emit("done", 100)
		return dryResult(plan), nil
	}
	if err := os.MkdirAll(e.TempRoot, 0o755); err != nil {
		return nil, err
	}
	tmpDir, err := os.MkdirTemp(e.TempRoot, "vc-job-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	work := rep.scaled(2, 99)
	var res *Result
	switch plan.Mode {
	case media.ModeSingle:
		res, err = e.processSingle(ctx, req, info, plan, tmpDir, work)
		if errors.Is(err, errSingleExhausted) {
			e.logf("%s: single-file target unreachable, splitting instead", req.InputPath)
			sp, splitErr := media.SplitPlanFor(info, req.PartBytes)
			if splitErr != nil {
				return nil, splitErr
			}
			res, err = e.processSplit(ctx, req, info, sp, tmpDir, work)
		}
	case media.ModeSplit:
		res, err = e.processSplit(ctx, req, info, plan.Split, tmpDir, work)
	default:
		err = fmt.Errorf("알 수 없는 처리 모드입니다: %s", plan.Mode)
	}
	if err != nil {
		return nil, err
	}
	rep.emit("done", 100)
	return res, nil
}

// dryResult describes the plan without encoding anything.
func dryResult(plan *media.Plan) *Result {
	res := &Result{
		Mode: plan.Mode, Width: plan.Info.Width, Height: plan.Info.Height, AudioKbps: plan.AudioKbps,
	}
	switch plan.Mode {
	case media.ModeSingle:
		res.Width = plan.Candidate.Width
		res.Height = plan.Candidate.Height
		res.VideoKbps = plan.Candidate.VideoKbps
		res.Parts = 1
	case media.ModeSplit:
		res.Width = plan.Split.Width
		res.Height = plan.Split.Height
		res.VideoKbps = plan.Split.VideoKbps
		res.AudioKbps = plan.Split.AudioKbps
		res.SegmentSeconds = plan.Split.SegmentSeconds
		res.Parts = plan.Split.Parts
		res.LowQuality = plan.Split.LowQuality
	}
	return res
}

// progressReporter maps scoped progress onto a single monotonic 0-100 stream.
// Children report 0-100 within their own [start, end] window; the root clamps
// so the bar never moves backwards, even across retries or phase changes.
type progressReporter struct {
	fn    func(Progress)
	root  *progressReporter
	start float64
	end   float64
	last  float64
}

func newProgressReporter(fn func(Progress)) *progressReporter {
	r := &progressReporter{fn: fn, end: 100}
	r.root = r
	return r
}

func (r *progressReporter) scaled(start, end float64) *progressReporter {
	return &progressReporter{root: r.root, start: start, end: end}
}

func (r *progressReporter) emit(stage string, pct float64) {
	if pct < r.last {
		pct = r.last
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	r.last = pct
	r.root.emitGlobal(stage, r.start+(r.end-r.start)*pct/100)
}

func (r *progressReporter) emitGlobal(stage string, pct float64) {
	if pct < r.last {
		pct = r.last
	}
	if pct > 100 {
		pct = 100
	}
	r.last = pct
	if r.fn != nil {
		r.fn(Progress{Stage: stage, Percent: pct})
	}
}
