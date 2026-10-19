package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"videocompress/internal/config"
	"videocompress/internal/engine"
	"videocompress/internal/ffmpeg"
	"videocompress/internal/jobs"
	"videocompress/internal/logging"
)

// Event names emitted to the frontend.
const (
	EventQueueUpdate  = "queue:update"
	EventFFmpegStatus = "ffmpeg:status"
	EventNotice       = "app:notice"
)

// FFmpegStatus is the FFmpeg readiness snapshot shown in the UI.
type FFmpegStatus struct {
	Found       bool    `json:"found"`
	Path        string  `json:"path"`
	Downloading bool    `json:"downloading"`
	Percent     float64 `json:"percent"`
	Error       string  `json:"error"`
}

// App is the Wails-bound application object.
type App struct {
	ctx context.Context

	mu           sync.Mutex
	settings     config.Settings
	settingsPath string
	tools        ffmpeg.Tools
	ffErr        string
	downloading  bool
	downloadPct  float64

	cacheDir   string
	tempRoot   string
	logger     *logging.Logger
	locator    ffmpeg.Locator
	downloader ffmpeg.Downloader
	newEngine  func(ffmpeg.Tools, string) *engine.Engine

	manager *jobs.Manager
	emit    func(event string, data ...any)

	openFilesFn  func(context.Context) ([]string, error)
	pickFFmpegFn func(context.Context) (string, error)
	revealFn     func(string) error
}

// NewApp wires the real environment. It never fails; missing paths surface
// through GetFFmpegStatus and SaveSettings errors.
func NewApp() *App {
	a := &App{settings: config.Default()}
	if p, err := config.DefaultSettingsPath(); err == nil {
		a.settingsPath = p
	}
	if p, err := config.CacheDirPath(); err == nil {
		a.cacheDir = p
		a.tempRoot = filepath.Join(p, "temp")
	}
	if exe, err := os.Executable(); err == nil {
		a.locator.AppDir = filepath.Dir(exe)
	}
	a.locator.CacheDir = a.cacheDir
	a.locator.LookPath = exec.LookPath
	a.newEngine = func(tools ffmpeg.Tools, tempRoot string) *engine.Engine {
		eng := engine.New(&ffmpeg.Client{Tools: tools, Logf: a.logf}, tempRoot)
		eng.Logf = a.logf
		return eng
	}
	a.emit = func(event string, data ...any) {
		if a.ctx != nil {
			wruntime.EventsEmit(a.ctx, event, data...)
		}
	}
	a.manager = jobs.NewManager(&lazyProcessor{app: a}, a.onQueueChange)
	return a
}

// lazyProcessor resolves FFmpeg at job time so a download that finishes after
// the queue was filled is picked up without restarting.
type lazyProcessor struct{ app *App }

func (p *lazyProcessor) Process(ctx context.Context, req engine.Request, onProgress func(engine.Progress)) (*engine.Result, error) {
	eng, err := p.app.engineFor()
	if err != nil {
		p.app.logf("job %s: %v", req.JobID, err)
		return nil, err
	}
	p.app.logf("job %s start: %s (dryRun=%v)", req.JobID, req.InputPath, req.DryRun)
	res, err := eng.Process(ctx, req, onProgress)
	if err != nil {
		p.app.logf("job %s failed: %v", req.JobID, err)
		return nil, err
	}
	if res != nil {
		p.app.logf("job %s done: mode=%s outputs=%d parts=%d lowQuality=%v", req.JobID, res.Mode, len(res.Outputs), res.Parts, res.LowQuality)
	}
	return res, err
}

func (a *App) engineFor() (*engine.Engine, error) {
	a.mu.Lock()
	tools, ffErr, cacheDir, newEngine := a.tools, a.ffErr, a.cacheDir, a.newEngine
	a.mu.Unlock()
	if tools.FFmpeg == "" {
		if ffErr != "" {
			return nil, errors.New(ffErr)
		}
		return nil, errors.New("FFmpeg를 찾을 수 없습니다. 상단에서 FFmpeg를 준비해 주세요.")
	}
	if newEngine != nil {
		return newEngine(tools, filepath.Join(cacheDir, "temp")), nil
	}
	return engine.New(&ffmpeg.Client{Tools: tools}, filepath.Join(cacheDir, "temp")), nil
}

