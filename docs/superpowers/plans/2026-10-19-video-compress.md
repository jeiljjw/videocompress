# VideoCompress 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 비디오를 5MB 이하 단일 MP4로 압축하고, 불가능하면 4MB 이하 재생 가능 조각으로 분할하는 Wails(GUI)·CLI 프로그램을 만든다.

**Architecture:** Wails v2(Go) 앱과 별도 콘솔 CLI `vc.exe`가 `internal/{config,media,ffmpeg,engine,jobs}`를 공유한다. `media`는 순수 계산(비트레이트·해상도·분할 계획), `ffmpeg`는 바이너리 탐색/다운로드/실행, `engine`은 실제 압축·분할·검증, `jobs`는 큐와 이벤트를 담당한다. 모든 출력은 임시 폴더에서 검증 후 원자적으로 이동한다.

**Tech Stack:** Go 1.26, Wails v2.11, React 18.3 + TypeScript 5.6 + Vite 5.4, Vitest 2.1 + Testing Library, FFmpeg(H.264/AAC, gyand.dev 자동 다운로드)

**Spec:** `docs/superpowers/specs/2026-10-19-video-compress-design.md`

## Global Constraints

- 모듈 이름은 `videocompress`. 모든 내부 패키지는 `videocompress/internal/...`로 임포트한다.
- MB = 1,000,000바이트. 기본 목표 5MB, 기본 조각 4MB. 설정 검증: `2 ≤ 목표 ≤ 100`, `1 ≤ 조각 < 목표`.
- 압축 안전 계수 0.95, 비디오 비트레이트 clamp `100..12000` kbps.
- 해상도 사다리: 1080p 박스 1920×1080, 720p 1280×720, 480p 854×480. 업스케일 금지.
- 화질 하한(결과 짧은 변 기준): `≥1000 → 1500`, `≥700 → 800`, 그 외 `400` kbps.
- 오디오: AAC이고 비트레이트가 0보다 크고 128kbps 이하일 때만 복사, 아니면 AAC 96kbps(스테레오)/64kbps(모노), 채널 최대 2. 분할 모드는 항상 재인코딩.
- 인코딩: `libx264 -preset medium -profile:v high -pix_fmt yuv420p`, 2-pass ABR, 단일 출력은 `-movflags +faststart`.
- 분할: 해상도 `min(원본, 720p 박스)`, 조각 길이 후보 `[120,60,30,15]`초(긴 것 우선, 하한 미달이면 15초+LowQuality), 키프레임 강제 + segment muxer, 조각별 크기 검증 후 초과분만 재인코딩.
- 검증: 단일은 크기·비디오 스트림·duration ±2초, 분할은 조각별 크기·조각 합 ±`max(2, 조각수×0.1)`초·첫 조각 start_time ≈ 0.
- 출력 이름: 단일 `<이름>_compressed.mp4`, 충돌 시 `<이름>_compressed (2).mp4`. 분할 `<이름>_part01.mp4`, 그룹 충돌 시 `<이름> (2)_part01.mp4`. 번호 자릿수 `max(2, 자릿수)`.
- 원본은 절대 수정하지 않는다. 기존 파일을 덮어쓰지 않는다.
- 임시 경로: `os.UserCacheDir()/VideoCompress/temp/vc-job-*/`(작업마다 임시 폴더를 만들고 끝나면 삭제), 설정: `os.UserConfigDir()/VideoCompress/settings.json`, FFmpeg: `<cache>/VideoCompress/ffmpeg/bin/`.
- GUI 이벤트: `queue:update`(Job 배열), `ffmpeg:status`(상태 객체), `app:notice`(`{level,message}`).
- UI 문자열은 한국어. 사용자에게 보이는 오류는 원인과 다음 행동을 포함한다.
- 모든 테스트는 `go test ./...`, `npm --prefix frontend run test -- --run`로 실행한다. FFmpeg가 필요한 테스트는 없으면 `t.Skip`한다.

## Review Focus

1. 회전 메타데이터가 있는 세로 영상: 출력이 회전된 채로 이중 회전되거나 스케일이 왜곡되지 않아야 한다.
2. 오디오 비트레이트를 알 수 없는 AAC 소스: 복사로 판단해 크기를 초과하는 일이 없어야 한다(모르면 재인코딩).
3. 매우 긴 영상(2시간 이상): 480p 하한에서도 목표 불가 → 분할로 폴백하고 조각 수가 과도해도 각 조각이 4MB 이하여야 한다.
4. 한글·공백 경로: 2-pass 로그, 임시 파일, 최종 이동이 모두 정상 동작해야 한다.
5. 취소/실패 중간 상태: 최종 위치에 부분 산출물이 남지 않고 임시 폴더가 정리되어야 한다.

---

## 파일 구조

```
main.go                      Wails 진입점과 옵션
app.go                       App 구조체와 바인딩 메서드
wails.json
go.mod
cmd/vc/main.go               CLI 진입점(cli.Run 호출)
internal/config/config.go    Settings, 검증, 로드/저장
internal/config/paths.go     설정/캐시/임시 경로
internal/media/info.go       Info, AudioInfo, ParseProbeJSON, HasVideoExt
internal/media/plan.go       FitBox, FloorKbps, VideoKbpsFor, 후보/계획 계산
internal/ffmpeg/tools.go     Tools, Locator, DefaultVerify
internal/ffmpeg/download.go  다운로드와 ZIP 추출
internal/ffmpeg/client.go    ffprobe/ffmpeg 실행, 진행률 파싱
internal/engine/engine.go    Process 오케스트레이션, FF 인터페이스, Progress
internal/engine/args.go      ffmpeg 인자 생성 + RetryKbps (순수 함수)
internal/engine/meter.go     재시도에 견디는 단조 진행률(RetryProgress)
internal/engine/single.go    단일 압축(해상도 사다리 + 재시도)
internal/engine/split.go     분할 인코딩, 조각 보정/반분할
internal/engine/names.go     출력 이름/충돌 회피, 원자적 이동
internal/engine/verify.go    산출물 검증
internal/jobs/manager.go     큐, 순차 처리, 취소, 이벤트
internal/cli/cli.go          CLI 플래그/실행/종료 코드
frontend/src/lib/api.ts      window.go 바인딩 래퍼와 이벤트 구독, 타입
frontend/src/lib/format.ts   바이트/시간/상태/경로 포맷
frontend/src/components/*.tsx  DropZone, JobRow, SettingsPanel, FFmpegBanner, Toolbar
frontend/src/App.tsx         화면 조립
frontend/src/styles.css      전역 스타일
frontend/src/**/*.test.ts(x) Vitest 테스트
docs/superpowers/specs/2026-10-19-video-compress-design.md
docs/superpowers/plans/2026-10-19-video-compress.md
README.md
```

## Task 1: 프로젝트 스캐폴딩 (Wails + React + Go 모듈 + 테스트 도구)

**Files:**
- Create: `go.mod`, `wails.json`, `main.go`, `app.go`
- Create: `cmd/vc/main.go` (임시 스텁)
- Create: `frontend/` (Wails react-ts 스캐폴드, 의존성 버전 고정, Vitest 설정)
- Create: `frontend/src/lib/smoke.test.ts` (도구 검증용)
- Modify: `.gitignore`

- [ ] **Step 1: Wails 스캐폴드를 임시 폴더에 생성하고 필요한 파일만 복사**

```powershell
$tmp = Join-Path $env:TEMP "vc-scaffold"
Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
wails init -n VideoCompress -t react-ts -d $tmp
# .gitignore, README.md는 복사하지 않는다(직접 관리)
Get-ChildItem $tmp -Force | Where-Object { $_.Name -notin @('.gitignore','README.md','go.mod','go.sum','app.go','main.go','wails.json') } |
  Copy-Item -Destination . -Recurse -Force
Remove-Item -Recurse -Force $tmp
```

- [ ] **Step 2: `go.mod`와 `wails.json` 작성**

`go.mod`:
```go
module videocompress

go 1.23

require github.com/wailsapp/wails/v2 v2.11.0
```

`wails.json`:
```json
{
  "$schema": "https://wails.io/schemas/config.v2.json",
  "name": "VideoCompress",
  "outputfilename": "VideoCompress",
  "frontend:install": "npm install",
  "frontend:build": "npm run build",
  "frontend:dev:watcher": "npm run dev",
  "frontend:dev:serverUrl": "auto",
  "author": { "name": "VideoCompress", "email": "noreply@example.com" }
}
```

`.gitignore`에 추가(기존 규칙 유지):
```
frontend/node_modules/
# Keep the directory present (go:embed needs at least one file) while ignoring
# build output. The directory itself must not be ignored, or git never descends
# into it and the negation below has no effect.
frontend/dist/*
!frontend/dist/.gitkeep
frontend/wailsjs/
build/bin/
bin/
*.exe
!build/windows/*.exe
.wails-scaffold/
```

- [ ] **Step 3: `frontend/package.json`을 검증된 버전으로 교체**

```json
{
  "name": "videocompress-frontend",
  "private": true,
  "version": "0.1.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc --noEmit && vite build",
    "preview": "vite preview",
    "test": "vitest"
  },
  "dependencies": {
    "react": "18.3.1",
    "react-dom": "18.3.1"
  },
  "devDependencies": {
    "@testing-library/dom": "10.4.0",
    "@testing-library/jest-dom": "6.6.3",
    "@testing-library/react": "16.3.3",
    "@types/react": "18.3.18",
    "@types/react-dom": "18.3.5",
    "@vitejs/plugin-react": "4.3.4",
    "jsdom": "25.0.1",
    "typescript": "5.6.3",
    "vite": "5.4.21",
    "vitest": "2.1.9"
  }
}
```

`frontend/vite.config.ts`:
```ts
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test-setup.ts'],
    css: false,
  },
})
```

`frontend/src/test-setup.ts`:
```ts
import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'

// Vitest is configured without global test APIs, so Testing Library's automatic
// cleanup is not registered for us. Without this, each render() leaves its DOM
// behind and later queries in the same file match multiple elements.
afterEach(() => {
  cleanup()
})
```

`frontend/tsconfig.json` (템플릿을 아래로 교체):
```json
{
  "compilerOptions": {
    "target": "ES2022",
    "lib": ["DOM", "DOM.Iterable", "ES2022"],
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "skipLibCheck": true,
    "isolatedModules": true,
    "resolveJsonModule": true,
    "allowSyntheticDefaultImports": true,
    "useDefineForClassFields": true,
    "noEmit": true,
    "types": ["vite/client"]
  },
  "include": ["src", "vite.config.ts"]
}
```

`frontend/src/lib/smoke.test.ts`:
```ts
import { describe, expect, it } from 'vitest'

describe('tooling', () => {
  it('runs vitest with jsdom', () => {
    expect(typeof document).toBe('object')
  })
})
```

- [ ] **Step 4: Go 스텁 작성(빌드 확인용)**

`main.go`(Task 13에서 최종 교체, 지금은 최소):
```go
package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()
	err := wails.Run(&options.App{
		Title:     "VideoCompress",
		Width:     1080,
		Height:    760,
		MinWidth:  900,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: app.startup,
		Bind:      []interface{}{app},
	})
	if err != nil {
		log.Fatal(err)
	}
}
```

`app.go`(Task 13에서 최종 교체, 지금은 최소):
```go
package main

import "context"

type App struct {
	ctx context.Context
}

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) { a.ctx = ctx }
```

`cmd/vc/main.go`(Task 12에서 최종 교체):
```go
package main

import (
	"fmt"
	"os"
)

const version = "0.1.0"

func main() {
	fmt.Printf("vc %s\n", version)
	os.Exit(0)
}
```

`frontend/src/App.tsx`(템플릿은 생성된 바인딩 `../wailsjs/go/main/App`의 `Greet`을
import하므로 스텁 `app.go`에는 존재하지 않아 `tsc`가 실패한다. Task 16에서 최종 교체):

```tsx
// Task 1 placeholder. Replaced with the real UI in Task 16.
function App() {
  return (
    <div id="App">
      <h1>VideoCompress</h1>
      <p>스캐폴딩 준비 완료</p>
    </div>
  )
}

export default App
```

`frontend/dist/.gitkeep` 파일을 만들어 embed 디렉터리를 보장한다.

- [ ] **Step 5: 검증**

```powershell
go mod tidy
go build ./...
npm --prefix frontend install
npm --prefix frontend run test -- --run
npm --prefix frontend run build
```
기대: `go build ./...` 성공, Vitest 1개 통과, Vite 빌드 산출물 `frontend/dist/index.html` 생성.

- [ ] **Step 6: `wails build` 1회 실행으로 바인딩 생성 확인**

```powershell
wails build -nopackage
```
기대: `build/bin/VideoCompress.exe` 생성. 실패하면 `frontend/dist` 존재 여부와 Node 경로를 확인한다.

- [ ] **Step 7: 커밋**

```powershell
git add -A
git commit -m "chore: scaffold wails+react project, cli stub, test tooling"
```

---

## Task 2: internal/config (설정·경로·검증)

**Files:**
- Create: `internal/config/config.go`, `internal/config/paths.go`, `internal/config/os_userdirs.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Settings{TargetMB, PartMB int; FFmpegPath string}`, `config.Default()`, `Settings.Validate() error`, `Settings.TargetBytes()/PartBytes() int64`, `config.Load(path string) (Settings, error)`, `config.Save(path string, s Settings) error`, `config.DefaultSettingsPath() (string, error)`, `config.CacheDirPath() (string, error)`, `config.TempRootPath() (string, error)`

- [ ] **Step 1: 실패하는 테스트 작성**

`internal/config/config_test.go`:
```go
package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	s := Default()
	if err := s.Validate(); err != nil {
		t.Fatalf("default settings invalid: %v", err)
	}
	if s.TargetMB != 5 || s.PartMB != 4 {
		t.Fatalf("unexpected defaults: %+v", s)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		s    Settings
		ok   bool
	}{
		{"default", Settings{TargetMB: 5, PartMB: 4}, true},
		{"target too small", Settings{TargetMB: 1, PartMB: 1}, false},
		{"part equals target", Settings{TargetMB: 5, PartMB: 5}, false},
		{"part greater", Settings{TargetMB: 5, PartMB: 6}, false},
		{"part zero", Settings{TargetMB: 5, PartMB: 0}, false},
		{"target too big", Settings{TargetMB: 101, PartMB: 4}, false},
		{"minimal", Settings{TargetMB: 2, PartMB: 1}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.Validate()
			if tc.ok && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestByteConversions(t *testing.T) {
	s := Settings{TargetMB: 5, PartMB: 4}
	if s.TargetBytes() != 5_000_000 {
		t.Fatalf("target bytes = %d", s.TargetBytes())
	}
	if s.PartBytes() != 4_000_000 {
		t.Fatalf("part bytes = %d", s.PartBytes())
	}
}

func TestLoadMissingReturnsDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if s != Default() {
		t.Fatalf("want default, got %+v", s)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	want := Settings{TargetMB: 7, PartMB: 3, FFmpegPath: `C:\tools\ffmpeg.exe`}
	if err := Save(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, want)
	}
}

func TestLoadInvalidFallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := writeFile(path, []byte("{not json")); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err == nil {
		t.Fatal("want error for invalid json")
	}
	if got != Default() {
		t.Fatalf("want default fallback, got %+v", got)
	}
}

func TestLoadInvalidValuesFallsBackToDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad-values.json")
	if err := writeFile(path, []byte(`{"targetMB":3,"partMB":9}`)); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err == nil {
		t.Fatal("want validation error")
	}
	if got != Default() {
		t.Fatalf("want default fallback, got %+v", got)
	}
}
```

- [ ] **Step 2: 테스트 실패 확인**

```powershell
go test ./internal/config
```
기대: 컴파일 실패(`undefined: Default` 등).

- [ ] **Step 3: 구현**

`internal/config/config.go`:
```go
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Settings holds user-configurable limits. Sizes are decimal megabytes.
type Settings struct {
	TargetMB   int    `json:"targetMB"`
	PartMB     int    `json:"partMB"`
	FFmpegPath string `json:"ffmpegPath,omitempty"`
}

// Default returns the built-in defaults (5MB target, 4MB parts).
func Default() Settings {
	return Settings{TargetMB: 5, PartMB: 4}
}

// Validate enforces 2 <= TargetMB <= 100 and 1 <= PartMB < TargetMB.
func (s Settings) Validate() error {
	if s.TargetMB < 2 || s.TargetMB > 100 {
		return fmt.Errorf("목표 크기는 2~100MB여야 합니다 (현재 %dMB)", s.TargetMB)
	}
	if s.PartMB < 1 || s.PartMB >= s.TargetMB {
		return fmt.Errorf("조각 크기는 1MB 이상, 목표 크기 미만이어야 합니다 (현재 %dMB)", s.PartMB)
	}
	return nil
}

func (s Settings) TargetBytes() int64 { return int64(s.TargetMB) * 1_000_000 }
func (s Settings) PartBytes() int64   { return int64(s.PartMB) * 1_000_000 }

// Load reads settings from path. A missing file yields defaults. Any read,
// parse, or validation error also yields defaults together with the error so
// callers can start in a safe state.
func Load(path string) (Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), nil
		}
		return Default(), err
	}
	s := Default()
	if err := json.Unmarshal(data, &s); err != nil {
		return Default(), fmt.Errorf("설정 파일을 읽을 수 없습니다: %w", err)
	}
	if err := s.Validate(); err != nil {
		return Default(), err
	}
	return s, nil
}

// Save validates and atomically writes settings to path.
func Save(path string, s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// writeFile is a small helper used by tests.
func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}
```

`internal/config/paths.go`:
```go
package config

import "path/filepath"

const appDirName = "VideoCompress"

// DefaultSettingsPath returns <UserConfigDir>/VideoCompress/settings.json.
func DefaultSettingsPath() (string, error) {
	base, err := osUserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appDirName, "settings.json"), nil
}

// CacheDirPath returns <UserCacheDir>/VideoCompress.
func CacheDirPath() (string, error) {
	base, err := osUserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appDirName), nil
}

// TempRootPath returns <UserCacheDir>/VideoCompress/temp.
func TempRootPath() (string, error) {
	cache, err := CacheDirPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "temp"), nil
}
```

`internal/config/os_userdirs.go`:
```go
package config

import "os"

func osUserConfigDir() (string, error) { return os.UserConfigDir() }
func osUserCacheDir() (string, error)  { return os.UserCacheDir() }
```

- [ ] **Step 4: 테스트 통과 확인**

```powershell
go test ./internal/config
```
기대: 모든 케이스 통과.

- [ ] **Step 5: vet + 커밋**

```powershell
go vet ./...
git add internal/config
git commit -m "feat(config): settings, validation, and app paths"
```

---

## Task 3: internal/media — 프로브 정보와 ffprobe JSON 파싱

**Files:**
- Create: `internal/media/info.go`
- Test: `internal/media/info_test.go`

**Interfaces:**
- Produces: `media.AudioInfo`, `media.Info`, `media.ParseProbeJSON(data []byte, path string, sizeBytes int64) (*Info, error)`, `media.HasVideoExt(name string) bool`, 오류 `media.ErrNoVideo`, `media.ErrNoDuration`

- [ ] **Step 1: 실패하는 테스트 작성**

`internal/media/info_test.go`:
```go
package media

import "testing"

const probeFull = `{
  "streams": [
    {"codec_type":"video","codec_name":"h264","width":1920,"height":1080,
     "duration":"120.5","avg_frame_rate":"30000/1001"},
    {"codec_type":"audio","codec_name":"aac","bit_rate":"127999","channels":2,"sample_rate":"48000"}
  ],
  "format": {"duration":"120.53","size":"52428800"}
}`

const probeRotated = `{
  "streams": [
    {"codec_type":"video","codec_name":"h264","width":1920,"height":1080,
     "side_data_list":[{"side_data_type":"Display Matrix","rotation":-90}]}
  ],
  "format": {"duration":"10.0"}
}`

const probeTagRotate = `{
  "streams": [
    {"codec_type":"video","codec_name":"h264","width":1080,"height":1920,
     "tags":{"rotate":"90"}}
  ],
  "format": {"duration":"10.0"}
}`

const probeAudioOnly = `{
  "streams": [{"codec_type":"audio","codec_name":"aac"}],
  "format": {"duration":"3.0"}
}`

const probeStreamDuration = `{
  "streams": [{"codec_type":"video","codec_name":"vp9","width":640,"height":360,"duration":"7.25"}],
  "format": {}
}`

func TestParseProbeFull(t *testing.T) {
	info, err := ParseProbeJSON([]byte(probeFull), `C:\videos\a.mp4`, 52428800)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if info.Width != 1920 || info.Height != 1080 {
		t.Fatalf("dims = %dx%d", info.Width, info.Height)
	}
	if info.Duration != 120.53 {
		t.Fatalf("duration = %v", info.Duration)
	}
	if info.Rotation != 0 {
		t.Fatalf("rotation = %d", info.Rotation)
	}
	// 127999 bit/s rounds to 128 kbps.
	if info.Audio == nil || info.Audio.Codec != "aac" || info.Audio.BitrateKbps != 128 || info.Audio.Channels != 2 {
		t.Fatalf("audio = %+v", info.Audio)
	}
	if info.SizeBytes != 52428800 {
		t.Fatalf("size = %d", info.SizeBytes)
	}
}

func TestParseProbeRotatesDimensions(t *testing.T) {
	info, err := ParseProbeJSON([]byte(probeRotated), "a.mp4", 1)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if info.Rotation != 270 {
		t.Fatalf("rotation = %d, want 270", info.Rotation)
	}
	if info.Width != 1080 || info.Height != 1920 {
		t.Fatalf("display dims = %dx%d, want 1080x1920", info.Width, info.Height)
	}
}

func TestParseProbeTagRotate(t *testing.T) {
	info, err := ParseProbeJSON([]byte(probeTagRotate), "a.mp4", 1)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if info.Rotation != 90 || info.Width != 1920 || info.Height != 1080 {
		t.Fatalf("got rotation=%d dims=%dx%d", info.Rotation, info.Width, info.Height)
	}
}

func TestParseProbeStreamDurationFallback(t *testing.T) {
	info, err := ParseProbeJSON([]byte(probeStreamDuration), "a.webm", 100)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if info.Duration != 7.25 {
		t.Fatalf("duration = %v", info.Duration)
	}
}

func TestParseProbeErrors(t *testing.T) {
	if _, err := ParseProbeJSON([]byte(probeAudioOnly), "a.mp3", 1); err != ErrNoVideo {
		t.Fatalf("audio only err = %v, want ErrNoVideo", err)
	}
	if _, err := ParseProbeJSON([]byte(`{"streams":[{"codec_type":"video","width":10,"height":10}]}`), "a.mp4", 1); err != ErrNoDuration {
		t.Fatalf("no duration err = %v, want ErrNoDuration", err)
	}
	if _, err := ParseProbeJSON([]byte(`not json`), "a.mp4", 1); err == nil {
		t.Fatal("want parse error")
	}
}

func TestHasVideoExt(t *testing.T) {
	yes := []string{"a.mp4", "A.MP4", "b.mov", "c.mkv", "d.webm", "e.avi", "f.m4v", "g.wmv"}
	no := []string{"a.txt", "b.jpg", "noext", "c"}
	for _, n := range yes {
		if !HasVideoExt(n) {
			t.Fatalf("%s should be a video ext", n)
		}
	}
	for _, n := range no {
		if HasVideoExt(n) {
			t.Fatalf("%s should not be a video ext", n)
		}
	}
}
```

