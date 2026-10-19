package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"videocompress/internal/engine"
	"videocompress/internal/media"
)

type fakeProc struct {
	mu     sync.Mutex
	calls  []engine.Request
	delay  time.Duration
	failOn map[string]error
	skips  map[string]bool
}

func (f *fakeProc) Process(ctx context.Context, req engine.Request, onProgress func(engine.Progress)) (*engine.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	delay := f.delay
	err := f.failOn[req.InputPath]
	skip := f.skips[req.InputPath]
	f.mu.Unlock()
	if onProgress != nil {
		onProgress(engine.Progress{Stage: "compressing 720p", Percent: 50})
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	if skip {
		return &engine.Result{Mode: media.ModeSkipped, Skipped: true}, nil
	}
	return &engine.Result{
		Mode: media.ModeSingle, Parts: 1,
		Outputs: []engine.Output{{Path: req.InputPath + ".out.mp4", Bytes: 100}},
	}, nil
}

func tmpVideo(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func newTestManager(proc Processor) (*Manager, *int) {
	updates := 0
	var mu sync.Mutex
	m := NewManager(proc, func() {
		mu.Lock()
		updates++
		mu.Unlock()
	})
	return m, &updates
}

func TestAddFiltersAndValidates(t *testing.T) {
	m, _ := newTestManager(&fakeProc{})
	good := tmpVideo(t, "a.mp4")
	bad := filepath.Join(filepath.Dir(good), "notes.txt")
	missing := filepath.Join(filepath.Dir(good), "ghost.mp4")

	added := m.Add(good, bad, missing, "  ")
	if len(added) != 3 {
		t.Fatalf("added %d jobs, want 3", len(added))
	}
	if added[0].Status != StatusQueued {
		t.Fatalf("video status = %s", added[0].Status)
	}
	if added[1].Status != StatusFailed || !strings.Contains(added[1].Message, "형식") {
		t.Fatalf("txt job = %+v", added[1])
	}
	if added[2].Status != StatusFailed || !strings.Contains(added[2].Message, "찾을 수") {
		t.Fatalf("missing job = %+v", added[2])
	}
	snap := m.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("snapshot len = %d", len(snap))
	}
}

func TestStartProcessesSequentially(t *testing.T) {
	proc := &fakeProc{}
	m, updates := newTestManager(proc)
	a := tmpVideo(t, "a.mp4")
	b := tmpVideo(t, "b.mp4")
	m.Add(a, b)

	if err := m.Start(Options{TargetBytes: 5_000_000, PartBytes: 4_000_000}); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool { return !m.IsRunning() })

	snap := m.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot = %+v", snap)
	}
	for _, j := range snap {
		if j.Status != StatusDone {
			t.Fatalf("job %s status = %s (%s)", j.ID, j.Status, j.Message)
		}
		if len(j.Outputs) != 1 {
			t.Fatalf("job %s outputs = %v", j.ID, j.Outputs)
		}
		if j.Percent != 100 {
			t.Fatalf("job %s percent = %v", j.ID, j.Percent)
		}
	}
	proc.mu.Lock()
	got := []string{proc.calls[0].InputPath, proc.calls[1].InputPath}
	proc.mu.Unlock()
	if got[0] != a || got[1] != b {
		t.Fatalf("processing order = %v", got)
	}
	if *updates == 0 {
		t.Fatal("notify was never called")
	}
}

func TestFailedJobDoesNotStopQueue(t *testing.T) {
	a := tmpVideo(t, "a.mp4")
	b := tmpVideo(t, "b.mp4")
	proc := &fakeProc{failOn: map[string]error{a: errors.New("boom")}}
	m, _ := newTestManager(proc)
	m.Add(a, b)
	if err := m.Start(Options{TargetBytes: 5_000_000, PartBytes: 4_000_000}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return !m.IsRunning() })
	snap := m.Snapshot()
	if snap[0].Status != StatusFailed || !strings.Contains(snap[0].Message, "boom") {
		t.Fatalf("first job = %+v", snap[0])
	}
	if snap[1].Status != StatusDone {
		t.Fatalf("second job = %+v", snap[1])
	}
}

func TestSkippedJobStatus(t *testing.T) {
	a := tmpVideo(t, "a.mp4")
	proc := &fakeProc{skips: map[string]bool{a: true}}
	m, _ := newTestManager(proc)
	m.Add(a)
	if err := m.Start(Options{TargetBytes: 5_000_000, PartBytes: 4_000_000}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return !m.IsRunning() })
	if got := m.Snapshot()[0]; got.Status != StatusSkipped {
		t.Fatalf("status = %s", got.Status)
	}
}

func TestCancelStopsCurrentAndQueued(t *testing.T) {
	a := tmpVideo(t, "a.mp4")
	b := tmpVideo(t, "b.mp4")
	proc := &fakeProc{delay: 2 * time.Second}
	m, _ := newTestManager(proc)
	m.Add(a, b)
	if err := m.Start(Options{TargetBytes: 5_000_000, PartBytes: 4_000_000}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		return m.Snapshot()[0].Status == StatusRunning
	})
	m.Cancel()
	waitFor(t, 5*time.Second, func() bool { return !m.IsRunning() })
	snap := m.Snapshot()
	if snap[0].Status != StatusCanceled {
		t.Fatalf("running job = %+v", snap[0])
	}
	if snap[1].Status != StatusCanceled {
		t.Fatalf("queued job = %+v", snap[1])
	}
}

func TestStartRejectsDoubleRunAndEmptyQueue(t *testing.T) {
	m, _ := newTestManager(&fakeProc{delay: time.Second})
	if err := m.Start(Options{TargetBytes: 5_000_000, PartBytes: 4_000_000}); err == nil {
		t.Fatal("want error for empty queue")
	}
	a := tmpVideo(t, "a.mp4")
	m.Add(a)
	if err := m.Start(Options{TargetBytes: 5_000_000, PartBytes: 4_000_000}); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(Options{TargetBytes: 5_000_000, PartBytes: 4_000_000}); err == nil {
		t.Fatal("want error for concurrent run")
	}
	m.Cancel()
	waitFor(t, 5*time.Second, func() bool { return !m.IsRunning() })
	if err := m.Start(Options{TargetBytes: 5_000_000, PartBytes: 5_000_000}); !errors.Is(err, media.ErrInvalidSettings) {
		t.Fatalf("want ErrInvalidSettings for bad sizes, got %v", err)
	}
}

func TestRemoveAndClearFinished(t *testing.T) {
	proc := &fakeProc{}
	m, _ := newTestManager(proc)
	a := tmpVideo(t, "a.mp4")
	b := tmpVideo(t, "b.mp4")
	m.Add(a, b)
	if !m.Remove(m.Snapshot()[0].ID) {
		t.Fatal("remove queued failed")
	}
	if m.Remove("does-not-exist") {
		t.Fatal("remove unknown should fail")
	}
	m.Add(a)
	if err := m.Start(Options{TargetBytes: 5_000_000, PartBytes: 4_000_000}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return !m.IsRunning() })
	if n := m.ClearFinished(); n != 2 {
		t.Fatalf("cleared %d, want 2", n)
	}
	if len(m.Snapshot()) != 0 {
		t.Fatalf("snapshot = %+v", m.Snapshot())
	}
}