// startup loads settings, registers native file drops, and prepares FFmpeg.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// The log lives beside the FFmpeg cache: %LOCALAPPDATA%\VideoCompress\logs.
	if a.cacheDir != "" {
		if lg, err := logging.Open(filepath.Join(a.cacheDir, "logs")); err == nil {
			a.logger = lg
		}
	}
	a.logf("app starting: settings=%s cache=%s", a.settingsPath, a.cacheDir)
	a.loadSettings()
	a.cleanupStaleTemp()
	wruntime.OnFileDrop(ctx, func(_, _ int, paths []string) {
		a.AddFiles(paths)
	})
	if err := a.prepareFFmpeg(ctx, false); err != nil {
		a.logf("ffmpeg unavailable: %v", err)
		a.notice("error", err.Error())
	} else {
		a.logf("ffmpeg ready: %s", a.GetFFmpegStatus().Path)
	}
}

func (a *App) shutdown(context.Context) {
	a.manager.Cancel()
	a.logf("app shutting down")
	_ = a.logger.Close()
}

// beforeClose asks before quitting while jobs run; returns true to block close.
func (a *App) beforeClose(ctx context.Context) bool {
	if !a.manager.IsRunning() {
		return false
	}
	choice, err := wruntime.MessageDialog(ctx, wruntime.MessageDialogOptions{
		Type:          wruntime.QuestionDialog,
		Title:         "작업 진행 중",
		Message:       "압축이 진행 중입니다. 종료하면 작업이 취소됩니다. 종료할까요?",
		Buttons:       []string{"종료", "계속 처리"},
		DefaultButton: "계속 처리",
		CancelButton:  "계속 처리",
	})
	if err != nil {
		return false
	}
	if choice == "종료" {
		a.manager.Cancel()
		return false
	}
	return true
}

func (a *App) loadSettings() {
	if a.settingsPath == "" {
		return
	}
	s, err := config.Load(a.settingsPath)
	a.mu.Lock()
	a.settings = s
	a.locator.Configured = s.FFmpegPath
	a.mu.Unlock()
	if err != nil {
		a.notice("warn", fmt.Sprintf("설정을 읽지 못해 기본값을 사용합니다: %v", err))
	}
}

// GetSettings returns the current settings.
func (a *App) GetSettings() config.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

// SaveSettings validates, persists, and applies new size limits.
func (a *App) SaveSettings(targetMB, partMB int) error {
	a.mu.Lock()
	current := a.settings
	a.mu.Unlock()
	next := config.Settings{TargetMB: targetMB, PartMB: partMB, FFmpegPath: current.FFmpegPath}
	if err := next.Validate(); err != nil {
		return err
	}
	if a.settingsPath != "" {
		if err := config.Save(a.settingsPath, next); err != nil {
			return fmt.Errorf("설정을 저장하지 못했습니다: %w", err)
		}
	}
	a.mu.Lock()
	a.settings = next
	a.mu.Unlock()
	a.notice("info", "설정을 저장했습니다.")
	return nil
}

// AddFiles queues files (used by native drops and the picker).
func (a *App) AddFiles(paths []string) []jobs.Job {
	return a.manager.Add(paths...)
}

// GetJobs returns the current queue snapshot.
func (a *App) GetJobs() []jobs.Job { return a.manager.Snapshot() }

// RemoveJob removes a non-running job.
func (a *App) RemoveJob(id string) bool {
	ok := a.manager.Remove(id)
	a.emitQueue()
	return ok
}

// ClearFinished removes finished jobs and returns how many were removed.
func (a *App) ClearFinished() int {
	n := a.manager.ClearFinished()
	a.emitQueue()
	return n
}

// StartProcessing starts the queue with the saved settings.
func (a *App) StartProcessing() error {
	if _, err := a.engineFor(); err != nil {
		return err
	}
	s := a.GetSettings()
	if err := a.manager.Start(jobs.Options{TargetBytes: s.TargetBytes(), PartBytes: s.PartBytes()}); err != nil {
		return err
	}
	return nil
}