- [ ] **Step 2: 테스트 실패 확인**

```powershell
go test ./internal/media
```
기대: 컴파일 실패.

- [ ] **Step 3: 구현**

`internal/media/info.go`:
```go
package media

import (
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strconv"
	"strings"
)

var (
	ErrNoVideo    = errors.New("비디오 스트림이 없습니다")
	ErrNoDuration = errors.New("영상 길이를 확인할 수 없습니다")
)

// AudioInfo describes the first audio stream.
type AudioInfo struct {
	Codec       string
	BitrateKbps int
	Channels    int
	SampleRate  int
}

// Info is the subset of ffprobe data the planner needs. Width and Height are
// display dimensions: for videos with 90/270 degree rotation they are swapped
// relative to the stored stream size, matching what FFmpeg outputs after
// automatic rotation.
type Info struct {
	Path       string
	SizeBytes  int64
	Duration   float64
	Width      int
	Height     int
	VideoCodec string
	Rotation   int
	Audio      *AudioInfo
}

func (i *Info) HasAudio() bool { return i != nil && i.Audio != nil }

type probeJSON struct {
	Streams []probeStream `json:"streams"`
	Format  probeFormat   `json:"format"`
}

type probeStream struct {
	CodecType    string            `json:"codec_type"`
	CodecName    string            `json:"codec_name"`
	Width        int               `json:"width"`
	Height       int               `json:"height"`
	Duration     string            `json:"duration"`
	BitRate      string            `json:"bit_rate"`
	Channels     int               `json:"channels"`
	SampleRate   string            `json:"sample_rate"`
	Tags         map[string]string `json:"tags"`
	SideDataList []probeSideData   `json:"side_data_list"`
}

type probeSideData struct {
	Rotation float64 `json:"rotation"`
}

type probeFormat struct {
	Duration string `json:"duration"`
	Size     string `json:"size"`
}

// ParseProbeJSON converts ffprobe -print_format json output into Info.
func ParseProbeJSON(data []byte, path string, sizeBytes int64) (*Info, error) {
	var raw probeJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	info := &Info{Path: path, SizeBytes: sizeBytes}
	var video *probeStream
	for i := range raw.Streams {
		s := &raw.Streams[i]
		switch s.CodecType {
		case "video":
			if video == nil {
				video = s
			}
		case "audio":
			if info.Audio == nil {
				info.Audio = &AudioInfo{
					Codec:       s.CodecName,
					BitrateKbps: parseKbps(s.BitRate),
					Channels:    s.Channels,
					SampleRate:  parseDurationInt(s.SampleRate),
				}
			}
		}
	}
	if video == nil {
		return nil, ErrNoVideo
	}
	info.VideoCodec = video.CodecName
	info.Width, info.Height = video.Width, video.Height

	info.Duration = parseDuration(raw.Format.Duration)
	if info.Duration <= 0 {
		for _, s := range raw.Streams {
			if d := parseDuration(s.Duration); d > info.Duration {
				info.Duration = d
			}
		}
	}
	if info.Duration <= 0 {
		return nil, ErrNoDuration
	}

	rot := 0.0
	if len(video.SideDataList) > 0 {
		rot = video.SideDataList[0].Rotation
	} else if v, ok := video.Tags["rotate"]; ok {
		rot, _ = strconv.ParseFloat(v, 64)
	}
	info.Rotation = normalizeRotation(rot)
	if info.Rotation == 90 || info.Rotation == 270 {
		info.Width, info.Height = info.Height, info.Width
	}
	return info, nil
}

func normalizeRotation(r float64) int {
	n := int(math.Round(r)) % 360
	if n < 0 {
		n += 360
	}
	switch n {
	case 90, 180, 270:
		return n
	default:
		return 0
	}
}

func parseDuration(s string) float64 {
	if s == "" || s == "N/A" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

func parseKbps(s string) int {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return 0
	}
	return int(math.Round(v / 1000))
}

func parseDurationInt(s string) int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return v
}

var videoExts = map[string]bool{
	".mp4": true, ".m4v": true, ".mov": true, ".mkv": true,
	".webm": true, ".avi": true, ".wmv": true, ".flv": true,
	".ts": true, ".mts": true, ".mpg": true, ".mpeg": true, ".3gp": true,
}

// HasVideoExt reports whether the file name has a known video extension.
func HasVideoExt(name string) bool {
	return videoExts[strings.ToLower(filepath.Ext(name))]
}
```

- [ ] **Step 4: 테스트 통과 확인**

```powershell
go test ./internal/media
go vet ./...
```
기대: 전부 통과.

- [ ] **Step 5: 커밋**

```powershell
git add internal/media
git commit -m "feat(media): probe info parsing with rotation and audio detection"
```

---

## Task 4: internal/media — 비트레이트·해상도·분할 계획 (순수 계산)

**Files:**
- Create: `internal/media/plan.go`
- Test: `internal/media/plan_test.go`

**Interfaces:**
- Consumes: `media.Info`, `media.AudioInfo`
- Produces:
  - `media.FitBox(w, h, boxW, boxH int) (int, int)`
  - `media.FloorKbps(w, h int) int`
  - `media.VideoKbpsFor(bytes int64, seconds float64, audioKbps int) int`
  - `media.AudioPlan(info *Info, forceEncode bool) (kbps int, copyOK bool)`
  - `media.SingleCandidates(info *Info, targetBytes int64) []Candidate`
  - `media.SplitPlanFor(info *Info, partBytes int64) (*SplitPlan, error)`
  - `media.PlanFor(info *Info, targetBytes, partBytes int64) (*Plan, error)`
  - 상수 `ModeSingle/ModeSplit/ModeSkipped`, `MinVideoKbps`, `MaxVideoKbps`; 오류 `ErrInvalidInfo`, `ErrInvalidSettings`

- [ ] **Step 1: 실패하는 테스트 작성**

`internal/media/plan_test.go`:
```go
package media

import (
	"errors"
	"testing"
)

func videoInfo(w, h int, dur float64, size int64, audio *AudioInfo) *Info {
	return &Info{Path: "a.mp4", SizeBytes: size, Duration: dur, Width: w, Height: h, VideoCodec: "h264", Audio: audio}
}

func TestFitBox(t *testing.T) {
	cases := []struct{ w, h, bw, bh, wantW, wantH int }{
		{1920, 1080, 1280, 720, 1280, 720},
		{3840, 2160, 1920, 1080, 1920, 1080},
		{1080, 1920, 1280, 720, 404, 720},
		{640, 360, 854, 480, 640, 360}, // no upscale
		{640, 480, 854, 480, 640, 480}, // no upscale, already fits
		{1000, 1000, 1920, 1080, 1000, 1000}, // already fits, never upscale
		{1920, 800, 1280, 720, 1280, 532},    // 533.3 → even 532
		{1919, 1079, 1920, 1080, 1918, 1078},
	}
	for _, c := range cases {
		gotW, gotH := FitBox(c.w, c.h, c.bw, c.bh)
		if gotW != c.wantW || gotH != c.wantH {
			t.Errorf("FitBox(%d,%d,%d,%d) = %d,%d want %d,%d", c.w, c.h, c.bw, c.bh, gotW, gotH, c.wantW, c.wantH)
		}
	}
}

func TestFloorKbps(t *testing.T) {
	cases := []struct{ w, h, want int }{
		{1920, 1080, 1500},
		{1080, 1920, 1500},
		{1280, 720, 800},
		{720, 1280, 800},
		{854, 480, 400},
		{640, 360, 400},
	}
	for _, c := range cases {
		if got := FloorKbps(c.w, c.h); got != c.want {
			t.Errorf("FloorKbps(%d,%d) = %d want %d", c.w, c.h, got, c.want)
		}
	}
}

func TestVideoKbpsFor(t *testing.T) {
	// 5MB / 60s, audio 128: floor(666.66*0.95)=633; 633-128=505
	if got := VideoKbpsFor(5_000_000, 60, 128); got != 505 {
		t.Fatalf("got %d want 505", got)
	}
	// 5MB / 10s, audio 96: 4000*0.95-96 = 3704
	if got := VideoKbpsFor(5_000_000, 10, 96); got != 3704 {
		t.Fatalf("got %d want 3704", got)
	}
	// 20MB / 1s, audio 96: 160000*0.95-96 → capped at 12000
	if got := VideoKbpsFor(20_000_000, 1, 96); got != 12000 {
		t.Fatalf("got %d want 12000", got)
	}
	// 1MB / 3600s, audio 96: clamped to 100
	if got := VideoKbpsFor(1_000_000, 3600, 96); got != 100 {
		t.Fatalf("got %d want 100", got)
	}
}

func TestAudioPlan(t *testing.T) {
	cases := []struct {
		name       string
		audio      *AudioInfo
		force      bool
		wantKbps   int
		wantCopy   bool
	}{
		{"none", nil, false, 0, false},
		{"aac 128 copy", &AudioInfo{Codec: "aac", BitrateKbps: 128, Channels: 2}, false, 128, true},
		{"aac 96 copy", &AudioInfo{Codec: "aac", BitrateKbps: 96, Channels: 2}, false, 96, true},
		{"aac 129 encode", &AudioInfo{Codec: "aac", BitrateKbps: 129, Channels: 2}, false, 96, false},
		{"aac unknown encode", &AudioInfo{Codec: "aac", BitrateKbps: 0, Channels: 2}, false, 96, false},
		{"mp3 stereo", &AudioInfo{Codec: "mp3", BitrateKbps: 128, Channels: 2}, false, 96, false},
		{"mono", &AudioInfo{Codec: "opus", BitrateKbps: 96, Channels: 1}, false, 64, false},
		{"force encode even if small aac", &AudioInfo{Codec: "aac", BitrateKbps: 64, Channels: 2}, true, 96, false},
		{"zero channels defaults stereo", &AudioInfo{Codec: "aac", BitrateKbps: 0, Channels: 0}, false, 96, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kbps, copyOK := AudioPlan(&Info{Audio: c.audio}, c.force)
			if kbps != c.wantKbps || copyOK != c.wantCopy {
				t.Fatalf("got kbps=%d copy=%v want kbps=%d copy=%v", kbps, copyOK, c.wantKbps, c.wantCopy)
			}
		})
	}
}

func TestSingleCandidatesLadder(t *testing.T) {
	// 20s 1080p source, target 5MB, aac 96 copy:
	// total = 2,000kbps *0.95 = 1900; video = 1900-96 = 1804 → 1080p(1500) ok.
	cands := SingleCandidates(videoInfo(1920, 1080, 20, 50_000_000, &AudioInfo{Codec: "aac", BitrateKbps: 96, Channels: 2}), 5_000_000)
	if len(cands) != 3 {
		t.Fatalf("want 3 candidates, got %d: %+v", len(cands), cands)
	}
	if cands[0].Width != 1920 || cands[0].Height != 1080 {
		t.Fatalf("first candidate = %+v", cands[0])
	}
	// 480p box 854x480 fitted to a 1920x1080 source → 852x480 (even, no upscale).
	if cands[1].Width != 1280 || cands[2].Width != 852 {
		t.Fatalf("ladder order wrong: %+v", cands)
	}
}

func TestSingleCandidatesSkipsFloors(t *testing.T) {
	// 120s 1080p, target 5MB → video = 333-96 = 237 (<400) → no candidates.
	cands := SingleCandidates(videoInfo(1920, 1080, 120, 50_000_000, &AudioInfo{Codec: "aac", BitrateKbps: 96, Channels: 2}), 5_000_000)
	if len(cands) != 0 {
		t.Fatalf("want none, got %+v", cands)
	}
}

func TestSingleCandidatesDedupeSmallSource(t *testing.T) {
	cands := SingleCandidates(videoInfo(640, 360, 10, 8_000_000, nil), 5_000_000)
	if len(cands) != 1 {
		t.Fatalf("want 1 deduped candidate, got %+v", cands)
	}
	if cands[0].Width != 640 || cands[0].Height != 360 {
		t.Fatalf("candidate = %+v", cands[0])
	}
}

func TestSplitPlan720p(t *testing.T) {
	// 180s 1080p, part 4MB, audio 96 → 30s chosen, 917kbps, 6 parts.
	sp, err := SplitPlanFor(videoInfo(1920, 1080, 180, 100_000_000, &AudioInfo{Codec: "aac", BitrateKbps: 96, Channels: 2}), 4_000_000)
	if err != nil {
		t.Fatalf("split plan: %v", err)
	}
	if sp.SegmentSeconds != 30 || sp.VideoKbps != 917 || sp.Parts != 6 {
		t.Fatalf("got %+v", sp)
	}
	if sp.Width != 1280 || sp.Height != 720 || sp.AudioKbps != 96 || sp.LowQuality {
		t.Fatalf("got %+v", sp)
	}
}

func TestSplitPlan480pUses60s(t *testing.T) {
	// 480p source stays 480p; 60s: 4e6*8/60/1000*0.95=506.6→506; 506-96=410 >= 400.
	sp, err := SplitPlanFor(videoInfo(854, 480, 300, 100_000_000, &AudioInfo{Codec: "aac", BitrateKbps: 96, Channels: 2}), 4_000_000)
	if err != nil {
		t.Fatalf("split plan: %v", err)
	}
	if sp.SegmentSeconds != 60 || sp.VideoKbps != 410 {
		t.Fatalf("got %+v", sp)
	}
}

func TestSplitPlanLowQualityFallback(t *testing.T) {
	// 0.6MB parts: even 15s cannot reach the 400kbps floor.
	// 6e5*8/15/1000 = 320; 320*0.95 = 304; 304-96 = 208.
	sp, err := SplitPlanFor(videoInfo(1920, 1080, 180, 100_000_000, &AudioInfo{Codec: "aac", BitrateKbps: 96, Channels: 2}), 600_000)
	if err != nil {
		t.Fatalf("split plan: %v", err)
	}
	if !sp.LowQuality || sp.SegmentSeconds != 15 {
		t.Fatalf("got %+v", sp)
	}
	if sp.VideoKbps != 208 || sp.Parts != 12 {
		t.Fatalf("video kbps = %d parts = %d", sp.VideoKbps, sp.Parts)
	}
}

func TestPlanForSkipAndModes(t *testing.T) {
	small := videoInfo(1280, 720, 20, 3_000_000, nil)
	p, err := PlanFor(small, 5_000_000, 4_000_000)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if p.Mode != ModeSkipped {
		t.Fatalf("mode = %s", p.Mode)
	}

	big := videoInfo(1920, 1080, 20, 50_000_000, &AudioInfo{Codec: "aac", BitrateKbps: 96, Channels: 2})
	p, err = PlanFor(big, 5_000_000, 4_000_000)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if p.Mode != ModeSingle || p.Candidate.Width != 1920 {
		t.Fatalf("plan = %+v", p)
	}

	long := videoInfo(1920, 1080, 180, 100_000_000, &AudioInfo{Codec: "aac", BitrateKbps: 96, Channels: 2})
	p, err = PlanFor(long, 5_000_000, 4_000_000)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if p.Mode != ModeSplit || p.Split == nil || p.Split.Parts != 6 {
		t.Fatalf("plan = %+v", p)
	}

	if _, err := PlanFor(videoInfo(0, 0, 0, 0, nil), 5_000_000, 4_000_000); !errors.Is(err, ErrInvalidInfo) {
		t.Fatalf("want ErrInvalidInfo, got %v", err)
	}
	if _, err := PlanFor(big, 5_000_000, 5_000_000); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("want ErrInvalidSettings, got %v", err)
	}
}
```

- [ ] **Step 2: 테스트 실패 확인**

```powershell
go test ./internal/media
```
기대: `undefined: FitBox` 등 컴파일 실패.

- [ ] **Step 3: 구현**

`internal/media/plan.go`:
```go
package media

import (
	"errors"
	"math"
)

const (
	SafetyFactor = 0.95
	MinVideoKbps = 100
	MaxVideoKbps = 12000

	ModeSingle  = "single"
	ModeSplit   = "split"
	ModeSkipped = "skipped"
)

var (
	ErrInvalidInfo     = errors.New("영상 정보가 올바르지 않습니다")
	ErrInvalidSettings = errors.New("목표 크기와 조각 크기 설정이 올바르지 않습니다")
)

// Candidate is one step of the single-file resolution ladder.
type Candidate struct {
	Label     string
	Width     int
	Height    int
	VideoKbps int
}

// SplitPlan describes a split-mode encode.
type SplitPlan struct {
	Width          int
	Height         int
	VideoKbps      int
	AudioKbps      int
	SegmentSeconds int
	Parts          int
	LowQuality     bool
}

// Plan is the outcome of planning for one input file.
type Plan struct {
	Mode      string
	Info      *Info
	AudioKbps int
	AudioCopy bool
	Candidate Candidate
	Stages    []Candidate
	Split     *SplitPlan
}

type box struct {
	label    string
	width    int
	height   int
}

var ladder = []box{
	{"1080p", 1920, 1080},
	{"720p", 1280, 720},
	{"480p", 854, 480},
}

var splitSegments = []int{120, 60, 30, 15}

// FitBox scales w×h to fit inside the box without upscaling, preserving the
// aspect ratio, and returns even dimensions (required by yuv420p).
func FitBox(w, h, boxW, boxH int) (int, int) {
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	if w <= boxW && h <= boxH {
		return even(w), even(h)
	}
	scale := math.Min(float64(boxW)/float64(w), float64(boxH)/float64(h))
	return even(int(math.Round(float64(w) * scale))), even(int(math.Round(float64(h) * scale)))
}

func even(v int) int {
	if v%2 != 0 {
		v--
	}
	if v < 2 {
		v = 2
	}
	return v
}

// FloorKbps returns the minimum acceptable video bitrate for a resolution,
// based on the shorter side.
func FloorKbps(w, h int) int {
	short := w
	if h < short {
		short = h
	}
	switch {
	case short >= 1000:
		return 1500
	case short >= 700:
		return 800
	default:
		return 400
	}
}

// VideoKbpsFor computes the video bitrate budget for a size and duration.
// The result is clamped to [MinVideoKbps, MaxVideoKbps].
func VideoKbpsFor(bytes int64, seconds float64, audioKbps int) int {
	if seconds <= 0 {
		return MinVideoKbps
	}
	total := float64(bytes) * 8 / seconds / 1000
	v := int(math.Floor(total*SafetyFactor)) - audioKbps
	if v < MinVideoKbps {
		return MinVideoKbps
	}
	if v > MaxVideoKbps {
		return MaxVideoKbps
	}
	return v
}

// AudioPlan returns the audio bitrate to reserve and whether the source audio
// can be stream-copied. Unknown AAC bitrates are re-encoded to stay safe.
func AudioPlan(info *Info, forceEncode bool) (int, bool) {
	if info == nil || info.Audio == nil {
		return 0, false
	}
	a := info.Audio
	if !forceEncode && a.Codec == "aac" && a.BitrateKbps > 0 && a.BitrateKbps <= 128 {
		return a.BitrateKbps, true
	}
	if a.Channels == 1 {
		return 64, false
	}
	return 96, false
}

// SingleCandidates returns ladder steps, highest quality first, whose floor is
// satisfied by the target-driven bitrate. Identical dimensions are deduped.
func SingleCandidates(info *Info, targetBytes int64) []Candidate {
	if info == nil || info.Width <= 0 || info.Height <= 0 || info.Duration <= 0 {
		return nil
	}
	audioKbps, _ := AudioPlan(info, false)
	kbps := VideoKbpsFor(targetBytes, info.Duration, audioKbps)
	var out []Candidate
	seen := map[[2]int]bool{}
	for _, b := range ladder {
		w, h := FitBox(info.Width, info.Height, b.width, b.height)
		if w <= 0 || h <= 0 {
			continue
		}
		key := [2]int{w, h}
		if seen[key] {
			continue
		}
		seen[key] = true
		if kbps < FloorKbps(w, h) {
			continue
		}
		out = append(out, Candidate{Label: b.label, Width: w, Height: h, VideoKbps: kbps})
	}
	return out
}

// SplitPlanFor selects the longest segment length whose bitrate meets the
// quality floor for the split resolution (min of source and 720p).
func SplitPlanFor(info *Info, partBytes int64) (*SplitPlan, error) {
	if info == nil || info.Duration <= 0 || info.Width <= 0 || info.Height <= 0 {
		return nil, ErrInvalidInfo
	}
	w, h := FitBox(info.Width, info.Height, 1280, 720)
	audioKbps, _ := AudioPlan(info, true)
	floor := FloorKbps(w, h)
	for _, seg := range splitSegments {
		kbps := VideoKbpsFor(partBytes, float64(seg), audioKbps)
		if kbps >= floor {
			return &SplitPlan{
				Width: w, Height: h, VideoKbps: kbps, AudioKbps: audioKbps,
				SegmentSeconds: seg, Parts: partsFor(info.Duration, seg),
			}, nil
		}
	}
	seg := splitSegments[len(splitSegments)-1]
	return &SplitPlan{
		Width: w, Height: h, VideoKbps: VideoKbpsFor(partBytes, float64(seg), audioKbps),
		AudioKbps: audioKbps, SegmentSeconds: seg, Parts: partsFor(info.Duration, seg),
		LowQuality: true,
	}, nil
}

func partsFor(duration float64, seg int) int {
	n := int(math.Ceil(duration / float64(seg)))
	if n < 1 {
		n = 1
	}
	return n
}

// PlanFor decides between skipping, single-file compression, and splitting.
func PlanFor(info *Info, targetBytes, partBytes int64) (*Plan, error) {
	if info == nil || info.Duration <= 0 || info.Width <= 0 || info.Height <= 0 {
		return nil, ErrInvalidInfo
	}
	if targetBytes <= 0 || partBytes <= 0 || partBytes >= targetBytes {
		return nil, ErrInvalidSettings
	}
	p := &Plan{Info: info}
	if info.SizeBytes > 0 && info.SizeBytes <= targetBytes {
		p.Mode = ModeSkipped
		return p, nil
	}
	p.AudioKbps, p.AudioCopy = AudioPlan(info, false)
	if stages := SingleCandidates(info, targetBytes); len(stages) > 0 {
		p.Mode = ModeSingle
		p.Stages = stages
		p.Candidate = stages[0]
		return p, nil
	}
	sp, err := SplitPlanFor(info, partBytes)
	if err != nil {
		return nil, err
	}
	p.Mode = ModeSplit
	p.Split = sp
	return p, nil
}
```

