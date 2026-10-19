package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"videocompress/internal/config"
	"videocompress/internal/engine"
	"videocompress/internal/ffmpeg"
	"videocompress/internal/media"
)

// Version is the CLI version string.
const Version = "0.1.0"

// Exit codes.
const (
	ExitOK        = 0
	ExitJobFailed = 1
	ExitUsage     = 2
	ExitNoFFmpeg  = 3
	ExitCanceled  = 130
)

// Options are the parsed flags.
type Options struct {
	TargetMB   int
	PartMB     int
	OutDir     string
	JSON       bool
	DryRun     bool
	FFmpegPath string
	FFmpegSet  bool // --ffmpeg was given on the command line
	NoDownload bool
	ShowVer    bool
	Files      []string
}

// Processor is implemented by *engine.Engine.
type Processor interface {
	Process(ctx context.Context, req engine.Request, onProgress func(engine.Progress)) (*engine.Result, error)
}

// Deps lets tests replace the parts that touch FFmpeg.
type Deps struct {
	Locator      ffmpeg.Locator
	Downloader   ffmpeg.Downloader
	NewProcessor func(ffmpeg.Tools) Processor
	SettingsPath string
}

// DefaultDeps wires the real locator, downloader, and engine.
func DefaultDeps() (Deps, error) {
	exe, err := os.Executable()
	if err != nil {
		return Deps{}, err
	}
	cache, err := config.CacheDirPath()
	if err != nil {
		return Deps{}, err
	}
	settingsPath, err := config.DefaultSettingsPath()
	if err != nil {
		return Deps{}, err
	}
	return Deps{
		Locator: ffmpeg.Locator{
			AppDir:   filepath.Dir(exe),
			CacheDir: cache,
			LookPath: exec.LookPath,
		},
		NewProcessor: func(tools ffmpeg.Tools) Processor {
			return engine.New(&ffmpeg.Client{Tools: tools}, filepath.Join(cache, "temp"))
		},
		SettingsPath: settingsPath,
	}, nil
}

// ParseArgs parses flags using settings values as defaults.
func ParseArgs(args []string, defaults config.Settings) (Options, error) {
	opts := Options{TargetMB: defaults.TargetMB, PartMB: defaults.PartMB}
	fs := flag.NewFlagSet("vc", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.IntVar(&opts.TargetMB, "target", opts.TargetMB, "목표 크기(MB)")
	fs.IntVar(&opts.PartMB, "part", opts.PartMB, "조각 크기(MB)")
	fs.StringVar(&opts.OutDir, "out", "", "출력 폴더")
	fs.BoolVar(&opts.JSON, "json", false, "JSON으로 출력")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "계획만 확인")
	fs.StringVar(&opts.FFmpegPath, "ffmpeg", "", "ffmpeg 실행 파일 경로")
	fs.BoolVar(&opts.NoDownload, "no-download", false, "FFmpeg 자동 다운로드 금지")
	fs.BoolVar(&opts.ShowVer, "version", false, "버전 출력")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "ffmpeg" {
			opts.FFmpegSet = true
		}
	})
	opts.Files = fs.Args()
	return opts, nil
}

const usageText = `사용법: vc [옵션] <파일...>

비디오를 목표 크기(MB) 이하로 압축하고, 불가능하면 조각 크기(MB) 이하의
재생 가능한 MP4 조각으로 나눕니다. 결과는 원본 옆에 저장되며 원본은 그대로 둡니다.

옵션:
  --target <MB>    목표 크기 (기본 5, 범위 2~100)
  --part <MB>      조각 크기 (기본 4, 목표보다 작아야 함)
  --out <폴더>     출력 폴더 (기본: 원본과 같은 폴더)
  --dry-run        인코딩 없이 계획만 출력
  --json           결과를 JSON으로 출력
  --ffmpeg <경로>  사용할 ffmpeg 실행 파일
  --no-download    FFmpeg 자동 다운로드 금지
  --version        버전 출력
`