// CancelJobs cancels the running job and marks queued jobs canceled.
func (a *App) CancelJobs() {
	a.manager.Cancel()
	a.emitQueue()
}

// IsRunning reports whether a run is in progress.
func (a *App) IsRunning() bool { return a.manager.IsRunning() }

// GetFFmpegStatus returns FFmpeg readiness for the UI.
func (a *App) GetFFmpegStatus() FFmpegStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return FFmpegStatus{
		Found: a.tools.FFmpeg != "", Path: a.tools.FFmpeg,
		Downloading: a.downloading, Percent: a.downloadPct, Error: a.ffErr,
	}
}

// DownloadFFmpeg starts the one-time FFmpeg download in the background and
// reports progress through the ffmpeg:status event.
func (a *App) DownloadFFmpeg() error {
	a.mu.Lock()
	if a.downloading {
		a.mu.Unlock()
		return errors.New("이미 FFmpeg를 내려받는 중입니다")
	}
	a.downloading = true
	a.ffErr = ""
	a.downloadPct = 0
	a.mu.Unlock()
	a.emitStatus()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
		defer cancel()
		err := a.prepareFFmpeg(ctx, true)
		a.mu.Lock()
		a.downloading = false
		if err != nil {
			a.ffErr = err.Error()
		}
		a.mu.Unlock()
		a.emitStatus()
		if err != nil {
			a.notice("error", "FFmpeg 준비에 실패했습니다: "+err.Error())
			return
		}
		a.notice("info", "FFmpeg 준비가 끝났습니다. 이제 압축을 시작할 수 있습니다.")
	}()
	return nil
}

// prepareFFmpeg locates FFmpeg, downloading it when allowDownload is set. A
// locate-only miss is not an error: the UI offers a download button.
func (a *App) prepareFFmpeg(ctx context.Context, allowDownload bool) error {
	a.mu.Lock()
	loc, dl, cacheDir := a.locator, a.downloader, a.cacheDir
	a.mu.Unlock()

	if tools, err := loc.Locate(ctx); err == nil {
		a.mu.Lock()
		a.tools = tools
		a.ffErr = ""
		a.mu.Unlock()
		a.emitStatus()
		return nil
	} else if !allowDownload {
		a.mu.Lock()
		a.ffErr = fmt.Sprintf("FFmpeg를 찾을 수 없습니다: %v", err)
		a.mu.Unlock()
		a.emitStatus()
		return nil
	}

	if cacheDir == "" {
		return errors.New("캐시 폴더를 준비할 수 없습니다")
	}
	tools, err := dl.Download(ctx, filepath.Join(cacheDir, "ffmpeg", "bin"), func(done, total int64) {
		pct := 0.0
		if total > 0 {
			pct = float64(done) / float64(total) * 100
		}
		a.mu.Lock()
		a.downloadPct = pct
		a.mu.Unlock()
		a.emitStatus()
	})
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.tools = tools
	a.ffErr = ""
	a.downloadPct = 100
	a.mu.Unlock()
	a.emitStatus()
	return nil
}

// PickFFmpeg lets the user point at an installed ffmpeg.exe when downloading is
// not an option. The chosen binary is verified (ffprobe must sit beside it),
// remembered in settings, and used immediately.
func (a *App) PickFFmpeg() (string, error) {
	if a.ctx == nil {
		return "", errors.New("앱이 아직 준비되지 않았습니다")
	}
	path, err := a.chooseFFmpeg()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(path) == "" {
		return "", errors.New("선택이 취소되었습니다")
	}
	a.mu.Lock()
	verify := a.locator.Verify
	a.mu.Unlock()
	// The manual pick must be usable on its own: never fall back to another copy.
	tools, err := ffmpeg.Locator{Override: path, Verify: verify}.Locate(a.ctx)
	if err != nil {
		return "", fmt.Errorf("선택한 FFmpeg를 사용할 수 없습니다(같은 폴더에 ffmpeg.exe와 ffprobe.exe가 있어야 합니다): %w", err)
	}
	a.mu.Lock()
	a.tools = tools
	a.ffErr = ""
	a.settings.FFmpegPath = tools.FFmpeg
	a.locator.Configured = tools.FFmpeg
	next, settingsPath := a.settings, a.settingsPath
	a.mu.Unlock()
	if settingsPath != "" {
		if err := config.Save(settingsPath, next); err != nil {
			a.notice("warn", fmt.Sprintf("FFmpeg 경로를 저장하지 못했습니다: %v", err))
		}
	}
	a.emitStatus()
	a.notice("info", "선택한 FFmpeg를 사용합니다.")
	return tools.FFmpeg, nil
}

