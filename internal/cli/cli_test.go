package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"videocompress/internal/config"
	"videocompress/internal/engine"
	"videocompress/internal/ffmpeg"
	"videocompress/internal/media"
)

type fakeProc struct {
	mu   sync.Mutex
	reqs []engine.Request
	res  *engine.Result
	err  error
}

func (f *fakeProc) Process(_ context.Context, req engine.Request, onProgress func(engine.Progress)) (*engine.Result, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()
	if onProgress != nil {
		onProgress(engine.Progress{Stage: "compressing 720p", Percent: 100})
	}
	if f.err != nil {
		return nil, f.err
	}
	if f.res != nil {
		return f.res, nil
	}
	return &engine.Result{
		Mode: media.ModeSingle, Parts: 1, Width: 1280, Height: 720, VideoKbps: 900, AudioKbps: 96,
		Outputs: []engine.Output{{
			Path: req.InputPath + "_compressed.mp4", Bytes: 4_800_000,
			Duration: 10, Width: 1280, Height: 720,
		}},
	}, nil
}

func testDeps(t *testing.T, proc Processor) Deps {
	t.Helper()
	dir := t.TempDir()
	return Deps{
		Locator: ffmpeg.Locator{
			Override: "fake-ffmpeg",
			Verify:   func(context.Context, string) error { return nil },
			LookPath: func(string) (string, error) { return "", errors.New("not on path") },
		},
		NewProcessor: func(ffmpeg.Tools, func(string, ...any)) Processor { return proc },
		SettingsPath: filepath.Join(dir, "settings.json"),
	}
}

func tmpVideo(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVersionFlag(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := Run([]string{"--version"}, &out, &errBuf, testDeps(t, &fakeProc{}))
	if code != ExitOK {
		t.Fatalf("code = %d (stderr %s)", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "vc "+Version) {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"--nope"}},
		{"no files", []string{}},
		{"target equals part", []string{"--target", "5", "--part", "5", "a.mp4"}},
		{"target too small", []string{"--target", "1", "--part", "1", "a.mp4"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			if code := Run(tc.args, &out, &errBuf, testDeps(t, &fakeProc{})); code != ExitUsage {
				t.Fatalf("code = %d want %d (stderr %q)", code, ExitUsage, errBuf.String())
			}
			if errBuf.Len() == 0 {
				t.Fatal("expected an error message on stderr")
			}
		})
	}
}