// Run executes the CLI and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer, deps Deps) int {
	if deps.SettingsPath == "" {
		if p, err := config.DefaultSettingsPath(); err == nil {
			deps.SettingsPath = p
		}
	}
	settings, loadErr := config.Load(deps.SettingsPath)
	if loadErr != nil {
		fmt.Fprintf(stderr, "설정을 읽지 못해 기본값을 사용합니다: %v\n", loadErr)
	}
	opts, err := ParseArgs(args, settings)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usageText)
			return ExitOK
		}
		fmt.Fprintf(stderr, "인수 오류: %v\n\n%s", err, usageText)
		return ExitUsage
	}
	if opts.ShowVer {
		fmt.Fprintf(stdout, "vc %s\n", Version)
		return ExitOK
	}
	if opts.FFmpegPath == "" {
		opts.FFmpegPath = settings.FFmpegPath
	}
	if err := (config.Settings{TargetMB: opts.TargetMB, PartMB: opts.PartMB}).Validate(); err != nil {
		fmt.Fprintf(stderr, "%v\n\n%s", err, usageText)
		return ExitUsage
	}
	files, rejected := classify(opts.Files)
	for _, r := range rejected {
		fmt.Fprintf(stderr, "건너뜀: %s (%s)\n", r.path, r.reason)
	}
	if len(files) == 0 {
		fmt.Fprintf(stderr, "처리할 비디오 파일이 없습니다.\n\n%s", usageText)
		return ExitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	tools, code := acquireFFmpeg(ctx, opts, deps, stderr)
	if code != ExitOK {
		return code
	}
	proc := deps.NewProcessor(tools)

	results := make([]fileResult, 0, len(files))
	exit := ExitOK
	for _, f := range files {
		r := processOne(ctx, proc, opts, f, stdout)
		results = append(results, r)
		switch r.Status {
		case "failed":
			exit = ExitJobFailed
		case "canceled":
			exit = ExitCanceled
		}
		if exit == ExitCanceled {
			break
		}
	}
	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(jsonReport(results, opts, exit))
	}
	return exit
}

type rejectedFile struct {
	path   string
	reason string
}

// classify splits inputs into video files and rejected entries.
func classify(paths []string) ([]string, []rejectedFile) {
	var files []string
	var rejected []rejectedFile
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !media.HasVideoExt(p) {
			rejected = append(rejected, rejectedFile{p, "지원하지 않는 형식"})
			continue
		}
		st, err := os.Stat(p)
		if err != nil {
			rejected = append(rejected, rejectedFile{p, "파일을 찾을 수 없음"})
			continue
		}
		if st.IsDir() {
			rejected = append(rejected, rejectedFile{p, "폴더는 지원하지 않음"})
			continue
		}
		files = append(files, p)
	}
	return files, rejected
}

// acquireFFmpeg locates FFmpeg, downloading it once when allowed. An explicit
// --ffmpeg is authoritative: it is never silently replaced by another copy.
func acquireFFmpeg(ctx context.Context, opts Options, deps Deps, stderr io.Writer) (ffmpeg.Tools, int) {
	loc := deps.Locator
	if opts.FFmpegSet && opts.FFmpegPath != "" {
		only := ffmpeg.Locator{Override: opts.FFmpegPath, Verify: deps.Locator.Verify}
		if tools, err := only.Locate(ctx); err == nil {
			return tools, ExitOK
		} else {
			fmt.Fprintf(stderr, "지정한 FFmpeg를 사용할 수 없습니다: %s\n%v\nffmpeg와 ffprobe가 함께 있는지 확인하세요.\n", opts.FFmpegPath, err)
			return ffmpeg.Tools{}, ExitNoFFmpeg
		}
	}
	if opts.FFmpegPath != "" {
		loc.Configured = opts.FFmpegPath // saved preference: still a candidate, not a hard requirement
	}
	if tools, err := loc.Locate(ctx); err == nil {
		return tools, ExitOK
	} else if opts.NoDownload {
		fmt.Fprintf(stderr, "FFmpeg를 찾을 수 없습니다: %v\n--ffmpeg <경로>로 지정하거나, 옵션 없이 다시 실행하면 자동으로 내려받습니다.\n", err)
		return ffmpeg.Tools{}, ExitNoFFmpeg
	}
	cache, err := config.CacheDirPath()
	if err != nil {
		fmt.Fprintf(stderr, "캐시 폴더를 준비할 수 없습니다: %v\n", err)
		return ffmpeg.Tools{}, ExitNoFFmpeg
	}
	fmt.Fprintln(stderr, "FFmpeg를 내려받습니다 (~115MB, 최초 1회)...")
	dl := deps.Downloader
	lastLine := false
	tools, err := dl.Download(ctx, filepath.Join(cache, "ffmpeg", "bin"), func(done, total int64) {
		if total > 0 {
			fmt.Fprintf(stderr, "\r다운로드 %3.0f%% (%d/%d MB)   ", float64(done)/float64(total)*100, done>>20, total>>20)
		} else {
			fmt.Fprintf(stderr, "\r다운로드 %d MB   ", done>>20)
		}
		lastLine = true
	})
	if lastLine {
		fmt.Fprintln(stderr)
	}
	if err != nil {
		fmt.Fprintf(stderr, "FFmpeg 다운로드 실패: %v\n수동으로 설치한 뒤 --ffmpeg <경로>를 사용하세요.\n", err)
		return ffmpeg.Tools{}, ExitNoFFmpeg
	}
	return tools, ExitOK
}