- [ ] **Step 4: 테스트 통과 확인**

```powershell
go test ./internal/media
go vet ./...
```

- [ ] **Step 5: 커밋**

```powershell
git add internal/media
git commit -m "feat(media): bitrate ladder and split planning"
```

---

## Task 5: internal/ffmpeg — 바이너리 탐색과 검증

**Files:**
- Create: `internal/ffmpeg/tools.go`
- Test: `internal/ffmpeg/tools_test.go`

**Interfaces:**
- Produces: `ffmpeg.Tools{FFmpeg, FFprobe string}`, `ffmpeg.Locator`, `(Locator).Locate(ctx) (Tools, error)`, `ffmpeg.DefaultVerify(ctx, ffmpegPath) error`, `ffmpeg.ErrNotFound`
- `Locator` 필드: `Override`, `Configured`, `AppDir`, `CacheDir string`; `LookPath func(string) (string, error)`; `Verify func(ctx, ffmpegPath) error` (테스트 주입용, nil이면 `DefaultVerify`)

- [ ] **Step 1: 실패하는 테스트 작성**

`internal/ffmpeg/tools_test.go`:
```go
package ffmpeg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fakeVerify succeeds when both ffmpeg and ffprobe files exist.
func fakeVerify(t *testing.T) func(context.Context, string) error {
	t.Helper()
	return func(_ context.Context, ffmpegPath string) error {
		if _, err := os.Stat(ffmpegPath); err != nil {
			return err
		}
		probe := filepath.Join(filepath.Dir(ffmpegPath), "ffprobe"+exeSuffix())
		if _, err := os.Stat(probe); err != nil {
			return errors.New("ffprobe missing")
		}
		return nil
	}
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestLocatePrefersOverride(t *testing.T) {
	dir := t.TempDir()
	overrideDir := filepath.Join(dir, "override")
	configuredDir := filepath.Join(dir, "configured")
	touch(t, filepath.Join(overrideDir, "ffmpeg"+exeSuffix()))
	touch(t, filepath.Join(overrideDir, "ffprobe"+exeSuffix()))
	touch(t, filepath.Join(configuredDir, "ffmpeg"+exeSuffix()))
	touch(t, filepath.Join(configuredDir, "ffprobe"+exeSuffix()))

	loc := Locator{
		Override:   filepath.Join(overrideDir, "ffmpeg"+exeSuffix()),
		Configured: filepath.Join(configuredDir, "ffmpeg"+exeSuffix()),
		Verify:     fakeVerify(t),
	}
	got, err := loc.Locate(context.Background())
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if got.FFmpeg != filepath.Join(overrideDir, "ffmpeg"+exeSuffix()) {
		t.Fatalf("got %q", got.FFmpeg)
	}
}

func TestLocateOrderAndCache(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	touch(t, filepath.Join(cacheDir, "ffmpeg", "bin", "ffmpeg"+exeSuffix()))
	touch(t, filepath.Join(cacheDir, "ffmpeg", "bin", "ffprobe"+exeSuffix()))

	loc := Locator{
		CacheDir: cacheDir,
		AppDir:   filepath.Join(dir, "app"),
		LookPath: func(string) (string, error) { return "", errors.New("not on path") },
		Verify:   fakeVerify(t),
	}
	got, err := loc.Locate(context.Background())
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if got.FFmpeg != filepath.Join(cacheDir, "ffmpeg", "bin", "ffmpeg"+exeSuffix()) {
		t.Fatalf("got %q", got.FFmpeg)
	}
	if got.FFprobe != filepath.Join(cacheDir, "ffmpeg", "bin", "ffprobe"+exeSuffix()) {
		t.Fatalf("ffprobe = %q", got.FFprobe)
	}
}

func TestLocateFallsBackToPath(t *testing.T) {
	dir := t.TempDir()
	pathDir := filepath.Join(dir, "pathbin")
	touch(t, filepath.Join(pathDir, "ffmpeg"+exeSuffix()))
	touch(t, filepath.Join(pathDir, "ffprobe"+exeSuffix()))

	loc := Locator{
		LookPath: func(name string) (string, error) { return filepath.Join(pathDir, name+exeSuffix()), nil },
		Verify:   fakeVerify(t),
	}
	got, err := loc.Locate(context.Background())
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if filepath.Dir(got.FFmpeg) != pathDir {
		t.Fatalf("got %q", got.FFmpeg)
	}
}

func TestLocateNotFound(t *testing.T) {
	loc := Locator{
		LookPath: func(string) (string, error) { return "", errors.New("no") },
		Verify:   func(context.Context, string) error { return errors.New("nope") },
	}
	_, err := loc.Locate(context.Background())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: 테스트 실패 확인**

```powershell
go test ./internal/ffmpeg
```
기대: 컴파일 실패.

- [ ] **Step 3: 구현**

`internal/ffmpeg/tools.go`:
```go
package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ErrNotFound is returned when no usable FFmpeg installation is available.
var ErrNotFound = errors.New("FFmpeg를 찾을 수 없습니다")

// Tools are the absolute paths of the ffmpeg and ffprobe executables.
type Tools struct {
	FFmpeg  string
	FFprobe string
}

// Locator searches for FFmpeg in a deterministic order.
type Locator struct {
	Override   string // --ffmpeg flag
	Configured string // settings.json ffmpegPath
	AppDir     string // directory of the running executable
	CacheDir   string // <UserCacheDir>/VideoCompress
	LookPath   func(string) (string, error)
	Verify     func(ctx context.Context, ffmpegPath string) error
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func (l Locator) verify() func(context.Context, string) error {
	if l.Verify != nil {
		return l.Verify
	}
	return DefaultVerify
}

// Candidates returns the search order, dropping empty entries.
func (l Locator) Candidates() []string {
	var out []string
	add := func(p string) {
		if strings.TrimSpace(p) == "" {
			return
		}
		for _, existing := range out {
			if existing == p {
				return
			}
		}
		out = append(out, p)
	}
	add(l.Override)
	add(l.Configured)
	if l.AppDir != "" {
		add(filepath.Join(l.AppDir, "bin", "ffmpeg"+exeSuffix()))
	}
	if l.CacheDir != "" {
		add(filepath.Join(l.CacheDir, "ffmpeg", "bin", "ffmpeg"+exeSuffix()))
	}
	if l.LookPath != nil {
		if p, err := l.LookPath("ffmpeg"); err == nil {
			add(p)
		}
	}
	return out
}

// Locate returns the first candidate that passes verification.
func (l Locator) Locate(ctx context.Context) (Tools, error) {
	verify := l.verify()
	var errs []string
	for _, candidate := range l.Candidates() {
		if err := verify(ctx, candidate); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", candidate, err))
			continue
		}
		return Tools{FFmpeg: candidate, FFprobe: siblingFFprobe(candidate)}, nil
	}
	if len(errs) == 0 {
		return Tools{}, ErrNotFound
	}
	return Tools{}, fmt.Errorf("%w (%s)", ErrNotFound, strings.Join(errs, "; "))
}

func siblingFFprobe(ffmpegPath string) string {
	return filepath.Join(filepath.Dir(ffmpegPath), "ffprobe"+exeSuffix())
}

// DefaultVerify runs `ffmpeg -version` and checks ffprobe exists.
func DefaultVerify(ctx context.Context, ffmpegPath string) error {
	probe := siblingFFprobe(ffmpegPath)
	if err := runVersion(ctx, probe); err != nil {
		return fmt.Errorf("ffprobe: %w", err)
	}
	if err := runVersion(ctx, ffmpegPath); err != nil {
		return err
	}
	return nil
}

func runVersion(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "-version")
	cmd.SysProcAttr = hiddenProcAttr()
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	if !strings.Contains(string(out), "version") {
		return fmt.Errorf("예상한 버전 출력이 아닙니다: %s", path)
	}
	return nil
}
```

`internal/ffmpeg/proc_windows.go`:
```go
//go:build windows

package ffmpeg

import "syscall"

// hiddenProcAttr avoids flashing console windows when the GUI spawns FFmpeg.
func hiddenProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
```

`internal/ffmpeg/proc_other.go`:
```go
//go:build !windows

package ffmpeg

import "syscall"

func hiddenProcAttr() *syscall.SysProcAttr { return nil }
```

- [ ] **Step 4: 테스트 통과 확인**

```powershell
go test ./internal/ffmpeg
go vet ./...
```

- [ ] **Step 5: 커밋**

```powershell
git add internal/ffmpeg
git commit -m "feat(ffmpeg): binary locator with injectable verification"
```

---

## Task 6: internal/ffmpeg — FFmpeg 자동 다운로드와 ZIP 추출

**Files:**
- Create: `internal/ffmpeg/download.go`
- Test: `internal/ffmpeg/download_test.go`

**Interfaces:**
- Produces: `ffmpeg.DefaultDownloadURL` 상수, `ffmpeg.Downloader{URL string; Client *http.Client; Verify func(ctx, string) error; MaxBytes int64}`, `(Downloader).Download(ctx, destBinDir string, progress func(done, total int64)) (Tools, error)`, `ffmpeg.ExtractTools(zipPath, destDir string) (Tools, error)`

- [ ] **Step 1: 실패하는 테스트 작성**

`internal/ffmpeg/download_test.go`:
```go
package ffmpeg

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func makeZip(t *testing.T, entries map[string][]byte) string {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, data := range entries {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "in.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractToolsFindsNestedBinaries(t *testing.T) {
	zipPath := makeZip(t, map[string][]byte{
		"ffmpeg-8.0-essentials_build/bin/ffmpeg.exe":  []byte("ff"),
		"ffmpeg-8.0-essentials_build/bin/ffprobe.exe": []byte("fp"),
		"ffmpeg-8.0-essentials_build/README.txt":      []byte("readme"),
	})
	dest := t.TempDir()
	tools, err := ExtractTools(zipPath, dest)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if tools.FFmpeg != filepath.Join(dest, "ffmpeg.exe") {
		t.Fatalf("ffmpeg path = %q", tools.FFmpeg)
	}
	if tools.FFprobe != filepath.Join(dest, "ffprobe.exe") {
		t.Fatalf("ffprobe path = %q", tools.FFprobe)
	}
}

func TestExtractToolsIgnoresPathTraversal(t *testing.T) {
	zipPath := makeZip(t, map[string][]byte{
		"../../evil.exe": []byte("evil"),
		"bin/ffmpeg.exe": []byte("ff"),
		"bin/ffprobe.exe": []byte("fp"),
	})
	dest := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractTools(zipPath, dest); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "evil.exe")); err == nil {
		t.Fatal("evil.exe should not have been extracted")
	}
}

func TestExtractToolsMissingBinary(t *testing.T) {
	zipPath := makeZip(t, map[string][]byte{"bin/ffmpeg.exe": []byte("ff")})
	if _, err := ExtractTools(zipPath, t.TempDir()); err == nil {
		t.Fatal("want error when ffprobe is missing")
	}
}

func TestDownloadInstallsAndVerifies(t *testing.T) {
	zipPath := makeZip(t, map[string][]byte{
		"ffmpeg-8.0-essentials_build/bin/ffmpeg.exe":  []byte("ff"),
		"ffmpeg-8.0-essentials_build/bin/ffprobe.exe": []byte("fp"),
	})
	data, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	var lastDone, lastTotal int64
	dest := filepath.Join(t.TempDir(), "ffmpeg", "bin")
	d := Downloader{
		URL:    srv.URL,
		Client: srv.Client(),
		Verify: func(_ context.Context, ffmpegPath string) error {
			if _, err := os.Stat(ffmpegPath); err != nil {
				return err
			}
			return nil
		},
	}
	tools, err := d.Download(context.Background(), dest, func(done, total int64) {
		lastDone, lastTotal = done, total
	})
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if lastDone != int64(len(data)) || lastTotal != int64(len(data)) {
		t.Fatalf("progress = %d/%d", lastDone, lastTotal)
	}
	for _, p := range []string{tools.FFmpeg, tools.FFprobe} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing %s: %v", p, err)
		}
	}
}

func TestDownloadRejectsTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 1024))
	}))
	defer srv.Close()
	d := Downloader{URL: srv.URL, Client: srv.Client(), MaxBytes: 100}
	if _, err := d.Download(context.Background(), filepath.Join(t.TempDir(), "bin"), nil); err == nil {
		t.Fatal("want size limit error")
	}
}

func TestDownloadRejectsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	d := Downloader{URL: srv.URL, Client: srv.Client()}
	if _, err := d.Download(context.Background(), filepath.Join(t.TempDir(), "bin"), nil); err == nil {
		t.Fatal("want HTTP error")
	}
}
```

- [ ] **Step 2: 테스트 실패 확인**

```powershell
go test ./internal/ffmpeg
```
기대: `undefined: ExtractTools` 등.

- [ ] **Step 3: 구현**

`internal/ffmpeg/download.go`:
```go
package ffmpeg

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// DefaultDownloadURL points at the gyan.dev essentials build (~115MB).
	DefaultDownloadURL = "https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip"
	DefaultMaxBytes    = int64(400 << 20)
	maxExtractBytes    = int64(600 << 20)
)

// Downloader fetches and installs FFmpeg binaries.
type Downloader struct {
	URL      string
	Client   *http.Client
	Verify   func(ctx context.Context, ffmpegPath string) error
	MaxBytes int64
}

// Download fetches the ZIP, extracts ffmpeg/ffprobe into destBinDir, and
// verifies the result. Existing files are only replaced after a successful
// download because extraction happens in a temp directory first.
func (d Downloader) Download(ctx context.Context, destBinDir string, progress func(done, total int64)) (Tools, error) {
	url := d.URL
	if url == "" {
		url = DefaultDownloadURL
	}
	maxBytes := d.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Tools{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return Tools{}, fmt.Errorf("FFmpeg 다운로드 실패: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Tools{}, fmt.Errorf("FFmpeg 다운로드 실패: HTTP %d", resp.StatusCode)
	}

	parent := filepath.Dir(destBinDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return Tools{}, err
	}
	tmpDir, err := os.MkdirTemp(parent, "ffmpeg-dl-")
	if err != nil {
		return Tools{}, err
	}
	defer os.RemoveAll(tmpDir)

	zipPath := filepath.Join(tmpDir, "ffmpeg.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		return Tools{}, err
	}
	total := resp.ContentLength
	var done int64
	buf := make([]byte, 256*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			done += int64(n)
			if done > maxBytes {
				f.Close()
				return Tools{}, fmt.Errorf("다운로드 용량이 제한(%dMB)을 초과했습니다", maxBytes>>20)
			}
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return Tools{}, werr
			}
			if progress != nil {
				progress(done, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			f.Close()
			return Tools{}, fmt.Errorf("FFmpeg 다운로드 실패: %w", readErr)
		}
	}
	if err := f.Close(); err != nil {
		return Tools{}, err
	}

	extractDir := filepath.Join(tmpDir, "bin")
	tools, err := ExtractTools(zipPath, extractDir)
	if err != nil {
		return Tools{}, err
	}

	verify := d.Verify
	if verify == nil {
		verify = DefaultVerify
	}
	if err := verify(ctx, tools.FFmpeg); err != nil {
		return Tools{}, fmt.Errorf("다운로드한 FFmpeg 검증 실패: %w", err)
	}

	if err := os.MkdirAll(destBinDir, 0o755); err != nil {
		return Tools{}, err
	}
	final := Tools{
		FFmpeg:  filepath.Join(destBinDir, filepath.Base(tools.FFmpeg)),
		FFprobe: filepath.Join(destBinDir, filepath.Base(tools.FFprobe)),
	}
	for _, pair := range [][2]string{{tools.FFmpeg, final.FFmpeg}, {tools.FFprobe, final.FFprobe}} {
		if _, err := os.Stat(pair[1]); err == nil {
			continue // never clobber a working install
		}
		if err := os.Rename(pair[0], pair[1]); err != nil {
			return Tools{}, err
		}
	}
	return final, nil
}

// ExtractTools copies the first ffmpeg/ffprobe entries (matched by base name)
// from the ZIP into destDir. Other entries, including absolute or traversal
// paths, are ignored.
func ExtractTools(zipPath, destDir string) (Tools, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return Tools{}, err
	}
	defer r.Close()
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return Tools{}, err
	}
	var found Tools
	var total int64
	for _, f := range r.File {
		base := strings.ToLower(filepath.Base(f.Name))
		if (base != "ffmpeg.exe" && base != "ffprobe.exe") || f.FileInfo().IsDir() {
			continue
		}
		target := filepath.Join(destDir, base)
		if err := copyZipEntry(f, target, &total); err != nil {
			return Tools{}, err
		}
		if base == "ffmpeg.exe" {
			found.FFmpeg = target
		} else {
			found.FFprobe = target
		}
	}
	if found.FFmpeg == "" || found.FFprobe == "" {
		return Tools{}, errors.New("ZIP에서 ffmpeg.exe/ffprobe.exe를 찾지 못했습니다")
	}
	return found, nil
}

func copyZipEntry(f *zip.File, target string, total *int64) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	w, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer w.Close()
	n, err := io.Copy(w, io.LimitReader(rc, maxExtractBytes-*total+1))
	if err != nil {
		return err
	}
	*total += n
	if *total > maxExtractBytes {
		return errors.New("압축 해제 용량 제한을 초과했습니다")
	}
	return nil
}
```

- [ ] **Step 4: 테스트 통과 확인**

```powershell
go test ./internal/ffmpeg
go vet ./...
```

- [ ] **Step 5: 실제 FFmpeg를 캐시에 설치(개발용, 1회)**

```powershell
$env:VC_INSTALL_FFMPEG="1"
go test ./internal/ffmpeg -run TestInstallRealFFmpeg -v -timeout 30m
```
이 단계에서 아래 테스트를 `download_real_test.go`로 추가한다:
```go
package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestInstallRealFFmpeg downloads FFmpeg into the app cache. It is opt-in via
// VC_INSTALL_FFMPEG to avoid 115MB downloads during normal test runs.
func TestInstallRealFFmpeg(t *testing.T) {
	if os.Getenv("VC_INSTALL_FFMPEG") != "1" {
		t.Skip("set VC_INSTALL_FFMPEG=1 to run")
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(cache, "VideoCompress", "ffmpeg", "bin")
	d := Downloader{}
	tools, err := d.Download(context.Background(), dest, func(done, total int64) {
		if done%(20<<20) < 256*1024 {
			t.Logf("downloaded %d/%d MB", done>>20, total>>20)
		}
	})
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	t.Logf("installed: %s", tools.FFmpeg)
}
```

- [ ] **Step 6: 커밋**

```powershell
git add internal/ffmpeg
git commit -m "feat(ffmpeg): auto-download with safe zip extraction"
```

---

## Task 7: internal/ffmpeg — 실행 클라이언트와 진행률 파싱

**Files:**
- Create: `internal/ffmpeg/client.go`
- Test: `internal/ffmpeg/client_test.go`

**Interfaces:**
- Produces: `ffmpeg.Client{Tools Tools; Logf func(string, ...any)}`, `(Client).Probe(ctx, path) (*media.Info, error)`, `(Client).Run(ctx, args []string, totalSec float64, onProgress func(percent float64)) error`, `ffmpeg.Progress{Seconds, Speed float64}`, `ffmpeg.ParseProgressLine(line string, p *Progress) (done bool)`

- [ ] **Step 1: 실패하는 테스트 작성**

`internal/ffmpeg/client_test.go`:
```go
package ffmpeg

import (
	"context"
	"testing"
)

func TestParseProgressLine(t *testing.T) {
	var p Progress
	if done := ParseProgressLine("out_time_us=1234567", &p); done {
		t.Fatal("not done yet")
	}
	if p.Seconds < 1.23 || p.Seconds > 1.24 {
		t.Fatalf("seconds = %v", p.Seconds)
	}
	ParseProgressLine("speed=2.5x", &p)
	if p.Speed != 2.5 {
		t.Fatalf("speed = %v", p.Speed)
	}
	if done := ParseProgressLine("progress=end", &p); !done {
		t.Fatal("want done")
	}
	// out_time_ms is microseconds in FFmpeg's progress output.
	var q Progress
	ParseProgressLine("out_time_ms=2500000", &q)
	if q.Seconds != 2.5 {
		t.Fatalf("seconds = %v", q.Seconds)
	}
	// N/A values are ignored.
	var r Progress
	ParseProgressLine("out_time_us=N/A", &r)
	ParseProgressLine("speed=N/A", &r)
	if r.Seconds != 0 || r.Speed != 0 {
		t.Fatalf("got %+v", r)
	}
}

func TestClientRunUnknownBinaryFails(t *testing.T) {
	c := &Client{Tools: Tools{FFmpeg: "definitely-not-ffmpeg"}}
	err := c.Run(context.Background(), []string{"-version"}, 0, nil)
	if err == nil {
		t.Fatal("want error")
	}
}
```

- [ ] **Step 2: 테스트 실패 확인**

```powershell
go test ./internal/ffmpeg -run TestParseProgressLine
```

- [ ] **Step 3: 구현**

`internal/ffmpeg/client.go`:
```go
package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"videocompress/internal/media"
)

// Progress holds the parsed values of one FFmpeg -progress block.
type Progress struct {
	Seconds float64
	Speed   float64
}