func (a *App) chooseFFmpeg() (string, error) {
	if a.pickFFmpegFn != nil {
		return a.pickFFmpegFn(a.ctx)
	}
	return wruntime.OpenFileDialog(a.ctx, wruntime.OpenDialogOptions{
		Title:   "ffmpeg.exe 선택",
		Filters: []wruntime.FileFilter{{DisplayName: "ffmpeg 실행 파일", Pattern: "ffmpeg.exe"}},
	})
}

// OpenFileDialog opens the native multi-file picker.
func (a *App) OpenFileDialog() ([]string, error) {
	if a.openFilesFn != nil {
		return a.openFilesFn(a.ctx)
	}
	return wruntime.OpenMultipleFilesDialog(a.ctx, wruntime.OpenDialogOptions{
		Title: "압축할 비디오 선택",
		Filters: []wruntime.FileFilter{{
			DisplayName: "비디오",
			Pattern:     "*.mp4;*.mov;*.mkv;*.webm;*.avi;*.m4v;*.wmv;*.flv;*.ts;*.mpg",
		}},
	})
}

// RevealInExplorer selects the file in Explorer (Windows) or opens its folder.
func (a *App) RevealInExplorer(path string) error {
	if a.revealFn != nil {
		return a.revealFn(path)
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("파일을 찾을 수 없습니다: %s", path)
	}
	if err := exec.Command("explorer", "/select,"+path).Start(); err != nil {
		if a.ctx != nil {
			wruntime.BrowserOpenURL(a.ctx, "file://"+filepath.ToSlash(filepath.Dir(path)))
			return nil
		}
		return err
	}
	return nil
}

// Quit cancels jobs and closes the window.
func (a *App) Quit() {
	a.manager.Cancel()
	if a.ctx != nil {
		wruntime.Quit(a.ctx)
	}
}

func (a *App) onQueueChange() { a.emitQueue() }

func (a *App) emitQueue() { a.emit(EventQueueUpdate, a.manager.Snapshot()) }

func (a *App) emitStatus() { a.emit(EventFFmpegStatus, a.GetFFmpegStatus()) }

func (a *App) notice(level, message string) {
	a.emit(EventNotice, map[string]string{"level": level, "message": message})
}

// logf appends a line to the app log. It is safe to call at any time: a nil
// logger and write errors are both ignored.
func (a *App) logf(format string, args ...any) {
	a.logger.Printf(format, args...)
}

// cleanupStaleTemp removes scratch folders left behind by a crashed run: job
// temp dirs and interrupted FFmpeg downloads.
func (a *App) cleanupStaleTemp() {
	sweepStale(a.tempRoot)
	if a.cacheDir != "" {
		sweepStale(filepath.Join(a.cacheDir, "ffmpeg"))
	}
}

// tempPrefixes are the only directory names sweepStale may delete, so a user's
// own files that happen to live next to ours are never touched.
var tempPrefixes = []string{"vc-job-", "ffmpeg-dl-"}

// sweepStale deletes scratch directories older than a day under dir.
func sweepStale(dir string) {
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, en := range entries {
		if !en.IsDir() || !hasAnyPrefix(en.Name(), tempPrefixes) {
			continue
		}
		info, err := en.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) > 24*time.Hour {
			_ = os.RemoveAll(filepath.Join(dir, en.Name()))
		}
	}
}

func hasAnyPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
