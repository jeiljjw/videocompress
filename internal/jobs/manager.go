package jobs

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"videocompress/internal/engine"
	"videocompress/internal/media"
)

// Job status values.
const (
	StatusQueued   = "queued"
	StatusRunning  = "running"
	StatusDone     = "done"
	StatusFailed   = "failed"
	StatusSkipped  = "skipped"
	StatusCanceled = "canceled"
)

// Job is one queued file.
type Job struct {
	ID         string         `json:"id"`
	Input      string         `json:"input"`
	Outputs    []string       `json:"outputs"`
	Status     string         `json:"status"`
	Stage      string         `json:"stage"`
	Percent    float64        `json:"percent"`
	Message    string         `json:"message"`
	Result     *engine.Result `json:"result,omitempty"`
	AddedAt    time.Time      `json:"addedAt"`
	DurationMS int64          `json:"durationMs"`
}

// Processor is implemented by *engine.Engine and by test fakes.
type Processor interface {
	Process(ctx context.Context, req engine.Request, onProgress func(engine.Progress)) (*engine.Result, error)
}

// Options configure one run. They are taken at Start time so a run is not
// affected by later settings changes.
type Options struct {
	TargetBytes int64
	PartBytes   int64
	OutDir      string
	DryRun      bool
}

// Manager owns the queue and the single worker goroutine.
type Manager struct {
	mu      sync.Mutex
	jobs    []*Job
	queue   []string
	proc    Processor
	notify  func()
	cancel  context.CancelFunc
	running bool
	seq     int
}

// NewManager returns a manager that runs jobs through proc. notify is called
// without the lock held whenever observable state changes.
func NewManager(proc Processor, notify func()) *Manager {
	return &Manager{proc: proc, notify: notify}
}

func (m *Manager) fire() {
	if m.notify != nil {
		m.notify()
	}
}

// Add queues files, rejecting non-video extensions and missing paths early.
func (m *Manager) Add(paths ...string) []Job {
	m.mu.Lock()
	added := make([]Job, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		m.seq++
		job := &Job{ID: fmt.Sprintf("job-%d", m.seq), Input: p, Status: StatusQueued, AddedAt: time.Now()}
		switch {
		case !media.HasVideoExt(p):
			job.Status = StatusFailed
			job.Message = "지원하지 않는 파일 형식입니다"
		default:
			st, err := os.Stat(p)
			if err != nil {
				job.Status = StatusFailed
				job.Message = "파일을 찾을 수 없습니다"
			} else if st.IsDir() {
				job.Status = StatusFailed
				job.Message = "폴더는 추가할 수 없습니다"
			} else {
				m.queue = append(m.queue, job.ID)
			}
		}
		m.jobs = append(m.jobs, job)
		added = append(added, *job)
	}
	m.mu.Unlock()
	m.fire()
	return added
}

// Remove deletes a job that is not running.
func (m *Manager) Remove(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, j := range m.jobs {
		if j.ID != id {
			continue
		}
		if j.Status == StatusRunning {
			return false
		}
		m.jobs = append(m.jobs[:i], m.jobs[i+1:]...)
		out := m.queue[:0]
		for _, q := range m.queue {
			if q != id {
				out = append(out, q)
			}
		}
		m.queue = out
		return true
	}
	return false
}

// ClearFinished removes finished/failed/canceled jobs and returns the count.
func (m *Manager) ClearFinished() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.jobs[:0]
	removed := 0
	for _, j := range m.jobs {
		switch j.Status {
		case StatusDone, StatusFailed, StatusSkipped, StatusCanceled:
			removed++
		default:
			kept = append(kept, j)
		}
	}
	m.jobs = kept
	return removed
}

// Snapshot returns a deep-enough copy of the current job list.
func (m *Manager) Snapshot() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		cp := *j
		cp.Outputs = append([]string(nil), j.Outputs...)
		out = append(out, cp)
	}
	return out
}