// Client executes ffmpeg/ffprobe.
type Client struct {
	Tools Tools
	Logf  func(format string, args ...any)
}

// Probe runs ffprobe and parses the result. File size comes from os.Stat so it
// is always the real on-disk size.
func (c *Client) Probe(ctx context.Context, path string) (*media.Info, error) {
	if c.Tools.FFprobe == "" {
		return nil, ErrNotFound
	}
	cmd := exec.CommandContext(ctx, c.Tools.FFprobe,
		"-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
	cmd.SysProcAttr = hiddenProcAttr()
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe 실패: %w", err)
	}
	size := int64(0)
	if st, statErr := os.Stat(path); statErr == nil {
		size = st.Size()
	}
	return media.ParseProbeJSON(out, path, size)
}

// Run executes ffmpeg with progress reporting. The args must not include
// -hide_banner/-progress/-nostats; they are added here.
func (c *Client) Run(ctx context.Context, args []string, totalSec float64, onProgress func(percent float64)) error {
	if c.Tools.FFmpeg == "" {
		return ErrNotFound
	}
	full := append([]string{
		"-hide_banner", "-nostdin", "-loglevel", "error",
		"-progress", "pipe:1", "-nostats",
	}, args...)
	cmd := exec.CommandContext(ctx, c.Tools.FFmpeg, full...)
	cmd.SysProcAttr = hiddenProcAttr()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := &tailWriter{max: 32 * 1024}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var p Progress
	for scanner.Scan() {
		done := ParseProgressLine(scanner.Text(), &p)
		if onProgress != nil && totalSec > 0 && p.Seconds > 0 {
			pct := p.Seconds / totalSec * 100
			if pct < 0 {
				pct = 0
			}
			if pct > 99.9 && !done {
				pct = 99.9
			}
			onProgress(pct)
		}
	}
	waitErr := cmd.Wait()
	if waitErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg 실패: %w\n%s", waitErr, stderr.String())
	}
	if onProgress != nil {
		onProgress(100)
	}
	return nil
}

// ParseProgressLine updates p from one key=value line and reports whether the
// block ended (progress=end).
func ParseProgressLine(line string, p *Progress) bool {
	line = strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "out_time_us="):
		if v, err := strconv.ParseInt(strings.TrimPrefix(line, "out_time_us="), 10, 64); err == nil {
			p.Seconds = float64(v) / 1e6
		}
	case strings.HasPrefix(line, "out_time_ms="):
		// Historially this value is microseconds despite the name.
		if v, err := strconv.ParseInt(strings.TrimPrefix(line, "out_time_ms="), 10, 64); err == nil {
			p.Seconds = float64(v) / 1e6
		}
	case strings.HasPrefix(line, "out_time="):
		if sec, ok := parseClock(strings.TrimPrefix(line, "out_time=")); ok {
			p.Seconds = sec
		}
	case strings.HasPrefix(line, "speed="):
		s := strings.TrimSuffix(strings.TrimPrefix(line, "speed="), "x")
		if v, err := strconv.ParseFloat(s, 64); err == nil && v > 0 {
			p.Speed = v
		}
	case line == "progress=end":
		return true
	}
	return false
}

func parseClock(s string) (float64, bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, false
	}
	h, err1 := strconv.ParseFloat(parts[0], 64)
	m, err2 := strconv.ParseFloat(parts[1], 64)
	sec, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, false
	}
	return h*3600 + m*60 + sec, true
}

// tailWriter keeps only the last max bytes written to it.
type tailWriter struct {
	max int
	buf bytes.Buffer
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	if w.buf.Len() > w.max {
		b := w.buf.Bytes()
		rest := b[w.buf.Len()-w.max:]
		kept := make([]byte, len(rest))
		copy(kept, rest)
		w.buf.Reset()
		w.buf.Write(kept)
	}
	return len(p), nil
}

func (w *tailWriter) String() string { return w.buf.String() }
```

- [ ] **Step 4: 테스트 통과 확인**

```powershell
go test ./internal/ffmpeg
go vet ./...
```

- [ ] **Step 5: 실제 FFmpeg로 프로브 스모크 테스트(선택)**

캐시에 FFmpeg가 있으면:
```powershell
$ff = "$env:LOCALAPPDATA\VideoCompress\ffmpeg\bin\ffmpeg.exe"
& $ff -y -f lavfi -i testsrc2=size=320x240:rate=10 -t 1 "$env:TEMP\vc-smoke.mp4"
```
기대: 파일 생성.

- [ ] **Step 6: 커밋**

```powershell
git add internal/ffmpeg
git commit -m "feat(ffmpeg): exec client with progress parsing"
```

---

## Task 8: internal/engine — 출력 이름, 원자적 이동, 검증

**Files:**
- Create: `internal/engine/names.go`, `internal/engine/verify.go`
- Modify: `internal/media/info.go` (StartTime 필드 추가)
- Modify: `internal/media/info_test.go`
- Test: `internal/engine/names_test.go`, `internal/engine/verify_test.go`

**Interfaces:**
- Produces:
  - `engine.SingleOutputPath(inputPath, outDir string) (string, error)`
  - `engine.SplitOutputPaths(inputPath, outDir string, count int) ([]string, error)`
  - `engine.MoveFileNoReplace(src, dst string) error`
  - `engine.VerifySingle(src *media.Info, out *media.Info, targetBytes int64) error`
  - `engine.VerifyParts(src *media.Info, parts []*media.Info, partBytes int64) error`
  - `engine.DurationTolerance(duration float64, parts int) float64`
  - 미디어 확장: `media.Info.StartTime float64`

- [ ] **Step 1: media.Info에 StartTime 추가(실패 테스트 먼저)**

`internal/media/info_test.go`에 추가:
```go
func TestParseProbeStartTime(t *testing.T) {
	data := `{"streams":[{"codec_type":"video","codec_name":"h264","width":10,"height":10}],
	          "format":{"duration":"5.0","start_time":"0.024"}}`
	info, err := ParseProbeJSON([]byte(data), "a.mp4", 1)
	if err != nil {
		t.Fatal(err)
	}
	if info.StartTime != 0.024 {
		t.Fatalf("start time = %v", info.StartTime)
	}
}
```

- [ ] **Step 2: 구현 수정**

`internal/media/info.go`: `Info` 구조체에 `StartTime float64` 추가, `probeFormat`에 `StartTime string \`json:"start_time"\`` 추가, 파싱 시 `info.StartTime = parseDuration(raw.Format.StartTime)`.

- [ ] **Step 3: names/verify 실패 테스트 작성**

`internal/engine/names_test.go`:
```go
package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSingleOutputPathNoCollision(t *testing.T) {
	dir := t.TempDir()
	got, err := SingleOutputPath(filepath.Join(dir, "movie.mp4"), "")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "movie_compressed.mp4")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSingleOutputPathCollisions(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "movie_compressed.mp4"))
	got, _ := SingleOutputPath(filepath.Join(dir, "movie.mp4"), "")
	if filepath.Base(got) != "movie_compressed (2).mp4" {
		t.Fatalf("got %q", got)
	}
	write(t, got)
	got, _ = SingleOutputPath(filepath.Join(dir, "movie.mp4"), "")
	if filepath.Base(got) != "movie_compressed (3).mp4" {
		t.Fatalf("got %q", got)
	}
}

func TestSingleOutputPathUsesOutDir(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	got, err := SingleOutputPath(filepath.Join(dir, "movie.mp4"), out)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(out, "movie_compressed.mp4") {
		t.Fatalf("got %q", got)
	}
}

func TestSplitOutputPaths(t *testing.T) {
	dir := t.TempDir()
	got, err := SplitOutputPaths(filepath.Join(dir, "movie.mp4"), "", 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(dir, "movie_part01.mp4"),
		filepath.Join(dir, "movie_part02.mp4"),
		filepath.Join(dir, "movie_part03.mp4"),
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("part %d = %q want %q", i, got[i], want[i])
		}
	}
}

func TestSplitOutputPathsGroupCollision(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "movie_part02.mp4"))
	got, err := SplitOutputPaths(filepath.Join(dir, "movie.mp4"), "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got[0]) != "movie (2)_part01.mp4" {
		t.Fatalf("got %q", got[0])
	}
}

func TestSplitOutputPathsWideCount(t *testing.T) {
	dir := t.TempDir()
	got, err := SplitOutputPaths(filepath.Join(dir, "movie.mp4"), "", 120)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got[0]) != "movie_part001.mp4" || filepath.Base(got[119]) != "movie_part120.mp4" {
		t.Fatalf("first=%q last=%q", filepath.Base(got[0]), filepath.Base(got[119]))
	}
}

func TestMoveFileNoReplace(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")
	write(t, src)
	if err := MoveFileNoReplace(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); err == nil {
		t.Fatal("source should be gone")
	}
	write(t, src)
	if err := MoveFileNoReplace(src, dst); err == nil {
		t.Fatal("want error when destination exists")
	}
}
```

`internal/engine/verify_test.go`:
```go
package engine

import (
	"testing"

	"videocompress/internal/media"
)

func TestVerifySingle(t *testing.T) {
	src := &media.Info{Duration: 20, Width: 1280, Height: 720}
	ok := &media.Info{SizeBytes: 4_900_000, Duration: 20.1, Width: 1280, Height: 720}
	if err := VerifySingle(src, ok, 5_000_000); err != nil {
		t.Fatalf("want ok: %v", err)
	}
	tooBig := &media.Info{SizeBytes: 5_100_000, Duration: 20, Width: 1280, Height: 720}
	if err := VerifySingle(src, tooBig, 5_000_000); err == nil {
		t.Fatal("want size error")
	}
	wrongDur := &media.Info{SizeBytes: 100, Duration: 40, Width: 1280, Height: 720}
	if err := VerifySingle(src, wrongDur, 5_000_000); err == nil {
		t.Fatal("want duration error")
	}
	noVideo := &media.Info{SizeBytes: 100, Duration: 20}
	if err := VerifySingle(src, noVideo, 5_000_000); err == nil {
		t.Fatal("want video error")
	}
}

func TestVerifyParts(t *testing.T) {
	// Source duration must equal the sum of the parts for the happy path.
	src := &media.Info{Duration: 90, Width: 1920, Height: 1080}
	parts := []*media.Info{
		{SizeBytes: 3_900_000, Duration: 30, Width: 1280, Height: 720, StartTime: 0},
		{SizeBytes: 3_900_000, Duration: 30, Width: 1280, Height: 720, StartTime: 0},
		{SizeBytes: 3_900_000, Duration: 30, Width: 1280, Height: 720, StartTime: 0},
	}
	if err := VerifyParts(src, parts, 4_000_000); err != nil {
		t.Fatalf("want ok: %v", err)
	}
	big := append([]*media.Info{}, parts...)
	big[1] = &media.Info{SizeBytes: 4_100_000, Duration: 30, Width: 1280, Height: 720}
	if err := VerifyParts(src, big, 4_000_000); err == nil {
		t.Fatal("want part size error")
	}
	short := append([]*media.Info{}, parts...)
	short[2] = &media.Info{SizeBytes: 100, Duration: 5, Width: 1280, Height: 720}
	if err := VerifyParts(src, short, 4_000_000); err == nil {
		t.Fatal("want duration sum error")
	}
	noVideo := append([]*media.Info{}, parts...)
	noVideo[0] = &media.Info{SizeBytes: 100, Duration: 30}
	if err := VerifyParts(src, noVideo, 4_000_000); err == nil {
		t.Fatal("want video error")
	}
}

func TestDurationToleranceGrowsWithParts(t *testing.T) {
	if DurationTolerance(6) < 2.0 {
		t.Fatal("tolerance must be at least 2s")
	}
	if DurationTolerance(200) <= DurationTolerance(6) {
		t.Fatal("tolerance must grow with part count")
	}
}
```

- [ ] **Step 4: 테스트 실패 확인**

```powershell
go test ./internal/media ./internal/engine
```

- [ ] **Step 5: 구현**

`internal/engine/names.go`:
```go
package engine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SingleOutputPath returns a non-existing `<base>_compressed.mp4` path,
// adding a ` (2)`, ` (3)` suffix on collision.
func SingleOutputPath(inputPath, outDir string) (string, error) {
	dir := outputDir(inputPath, outDir)
	base := baseName(inputPath)
	for i := 1; i <= 9999; i++ {
		name := base + "_compressed.mp4"
		if i > 1 {
			name = fmt.Sprintf("%s_compressed (%d).mp4", base, i)
		}
		p := filepath.Join(dir, name)
		if !exists(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("출력 파일 이름을 정할 수 없습니다: %s", base)
}

// SplitOutputPaths returns non-existing part paths. If any member of a group
// would collide, the next group number ` (2)`, ` (3)` is used.
func SplitOutputPaths(inputPath, outDir string, count int) ([]string, error) {
	if count < 1 {
		return nil, fmt.Errorf("조각 수가 올바르지 않습니다: %d", count)
	}
	dir := outputDir(inputPath, outDir)
	base := baseName(inputPath)
	digits := 2
	if n := len(strconv.Itoa(count)); n > digits {
		digits = n
	}
	for g := 1; g <= 9999; g++ {
		suffix := ""
		if g > 1 {
			suffix = fmt.Sprintf(" (%d)", g)
		}
		paths := make([]string, 0, count)
		conflict := false
		for i := 1; i <= count; i++ {
			p := filepath.Join(dir, fmt.Sprintf("%s%s_part%0*d.mp4", base, suffix, digits, i))
			if exists(p) {
				conflict = true
				break
			}
			paths = append(paths, p)
		}
		if !conflict {
			return paths, nil
		}
	}
	return nil, fmt.Errorf("출력 파일 이름을 정할 수 없습니다: %s", base)
}

func outputDir(inputPath, outDir string) string {
	if strings.TrimSpace(outDir) != "" {
		return outDir
	}
	return filepath.Dir(inputPath)
}

func baseName(inputPath string) string {
	return strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// MoveFileNoReplace moves src to dst, refusing to overwrite an existing file.
// If rename fails (different volumes), it falls back to copy+remove.
func MoveFileNoReplace(src, dst string) error {
	if exists(dst) {
		return fmt.Errorf("대상 파일이 이미 존재합니다: %s", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyExclusive(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

func copyExclusive(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}
```

`internal/engine/verify.go`:
```go
package engine

import (
	"fmt"
	"math"

	"videocompress/internal/media"
)

// DurationTolerance allows container rounding: at least 2 seconds, growing
// slightly (0.1s) with the number of parts.
func DurationTolerance(parts int) float64 {
	tol := 2.0
	if byParts := float64(parts) * 0.1; byParts > tol {
		tol = byParts
	}
	return tol
}

// VerifySingle checks the size, presence of video, and duration of one output.
func VerifySingle(src, out *media.Info, targetBytes int64) error {
	if out == nil {
		return fmt.Errorf("출력 정보를 확인할 수 없습니다")
	}
	if out.SizeBytes > targetBytes {
		return fmt.Errorf("출력이 목표 크기를 초과했습니다: %d > %d", out.SizeBytes, targetBytes)
	}
	if out.Width <= 0 || out.Height <= 0 {
		return fmt.Errorf("출력에 비디오 스트림이 없습니다")
	}
	tol := DurationTolerance(1)
	if math.Abs(out.Duration-src.Duration) > tol {
		return fmt.Errorf("출력 길이가 원본과 다릅니다: %.1fs vs %.1fs", out.Duration, src.Duration)
	}
	return nil
}

// VerifyParts checks each part size, video presence, and the total duration.
func VerifyParts(src *media.Info, parts []*media.Info, partBytes int64) error {
	if len(parts) == 0 {
		return fmt.Errorf("분할 결과가 없습니다")
	}
	var sum float64
	for i, p := range parts {
		if p == nil {
			return fmt.Errorf("조각 %d 정보를 확인할 수 없습니다", i+1)
		}
		if p.SizeBytes > partBytes {
			return fmt.Errorf("조각 %d가 조각 크기를 초과했습니다: %d > %d", i+1, p.SizeBytes, partBytes)
		}
		if p.Width <= 0 || p.Height <= 0 {
			return fmt.Errorf("조각 %d에 비디오 스트림이 없습니다", i+1)
		}
		if p.Duration <= 0 {
			return fmt.Errorf("조각 %d의 길이를 확인할 수 없습니다", i+1)
		}
		sum += p.Duration
	}
	if len(parts[0].Path) > 0 && parts[0].StartTime > 0.5 {
		return fmt.Errorf("첫 조각이 0초에서 시작하지 않습니다: %.2fs", parts[0].StartTime)
	}
	tol := DurationTolerance(len(parts))
	if math.Abs(sum-src.Duration) > tol {
		return fmt.Errorf("조각 길이 합이 원본과 다릅니다: %.1fs vs %.1fs", sum, src.Duration)
	}
	return nil
}
```

- [ ] **Step 6: 테스트 통과 확인 및 커밋**

```powershell
go test ./internal/media ./internal/engine
go vet ./...
git add internal/media internal/engine
git commit -m "feat(engine): output naming, atomic move, and verification"
```

---

## Task 9: internal/engine — 인자 생성, 진행률 미터, 재시도 계산 (순수 함수)

**Files:**
- Create: `internal/engine/args.go`, `internal/engine/meter.go`
- Test: `internal/engine/args_test.go`, `internal/engine/meter_test.go`

**Interfaces:**
- Produces:
  - `engine.EncodeSpec` 구조체(아래)
  - `engine.SingleArgs(spec EncodeSpec) []string` (Pass 1/2 공용, `spec.SegmentPattern`이 있으면 segment 출력)
  - `engine.RangeArgs(spec RangeSpec) []string` (구간 재인코딩)
  - `engine.RetryKbps(current int, targetBytes, actualBytes int64) int`
  - `engine.RetryProgress` (base/span 기반 단조 진행률)

- [ ] **Step 1: 실패하는 테스트 작성**

`internal/engine/args_test.go`:
```go
package engine

import (
	"strings"
	"testing"
)

func join(args []string) string { return strings.Join(args, " ") }

func TestSingleArgsPass1(t *testing.T) {
	spec := EncodeSpec{
		Input: "in.mp4", PassLog: "log", Pass: 1,
		Width: 1280, Height: 720, Scale: true, VideoKbps: 917,
		AudioMode: AudioNone,
	}
	args := SingleArgs(spec)
	got := join(args)
	for _, want := range []string{"-i in.mp4", "-vf scale=1280:720", "-b:v 917k", "-pass 1", "-passlogfile log", "-an", "-f null -"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "-movflags") {
		t.Fatal("pass 1 must not set movflags")
	}
}

func TestSingleArgsPass2CopyWithFaststart(t *testing.T) {
	spec := EncodeSpec{
		Input: "in.mp4", Output: "out.mp4", PassLog: "log", Pass: 2,
		Width: 1920, Height: 1080, Scale: false, VideoKbps: 1804,
		AudioMode: AudioCopy, FastStart: true,
	}
	args := SingleArgs(spec)
	got := join(args)
	for _, want := range []string{"-map 0:a:0", "-c:a copy", "-movflags +faststart", "-f mp4 out.mp4"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "-vf") {
		t.Fatal("must not scale when Scale=false")
	}
	if strings.Contains(got, "-b:a") {
		t.Fatal("copy must not set audio bitrate")
	}
}

func TestSingleArgsPass2EncodeAudio(t *testing.T) {
	spec := EncodeSpec{
		Input: "in.mp4", Output: "out.mp4", PassLog: "log", Pass: 2,
		Width: 854, Height: 480, VideoKbps: 410,
		AudioMode: AudioAAC, AudioKbps: 64, AudioChannels: 1, FastStart: true,
	}
	got := join(SingleArgs(spec))
	for _, want := range []string{"-c:a aac", "-b:a 64k", "-ac 1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
}

func TestSingleArgsNoAudio(t *testing.T) {
	spec := EncodeSpec{Input: "in.mp4", Output: "out.mp4", Pass: 2, VideoKbps: 500, Width: 640, Height: 360}
	got := join(SingleArgs(spec))
	if !strings.Contains(got, "-an") {
		t.Fatalf("missing -an in %s", got)
	}
}

func TestSingleArgsSegmentOutput(t *testing.T) {
	spec := EncodeSpec{
		Input: "in.mp4", PassLog: "log", Pass: 2, VideoKbps: 917,
		Width: 1280, Height: 720, Scale: true,
		AudioMode: AudioAAC, AudioKbps: 96, AudioChannels: 2,
		ForceKeySec: 30, SegmentSeconds: 30, SegmentPattern: "seg_%03d.mp4",
	}
	got := join(SingleArgs(spec))
	for _, want := range []string{
		"-force_key_frames expr:gte(t,n_forced*30)", "-sc_threshold 0",
		"-f segment", "-segment_time 30", "-segment_format mp4",
		"-reset_timestamps 1", "seg_%03d.mp4",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "-movflags") {
		t.Fatal("segment output must not set movflags")
	}
}

// kbpsWant recomputes the retry bitrate without constant folding so the
// expectation stays a real (non-constant) float expression.
func kbpsWant(current int, targetBytes, actualBytes int64) int {
	return int(float64(current) * float64(targetBytes) / float64(actualBytes) * 0.95)
}

func TestRetryKbps(t *testing.T) {
	// 917 kbps produced 4.4MB for a 4.0MB target.
	got := RetryKbps(917, 4_000_000, 4_400_000)
	if got >= 917 || got < 100 {
		t.Fatalf("got %d", got)
	}
	want := kbpsWant(917, 4_000_000, 4_400_000)
	if got != want {
		t.Fatalf("got %d want %d", got, want)
	}
	if RetryKbps(100, 4_000_000, 40_000_000) != 100 {
		t.Fatal("must clamp at 100")
	}
}
```

`internal/engine/meter_test.go`:
```go
package engine

import (
	"math"
	"testing"
)

func TestRetryProgressIsMonotonic(t *testing.T) {
	var got []float64
	rp := newRetryProgress(func(_ string, pct float64) { got = append(got, pct) })
	rp.within("compressing", 0.0)
	rp.within("compressing", 0.5)
	rp.within("compressing", 1.0)
	rp.fail()
	rp.within("compressing", 0.0)
	rp.within("compressing", 1.0)
	rp.succeed()
	rp.finish("saving")
	for i := 1; i < len(got); i++ {
		if got[i]+0.001 < got[i-1] {
			t.Fatalf("progress went backwards: %v", got)
		}
	}
	if got[len(got)-1] != 100 {
		t.Fatalf("final = %v", got[len(got)-1])
	}
	if math.Abs(got[0]-0) > 0.001 {
		t.Fatalf("first = %v", got[0])
	}
}
```