type fileResult struct {
	Input          string          `json:"input"`
	Status         string          `json:"status"`
	Mode           string          `json:"mode,omitempty"`
	Outputs        []engine.Output `json:"outputs,omitempty"`
	Parts          int             `json:"parts,omitempty"`
	Width          int             `json:"width,omitempty"`
	Height         int             `json:"height,omitempty"`
	VideoKbps      int             `json:"videoKbps,omitempty"`
	AudioKbps      int             `json:"audioKbps,omitempty"`
	SegmentSeconds int             `json:"segmentSeconds,omitempty"`
	LowQuality     bool            `json:"lowQuality,omitempty"`
	Message        string          `json:"message,omitempty"`
	Error          string          `json:"error,omitempty"`
	Seconds        float64         `json:"seconds"`
}

func jsonReport(results []fileResult, opts Options, exit int) map[string]any {
	return map[string]any{
		"version":  Version,
		"dryRun":   opts.DryRun,
		"target":   opts.TargetMB,
		"part":     opts.PartMB,
		"ok":       exit == ExitOK,
		"exitCode": exit,
		"files":    results,
	}
}

func processOne(ctx context.Context, proc Processor, opts Options, input string, stdout io.Writer) fileResult {
	started := time.Now()
	printer := newProgressPrinter(stdout, opts.JSON, filepath.Base(input))
	res, err := proc.Process(ctx, engine.Request{
		InputPath:   input,
		OutDir:      opts.OutDir,
		TargetBytes: int64(opts.TargetMB) * 1_000_000,
		PartBytes:   int64(opts.PartMB) * 1_000_000,
		DryRun:      opts.DryRun,
	}, printer.update)
	printer.close()

	out := fileResult{Input: input, Seconds: time.Since(started).Seconds()}
	switch {
	case err != nil && errors.Is(err, context.Canceled):
		out.Status = "canceled"
		out.Message = "사용자가 취소했습니다"
		printer.println("취소: %s", input)
	case err != nil:
		out.Status = "failed"
		out.Error = err.Error()
		printer.println("실패: %s - %v", input, err)
	case res == nil:
		out.Status = "failed"
		out.Error = "결과를 받지 못했습니다"
		printer.println("실패: %s - 결과를 받지 못했습니다", input)
	case res.Skipped:
		out.Status = "skipped"
		out.Message = "이미 목표 크기 이하입니다"
		printer.println("건너뜀: %s (이미 %s 이하)", input, humanBytes(int64(opts.TargetMB)*1_000_000))
	default:
		out.Status = "done"
		out.Mode = res.Mode
		out.Outputs = res.Outputs
		out.Parts = res.Parts
		out.Width = res.Width
		out.Height = res.Height
		out.VideoKbps = res.VideoKbps
		out.AudioKbps = res.AudioKbps
		out.SegmentSeconds = res.SegmentSeconds
		out.LowQuality = res.LowQuality
		printer.println("%s", describe(input, res, opts.DryRun))
	}
	return out
}

