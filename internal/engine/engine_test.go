package engine

import (
	"context"
	"errors"
	"testing"

	"videocompress/internal/media"
)

type fakeFF struct {
	info *media.Info
	err  error
	runs int
}

func (f *fakeFF) Probe(context.Context, string) (*media.Info, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.info, nil
}

func (f *fakeFF) Run(context.Context, []string, float64, func(float64)) error {
	f.runs++
	return nil
}

func TestProgressReporterIsMonotonicAcrossScopes(t *testing.T) {
	var got []Progress
	root := newProgressReporter(func(p Progress) { got = append(got, p) })
	a := root.scaled(0, 50)
	b := root.scaled(50, 100)
	a.emit("first", 0)
	a.emit("first", 100)
	b.emit("second", 0)
	b.emit("second", 100)
	for i := 1; i < len(got); i++ {
		if got[i].Percent+0.001 < got[i-1].Percent {
			t.Fatalf("progress regressed: %+v", got)
		}
	}
	if got[len(got)-1].Percent != 100 || got[len(got)-1].Stage != "second" {
		t.Fatalf("last = %+v", got[len(got)-1])
	}
}

func TestProgressReporterKeepsStageWhileClamping(t *testing.T) {
	var got []Progress
	root := newProgressReporter(func(p Progress) { got = append(got, p) })
	root.emit("phase-a", 80)
	root.emit("phase-b", 10)
	if got[1].Percent != 80 || got[1].Stage != "phase-b" {
		t.Fatalf("got %+v", got[1])
	}
}

func TestProcessDryRunDoesNotEncode(t *testing.T) {
	ff := &fakeFF{info: &media.Info{
		Path: "a.mp4", SizeBytes: 50_000_000, Duration: 20, Width: 1920, Height: 1080,
		Audio: &media.AudioInfo{Codec: "aac", BitrateKbps: 96, Channels: 2},
	}}
	eng := New(ff, t.TempDir())
	res, err := eng.Process(context.Background(), Request{
		InputPath: "a.mp4", TargetBytes: 5_000_000, PartBytes: 4_000_000, DryRun: true,
	}, nil)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if ff.runs != 0 {
		t.Fatalf("dry run must not encode (runs=%d)", ff.runs)
	}
	if res.Mode != media.ModeSingle || res.Parts != 1 || res.VideoKbps == 0 {
		t.Fatalf("res = %+v", res)
	}
}

func TestProcessSkipFastPath(t *testing.T) {
	ff := &fakeFF{info: &media.Info{Path: "a.mp4", SizeBytes: 1_000_000, Duration: 10, Width: 640, Height: 360}}
	eng := New(ff, t.TempDir())
	res, err := eng.Process(context.Background(), Request{
		InputPath: "a.mp4", TargetBytes: 5_000_000, PartBytes: 4_000_000,
	}, nil)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if !res.Skipped || res.Mode != media.ModeSkipped || ff.runs != 0 {
		t.Fatalf("res=%+v runs=%d", res, ff.runs)
	}
}

func TestProcessRejectsBadSettings(t *testing.T) {
	eng := New(&fakeFF{}, t.TempDir())
	_, err := eng.Process(context.Background(), Request{
		InputPath: "a.mp4", TargetBytes: 5_000_000, PartBytes: 5_000_000,
	}, nil)
	if !errors.Is(err, media.ErrInvalidSettings) {
		t.Fatalf("got %v, want ErrInvalidSettings", err)
	}
}

func TestProcessProbeErrorPropagates(t *testing.T) {
	boom := errors.New("probe failed")
	eng := New(&fakeFF{err: boom}, t.TempDir())
	_, err := eng.Process(context.Background(), Request{
		InputPath: "a.mp4", TargetBytes: 5_000_000, PartBytes: 4_000_000,
	}, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
}