- [ ] **Step 2: 테스트 실패 확인**

```powershell
go test ./internal/engine -run 'TestSingleArgs|TestRetry'
```

- [ ] **Step 3: 구현**

`internal/engine/args.go`:
```go
package engine

import (
	"fmt"
)

// Audio modes for EncodeSpec.
const (
	AudioNone = "none"
	AudioCopy = "copy"
	AudioAAC  = "aac"
)

// EncodeSpec describes one ffmpeg invocation for single-file or segment output.
type EncodeSpec struct {
	Input          string
	Output         string
	PassLog        string
	Pass           int
	Width          int
	Height         int
	Scale          bool
	VideoKbps      int
	AudioMode      string
	AudioKbps      int
	AudioChannels  int
	FastStart      bool
	ForceKeySec    int
	SegmentSeconds int
	SegmentPattern string
}

// SingleArgs builds ffmpeg arguments. Pass 1 writes to the null muxer; pass 2
// writes a single MP4 or a segment pattern.
func SingleArgs(spec EncodeSpec) []string {
	args := []string{"-y", "-i", spec.Input, "-map", "0:v:0"}
	if spec.Scale && spec.Width > 0 && spec.Height > 0 {
		args = append(args, "-vf", fmt.Sprintf("scale=%d:%d", spec.Width, spec.Height))
	}
	args = append(args,
		"-c:v", "libx264",
		"-preset", "medium",
		"-profile:v", "high",
		"-pix_fmt", "yuv420p",
		"-b:v", fmt.Sprintf("%dk", spec.VideoKbps),
		"-pass", fmt.Sprintf("%d", spec.Pass),
		"-passlogfile", spec.PassLog,
	)
	if spec.Pass == 1 {
		args = append(args, "-an", "-sn", "-dn", "-f", "null", "-")
		return args
	}
	args = appendAudioArgs(args, spec)
	if spec.ForceKeySec > 0 {
		args = append(args,
			"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", spec.ForceKeySec),
			"-sc_threshold", "0",
		)
	}
	if spec.SegmentPattern != "" {
		args = append(args,
			"-f", "segment",
			"-segment_time", fmt.Sprintf("%d", spec.SegmentSeconds),
			"-segment_format", "mp4",
			"-reset_timestamps", "1",
			"-segment_start_number", "1",
			spec.SegmentPattern,
		)
		return args
	}
	if spec.FastStart {
		args = append(args, "-movflags", "+faststart")
	}
	args = append(args, "-f", "mp4", spec.Output)
	return args
}

func appendAudioArgs(args []string, spec EncodeSpec) []string {
	switch spec.AudioMode {
	case AudioCopy:
		args = append(args, "-map", "0:a:0", "-c:a", "copy")
	case AudioAAC:
		ch := spec.AudioChannels
		if ch < 1 {
			ch = 2
		}
		if ch > 2 {
			ch = 2
		}
		args = append(args,
			"-map", "0:a:0",
			"-c:a", "aac",
			"-b:a", fmt.Sprintf("%dk", spec.AudioKbps),
			"-ac", fmt.Sprintf("%d", ch),
		)
	default:
		args = append(args, "-an")
	}
	return args
}

// RangeSpec describes re-encoding a time range of the source.
type RangeSpec struct {
	Input         string
	Output        string
	Start         float64
	Duration      float64
	Width         int
	Height        int
	Scale         bool
	VideoKbps     int
	AudioKbps     int
	AudioChannels int
	HasAudio      bool
}

// RangeArgs builds arguments that re-encode [Start, Start+Duration) from the
// source with a single pass and faststart.
func RangeArgs(spec RangeSpec) []string {
	args := []string{"-y"}
	if spec.Start > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", spec.Start))
	}
	args = append(args, "-t", fmt.Sprintf("%.3f", spec.Duration), "-i", spec.Input, "-map", "0:v:0")
	if spec.Scale {
		args = append(args, "-vf", fmt.Sprintf("scale=%d:%d", spec.Width, spec.Height))
	}
	args = append(args,
		"-c:v", "libx264",
		"-preset", "medium",
		"-profile:v", "high",
		"-pix_fmt", "yuv420p",
		"-b:v", fmt.Sprintf("%dk", spec.VideoKbps),
	)
	if spec.HasAudio {
		ch := spec.AudioChannels
		if ch < 1 {
			ch = 2
		}
		if ch > 2 {
			ch = 2
		}
		args = append(args, "-map", "0:a:0", "-c:a", "aac", "-b:a", fmt.Sprintf("%dk", spec.AudioKbps), "-ac", fmt.Sprintf("%d", ch))
	} else {
		args = append(args, "-an")
	}
	args = append(args, "-sn", "-dn", "-movflags", "+faststart", "-f", "mp4", spec.Output)
	return args
}

// RetryKbps scales the video bitrate by the size overshoot with 5% headroom.
func RetryKbps(current int, targetBytes, actualBytes int64) int {
	if actualBytes <= 0 {
		return current
	}
	next := int(float64(current) * float64(targetBytes) / float64(actualBytes) * 0.95)
	if next < 100 {
		next = 100
	}
	if next >= current {
		next = current - 1
	}
	if next < 100 {
		next = 100
	}
	return next
}
```

`internal/engine/meter.go`:
```go
package engine

// RetryProgress reports monotonic progress across retries: each failed attempt
// consumes half of the remaining span, so the bar slows down but never resets.
type RetryProgress struct {
	report func(stage string, pct float64)
	base   float64
	span   float64
	last   float64
}

func newRetryProgress(report func(stage string, pct float64)) *RetryProgress {
	return &RetryProgress{report: report, span: 90}
}

func (r *RetryProgress) within(stage string, frac float64) {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	r.emit(stage, r.base+frac*r.span)
}

// fail keeps the bar where it is and halves the remaining span.
func (r *RetryProgress) fail() {
	r.base += r.span / 2
	r.span /= 2
}

// succeed marks the encode phase complete so verification/saving fill the
// remaining 10%.
func (r *RetryProgress) succeed() {
	r.base = 90
	r.span = 0
	r.emit("verifying", 90)
}

func (r *RetryProgress) finish(stage string) {
	r.emit(stage, 100)
}

func (r *RetryProgress) emit(stage string, pct float64) {
	if r.report == nil {
		return
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	if pct < r.last {
		pct = r.last // a retry must never move the bar backwards
	}
	r.last = pct
	r.report(stage, pct)
}
```

- [ ] **Step 4: 테스트 통과 확인 및 커밋**

```powershell
go test ./internal/engine
go vet ./...
git add internal/engine
git commit -m "feat(engine): ffmpeg arg builders, retry math, progress meter"
```

---

## Task 10: internal/engine — 프로세스 오케스트레이션과 단일 압축

**Files:**
- Create: `internal/engine/engine.go`, `internal/engine/single.go`
- Test: `internal/engine/engine_test.go`, `internal/engine/integration_test.go`

**Interfaces:**
- Consumes: `media.PlanFor/SplitPlanFor/Mode*`, `SingleArgs/EncodeSpec`, `RetryProgress`, `VerifySingle`, `SingleOutputPath`, `MoveFileNoReplace`
- Produces: `engine.FF` 인터페이스, `engine.Progress{Stage string; Percent float64}`, `engine.Request`, `engine.Output`, `engine.Result`, `engine.Engine`, `engine.New(ff FF, tempRoot string) *Engine`, `(Engine).Process(ctx, Request, func(Progress)) (*Result, error)`, 내부 `newProgressReporter`, `(progressReporter).scaled/emit`

- [ ] **Step 1: 실패하는 단위 테스트 작성**

`internal/engine/engine_test.go`:
```go
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
```

- [ ] **Step 2: 테스트 실패 확인**

```powershell
go test ./internal/engine -run 'TestProgress|TestProcess'
```
기대: `undefined: newProgressReporter`, `undefined: New` 등 컴파일 실패.

- [ ] **Step 3: `internal/engine/engine.go` 구현**

```go
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
```

- [ ] **Step 4: `internal/engine/single.go` 구현**

```go
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
				if outInfo.SizeBytes <= req.TargetBytes || attempt == maxSingleAttempts {
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
```

- [ ] **Step 5: 단위 테스트 통과 확인**

```powershell
go test ./internal/engine -run 'TestProgress|TestProcess'
```
기대: 전부 통과(`Process`의 분할 경로는 Task 11에서 추가되므로, 이 시점에는 `processSplit` 심볼이 없어 컴파일이 실패한다면 다음처럼 임시 스텁을 둔다).

`internal/engine/split.go`(Task 11에서 전체 교체, 지금은 임시):
```go
package engine

import (
	"context"
	"errors"

	"videocompress/internal/media"
)

func (e *Engine) processSplit(context.Context, Request, *media.Info, *media.SplitPlan, string, *progressReporter) (*Result, error) {
	return nil, errors.New("분할 모드는 아직 구현되지 않았습니다")
}
```

- [ ] **Step 6: 통합 테스트 헬퍼와 단일 압축 통합 테스트 작성**

`internal/engine/integration_test.go`:
```go
package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"videocompress/internal/ffmpeg"
	"videocompress/internal/media"
)

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// testClient returns a real ffmpeg client, skipping when none is installed.
// Set VC_TEST_FFMPEG to point at an ffmpeg binary to override.
func testClient(t *testing.T) *ffmpeg.Client {
	t.Helper()
	ffmpegPath := os.Getenv("VC_TEST_FFMPEG")
	if ffmpegPath == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			t.Fatal(err)
		}
		ffmpegPath = filepath.Join(cache, "VideoCompress", "ffmpeg", "bin", "ffmpeg"+exeSuffix())
	}
	if _, err := os.Stat(ffmpegPath); err != nil {
		t.Skipf("FFmpeg not found at %s; set VC_TEST_FFMPEG to run integration tests", ffmpegPath)
	}
	return &ffmpeg.Client{Tools: ffmpeg.Tools{
		FFmpeg:  ffmpegPath,
		FFprobe: filepath.Join(filepath.Dir(ffmpegPath), "ffprobe"+exeSuffix()),
	}}
}

func runFF(t *testing.T, bin string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append([]string{"-hide_banner", "-loglevel", "error", "-nostdin"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
	}
}

// makeVideo synthesizes a deterministic clip with optional audio.
func makeVideo(t *testing.T, ff *ffmpeg.Client, path string, w, h, seconds, kbps int, audio bool) {
	t.Helper()
	args := []string{"-y", "-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=%dx%d:rate=30", w, h)}
	if audio {
		args = append(args, "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000")
	}
	args = append(args, "-t", strconv.Itoa(seconds),
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-b:v", fmt.Sprintf("%dk", kbps))
	if audio {
		args = append(args, "-c:a", "aac", "-b:a", "128k", "-shortest")
	}
	args = append(args, "-movflags", "+faststart", path)
	runFF(t, ff.Tools.FFmpeg, args...)
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return st.Size()
}

func TestProcessSingleIntegration(t *testing.T) {
	ff := testClient(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "한글 영상.mp4")
	makeVideo(t, ff, src, 640, 360, 4, 1500, true)

	eng := New(ff, filepath.Join(dir, "tmp"))
	var stages []string
	res, err := eng.Process(context.Background(), Request{
		InputPath: src, TargetBytes: 400_000, PartBytes: 300_000,
	}, func(p Progress) { stages = append(stages, p.Stage) })
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if res.Mode != media.ModeSingle || len(res.Outputs) != 1 {
		t.Fatalf("result = %+v", res)
	}
	out := res.Outputs[0]
	if out.Bytes > 400_000 {
		t.Fatalf("output size %d exceeds target", out.Bytes)
	}
	if filepath.Base(out.Path) != "한글 영상_compressed.mp4" {
		t.Fatalf("output name = %q", out.Path)
	}
	if out.Bytes >= fileSize(t, src) {
		t.Fatalf("output (%d) not smaller than source (%d)", out.Bytes, fileSize(t, src))
	}
	if _, err := os.Stat(out.Path); err != nil {
		t.Fatalf("output missing: %v", err)
	}
	if len(stages) == 0 {
		t.Fatal("no progress reports")
	}
}

func TestProcessSkipIntegration(t *testing.T) {
	ff := testClient(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "tiny.mp4")
	makeVideo(t, ff, src, 320, 240, 2, 200, false)

	eng := New(ff, filepath.Join(dir, "tmp"))
	res, err := eng.Process(context.Background(), Request{
		InputPath: src, TargetBytes: 5_000_000, PartBytes: 4_000_000,
	}, nil)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if !res.Skipped || res.Mode != media.ModeSkipped {
		t.Fatalf("result = %+v", res)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, en := range entries {
		if strings.Contains(en.Name(), "_compressed") {
			t.Fatalf("skip must not create outputs, found %s", en.Name())
		}
	}
}

func TestProcessSingleFallsBackToSplitIntegration(t *testing.T) {
	ff := testClient(t)
	dir := t.TempDir()
	// A long clip cannot meet a tiny single-file target even at 480p, so the
	// engine must switch to splitting.
	src := filepath.Join(dir, "long.mp4")
	makeVideo(t, ff, src, 640, 360, 60, 700, true)

	eng := New(ff, filepath.Join(dir, "tmp"))
	res, err := eng.Process(context.Background(), Request{
		InputPath: src, TargetBytes: 700_000, PartBytes: 500_000,
	}, nil)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if res.Mode != media.ModeSplit || len(res.Outputs) < 2 {
		t.Fatalf("result = %+v", res)
	}
	for _, o := range res.Outputs {
		if o.Bytes > 500_000 {
			t.Fatalf("part %s is %d bytes", o.Path, o.Bytes)
		}
	}
}
```

*Note:* `TestProcessSingleFallsBackToSplitIntegration`는 Task 11의 `processSplit` 구현이 필요하므로 Task 11 완료 후 실행한다. Task 10에서는 앞의 두 테스트만 실행한다.

- [ ] **Step 7: 통합 테스트 실행(FFmpeg 설치 필요)**

```powershell
go test ./internal/engine -run 'TestProcessSingleIntegration|TestProcessSkipIntegration' -v -timeout 20m
```
기대: 통과(FFmpeg가 없으면 SKIP). 이 시점에 FFmpeg가 없다면 Task 6의 `VC_INSTALL_FFMPEG=1` 다운로드를 먼저 수행한다.

- [ ] **Step 8: vet + 커밋**

```powershell
go vet ./...
git add internal/engine
git commit -m "feat(engine): process orchestration, single compression with retries"
```

---

## Task 11: internal/engine — 분할 모드와 조각 보정

**Files:**
- Replace: `internal/engine/split.go` (Task 10의 임시 스텁 대체)
- Test: `internal/engine/integration_test.go` (분할 통합 테스트 추가)

**Interfaces:**
- Consumes: `SingleArgs`, `RangeArgs`, `RetryKbps`, `listSegments`, `remuxFastStart`, `VerifyParts`, `SplitOutputPaths`, `MoveFileNoReplace`
- Produces: `(Engine).processSplit`, 내부 `(Engine).fitSegment`, `(Engine).remuxFastStart`, `listSegments`, `audioChannelsOf`
- 상수: `maxRepairAttempts = 3`, `maxHalves = 2`

**동작 규칙(이 태스크의 핵심):**
1. 2-pass 인코딩을 `-f segment`로 출력해 키프레임 경계마다 조각을 만든다.
2. 조각은 `-reset_timestamps 1`이라 각자 0초에서 시작한다. 원본에서 재인코딩할 구간은 앞 조각들의 duration을 누적해 복원한다(절대 타임스탬프를 믿지 않는다).
3. 크기 초과 조각은 `RetryKbps`로 비트레이트를 낮춰 최대 3회 재인코딩한다.
4. 그래도 초과하면 **비트레이트는 유지한 채** 구간을 반으로 나눈다(크기가 약 절반이 됨). 최대 2회(=최대 4조각)까지.
5. 모든 최종 조각은 `-movflags +faststart`로 리먹스한다. 실패하면 조각 수 ≤ 4×원래 조각 수로 오류를 반환한다.

- [ ] **Step 1: `internal/engine/split.go` 구현**

```go
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
```

- [ ] **Step 2: 컴파일 및 단위/통합 테스트 실행**

```powershell
go test ./internal/engine -run 'TestProgress|TestProcess' -v
go test ./internal/engine -run 'TestProcessSingleFallsBackToSplitIntegration' -v -timeout 30m
```
기대: 폴백 통합 테스트 통과(FFmpeg 없으면 SKIP). 이 테스트는 60초 분할을 만들므로 실제 FFmpeg가 있으면 1~3분 걸릴 수 있다.

- [ ] **Step 3: 분할 통합 테스트 추가**

`internal/engine/integration_test.go`에 추가:
```go
func TestProcessSplitIntegration(t *testing.T) {
	ff := testClient(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "series.mp4")
	makeVideo(t, ff, src, 640, 360, 45, 600, true)

	eng := New(ff, filepath.Join(dir, "tmp"))
	res, err := eng.Process(context.Background(), Request{
		InputPath: src, TargetBytes: 1_500_000, PartBytes: 1_200_000,
	}, nil)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if res.Mode != media.ModeSplit {
		t.Fatalf("mode = %s (result %+v)", res.Mode, res)
	}
	if len(res.Outputs) < 2 {
		t.Fatalf("expected multiple parts, got %d", len(res.Outputs))
	}
	var sum float64
	for i, o := range res.Outputs {
		if o.Bytes > 1_200_000 {
			t.Fatalf("part %d size %d exceeds part target", i+1, o.Bytes)
		}
		if _, err := os.Stat(o.Path); err != nil {
			t.Fatalf("part %d missing: %v", i+1, err)
		}
		if !strings.Contains(filepath.Base(o.Path), "_part") {
			t.Fatalf("part %d name = %q", i+1, o.Path)
		}
		sum += o.Duration
	}
	if sum < 40 || sum > 50 {
		t.Fatalf("part durations sum to %.1fs", sum)
	}
}

// The part limit here is below what a single-quality part can produce, so the
// engine must repair and/or halve segments. The assertions are path-agnostic:
// whatever route it takes, every part must fit and the mode must be split.
func TestProcessSplitRepairAndHalveIntegration(t *testing.T) {
	ff := testClient(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "tough.mp4")
	makeVideo(t, ff, src, 640, 360, 30, 600, true)

	eng := New(ff, filepath.Join(dir, "tmp"))
	res, err := eng.Process(context.Background(), Request{
		InputPath: src, TargetBytes: 400_000, PartBytes: 260_000,
	}, nil)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if res.Mode != media.ModeSplit {
		t.Fatalf("mode = %s", res.Mode)
	}
	if !res.LowQuality {
		t.Fatal("expected LowQuality to be set for a repaired split")
	}
	if len(res.Outputs) < 2 {
		t.Fatalf("expected at least 2 parts, got %d", len(res.Outputs))
	}
	for i, o := range res.Outputs {
		if o.Bytes > 260_000 {
			t.Fatalf("part %d size %d exceeds 260000", i+1, o.Bytes)
		}
	}
}
```

- [ ] **Step 4: 분할 통합 테스트 실행**

```powershell
go test ./internal/engine -run 'TestProcessSplit' -v -timeout 40m
```
기대: 두 테스트 모두 통과(FFmpeg 없으면 SKIP). 실패하면 조각 크기 검증과 조각 duration 합 검증 메시지를 확인한다.

- [ ] **Step 5: 취소 동작 수동 확인**

```powershell
# 아무 긴 영상과 함께 실행한 뒤 Ctrl+C
go run ./cmd/vc --target 1 --part 1 "영상 파일.mp4"
```
기대: 중단 시 최종 폴더에 `_part`·`_compressed` 파일이 생기지 않고 프로세스가 종료된다(임시 폴더는 Task 16의 정리 로직에서 함께 확인).

- [ ] **Step 6: vet + 커밋**

```powershell
go vet ./...
git add internal/engine
git commit -m "feat(engine): split mode with per-part repair and halving"
```

---

## Task 12: internal/jobs — 큐, 순차 처리, 취소

**Files:**
- Create: `internal/jobs/manager.go`
- Test: `internal/jobs/manager_test.go`

**Interfaces:**
- Consumes: `engine.Request`, `engine.Result`, `engine.Progress`, `media.HasVideoExt`, `media.ErrInvalidSettings`
- Produces:
  - 상태 상수 `jobs.StatusQueued/Running/Done/Failed/Skipped/Canceled`
  - `jobs.Job` (JSON 태그 포함), `jobs.Options{TargetBytes, PartBytes, OutDir, DryRun}`
  - `jobs.Processor` 인터페이스
  - `jobs.NewManager(proc Processor, notify func()) *Manager`
  - `(Manager) Add(paths ...string) []Job`, `Remove(id string) bool`, `ClearFinished() int`
  - `(Manager) Start(opts Options) error`, `Cancel()`, `IsRunning() bool`, `Snapshot() []Job`

- [ ] **Step 1: 실패하는 테스트 작성**

`internal/jobs/manager_test.go`:
```go
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
	if err := m.Start(Options{TargetBytes: 5_000_000, PartBytes: 4_000_000}); !errors.Is(err, media.ErrInvalidSettings) {
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
```

- [ ] **Step 2: 테스트 실패 확인**

```powershell
go test ./internal/jobs
```
기대: `undefined: NewManager` 등 컴파일 실패.

- [ ] **Step 3: 구현**

`internal/jobs/manager.go`:
```go
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
```

- [ ] **Step 4: 테스트 통과 확인**

```powershell
go test ./internal/jobs -race
go vet ./...
```
기대: 전부 통과하며 race detector 경고가 없다.

- [ ] **Step 5: 커밋**

```powershell
git add internal/jobs
git commit -m "feat(jobs): sequential queue with cancel and progress"
```

---

## Task 13: internal/cli + cmd/vc — 콘솔 CLI

**Files:**
- Create: `internal/cli/cli.go`
- Create: `cmd/vc/main.go` (Task 1의 임시 스텁 교체)
- Test: `internal/cli/cli_test.go`