func describe(input string, res *engine.Result, dry bool) string {
	name := filepath.Base(input)
	switch res.Mode {
	case media.ModeSingle:
		var size int64
		var outPath string
		if len(res.Outputs) > 0 {
			size = res.Outputs[0].Bytes
			outPath = filepath.Base(res.Outputs[0].Path)
		}
		if dry {
			return fmt.Sprintf("예상: %s → 단일 파일, %dx%d, %dkbps, 오디오 %s",
				name, res.Width, res.Height, res.VideoKbps, audioLabel(res.AudioKbps))
		}
		return fmt.Sprintf("완료: %s → %s, %s, %dx%d, %dkbps",
			name, outPath, humanBytes(size), res.Width, res.Height, res.VideoKbps)
	case media.ModeSplit:
		note := ""
		if res.LowQuality {
			note = " (화질 하한 이하)"
		}
		if dry {
			return fmt.Sprintf("예상: %s → %d조각, %dx%d, %dkbps, 조각 %d초%s",
				name, res.Parts, res.Width, res.Height, res.VideoKbps, res.SegmentSeconds, note)
		}
		var maxSize int64
		for _, o := range res.Outputs {
			if o.Bytes > maxSize {
				maxSize = o.Bytes
			}
		}
		return fmt.Sprintf("완료: %s → %d조각 (%s ~ %s, 최대 %s), %dx%d, %dkbps, 조각 %d초%s",
			name, len(res.Outputs), filepath.Base(firstPath(res)), filepath.Base(lastPath(res)),
			humanBytes(maxSize), res.Width, res.Height, res.VideoKbps, res.SegmentSeconds, note)
	}
	return fmt.Sprintf("%s: %s", name, res.Mode)
}

func firstPath(res *engine.Result) string {
	if len(res.Outputs) == 0 {
		return ""
	}
	return res.Outputs[0].Path
}

func lastPath(res *engine.Result) string {
	if len(res.Outputs) == 0 {
		return ""
	}
	return res.Outputs[len(res.Outputs)-1].Path
}

func audioLabel(kbps int) string {
	if kbps == 0 {
		return "없음"
	}
	return fmt.Sprintf("%dkbps", kbps)
}

// humanBytes formats a decimal byte count with one decimal place.
func humanBytes(n int64) string {
	const unit = 1_000_000
	if n < unit {
		return fmt.Sprintf("%.0fKB", float64(n)/1000)
	}
	return fmt.Sprintf("%.1fMB", float64(n)/unit)
}

// progressPrinter keeps a single updating status line. It is silent in JSON
// mode so stdout stays machine-readable.
type progressPrinter struct {
	w      io.Writer
	quiet  bool
	label  string
	last   float64
	lastAt time.Time
}

func newProgressPrinter(w io.Writer, quiet bool, label string) *progressPrinter {
	return &progressPrinter{w: w, quiet: quiet, label: label}
}

func (p *progressPrinter) update(pr engine.Progress) {
	if p.quiet {
		return
	}
	if pr.Percent < p.last {
		pr.Percent = p.last
	}
	now := time.Now()
	if pr.Percent < 100 && pr.Percent-p.last < 1 && now.Sub(p.lastAt) < 200*time.Millisecond {
		return
	}
	p.last = pr.Percent
	p.lastAt = now
	fmt.Fprintf(p.w, "\r%s: %s %.0f%%    ", p.label, stageLabel(pr.Stage), pr.Percent)
}

func (p *progressPrinter) close() {
	if !p.quiet {
		fmt.Fprint(p.w, "\r"+strings.Repeat(" ", 60)+"\r")
	}
}

func (p *progressPrinter) println(format string, args ...any) {
	if p.quiet {
		return
	}
	fmt.Fprint(p.w, "\r"+strings.Repeat(" ", 60)+"\r")
	fmt.Fprintf(p.w, format+"\n", args...)
}

func stageLabel(stage string) string {
	switch {
	case strings.HasPrefix(stage, "compressing"):
		return "압축 중"
	case stage == "repairing":
		return "조각 보정 중"
	case stage == "verifying":
		return "검증 중"
	case stage == "saving":
		return "저장 중"
	case stage == "probing":
		return "확인 중"
	case stage == "done":
		return "완료"
	default:
		return stage
	}
}