func TestHelpIsSuccess(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := Run([]string{"--help"}, &out, &errBuf, testDeps(t, &fakeProc{})); code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out.String(), "사용법") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestSingleFileHumanOutput(t *testing.T) {
	proc := &fakeProc{}
	src := tmpVideo(t, "movie.mp4")
	var out, errBuf bytes.Buffer
	code := Run([]string{"--target", "5", "--part", "4", src}, &out, &errBuf, testDeps(t, proc))
	if code != ExitOK {
		t.Fatalf("code = %d stderr=%s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "movie.mp4") || !strings.Contains(out.String(), "완료") {
		t.Fatalf("stdout = %q", out.String())
	}
	proc.mu.Lock()
	defer proc.mu.Unlock()
	if len(proc.reqs) != 1 || proc.reqs[0].TargetBytes != 5_000_000 || proc.reqs[0].PartBytes != 4_000_000 {
		t.Fatalf("requests = %+v", proc.reqs)
	}
}

func TestJSONOutput(t *testing.T) {
	proc := &fakeProc{}
	src := tmpVideo(t, "movie.mp4")
	var out, errBuf bytes.Buffer
	code := Run([]string{"--json", src}, &out, &errBuf, testDeps(t, proc))
	if code != ExitOK {
		t.Fatalf("code = %d stderr=%s", code, errBuf.String())
	}
	var report struct {
		Version string `json:"version"`
		Files   []struct {
			Status  string `json:"status"`
			Mode    string `json:"mode"`
			Outputs []struct {
				Path  string `json:"path"`
				Bytes int64  `json:"bytes"`
			} `json:"outputs"`
		} `json:"files"`
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, out.String())
	}
	if !report.OK || len(report.Files) != 1 || report.Files[0].Status != "done" {
		t.Fatalf("report = %+v", report)
	}
	if report.Files[0].Mode != media.ModeSingle || len(report.Files[0].Outputs) != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestFailedFileSetsExitCode(t *testing.T) {
	proc := &fakeProc{err: errors.New("인코딩 실패")}
	src := tmpVideo(t, "movie.mp4")
	var out, errBuf bytes.Buffer
	if code := Run([]string{src}, &out, &errBuf, testDeps(t, proc)); code != ExitJobFailed {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out.String()+errBuf.String(), "인코딩 실패") {
		t.Fatalf("no failure message: out=%q err=%q", out.String(), errBuf.String())
	}
}

func TestDryRunIsForwarded(t *testing.T) {
	proc := &fakeProc{}
	src := tmpVideo(t, "movie.mp4")
	var out, errBuf bytes.Buffer
	if code := Run([]string{"--dry-run", src}, &out, &errBuf, testDeps(t, proc)); code != ExitOK {
		t.Fatalf("code = %d stderr=%s", code, errBuf.String())
	}
	proc.mu.Lock()
	defer proc.mu.Unlock()
	if !proc.reqs[0].DryRun {
		t.Fatal("DryRun was not forwarded to the engine")
	}
}

func TestNoDownloadFailsFast(t *testing.T) {
	deps := testDeps(t, &fakeProc{})
	deps.Locator = ffmpeg.Locator{
		Override: "missing-ffmpeg",
		Verify:   func(context.Context, string) error { return errors.New("missing") },
		LookPath: func(string) (string, error) { return "", errors.New("not on path") },
	}
	src := tmpVideo(t, "movie.mp4")
	var out, errBuf bytes.Buffer
	if code := Run([]string{"--no-download", src}, &out, &errBuf, deps); code != ExitNoFFmpeg {
		t.Fatalf("code = %d stderr=%s", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "FFmpeg") {
		t.Fatalf("stderr = %q", errBuf.String())
	}
}

// An explicit --ffmpeg must be authoritative: silently falling back to another
// FFmpeg would ignore what the user asked for.
func TestExplicitFFmpegMustBeUsable(t *testing.T) {
	proc := &fakeProc{}
	deps := testDeps(t, proc)
	deps.Locator.Verify = func(_ context.Context, p string) error {
		if p == "good-ffmpeg" {
			return nil
		}
		return errors.New("broken")
	}
	// A working FFmpeg is reachable through PATH, which must not be consulted
	// when the user names a specific binary.
	deps.Locator.LookPath = func(string) (string, error) { return "good-ffmpeg", nil }
	src := tmpVideo(t, "movie.mp4")

	var out, errBuf bytes.Buffer
	if code := Run([]string{"--ffmpeg", "bad-ffmpeg", src}, &out, &errBuf, deps); code != ExitNoFFmpeg {
		t.Fatalf("code = %d, want ExitNoFFmpeg (stderr %q)", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "bad-ffmpeg") {
		t.Fatalf("stderr should name the rejected binary: %q", errBuf.String())
	}

	out.Reset()
	errBuf.Reset()
	if code := Run([]string{"--ffmpeg", "good-ffmpeg", src}, &out, &errBuf, deps); code != ExitOK {
		t.Fatalf("code = %d stderr=%s", code, errBuf.String())
	}
	proc.mu.Lock()
	defer proc.mu.Unlock()
	if len(proc.reqs) != 1 {
		t.Fatalf("requests = %+v", proc.reqs)
	}
}

func TestParseArgsUsesDefaults(t *testing.T) {
	opts, err := ParseArgs([]string{"--json", "a.mp4"}, config.Settings{TargetMB: 7, PartMB: 3})
	if err != nil {
		t.Fatal(err)
	}
	if opts.TargetMB != 7 || opts.PartMB != 3 || !opts.JSON || len(opts.Files) != 1 {
		t.Fatalf("opts = %+v", opts)
	}
}

// Progress belongs on stderr; stdout carries only results so a caller can pipe
// it (spec §8).
func TestProgressGoesToStderrAndResultsToStdout(t *testing.T) {
	src := tmpVideo(t, "movie.mp4")
	var out, errBuf bytes.Buffer
	if code := Run([]string{src}, &out, &errBuf, testDeps(t, &fakeProc{})); code != ExitOK {
		t.Fatalf("code = %d stderr=%s", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "압축 중") {
		t.Fatalf("progress should be on stderr: %q", errBuf.String())
	}
	if strings.Contains(out.String(), "압축 중") {
		t.Fatalf("progress must not pollute stdout: %q", out.String())
	}
	if !strings.Contains(out.String(), "완료") || !strings.Contains(out.String(), "movie.mp4") {
		t.Fatalf("result line missing from stdout: %q", out.String())
	}
}

func TestMultiFileOutputHasCounterAndSummary(t *testing.T) {
	a := tmpVideo(t, "a.mp4")
	b := tmpVideo(t, "b.mp4")
	var out, errBuf bytes.Buffer
	if code := Run([]string{a, b}, &out, &errBuf, testDeps(t, &fakeProc{})); code != ExitOK {
		t.Fatalf("code = %d stderr=%s", code, errBuf.String())
	}
	for _, want := range []string{"[1/2]", "[2/2]", "요약", "성공 2"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("stdout missing %q: %q", want, out.String())
		}
	}
}

func TestVerbosePassesLoggerToProcessor(t *testing.T) {
	src := tmpVideo(t, "movie.mp4")
	var loggers []func(string, ...any)
	deps := testDeps(t, &fakeProc{})
	deps.NewProcessor = func(_ ffmpeg.Tools, logf func(string, ...any)) Processor {
		loggers = append(loggers, logf)
		return &fakeProc{}
	}

	var out, errBuf bytes.Buffer
	if code := Run([]string{src}, &out, &errBuf, deps); code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	if len(loggers) != 1 || loggers[0] != nil {
		t.Fatalf("logger must be nil without --verbose: %v", loggers)
	}

	out.Reset()
	errBuf.Reset()
	if code := Run([]string{"--verbose", src}, &out, &errBuf, deps); code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	if len(loggers) != 2 || loggers[1] == nil {
		t.Fatal("--verbose should hand the processor a logger")
	}
	loggers[1]("$ ffmpeg -i in.mp4")
	if !strings.Contains(errBuf.String(), "[ffmpeg] $ ffmpeg -i in.mp4") {
		t.Fatalf("stderr = %q", errBuf.String())
	}
}