**Interfaces:**
- Produces:
  - `cli.Version` 상수
  - 종료 코드 `cli.ExitOK(0)/ExitJobFailed(1)/ExitUsage(2)/ExitNoFFmpeg(3)/ExitCanceled(130)`
  - `cli.Options`, `cli.Deps{Locator; Downloader; NewProcessor; SettingsPath}`, `cli.Processor`
  - `cli.DefaultDeps() (Deps, error)`
  - `cli.ParseArgs(args []string, defaults config.Settings) (Options, error)`
  - `cli.Run(args []string, stdout, stderr io.Writer, deps Deps) int`

- [ ] **Step 1: 실패하는 테스트 작성**

`internal/cli/cli_test.go`:
```go
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
		NewProcessor: func(ffmpeg.Tools) Processor { return proc },
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

func TestParseArgsUsesDefaults(t *testing.T) {
	opts, err := ParseArgs([]string{"--json", "a.mp4"}, config.Settings{TargetMB: 7, PartMB: 3})
	if err != nil {
		t.Fatal(err)
	}
	if opts.TargetMB != 7 || opts.PartMB != 3 || !opts.JSON || len(opts.Files) != 1 {
		t.Fatalf("opts = %+v", opts)
	}
}
```

- [ ] **Step 2: 테스트 실패 확인**

```powershell
go test ./internal/cli
```
기대: `undefined: Run` 등 컴파일 실패.

- [ ] **Step 3: `internal/cli/cli.go` 구현**

```go
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
		loc.Override = opts.FFmpegPath // saved preference: still a candidate, not a hard requirement
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
		"version": Version,
		"dryRun":  opts.DryRun,
		"target":  opts.TargetMB,
		"part":    opts.PartMB,
		"ok":      exit == ExitOK,
		"exitCode": exit,
		"files":   results,
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
```

- [ ] **Step 4: `cmd/vc/main.go` 스텁 교체**

```go
package main

import (
	"fmt"
	"os"

	"videocompress/internal/cli"
)

func main() {
	deps, err := cli.DefaultDeps()
	if err != nil {
		fmt.Fprintf(os.Stderr, "초기화 실패: %v\n", err)
		os.Exit(cli.ExitUsage)
	}
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr, deps))
}
```

- [ ] **Step 5: 테스트와 빌드 확인**

```powershell
go test ./internal/cli
go vet ./...
go build ./cmd/vc
```

- [ ] **Step 6: 실제 CLI 수동 스모크 테스트(FFmpeg 필요)**

```powershell
go run ./cmd/vc --version
go run ./cmd/vc --dry-run --target 5 --part 4 "영상 파일.mp4"
go run ./cmd/vc --target 5 --part 4 "영상 파일.mp4"          # 단일 압축
go run ./cmd/vc --json --target 2 --part 1 "영상 파일.mp4"   # 분할 + JSON
go run ./cmd/vc --no-download --ffmpeg "없는경로\ffmpeg.exe" "영상 파일.mp4"  # 종료 코드 3
```
기대: `--dry-run`은 인코딩 없이 계획만 출력하고, 압축 실행 시 `<이름>_compressed.mp4` 또는 `<이름>_partNN.mp4`가 원본 옆에 생성되며 원본은 수정되지 않는다.

- [ ] **Step 7: 커밋**

```powershell
git add internal/cli cmd/vc
git commit -m "feat(cli): vc console tool sharing the engine"
```

---

## Task 14: Wails 앱 — 바인딩, 이벤트, 네이티브 드롭

**Files:**
- Replace: `app.go` (Task 1의 최소 스텁)
- Replace: `main.go` (Task 1의 최소 스텁)
- Test: `app_test.go`

**Interfaces:**
- Consumes: `jobs.Manager`, `engine.Engine`, `ffmpeg.Locator/Downloader/Client/Tools`, `config`
- Produces(GUI에서 `window.go.main.App`로 노출):
  - `GetSettings() config.Settings`, `SaveSettings(targetMB, partMB int) error`
  - `GetJobs() []jobs.Job`, `AddFiles(paths []string) []jobs.Job`, `RemoveJob(id string) bool`, `ClearFinished() int`
  - `StartProcessing() error`, `CancelJobs()`
  - `GetFFmpegStatus() FFmpegStatus`, `DownloadFFmpeg() error`
  - `OpenFileDialog() ([]string, error)`, `RevealInExplorer(path string) error`, `Quit()`
- 이벤트 상수: `EventQueueUpdate = "queue:update"`, `EventFFmpegStatus = "ffmpeg:status"`, `EventNotice = "app:notice"`

- [ ] **Step 1: 실패하는 테스트 작성**

`app_test.go`:
```go
package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"videocompress/internal/config"
	"videocompress/internal/engine"
	"videocompress/internal/ffmpeg"
	"videocompress/internal/jobs"
	"videocompress/internal/media"
)

// stubFF makes the engine run without a real FFmpeg.
type stubFF struct{}

func (stubFF) Probe(context.Context, string) (*media.Info, error) {
	return &media.Info{Path: "x", SizeBytes: 1_000_000, Duration: 10, Width: 640, Height: 360}, nil
}

func (stubFF) Run(context.Context, []string, float64, func(float64)) error { return nil }

func testApp(t *testing.T) *App {
	t.Helper()
	a := NewApp()
	dir := t.TempDir()
	a.settingsPath = filepath.Join(dir, "settings.json")
	a.cacheDir = filepath.Join(dir, "cache")
	a.tempRoot = filepath.Join(a.cacheDir, "temp")
	a.emit = func(string, ...any) {}
	a.locator = ffmpeg.Locator{
		Verify:   func(context.Context, string) error { return os.ErrNotExist },
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
	}
	a.downloader = ffmpeg.Downloader{
		URL:    "http://127.0.0.1:0/unused",
		Verify: func(context.Context, string) error { return nil },
	}
	return a
}

func tmpFile(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestSaveAndGetSettings(t *testing.T) {
	a := testApp(t)
	if err := a.SaveSettings(6, 3); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := a.GetSettings(); got.TargetMB != 6 || got.PartMB != 3 {
		t.Fatalf("settings = %+v", got)
	}
	if err := a.SaveSettings(1, 1); err == nil {
		t.Fatal("want validation error for target 1")
	}
	// persisted to disk
	loaded, err := config.Load(a.settingsPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.TargetMB != 6 || loaded.PartMB != 3 {
		t.Fatalf("persisted = %+v", loaded)
	}
}

func TestAddFilesRejectsNonVideo(t *testing.T) {
	a := testApp(t)
	mp4 := tmpFile(t, "clip.mp4")
	txt := tmpFile(t, "notes.txt")
	added := a.AddFiles([]string{mp4, txt})
	if len(added) != 2 {
		t.Fatalf("added = %+v", added)
	}
	if added[0].Status != jobs.StatusQueued {
		t.Fatalf("mp4 status = %s", added[0].Status)
	}
	if added[1].Status != jobs.StatusFailed {
		t.Fatalf("txt status = %s", added[1].Status)
	}
	if len(a.GetJobs()) != 2 {
		t.Fatalf("jobs = %+v", a.GetJobs())
	}
}

func TestStartProcessingRequiresFFmpeg(t *testing.T) {
	a := testApp(t)
	a.AddFiles([]string{tmpFile(t, "clip.mp4")})
	err := a.StartProcessing()
	if err == nil || !strings.Contains(err.Error(), "FFmpeg") {
		t.Fatalf("err = %v", err)
	}
	if a.GetJobs()[0].Status != jobs.StatusQueued {
		t.Fatalf("job should stay queued: %+v", a.GetJobs()[0])
	}
}

func TestStartProcessingRunsQueuedJobs(t *testing.T) {
	a := testApp(t)
	a.tools = ffmpeg.Tools{FFmpeg: "fake-ffmpeg", FFprobe: "fake-ffprobe"}
	a.newEngine = func(ffmpeg.Tools, string) *engine.Engine {
		return engine.New(stubFF{}, t.TempDir())
	}
	clip := tmpFile(t, "clip.mp4")
	a.AddFiles([]string{clip})
	if err := a.StartProcessing(); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitFor(t, func() bool { return a.GetJobs()[0].Status != jobs.StatusRunning && a.GetJobs()[0].Status != jobs.StatusQueued })
	got := a.GetJobs()[0]
	if got.Status != jobs.StatusSkipped {
		t.Fatalf("job = %+v", got)
	}
	if a.IsRunning() {
		t.Fatal("manager should be idle")
	}
}

func TestPrepareFFmpegReportsMissing(t *testing.T) {
	a := testApp(t)
	if err := a.prepareFFmpeg(context.Background(), false); err != nil {
		t.Fatalf("prepare (locate-only) must not fail: %v", err)
	}
	st := a.GetFFmpegStatus()
	if st.Found || st.Error == "" {
		t.Fatalf("status = %+v", st)
	}
}

func TestRevealInExplorerMissingPath(t *testing.T) {
	a := testApp(t)
	if err := a.RevealInExplorer(filepath.Join(t.TempDir(), "nope.mp4")); err == nil {
		t.Fatal("want error for a missing path")
	}
}

func TestBeforeCloseAllowsWhenIdle(t *testing.T) {
	a := testApp(t)
	if a.beforeClose(context.Background()) {
		t.Fatal("idle app should close without prompting")
	}
}
```

- [ ] **Step 2: 테스트 실패 확인**

```powershell
go test . -run 'TestSaveAndGetSettings'
```
기대: `undefined: NewApp` 또는 `a.SaveSettings undefined` 등.

- [ ] **Step 3: `app.go` 구현**

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"videocompress/internal/config"
	"videocompress/internal/engine"
	"videocompress/internal/ffmpeg"
	"videocompress/internal/jobs"
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
	locator    ffmpeg.Locator
	downloader ffmpeg.Downloader
	newEngine  func(ffmpeg.Tools, string) *engine.Engine

	manager *jobs.Manager
	emit    func(event string, data ...any)

	openFilesFn func(context.Context) ([]string, error)
	revealFn    func(string) error
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
		return engine.New(&ffmpeg.Client{Tools: tools}, tempRoot)
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
		return nil, err
	}
	return eng.Process(ctx, req, onProgress)
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
	a.loadSettings()
	a.cleanupStaleTemp()
	wruntime.OnFileDrop(ctx, func(_, _ int, paths []string) {
		a.AddFiles(paths)
	})
	if err := a.prepareFFmpeg(ctx, false); err != nil {
		a.notice("error", err.Error())
	}
}

func (a *App) shutdown(context.Context) {
	a.manager.Cancel()
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

// cleanupStaleTemp removes scratch folders left behind by a crashed run.
func (a *App) cleanupStaleTemp() {
	if a.tempRoot == "" {
		return
	}
	entries, err := os.ReadDir(a.tempRoot)
	if err != nil {
		return
	}
	for _, en := range entries {
		if !en.IsDir() {
			continue
		}
		info, err := en.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) > 24*time.Hour {
			_ = os.RemoveAll(filepath.Join(a.tempRoot, en.Name()))
		}
	}
}
```

- [ ] **Step 4: `main.go` 최종 구현**

```go
package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()
	err := wails.Run(&options.App{
		Title:     "VideoCompress",
		Width:     1080,
		Height:    760,
		MinWidth:  940,
		MinHeight: 620,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:     app.startup,
		OnShutdown:    app.shutdown,
		OnBeforeClose: app.beforeClose,
		Bind:          []interface{}{app},
	})
	if err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 5: 테스트와 바인딩 생성 확인**

```powershell
go test . -run 'TestSave|TestAdd|TestStart|TestPrepare|TestReveal|TestBeforeClose'
go vet ./...
npm --prefix frontend run build
wails build -nopackage
```
기대: Go 테스트 통과, `frontend/wailsjs/go/main/App.d.ts`에 바인딩이 생성됨(커밋하지 않음, .gitignore 대상).

- [ ] **Step 6: GUI 수동 스모크(FFmpeg 필요)**

```powershell
wails build -nopackage
.\build\bin\VideoCompress.exe
```
기대: 창이 뜨고, 파일을 드래그하면 목록에 추가되고(네이티브 드롭), FFmpeg가 없으면 다운로드 배너가 보인다.

- [ ] **Step 7: 커밋**

```powershell
git add app.go main.go app_test.go
git commit -m "feat(gui): wails bindings, events, native file drop"
```

---

## Task 15: frontend 라이브러리 — Wails 바인딩 래퍼와 포매터

**Files:**
- Create: `frontend/src/lib/api.ts`
- Create: `frontend/src/lib/format.ts`
- Test: `frontend/src/lib/api.test.ts`, `frontend/src/lib/format.test.ts`
- Delete: `frontend/src/lib/smoke.test.ts` (Task 1의 도구 검증용)

**Interfaces:**
- `api.ts` Produces:
  - 타입: `JobStatus`, `Output`, `JobResult`, `Job`, `Settings`, `FFmpegStatus`, `Notice`, `AppAPI`
  - 상수: `EVENT_QUEUE_UPDATE`, `EVENT_FFMPEG_STATUS`, `EVENT_NOTICE`
  - 함수: `app(): AppAPI | null`, `requireApp(): AppAPI`, `runtime()`, `on(name, cb): () => void`
- `format.ts` Produces: `formatBytes`, `formatDuration`, `basename`, `dirname`, `stageLabel`, `statusLabel`, `summarizeJob`

**설계 메모:** `frontend/wailsjs/**`는 .gitignore 대상이라 임포트하지 않는다. 대신 `window.go.main.App`과 `window.runtime`을 얇게 감싸 테스트에서 그대로 가짜 객체를 주입할 수 있게 한다.

- [ ] **Step 1: 실패하는 테스트 작성**

`frontend/src/lib/api.test.ts`:
```ts
import { afterEach, describe, expect, it, vi } from 'vitest'
import { app, on, requireApp } from './api'

afterEach(() => {
  delete (window as unknown as { go?: unknown }).go
  delete (window as unknown as { runtime?: unknown }).runtime
})

describe('app()', () => {
  it('returns null outside the desktop app', () => {
    expect(app()).toBeNull()
  })

  it('returns the Go binding when present', () => {
    const binding = { GetJobs: vi.fn() }
    ;(window as unknown as { go: unknown }).go = { main: { App: binding } }
    expect(app()).toBe(binding)
  })
})

describe('requireApp()', () => {
  it('throws a helpful error when the binding is missing', () => {
    expect(() => requireApp()).toThrowError(/데스크톱/)
  })
})

describe('on()', () => {
  it('is a no-op without the wails runtime', () => {
    const off = on('queue:update', () => {})
    expect(typeof off).toBe('function')
    expect(() => off()).not.toThrow()
  })

  it('subscribes and unsubscribes through the runtime', () => {
    const cancel = vi.fn()
    const EventsOn = vi.fn(() => cancel)
    ;(window as unknown as { runtime: unknown }).runtime = { EventsOn, EventsOff: vi.fn() }
    const cb = vi.fn()
    const off = on('queue:update', cb)
    expect(EventsOn).toHaveBeenCalledWith('queue:update', cb)
    off()
    expect(cancel).toHaveBeenCalled()
  })
})
```

`frontend/src/lib/format.test.ts`:
```ts
import { describe, expect, it } from 'vitest'
import { basename, dirname, formatBytes, formatDuration, stageLabel, statusLabel, summarizeJob } from './format'
import type { Job } from './api'

function job(overrides: Partial<Job> = {}): Job {
  return {
    id: 'job-1', input: 'C:\\videos\\movie.mp4', outputs: null, status: 'queued',
    stage: 'probing', percent: 0, message: '', addedAt: '', durationMs: 0, ...overrides,
  }
}

describe('formatBytes', () => {
  it('formats bytes, kilobytes and megabytes', () => {
    expect(formatBytes(500)).toBe('500B')
    expect(formatBytes(4_500)).toBe('5KB')
    expect(formatBytes(4_800_000)).toBe('4.8MB')
    expect(formatBytes(Number.NaN)).toBe('-')
  })
})

describe('formatDuration', () => {
  it('formats seconds and minutes', () => {
    expect(formatDuration(12_000)).toBe('12초')
    expect(formatDuration(95_000)).toBe('1분 35초')
    expect(formatDuration(-1)).toBe('-')
  })
})

describe('path helpers', () => {
  it('handles windows and posix separators', () => {
    expect(basename('C:\\videos\\movie.mp4')).toBe('movie.mp4')
    expect(basename('/tmp/a b.mp4')).toBe('a b.mp4')
    expect(dirname('C:\\videos\\movie.mp4')).toBe('C:\\videos')
    expect(dirname('/tmp/a.mp4')).toBe('/tmp')
  })
})

describe('labels', () => {
  it('maps engine stages', () => {
    expect(stageLabel('compressing 720p')).toBe('압축 중')
    expect(stageLabel('repairing')).toBe('조각 보정 중')
    expect(stageLabel('')).toBe('대기 중')
  })

  it('maps job statuses', () => {
    expect(statusLabel('failed')).toBe('실패')
    expect(statusLabel('canceled')).toBe('취소됨')
  })
})

describe('summarizeJob', () => {
  it('describes each state', () => {
    expect(summarizeJob(job({ status: 'queued' }))).toBe('대기 중')
    expect(summarizeJob(job({ status: 'running', stage: 'compressing 720p', percent: 42 }))).toContain('42%')
    expect(summarizeJob(job({ status: 'done', outputs: ['a.mp4'] }))).toBe('압축 완료')
    expect(summarizeJob(job({ status: 'done', outputs: ['a.mp4', 'b.mp4'] }))).toBe('2개 조각으로 저장됨')
    expect(summarizeJob(job({ status: 'skipped' }))).toBe('이미 목표 크기 이하')
    expect(summarizeJob(job({ status: 'failed', message: '인코딩 실패' }))).toBe('인코딩 실패')
    expect(summarizeJob(job({ status: 'canceled' }))).toBe('취소됨')
  })
})
```

- [ ] **Step 2: 테스트 실패 확인**

```powershell
npm --prefix frontend run test -- --run src/lib
```
기대: `Failed to resolve import "./api"` 등 실패.

- [ ] **Step 3: `frontend/src/lib/api.ts` 구현**

```ts
// Types mirror the Go structs in internal/jobs and internal/config.

export type JobStatus = 'queued' | 'running' | 'done' | 'failed' | 'skipped' | 'canceled'

export interface Output {
  path: string
  bytes: number
  duration: number
  width: number
  height: number
}

export interface JobResult {
  mode: string
  outputs?: Output[] | null
  width?: number
  height?: number
  videoKbps?: number
  audioKbps?: number
  segmentSeconds?: number
  parts?: number
  lowQuality?: boolean
  skipped?: boolean
}

export interface Job {
  id: string
  input: string
  outputs: string[] | null
  status: JobStatus
  stage: string
  percent: number
  message: string
  result?: JobResult | null
  addedAt: string
  durationMs: number
}

export interface Settings {
  targetMB: number
  partMB: number
  ffmpegPath?: string
}

export interface FFmpegStatus {
  found: boolean
  path: string
  downloading: boolean
  percent: number
  error: string
}

export interface Notice {
  level: 'info' | 'warn' | 'error'
  message: string
}

export interface AppAPI {
  GetSettings(): Promise<Settings>
  SaveSettings(targetMB: number, partMB: number): Promise<void>
  GetJobs(): Promise<Job[]>
  AddFiles(paths: string[]): Promise<Job[]>
  RemoveJob(id: string): Promise<boolean>
  ClearFinished(): Promise<number>
  StartProcessing(): Promise<void>
  CancelJobs(): Promise<void>
  IsRunning(): Promise<boolean>
  GetFFmpegStatus(): Promise<FFmpegStatus>
  DownloadFFmpeg(): Promise<void>
  OpenFileDialog(): Promise<string[]>
  RevealInExplorer(path: string): Promise<void>
  Quit(): Promise<void>
}

interface WailsRuntime {
  EventsOn(name: string, cb: (...data: unknown[]) => void): () => void
  EventsOff(name: string, ...additional: string[]): void
}

interface WailsWindow extends Window {
  go?: { main?: { App?: AppAPI } }
  runtime?: WailsRuntime
}

export const EVENT_QUEUE_UPDATE = 'queue:update'
export const EVENT_FFMPEG_STATUS = 'ffmpeg:status'
export const EVENT_NOTICE = 'app:notice'

/** app() returns the Go binding, or null when running in a plain browser. */
export function app(): AppAPI | null {
  return (window as WailsWindow).go?.main?.App ?? null
}

/** runtime() returns the Wails JS runtime, or null in a plain browser. */
export function runtime(): WailsRuntime | null {
  return (window as WailsWindow).runtime ?? null
}

/** requireApp() throws a user-facing error when the binding is unavailable. */
export function requireApp(): AppAPI {
  const a = app()
  if (!a) {
    throw new Error('데스크톱 앱에서 실행해 주세요.')
  }
  return a
}

/** on() subscribes to a Wails event and returns an unsubscribe function. */
export function on(name: string, cb: (...data: unknown[]) => void): () => void {
  const rt = runtime()
  if (!rt) {
    return () => {}
  }
  const off = rt.EventsOn(name, cb)
  return () => {
    if (typeof off === 'function') {
      off()
    } else {
      rt.EventsOff(name)
    }
  }
}
```

- [ ] **Step 4: `frontend/src/lib/format.ts` 구현**