// IsRunning reports whether the worker is active.
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// Start launches sequential processing of the queued jobs.
func (m *Manager) Start(opts Options) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return errors.New("이미 작업이 진행 중입니다")
	}
	if opts.TargetBytes <= 0 || opts.PartBytes <= 0 || opts.PartBytes >= opts.TargetBytes {
		m.mu.Unlock()
		return media.ErrInvalidSettings
	}
	if len(m.queue) == 0 {
		m.mu.Unlock()
		return errors.New("처리할 파일이 없습니다")
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.running = true
	go m.run(ctx, opts)
	m.mu.Unlock()
	m.fire() // never while holding the lock: notify re-enters Snapshot()
	return nil
}

// Cancel cancels the running job; queued jobs are marked canceled by the
// worker as it unwinds.
func (m *Manager) Cancel() {
	m.mu.Lock()
	cancel := m.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (m *Manager) run(ctx context.Context, opts Options) {
	defer func() {
		m.mu.Lock()
		m.running = false
		m.cancel = nil
		m.mu.Unlock()
		m.fire()
	}()

	for {
		m.mu.Lock()
		if len(m.queue) == 0 {
			m.mu.Unlock()
			return
		}
		id := m.queue[0]
		m.queue = m.queue[1:]
		job := m.findLocked(id)
		if job == nil {
			m.mu.Unlock()
			continue
		}
		job.Status = StatusRunning
		job.Stage = "probing"
		job.Percent = 0
		input := job.Input
		m.mu.Unlock()
		m.fire()

		if ctx.Err() != nil {
			m.finishCanceled(id)
			return
		}

		started := time.Now()
		res, err := m.proc.Process(ctx, engine.Request{
			JobID:       id,
			InputPath:   input,
			OutDir:      opts.OutDir,
			TargetBytes: opts.TargetBytes,
			PartBytes:   opts.PartBytes,
			DryRun:      opts.DryRun,
		}, func(p engine.Progress) {
			m.mu.Lock()
			changed := false
			if j := m.findLocked(id); j != nil {
				if p.Stage != j.Stage || math.Abs(p.Percent-j.Percent) >= 0.5 || p.Percent >= 100 {
					j.Stage, j.Percent = p.Stage, p.Percent
					changed = true
				}
			}
			m.mu.Unlock()
			if changed {
				m.fire()
			}
		})

		m.mu.Lock()
		if job := m.findLocked(id); job != nil {
			job.DurationMS = time.Since(started).Milliseconds()
			switch {
			case err != nil && ctx.Err() != nil:
				job.Status = StatusCanceled
				job.Message = "사용자가 취소했습니다"
			case err != nil:
				job.Status = StatusFailed
				job.Message = err.Error()
			case res != nil && res.Skipped:
				job.Status = StatusSkipped
				job.Stage = "done"
				job.Percent = 100
				job.Message = "이미 목표 크기 이하입니다"
				job.Result = res
			default:
				job.Status = StatusDone
				job.Stage = "done"
				job.Percent = 100
				job.Result = res
				if res != nil {
					for _, o := range res.Outputs {
						job.Outputs = append(job.Outputs, o.Path)
					}
				}
			}
		}
		canceled := ctx.Err() != nil
		if canceled {
			for _, j := range m.jobs {
				if j.Status == StatusQueued {
					j.Status = StatusCanceled
					j.Message = "취소되었습니다"
				}
			}
			m.queue = nil
		}
		m.mu.Unlock()
		m.fire()
		if canceled {
			return
		}
	}
}

func (m *Manager) finishCanceled(id string) {
	m.mu.Lock()
	if j := m.findLocked(id); j != nil && j.Status == StatusRunning {
		j.Status = StatusCanceled
		j.Message = "사용자가 취소했습니다"
	}
	for _, j := range m.jobs {
		if j.Status == StatusQueued {
			j.Status = StatusCanceled
			j.Message = "취소되었습니다"
		}
	}
	m.queue = nil
	m.mu.Unlock()
	m.fire()
}

func (m *Manager) findLocked(id string) *Job {
	for _, j := range m.jobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}