```ts
import type { Job, JobStatus } from './api'

/** formatBytes formats a decimal byte count for display. */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '-'
  if (bytes < 1000) return `${Math.round(bytes)}B`
  if (bytes < 1_000_000) return `${Math.round(bytes / 1000)}KB`
  return `${(bytes / 1_000_000).toFixed(1)}MB`
}

/** formatDuration formats a millisecond duration in Korean. */
export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '-'
  const total = Math.round(ms / 1000)
  const minutes = Math.floor(total / 60)
  const seconds = total % 60
  if (minutes === 0) return `${seconds}초`
  return `${minutes}분 ${seconds}초`
}

/** basename returns the last path segment for either separator. */
export function basename(path: string): string {
  const norm = path.replace(/[\\/]+$/, '')
  const idx = Math.max(norm.lastIndexOf('/'), norm.lastIndexOf('\\'))
  return idx >= 0 ? norm.slice(idx + 1) : norm
}

/** dirname returns everything before the last path segment. */
export function dirname(path: string): string {
  const norm = path.replace(/[\\/]+$/, '')
  const idx = Math.max(norm.lastIndexOf('/'), norm.lastIndexOf('\\'))
  return idx > 0 ? norm.slice(0, idx) : norm
}

const STAGE_LABELS: Record<string, string> = {
  queued: '대기 중',
  probing: '확인 중',
  repairing: '조각 보정 중',
  verifying: '검증 중',
  saving: '저장 중',
  done: '완료',
}

/** stageLabel turns an engine stage string into Korean. */
export function stageLabel(stage: string): string {
  if (stage in STAGE_LABELS) return STAGE_LABELS[stage]
  if (stage.startsWith('compressing')) return '압축 중'
  return stage || '대기 중'
}

const STATUS_LABELS: Record<JobStatus, string> = {
  queued: '대기',
  running: '진행 중',
  done: '완료',
  failed: '실패',
  skipped: '건너뜀',
  canceled: '취소됨',
}

/** statusLabel turns a job status into Korean. */
export function statusLabel(status: JobStatus): string {
  return STATUS_LABELS[status] ?? status
}

/** summarizeJob is the one-line description shown under each progress bar. */
export function summarizeJob(job: Job): string {
  switch (job.status) {
    case 'done': {
      const parts = job.outputs?.length ?? 0
      return parts > 1 ? `${parts}개 조각으로 저장됨` : '압축 완료'
    }
    case 'skipped':
      return '이미 목표 크기 이하'
    case 'failed':
      return job.message || '실패'
    case 'canceled':
      return '취소됨'
    case 'running':
      return `${stageLabel(job.stage)} ${Math.round(job.percent)}%`
    default:
      return '대기 중'
  }
}
```

- [ ] **Step 5: `frontend/src/lib/smoke.test.ts` 삭제 및 테스트 통과 확인**

```powershell
Remove-Item frontend/src/lib/smoke.test.ts
npm --prefix frontend run test -- --run src/lib
npm --prefix frontend run build
```

- [ ] **Step 6: 커밋**

```powershell
git add frontend/src/lib
git commit -m "feat(ui): wails binding wrapper and display formatters"
```

---

## Task 16: frontend 컴포넌트 — 드롭존, 작업 목록, 설정, 배너, 툴바

**Files:**
- Create: `frontend/src/components/DropZone.tsx`, `JobRow.tsx`, `SettingsPanel.tsx`, `FFmpegBanner.tsx`, `Toolbar.tsx`
- Create: `frontend/src/styles.css`
- Replace: `frontend/src/App.tsx`, `frontend/src/main.tsx`
- Modify: `frontend/index.html` (`lang="ko"`, title)
- Delete: Wails 템플릿 잔여물(`App.css`, `style.css`, `assets/`, `logo.svg` 등 존재하는 것)
- Test: `frontend/src/components/*.test.tsx`, `frontend/src/App.test.tsx`

**Interfaces:**
- Consumes: `frontend/src/lib/api.ts`, `format.ts`
- Produces: `App`(기본 내보내기), 컴포넌트 5종

**설계 메모:** 실제 파일 드롭은 Go의 `OnFileDrop`이 처리하고 브라우저 드롭 이벤트는 기본 동작만 막는다(웹뷰가 파일로 이동하는 것 방지). 드롭존 클릭은 `OpenFileDialog`를 호출한다.

- [ ] **Step 1: 실패하는 테스트 작성**

`frontend/src/components/DropZone.test.tsx`:
```tsx
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import DropZone from './DropZone'

describe('DropZone', () => {
  it('calls onPick when clicked', () => {
    const onPick = vi.fn()
    render(<DropZone disabled={false} onPick={onPick} />)
    fireEvent.click(screen.getByRole('button'))
    expect(onPick).toHaveBeenCalledTimes(1)
  })

  it('ignores clicks while disabled', () => {
    const onPick = vi.fn()
    render(<DropZone disabled onPick={onPick} />)
    fireEvent.click(screen.getByRole('button'))
    expect(onPick).not.toHaveBeenCalled()
  })

  it('highlights on drag over and clears on drag leave', () => {
    render(<DropZone disabled={false} onPick={() => {}} />)
    const zone = screen.getByRole('button')
    fireEvent.dragOver(zone)
    expect(zone.className).toContain('dropzone--hover')
    fireEvent.dragLeave(zone)
    expect(zone.className).not.toContain('dropzone--hover')
  })

  it('does not highlight while disabled', () => {
    render(<DropZone disabled onPick={() => {}} />)
    const zone = screen.getByRole('button')
    fireEvent.dragOver(zone)
    expect(zone.className).not.toContain('dropzone--hover')
  })
})
```

`frontend/src/components/JobRow.test.tsx`:
```tsx
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import JobRow from './JobRow'
import type { Job } from '../lib/api'

const base: Job = {
  id: 'job-1', input: 'C:\\videos\\movie.mp4', outputs: null, status: 'running',
  stage: 'compressing 720p', percent: 40, message: '', addedAt: '', durationMs: 0,
}

describe('JobRow', () => {
  it('shows the file name, status and progress', () => {
    render(<JobRow job={base} running onReveal={() => {}} onRemove={() => {}} />)
    expect(screen.getByText('movie.mp4')).toBeInTheDocument()
    expect(screen.getByText('진행 중')).toBeInTheDocument()
    const bar = screen.getByRole('progressbar')
    expect(bar).toHaveAttribute('aria-valuenow', '40')
    expect(bar.firstElementChild).toHaveStyle({ width: '40%' })
  })

  it('hides the remove button while a run is active', () => {
    render(<JobRow job={base} running onReveal={() => {}} onRemove={() => {}} />)
    expect(screen.queryByLabelText(/목록에서 제거/)).toBeNull()
  })

  it('removes a job when idle', () => {
    const onRemove = vi.fn()
    render(<JobRow job={{ ...base, status: 'queued' }} running={false} onReveal={() => {}} onRemove={onRemove} />)
    fireEvent.click(screen.getByLabelText(/목록에서 제거/))
    expect(onRemove).toHaveBeenCalledWith('job-1')
  })

  it('lists outputs with sizes and reveals them on click', () => {
    const onReveal = vi.fn()
    const done: Job = {
      ...base, status: 'done', percent: 100, durationMs: 12_000,
      outputs: ['C:\\videos\\movie_part01.mp4', 'C:\\videos\\movie_part02.mp4'],
      result: {
        mode: 'split', width: 1280, height: 720, videoKbps: 917,
        outputs: [
          { path: 'C:\\videos\\movie_part01.mp4', bytes: 3_900_000, duration: 30, width: 1280, height: 720 },
          { path: 'C:\\videos\\movie_part02.mp4', bytes: 3_700_000, duration: 28, width: 1280, height: 720 },
        ],
      },
    }
    render(<JobRow job={done} running={false} onReveal={onReveal} onRemove={() => {}} />)
    expect(screen.getByText('2개 조각으로 저장됨')).toBeInTheDocument()
    expect(screen.getByText('3.9MB')).toBeInTheDocument()
    fireEvent.click(screen.getByText('movie_part02.mp4'))
    expect(onReveal).toHaveBeenCalledWith('C:\\videos\\movie_part02.mp4')
  })

  it('shows the failure message', () => {
    render(<JobRow job={{ ...base, status: 'failed', message: '인코딩 실패' }} running={false} onReveal={() => {}} onRemove={() => {}} />)
    expect(screen.getByText('인코딩 실패')).toBeInTheDocument()
  })
})
```

`frontend/src/components/SettingsPanel.test.tsx`:
```tsx
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import SettingsPanel from './SettingsPanel'

describe('SettingsPanel', () => {
  it('shows the current settings', () => {
    render(<SettingsPanel settings={{ targetMB: 5, partMB: 4 }} running={false} onSave={vi.fn()} />)
    expect(screen.getByLabelText('목표 크기 (MB)')).toHaveValue('5')
    expect(screen.getByLabelText('조각 크기 (MB)')).toHaveValue('4')
  })

  it('rejects a part size that is not smaller than the target', async () => {
    const onSave = vi.fn()
    render(<SettingsPanel settings={{ targetMB: 5, partMB: 4 }} running={false} onSave={onSave} />)
    fireEvent.change(screen.getByLabelText('조각 크기 (MB)'), { target: { value: '6' } })
    fireEvent.click(screen.getByRole('button', { name: '설정 저장' }))
    expect(await screen.findByText(/조각 크기는/)).toBeInTheDocument()
    expect(onSave).not.toHaveBeenCalled()
  })

  it('saves valid values as numbers', async () => {
    const onSave = vi.fn(async () => {})
    render(<SettingsPanel settings={{ targetMB: 5, partMB: 4 }} running={false} onSave={onSave} />)
    fireEvent.change(screen.getByLabelText('목표 크기 (MB)'), { target: { value: '8' } })
    fireEvent.change(screen.getByLabelText('조각 크기 (MB)'), { target: { value: '3' } })
    fireEvent.click(screen.getByRole('button', { name: '설정 저장' }))
    await waitFor(() => expect(onSave).toHaveBeenCalledWith(8, 3))
  })

  it('disables the form while running', () => {
    render(<SettingsPanel settings={{ targetMB: 5, partMB: 4 }} running onSave={vi.fn()} />)
    expect(screen.getByRole('button', { name: '설정 저장' })).toBeDisabled()
  })

  it('surfaces save failures', async () => {
    const onSave = vi.fn(async () => {
      throw new Error('설정을 저장하지 못했습니다: 권한 없음')
    })
    render(<SettingsPanel settings={{ targetMB: 5, partMB: 4 }} running={false} onSave={onSave} />)
    fireEvent.click(screen.getByRole('button', { name: '설정 저장' }))
    expect(await screen.findByText(/권한 없음/)).toBeInTheDocument()
  })
})
```

`frontend/src/components/FFmpegBanner.test.tsx`:
```tsx
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import FFmpegBanner from './FFmpegBanner'

describe('FFmpegBanner', () => {
  it('renders nothing when FFmpeg is ready', () => {
    const { container } = render(
      <FFmpegBanner status={{ found: true, path: 'ff', downloading: false, percent: 0, error: '' }} onDownload={() => {}} />,
    )
    expect(container).toBeEmptyDOMElement()
  })

  it('offers a download button when missing', () => {
    const onDownload = vi.fn()
    render(<FFmpegBanner status={{ found: false, path: '', downloading: false, percent: 0, error: '찾을 수 없습니다' }} onDownload={onDownload} />)
    fireEvent.click(screen.getByRole('button', { name: 'FFmpeg 내려받기' }))
    expect(onDownload).toHaveBeenCalled()
    expect(screen.getByText('찾을 수 없습니다')).toBeInTheDocument()
  })

  it('shows download progress and disables the button', () => {
    render(<FFmpegBanner status={{ found: false, path: '', downloading: true, percent: 42, error: '' }} onDownload={() => {}} />)
    expect(screen.getByRole('button', { name: /내려받는 중 42%/ })).toBeDisabled()
  })
})
```

`frontend/src/components/Toolbar.test.tsx`:
```tsx
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import Toolbar from './Toolbar'

const counts = { total: 3, queued: 1, finished: 2 }

describe('Toolbar', () => {
  it('enables start only with queued jobs and no run in progress', () => {
    const onStart = vi.fn()
    render(<Toolbar running={false} counts={counts} onStart={onStart} onCancel={() => {}} onClear={() => {}} />)
    fireEvent.click(screen.getByRole('button', { name: '압축 시작' }))
    expect(onStart).toHaveBeenCalled()
  })

  it('disables start with an empty queue', () => {
    render(<Toolbar running={false} counts={{ total: 0, queued: 0, finished: 0 }} onStart={() => {}} onCancel={() => {}} onClear={() => {}} />)
    expect(screen.getByRole('button', { name: '압축 시작' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '완료 항목 지우기' })).toBeDisabled()
  })

  it('enables cancel only while running', () => {
    const onCancel = vi.fn()
    render(<Toolbar running counts={counts} onStart={() => {}} onCancel={onCancel} onClear={() => {}} />)
    expect(screen.getByRole('button', { name: '압축 시작' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: '취소' }))
    expect(onCancel).toHaveBeenCalled()
  })
})
```

`frontend/src/App.test.tsx`:
```tsx
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from './App'
import type { FFmpegStatus, Job } from './lib/api'
import { EVENT_NOTICE } from './lib/api'

type Handlers = Record<string, (...args: unknown[]) => void>

const queuedJob: Job = {
  id: 'job-1', input: 'C:\\videos\\a.mp4', outputs: null, status: 'queued',
  stage: 'probing', percent: 0, message: '', addedAt: '', durationMs: 0,
}

const readyStatus: FFmpegStatus = { found: true, path: 'ff', downloading: false, percent: 0, error: '' }

function installBinding(overrides: Record<string, unknown> = {}) {
  const handlers: Handlers = {}
  const api = {
    GetSettings: vi.fn(async () => ({ targetMB: 5, partMB: 4 })),
    SaveSettings: vi.fn(async () => {}),
    GetJobs: vi.fn(async () => []),
    AddFiles: vi.fn(async () => []),
    RemoveJob: vi.fn(async () => true),
    ClearFinished: vi.fn(async () => 0),
    StartProcessing: vi.fn(async () => {}),
    CancelJobs: vi.fn(async () => {}),
    IsRunning: vi.fn(async () => false),
    GetFFmpegStatus: vi.fn(async () => readyStatus),
    DownloadFFmpeg: vi.fn(async () => {}),
    OpenFileDialog: vi.fn(async () => []),
    RevealInExplorer: vi.fn(async () => {}),
    Quit: vi.fn(async () => {}),
    ...overrides,
  }
  ;(window as unknown as { go: unknown }).go = { main: { App: api } }
  ;(window as unknown as { runtime: unknown }).runtime = {
    EventsOn: (name: string, cb: (...a: unknown[]) => void) => {
      handlers[name] = cb
      return () => {
        delete handlers[name]
      }
    },
    EventsOff: vi.fn(),
  }
  return { api, handlers }
}

afterEach(() => {
  delete (window as unknown as { go?: unknown }).go
  delete (window as unknown as { runtime?: unknown }).runtime
})

describe('App', () => {
  it('tells the user to run the desktop app when the binding is missing', () => {
    render(<App />)
    expect(screen.getByText(/데스크톱 앱에서 실행해 주세요/)).toBeInTheDocument()
  })

  it('renders jobs loaded from the backend', async () => {
    installBinding({ GetJobs: vi.fn(async () => [queuedJob]) })
    render(<App />)
    expect(await screen.findByText('a.mp4')).toBeInTheDocument()
    expect(screen.getByText('대기')).toBeInTheDocument()
  })

  it('starts processing and disables start while running', async () => {
    const { api } = installBinding({ GetJobs: vi.fn(async () => [queuedJob]) })
    render(<App />)
    await screen.findByText('a.mp4')
    fireEvent.click(screen.getByRole('button', { name: '압축 시작' }))
    await waitFor(() => expect(api.StartProcessing).toHaveBeenCalled())
    await waitFor(() => expect(screen.getByRole('button', { name: '압축 시작' })).toBeDisabled())
  })

  it('opens the picker and appends the added jobs', async () => {
    const added: Job = { ...queuedJob, id: 'job-9', input: 'C:\\videos\\b.mp4' }
    const { api } = installBinding({
      OpenFileDialog: vi.fn(async () => ['C:\\videos\\b.mp4']),
      AddFiles: vi.fn(async () => [added]),
    })
    render(<App />)
    fireEvent.click(await screen.findByRole('button', { name: /비디오 파일을/ }))
    await waitFor(() => expect(api.AddFiles).toHaveBeenCalledWith(['C:\\videos\\b.mp4']))
    expect(await screen.findByText('b.mp4')).toBeInTheDocument()
  })

  it('shows the ffmpeg banner when ffmpeg is missing', async () => {
    installBinding({ GetFFmpegStatus: vi.fn(async () => ({ ...readyStatus, found: false, path: '' })) })
    render(<App />)
    expect(await screen.findByText(/FFmpeg가 필요합니다/)).toBeInTheDocument()
  })

  it('shows notices pushed from the backend', async () => {
    const { handlers } = installBinding()
    render(<App />)
    await waitFor(() => expect(handlers[EVENT_NOTICE]).toBeDefined())
    act(() => {
      handlers[EVENT_NOTICE]({ level: 'error', message: 'FFmpeg 준비에 실패했습니다' })
    })
    expect(await screen.findByText('FFmpeg 준비에 실패했습니다')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 테스트 실패 확인**

```powershell
npm --prefix frontend run test -- --run src/components src/App.test.tsx
```
기대: 컴포넌트/App 모듈을 찾을 수 없어 실패.

- [ ] **Step 3: 컴포넌트 구현**

`frontend/src/components/DropZone.tsx`:
```tsx
import { useCallback, useState } from 'react'
import type { DragEvent } from 'react'

interface Props {
  disabled: boolean
  onPick: () => void
}

export default function DropZone({ disabled, onPick }: Props) {
  const [hover, setHover] = useState(false)

  const handleDragOver = useCallback(
    (e: DragEvent<HTMLDivElement>) => {
      e.preventDefault()
      if (!disabled) setHover(true)
    },
    [disabled],
  )

  return (
    <div
      className={`dropzone${hover ? ' dropzone--hover' : ''}${disabled ? ' dropzone--disabled' : ''}`}
      role="button"
      tabIndex={0}
      aria-disabled={disabled}
      onDragOver={handleDragOver}
      onDragLeave={() => setHover(false)}
      onDrop={(e) => {
        e.preventDefault()
        setHover(false)
      }}
      onClick={() => {
        if (!disabled) onPick()
      }}
      onKeyDown={(e) => {
        if (!disabled && (e.key === 'Enter' || e.key === ' ')) onPick()
      }}
    >
      <p className="dropzone__title">비디오 파일을 여기로 끌어다 놓으세요</p>
      <p className="dropzone__hint">또는 클릭해서 선택하세요 · 여러 개를 한 번에 추가할 수 있습니다</p>
    </div>
  )
}
```

`frontend/src/components/JobRow.tsx`:
```tsx
import type { Job } from '../lib/api'
import { basename, formatBytes, formatDuration, stageLabel, statusLabel, summarizeJob } from '../lib/format'

interface Props {
  job: Job
  running: boolean
  onReveal: (path: string) => void
  onRemove: (id: string) => void
}

function outputBytes(job: Job, path: string): number | undefined {
  return job.result?.outputs?.find((o) => o.path === path)?.bytes
}

export default function JobRow({ job, running, onReveal, onRemove }: Props) {
  const finished = job.status === 'done' || job.status === 'skipped'
  const percent = Math.max(0, Math.min(100, finished ? 100 : job.percent))
  const name = basename(job.input)

  return (
    <li className={`job job--${job.status}`}>
      <div className="job__head">
        <span className="job__name" title={job.input}>
          {name}
        </span>
        <span className={`chip chip--${job.status}`}>{statusLabel(job.status)}</span>
        {!running && (
          <button className="job__remove" onClick={() => onRemove(job.id)} aria-label={`${name} 목록에서 제거`}>
            ×
          </button>
        )}
      </div>
      <div
        className="job__bar"
        role="progressbar"
        aria-valuenow={Math.round(percent)}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-label={`${name} 진행률`}
      >
        <div className="job__bar-fill" style={{ width: `${percent}%` }} />
      </div>
      <div className="job__meta">
        {/* A failed job shows its message once, in the error paragraph below. */}
        {job.status !== 'failed' && <span>{summarizeJob(job)}</span>}
        {job.status === 'running' && <span className="job__stage">{stageLabel(job.stage)}</span>}
        {job.status === 'done' && job.result && (
          <span className="job__detail">
            {job.result.width}×{job.result.height} · {job.result.videoKbps}kbps
          </span>
        )}
        {job.status === 'done' && <span className="job__time">{formatDuration(job.durationMs)}</span>}
      </div>
      {job.outputs && job.outputs.length > 0 && (
        <ul className="job__outputs">
          {job.outputs.map((p) => (
            <li key={p}>
              <button className="link" onClick={() => onReveal(p)}>
                {basename(p)}
              </button>
              {outputBytes(job, p) !== undefined && <span className="job__outmeta">{formatBytes(outputBytes(job, p) as number)}</span>}
            </li>
          ))}
        </ul>
      )}
      {job.status === 'failed' && job.message && <p className="job__error">{job.message}</p>}
    </li>
  )
}
```

`frontend/src/components/SettingsPanel.tsx`:
```tsx
import { useEffect, useState } from 'react'
import type { Settings } from '../lib/api'

interface Props {
  settings: Settings
  running: boolean
  onSave: (targetMB: number, partMB: number) => Promise<void>
}

export default function SettingsPanel({ settings, running, onSave }: Props) {
  const [target, setTarget] = useState(String(settings.targetMB))
  const [part, setPart] = useState(String(settings.partMB))
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    setTarget(String(settings.targetMB))
    setPart(String(settings.partMB))
  }, [settings.targetMB, settings.partMB])

  const validate = (): string => {
    const t = Number(target)
    const p = Number(part)
    if (!Number.isInteger(t) || t < 2 || t > 100) return '목표 크기는 2~100MB 사이의 정수여야 합니다.'
    if (!Number.isInteger(p) || p < 1 || p >= t) return '조각 크기는 1MB 이상, 목표 크기보다 작아야 합니다.'
    return ''
  }

  return (
    <section className="panel" aria-label="크기 설정">
      <h2 className="panel__title">크기 설정</h2>
      <label className="field">
        <span>목표 크기 (MB)</span>
        <input
          value={target}
          inputMode="numeric"
          disabled={running}
          onChange={(e) => {
            setTarget(e.target.value)
            setSaved(false)
          }}
        />
      </label>
      <label className="field">
        <span>조각 크기 (MB)</span>
        <input
          value={part}
          inputMode="numeric"
          disabled={running}
          onChange={(e) => {
            setPart(e.target.value)
            setSaved(false)
          }}
        />
      </label>
      {error && <p className="error">{error}</p>}
      {saved && !error && <p className="ok">저장했습니다.</p>}
      <button
        className="btn"
        disabled={running}
        onClick={async () => {
          const message = validate()
          setError(message)
          setSaved(false)
          if (message) return
          try {
            await onSave(Number(target), Number(part))
            setSaved(true)
          } catch (e) {
            setError(e instanceof Error ? e.message : String(e))
          }
        }}
      >
        설정 저장
      </button>
      <p className="panel__hint">
        목표 크기를 넘기지 못하면 480p까지 내려가고, 그래도 안 되면 조각 크기 이하로 재생 가능한 조각을 만듭니다.
      </p>
    </section>
  )
}
```

`frontend/src/components/FFmpegBanner.tsx`:
```tsx
import type { FFmpegStatus } from '../lib/api'

interface Props {
  status: FFmpegStatus
  onDownload: () => void
}

export default function FFmpegBanner({ status, onDownload }: Props) {
  if (status.found) return null
  return (
    <section className="banner" role="alert">
      <div className="banner__text">
        <strong>FFmpeg가 필요합니다.</strong>
        <p>최초 1회 자동으로 내려받습니다(약 115MB). 이미 설치되어 있으면 자동으로 찾아 사용합니다.</p>
        {status.error && <p className="banner__error">{status.error}</p>}
      </div>
      <button className="btn btn--primary" onClick={onDownload} disabled={status.downloading}>
        {status.downloading ? `내려받는 중 ${Math.round(status.percent)}%` : 'FFmpeg 내려받기'}
      </button>
    </section>
  )
}
```

`frontend/src/components/Toolbar.tsx`:
```tsx
interface Props {
  running: boolean
  counts: { total: number; queued: number; finished: number }
  onStart: () => void
  onCancel: () => void
  onClear: () => void
}

export default function Toolbar({ running, counts, onStart, onCancel, onClear }: Props) {
  return (
    <div className="toolbar">
      <button className="btn btn--primary" onClick={onStart} disabled={running || counts.queued === 0}>
        압축 시작
      </button>
      <button className="btn" onClick={onCancel} disabled={!running}>
        취소
      </button>
      <button className="btn" onClick={onClear} disabled={running || counts.finished === 0}>
        완료 항목 지우기
      </button>
      <span className="toolbar__count">
        대기 {counts.queued} · 전체 {counts.total}
      </span>
    </div>
  )
}
```

- [ ] **Step 4: `frontend/src/App.tsx` 구현**

```tsx
import { useCallback, useEffect, useMemo, useState } from 'react'
import DropZone from './components/DropZone'
import FFmpegBanner from './components/FFmpegBanner'
import JobRow from './components/JobRow'
import SettingsPanel from './components/SettingsPanel'
import Toolbar from './components/Toolbar'
import {
  EVENT_FFMPEG_STATUS,
  EVENT_NOTICE,
  EVENT_QUEUE_UPDATE,
  app,
  on,
  requireApp,
} from './lib/api'
import type { FFmpegStatus, Job, Notice, Settings } from './lib/api'

const EMPTY_STATUS: FFmpegStatus = { found: false, path: '', downloading: false, percent: 0, error: '' }
const FINISHED = ['done', 'failed', 'skipped', 'canceled']

/** unwrap handles both Wails call shapes: value or [value]. */
function unwrap<T>(data: unknown): T | undefined {
  if (Array.isArray(data)) return data[0] as T
  return data as T
}

export default function App() {
  const api = app()
  const [settings, setSettings] = useState<Settings>({ targetMB: 5, partMB: 4 })
  const [jobs, setJobs] = useState<Job[]>([])
  const [status, setStatus] = useState<FFmpegStatus>(EMPTY_STATUS)
  const [notice, setNotice] = useState('')
  const [running, setRunning] = useState(false)

  useEffect(() => {
    if (!api) return
    void (async () => {
      try {
        const [s, j, st] = await Promise.all([api.GetSettings(), api.GetJobs(), api.GetFFmpegStatus()])
        setSettings(s)
        setJobs(j ?? [])
        setStatus(st)
      } catch (e) {
        setNotice(e instanceof Error ? e.message : String(e))
      }
    })()
  }, [api])

  useEffect(() => {
    if (!api) return
    const offQueue = on(EVENT_QUEUE_UPDATE, (data) => {
      const list = unwrap<Job[]>(data)
      if (list) setJobs(list)
      void api.IsRunning().then(setRunning).catch(() => {})
    })
    const offStatus = on(EVENT_FFMPEG_STATUS, (data) => {
      const next = unwrap<FFmpegStatus>(data)
      if (next) setStatus(next)
    })
    const offNotice = on(EVENT_NOTICE, (data) => {
      const n = unwrap<Notice>(data)
      if (n) setNotice(n.message)
    })
    return () => {
      offQueue()
      offStatus()
      offNotice()
    }
  }, [api])

  const onPick = useCallback(async () => {
    if (!api) return
    try {
      const paths = await api.OpenFileDialog()
      if (!paths || paths.length === 0) return
      const added = await api.AddFiles(paths)
      if (added?.length) setJobs((prev) => [...prev, ...added])
      setNotice('')
    } catch (e) {
      setNotice(e instanceof Error ? e.message : String(e))
    }
  }, [api])

  const onReveal = useCallback(
    async (path: string) => {
      try {
        await api?.RevealInExplorer(path)
      } catch (e) {
        setNotice(e instanceof Error ? e.message : String(e))
      }
    },
    [api],
  )

  const onRemove = useCallback(
    async (id: string) => {
      if (!api) return
      await api.RemoveJob(id)
      setJobs(await api.GetJobs())
    },
    [api],
  )

  const onClear = useCallback(async () => {
    if (!api) return
    await api.ClearFinished()
    setJobs(await api.GetJobs())
  }, [api])

  const onStart = useCallback(async () => {
    if (!api) return
    try {
      await api.StartProcessing()
      setRunning(true)
      setNotice('')
    } catch (e) {
      setNotice(e instanceof Error ? e.message : String(e))
    }
  }, [api])

  const onCancel = useCallback(async () => {
    if (!api) return
    await api.CancelJobs()
    setRunning(false)
  }, [api])

  const onSaveSettings = useCallback(async (targetMB: number, partMB: number) => {
    await requireApp().SaveSettings(targetMB, partMB)
    setSettings(await requireApp().GetSettings())
  }, [])

  const counts = useMemo(() => {
    const queued = jobs.filter((j) => j.status === 'queued').length
    const finished = jobs.filter((j) => FINISHED.includes(j.status)).length
    return { total: jobs.length, queued, finished }
  }, [jobs])

  if (!api) {
    return (
      <main className="app">
        <h1 className="app__title">VideoCompress</h1>
        <p className="error">데스크톱 앱에서 실행해 주세요. 이 화면은 Wails 앱 창에서만 동작합니다.</p>
      </main>
    )
  }

  return (
    <main className="app">
      <header className="app__header">
        <h1 className="app__title">VideoCompress</h1>
        <p className="app__subtitle">
          비디오를 {settings.targetMB}MB 이하로 압축하고, 어렵다면 {settings.partMB}MB 이하 조각으로 나눕니다.
        </p>
      </header>

      <FFmpegBanner status={status} onDownload={() => void api.DownloadFFmpeg().catch(() => {})} />

      <DropZone disabled={running} onPick={() => void onPick()} />

      <Toolbar running={running} counts={counts} onStart={() => void onStart()} onCancel={() => void onCancel()} onClear={() => void onClear()} />

      {notice && <p className="notice">{notice}</p>}

      <section className="jobs" aria-label="작업 목록">
        {jobs.length === 0 ? (
          <p className="jobs__empty">아직 추가된 파일이 없습니다.</p>
        ) : (
          <ul className="jobs__list">
            {jobs.map((job) => (
              <JobRow key={job.id} job={job} running={running} onReveal={(p) => void onReveal(p)} onRemove={(id) => void onRemove(id)} />
            ))}
          </ul>
        )}
      </section>

      <SettingsPanel settings={settings} running={running} onSave={onSaveSettings} />

      <footer className="app__footer">
        결과는 원본과 같은 폴더에 저장됩니다. CLI는 <code>vc --help</code>로 확인하세요.
      </footer>
    </main>
  )
}
```

- [ ] **Step 5: 진입점·스타일·HTML 정리**

`frontend/src/main.tsx`:
```tsx
import React from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import './styles.css'

const container = document.getElementById('root')
if (!container) {
  throw new Error('root 엘리먼트를 찾을 수 없습니다')
}

createRoot(container).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
```

`frontend/src/styles.css`:
```css
:root {
  --bg: #12141a;
  --panel: #1b1f27;
  --panel-2: #232936;
  --text: #e8ecf3;
  --muted: #9aa4b6;
  --accent: #4f8cff;
  --ok: #46c07a;
  --warn: #e0a63c;
  --err: #ef5f5f;
  --line: #2c3341;
  font-family: 'Segoe UI', 'Malgun Gothic', system-ui, sans-serif;
  color-scheme: dark;
}

* { box-sizing: border-box; }

body {
  margin: 0;
  background: var(--bg);
  color: var(--text);
}

.app {
  max-width: 900px;
  margin: 0 auto;
  padding: 24px 24px 48px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.app__header { display: flex; flex-direction: column; gap: 4px; }
.app__title { margin: 0; font-size: 24px; }
.app__subtitle { margin: 0; color: var(--muted); font-size: 14px; }
.app__footer { color: var(--muted); font-size: 12px; border-top: 1px solid var(--line); padding-top: 12px; }
.app__footer code { background: var(--panel-2); padding: 2px 6px; border-radius: 4px; }

.btn {
  border: 1px solid var(--line);
  background: var(--panel-2);
  color: var(--text);
  padding: 8px 14px;
  border-radius: 8px;
  cursor: pointer;
  font-size: 14px;
}
.btn:hover:enabled { border-color: var(--accent); }
.btn:disabled { opacity: 0.45; cursor: not-allowed; }
.btn--primary { background: var(--accent); border-color: var(--accent); color: #0d1117; font-weight: 600; }

.dropzone {
  border: 2px dashed var(--line);
  border-radius: 12px;
  padding: 28px 16px;
  text-align: center;
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s;
}
.dropzone--hover { border-color: var(--accent); background: rgba(79, 140, 255, 0.08); }
.dropzone--disabled { opacity: 0.5; cursor: not-allowed; }
.dropzone__title { margin: 0 0 6px; font-size: 16px; font-weight: 600; }
.dropzone__hint { margin: 0; color: var(--muted); font-size: 13px; }

.toolbar { display: flex; align-items: center; gap: 8px; }
.toolbar__count { margin-left: auto; color: var(--muted); font-size: 13px; }

.banner {
  display: flex; gap: 12px; align-items: center; justify-content: space-between;
  background: rgba(224, 166, 60, 0.12);
  border: 1px solid var(--warn);
  border-radius: 10px; padding: 12px 16px;
}
.banner p { margin: 4px 0 0; color: var(--muted); font-size: 13px; }
.banner__error { color: var(--err) !important; }

.jobs { min-height: 60px; }
.jobs__empty { color: var(--muted); font-size: 14px; }
.jobs__list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 10px; }

.job { background: var(--panel); border: 1px solid var(--line); border-radius: 10px; padding: 12px 14px; }
.job__head { display: flex; align-items: center; gap: 8px; }
.job__name { font-weight: 600; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.job__remove { background: none; border: none; color: var(--muted); font-size: 18px; cursor: pointer; line-height: 1; }
.job__remove:hover { color: var(--err); }

.chip { font-size: 12px; padding: 2px 8px; border-radius: 999px; border: 1px solid var(--line); color: var(--muted); }
.chip--running { color: var(--accent); border-color: var(--accent); }
.chip--done { color: var(--ok); border-color: var(--ok); }
.chip--failed { color: var(--err); border-color: var(--err); }
.chip--skipped { color: var(--warn); border-color: var(--warn); }

.job__bar { height: 6px; background: var(--panel-2); border-radius: 999px; margin: 10px 0 8px; overflow: hidden; }
.job__bar-fill { height: 100%; background: var(--accent); transition: width 0.2s; }
.job--done .job__bar-fill { background: var(--ok); }
.job--failed .job__bar-fill { background: var(--err); }

.job__meta { display: flex; gap: 12px; color: var(--muted); font-size: 13px; }
.job__detail, .job__time { margin-left: auto; }
.job__time { margin-left: 0; }

.job__outputs { list-style: none; margin: 8px 0 0; padding: 0; display: flex; flex-direction: column; gap: 4px; }
.job__outputs li { display: flex; gap: 8px; align-items: center; font-size: 13px; }
.job__outmeta { color: var(--muted); }

.link { background: none; border: none; color: var(--accent); cursor: pointer; padding: 0; font-size: 13px; }
.link:hover { text-decoration: underline; }

.job__error { margin: 8px 0 0; color: var(--err); font-size: 13px; }

.panel { background: var(--panel); border: 1px solid var(--line); border-radius: 10px; padding: 16px; display: flex; flex-direction: column; gap: 10px; }
.panel__title { margin: 0; font-size: 15px; }
.panel__hint { margin: 0; color: var(--muted); font-size: 12px; }
.field { display: flex; align-items: center; gap: 12px; font-size: 14px; }
.field span { width: 140px; color: var(--muted); }
.field input { flex: 1; background: var(--panel-2); border: 1px solid var(--line); border-radius: 8px; color: var(--text); padding: 8px 10px; }
.field input:disabled { opacity: 0.5; }

.error { color: var(--err); font-size: 13px; margin: 0; }
.ok { color: var(--ok); font-size: 13px; margin: 0; }
.notice { margin: 0; color: var(--warn); font-size: 13px; }
```

`frontend/index.html`: `<html lang="ko">`, `<title>VideoCompress</title>`로 수정한다.

템플릿 잔여물 삭제:
```powershell
Remove-Item -Recurse -Force -ErrorAction SilentlyContinue frontend/src/App.css, frontend/src/style.css, frontend/src/assets, frontend/src/logo.svg, frontend/src/assets/react.svg
```

- [ ] **Step 6: 테스트·빌드·실행 확인**

```powershell
npm --prefix frontend run test -- --run
npm --prefix frontend run build
go vet ./...
wails build -nopackage
.\build\bin\VideoCompress.exe
```
기대: 프런트 테스트 전부 통과, 빌드 성공, GUI에서 드롭/설정 저장/시작/취소/완료 항목 지우기/폴더 열기가 동작.

- [ ] **Step 7: 커밋**

```powershell
git add frontend
git commit -m "feat(ui): drop zone, job list, settings, ffmpeg banner and toolbar"
```

---

## Task 17: 문서와 최종 검증

**Files:**
- Create: `README.md`
- Modify: `.gitignore` (필요 시 보완)
- Test: 전체 스위트 실행(새 테스트 파일 없음)

**Interfaces:** 없음(문서·검증 태스크)

- [ ] **Step 1: `README.md` 작성**

```markdown
# VideoCompress

비디오 파일을 **목표 크기 이하의 단일 MP4**로 압축하고, 불가능하면 **조각 크기 이하의 재생 가능한 MP4 조각**으로 나눕니다.
결과는 원본과 같은 폴더에 저장되며 원본은 절대 수정·삭제되지 않습니다.

- 기본값: 목표 5MB, 조각 4MB (1MB = 1,000,000바이트)
- 코덱: H.264 + AAC (`libx264 -preset medium -profile:v high -pix_fmt yuv420p`, 2-pass ABR)
- 화질 하한: 1080p 1,500kbps / 720p 800kbps / 480p 400kbps. 480p에서도 목표를 못 맞추면 분할합니다.
- FFmpeg는 최초 1회 자동으로 내려받습니다(사용자 설치 불필요, 약 115MB).

## 사용법 (GUI)

1. `VideoCompress.exe` 실행
2. 창에 비디오 파일을 끌어다 놓기 (여러 개 가능)
3. 크기 설정에서 목표/조각 크기를 확인하거나 수정
4. **압축 시작**

처리 결과 이름:

- 단일: `이름_compressed.mp4`
- 분할: `이름_part01.mp4`, `이름_part02.mp4`, …
- 같은 이름이 이미 있으면 `이름_compressed (2).mp4`, `이름 (2)_part01.mp4`처럼 번호가 붙습니다(덮어쓰지 않음).

## 사용법 (CLI)

```
vc [옵션] <파일...>

  --target <MB>    목표 크기 (기본 5, 2~100)
  --part <MB>      조각 크기 (기본 4, 목표보다 작아야 함)
  --out <폴더>     출력 폴더 (기본: 원본과 같은 폴더)
  --dry-run        인코딩 없이 계획만 출력
  --json           결과를 JSON으로 출력
  --ffmpeg <경로>  사용할 ffmpeg 실행 파일
  --no-download    FFmpeg 자동 다운로드 금지
  --version        버전 출력
```

예시:

```powershell
vc --dry-run --target 5 --part 4 "D:\videos\clip.mov"
vc --target 5 --part 4 "D:\videos\clip.mov"
vc --json --target 3 --part 2 D:\videos\*.mp4
```

종료 코드: `0` 성공(건너뜀 포함), `1` 처리 실패, `2` 사용법·설정 오류, `3` FFmpeg 없음/다운로드 실패, `130` 취소.

## 빌드

필요 도구: Go 1.23+, Node 20+, [Wails CLI](https://wails.io) v2.11, (Windows) WebView2 런타임.

```powershell
npm --prefix frontend install
wails build -nopackage            # GUI: build/bin/VideoCompress.exe
go build -o bin/vc.exe ./cmd/vc   # CLI
```

## 테스트

```powershell
go vet ./...
go test ./...
npm --prefix frontend run test -- --run
```

FFmpeg가 필요한 통합 테스트는 FFmpeg가 없으면 자동으로 SKIP됩니다. 특정 바이너리를 쓰려면:

```powershell
$env:VC_TEST_FFMPEG = "$env:LOCALAPPDATA\VideoCompress\ffmpeg\bin\ffmpeg.exe"
go test ./internal/engine -v -timeout 40m
```

FFmpeg를 미리 받아 두려면:

```powershell
$env:VC_INSTALL_FFMPEG = "1"
go test ./internal/ffmpeg -run TestInstallRealFFmpeg -v -timeout 30m
```

## 파일 위치

| 항목 | 경로 |
| --- | --- |
| 설정 | `%AppData%\VideoCompress\settings.json` |
| FFmpeg | `%LocalAppData%\VideoCompress\ffmpeg\bin\` |
| 임시 작업 | `%LocalAppData%\VideoCompress\temp\vc-job-*\` (작업 종료 시 삭제) |

## 문제 해결

- **FFmpeg 다운로드 실패**: 네트워크/프록시를 확인하거나 [gyan.dev 빌드](https://www.gyan.dev/ffmpeg/builds/)를 직접 받아 `--ffmpeg <ffmpeg.exe 경로>`로 지정합니다.
- **Windows SmartScreen 경고**: 서명 없는 실행 파일이라 처음 실행 시 경고가 뜰 수 있습니다. "추가 정보 → 실행"을 선택합니다.
- **압축이 오래 걸림**: 2-pass 인코딩은 원본 길이의 1~3배 시간이 걸릴 수 있습니다. GUI에서는 취소할 수 있습니다.
- **화질이 낮음**: 목표 크기를 키우거나 조각 크기를 줄여 분할되게 하세요.

## v1 범위 밖

H.265/AV1, 하드웨어 인코더(NVENC/QSV), 자막·다중 오디오 트랙 보존, 동시 인코딩, GUI 출력 폴더 지정.
```

- [ ] **Step 2: `.gitignore` 최종 확인**

다음을 포함해야 한다:

```
frontend/node_modules/
# Keep the directory present (go:embed needs at least one file) while ignoring
# build output. The directory itself must not be ignored, or git never descends
# into it and the negation below has no effect.
frontend/dist/*
!frontend/dist/.gitkeep
frontend/wailsjs/
build/bin/
bin/
*.exe
!build/windows/*.exe
.wails-scaffold/
```

`frontend/dist/.gitkeep`이 존재하고 커밋되는지 확인한다:

```powershell
git check-ignore -v frontend/dist/.gitkeep   # 출력이 없어야 정상(무시되지 않음)
```

- [ ] **Step 3: 전체 검증 실행**

```powershell
go vet ./...
go test ./... -count=1
npm --prefix frontend run test -- --run
npm --prefix frontend run build
go build ./...
wails build -nopackage
```
기대: 모든 명령 성공. FFmpeg가 설치되어 있다면 통합 테스트까지 실제로 수행된다.

- [ ] **Step 4: 수동 종합 점검(체크리스트)**

- [ ] FFmpeg가 없는 상태에서 GUI를 켜면 다운로드 배너가 보이고, 버튼으로 내려받아 진행률이 갱신된다.
- [ ] 5초짜리 작은 MP4를 드롭하면 `_compressed.mp4`가 생기고 재생된다(원본 크기 이하).
- [ ] 10분짜리 1080p 영상을 목표 5MB로 처리하면 480p까지 내려가거나 `_partNN.mp4`로 분할된다.
- [ ] 한글·공백이 포함된 경로에서 정상 동작한다.
- [ ] 같은 파일을 두 번 처리하면 `... (2).mp4`가 생기고 기존 파일이 덮어써지지 않는다.
- [ ] 처리 중 **취소**하면 부분 산출물 없이 멈추고, 임시 폴더가 정리된다.
- [ ] 창을 닫으려 하면 진행 중일 때 확인 대화상자가 뜬다.
- [ ] `vc --dry-run` 결과와 GUI 결과의 모드/해상도가 일치한다.
- [ ] 분할 결과의 각 조각이 모두 재생되고, 조각 길이 합이 원본과 ±2초 내로 맞는다.

- [ ] **Step 5: 계획·스펙과 구현의 최종 대조**

`docs/superpowers/specs/2026-10-19-video-compress-design.md`의 결정 사항(해상도 사다리, 하한, 안전 계수 0.95, 이름 규칙, 검증 허용 오차, 자동 다운로드)이 모두 구현되었는지 확인한다. 차이가 있으면 스펙을 갱신하고 커밋한다.

- [ ] **Step 6: 최종 커밋**

```powershell
git add -A
git commit -m "docs: readme and final verification pass"
```

---

## 실행 완료 기준 (Definition of Done)

- `go vet ./...`, `go test ./...`, `npm --prefix frontend run test -- --run`, `npm --prefix frontend run build`, `wails build -nopackage`가 모두 성공한다.
- CLI `vc`와 GUI가 동일한 `internal/engine`을 사용하며, 같은 입력에 같은 모드/해상도 계획을 세운다.
- 목표 크기를 넘기지 못하는 단일 결과물이 최종 위치에 남지 않는다(초과 시 재시도 → 해상도 하향 → 분할).
- 어떤 실패/취소 상황에서도 원본이 변경되지 않고, 최종 폴더에 불완전한 산출물이 남지 않는다.
- 모든 사용자 노출 문자열이 한국어이고, 오류 메시지가 원인과 다음 행동을 알려준다.
