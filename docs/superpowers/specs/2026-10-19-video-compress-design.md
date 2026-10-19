# VideoCompress 설계 문서

- 작성일: 2026-10-19
- 상태: 승인됨 (구현 계획 작성 전)
- 기술 스택: Wails v2 + React/TypeScript + Go

## 1. 목적

비디오 파일을 놓으면 **5MB 이하의 단일 MP4**로 압축한다. 화질 하한(480p)에서도
5MB를 달성할 수 없으면 **4MB 이하의 재생 가능한 조각**으로 분할한다.
GUI(드래그 앤 드롭)와 CLI 두 가지 진입점을 제공하며, 두 진입점은 같은 엔진을 공유한다.

### 성공 기준

1. 목표 크기를 초과하는 단일 출력 파일이 생성되지 않는다.
2. 분할 모드에서 4MB를 초과하는 조각이 생성되지 않는다.
3. 모든 출력 파일은 단독으로 재생 가능한 MP4다.
4. 원본 파일은 절대 수정·삭제되지 않는다.
5. GUI에서 여러 파일을 순차 처리하고, 파일별 진행률과 결과를 볼 수 있다.
6. CLI에서 같은 작업을 자동화할 수 있고, 종료 코드로 결과를 판별할 수 있다.

### 용어와 단위

- **MB = 1,000,000바이트** (플랫폼 업로드 제한이 보통 10진수 기준이므로 보수적으로 해석)
- 기본 목표: 5MB (5,000,000바이트), 기본 조각 크기: 4MB (4,000,000바이트)
- 목표·조각 크기는 정수 MB로 설정하며, `1 ≤ 조각 < 목표 ≤ 100`을 강제한다.
- "화질 하한"은 해상도별 최소 비디오 비트레이트를 의미한다.

## 2. 사용자 흐름

### GUI

1. 창에 파일을 끌어다 놓거나 "파일 추가" 버튼으로 선택한다.
2. 큐에 추가된 파일을 확인하고 필요하면 제거한다.
3. 목표 크기와 조각 크기를 조정한다(기본 5MB / 4MB).
4. "시작"을 누르면 큐를 **순차 처리**한다.
5. 각 항목에 단계, 진행률, 선택된 해상도, 결과(단일/분할/건너뜀/실패)를 표시한다.
6. 완료된 항목의 출력 폴더를 열 수 있다. 실패 항목은 오류 메시지를 보여준다.
7. FFmpeg가 없으면 다운로드 진행률과 재시도/직접 선택 버튼을 표시한다.

### CLI

```
vc <files...> [--target 5] [--part 4] [--out-dir DIR] [--json]
              [--dry-run] [--ffmpeg PATH] [--no-download] [--verbose]
```

- `--dry-run`: 인코딩 없이 예상 모드·해상도·조각 수를 출력한다. ffprobe는 사용한다.
- `--json`: 최종 결과 배열을 stdout에 출력한다. 진행률은 stderr에 출력한다.
- `--no-download`: FFmpeg 자동 다운로드를 금지한다.
- `--out-dir`: 출력 폴더를 지정한다. 생략 시 원본과 같은 폴더.
- 종료 코드: `0` 전체 성공(건너뜀 포함), `1` 일반 오류/일부 실패,
  `2` FFmpeg 없음, `3` 목표 달성 불가.

## 3. 아키텍처

```
Wails v2 (Go) ── React/TypeScript (Vite) UI
   │
   ├─ internal/media     ffprobe 분석, 비트레이트/해상도/분할 계획 계산
   ├─ internal/engine    압축·분할 실행, 검증, 임시파일·원자적 저장
   ├─ internal/ffmpeg    ffmpeg 탐색·자동 다운로드·실행·진행률 파싱
   ├─ internal/jobs      큐, 상태, 취소, 이벤트 발행
   ├─ internal/config    설정 저장/검증, 경로 결정
   └─ cmd/vc             CLI 진입점 (엔진 공유)
```

### 원칙

- **단일 진실 원천**: Go의 큐가 작업 상태를 소유한다. React는 이벤트로 받은
  전체 스냅샷을 렌더링만 한다.
- **엔진은 UI를 모른다**: `engine`은 `Prober`, `Runner`, `Downloader` 인터페이스에만
  의존한다. CLI와 GUI는 이 인터페이스의 구현을 주입한다.
- **순차 처리**: v1은 동시에 한 파일만 인코딩한다. FFmpeg CPU/디스크 경합과
  크기 예측 오차를 줄인다.
- **원본 불변**: 원본 경로는 읽기만 한다. 모든 출력은 임시 폴더에서 완성·검증한
  뒤 최종 위치로 이동한다.

### 데이터 모델

```go
type Status string // queued|probing|compressing|splitting|verifying|done|failed|canceled|skipped

type Job struct {
    ID        string
    InputPath string
    SizeBytes int64
    Status    Status
    Stage     string
    Progress  float64 // 0..100
    Result    *Result
    Error     string
}

type Result struct {
    Mode           string   // single|split|skipped
    Outputs        []Output
    Width, Height  int
    VideoKbps      int
    SegmentSeconds int
}

type Output struct {
    Path      string
    Bytes     int64
    Duration  float64
    Width     int
    Height    int
}
```

- GUI 이벤트: `queue:update` (전체 큐 스냅샷, 100ms 스로틀),
  `ffmpeg:download` (다운로드 진행률), `app:notice` (사용자 알림).
- 설정은 `os.UserConfigDir()/VideoCompress/settings.json`에 저장한다.
- 임시 파일·FFmpeg 캐시는 `os.UserCacheDir()/VideoCompress/` 아래에 둔다.

## 4. 압축 알고리즘

### 4.1 분석

ffprobe로 다음을 읽는다.

- `format.duration` (없으면 스트림 duration의 최댓값)
- 첫 비디오 스트림의 `codec_type`, `width`, `height`, `avg_frame_rate`
- 첫 오디오 스트림의 `codec_name`, `bit_rate`, `channels`, `sample_rate`

비디오 스트림이 없거나 duration이 0이면 해당 파일을 실패 처리한다.
원본 크기가 목표 이하이면 재인코딩하지 않고 `skipped`로 표시한다.

### 4.2 비트레이트 계산

```
totalKbps   = 목표바이트 × 8 ÷ durationSec ÷ 1000
videoKbps   = floor(totalKbps × 0.95) − audioKbps
videoKbps   = clamp(videoKbps, 100, 12000)
```

- `0.95`: 컨테이너 오버헤드와 VBR 편차를 흡수하는 안전 계수.
- `audioKbps`: 오디오 없음 → 0, 복사 → 원본 오디오 비트레이트(미상이면 128),
  재인코딩 → 스테레오 96 / 모노 64.
- `videoKbps ≤ 0`이면 분할 모드로 간다. 분할 계획도 불가능하면 실패 처리한다.

### 4.3 해상도 사다리

| 단계 | 박스(가로 영상) | 박스(세로 영상) | 화질 하한 |
|---|---|---|---|
| 1080p | 1920×1080 | 1080×1920 | 1,500 kbps |
| 720p | 1280×720 | 720×1280 | 800 kbps |
| 480p | 854×480 | 480×854 | 400 kbps |

- 비율을 유지하며 박스에 맞추고, 짝수 차원으로 맞춘다.
- **업스케일하지 않는다.** 원본이 박스보다 작으면 원본 크기를 유지한다.
- 하한은 결과 해상도의 짧은 변 기준으로 정한다:
  `≥1000 → 1500`, `≥700 → 800`, 그 외 `→ 400`.
- 위에서 아래로 내려가며 `videoKbps ≥ 하한`인 첫 단계를 선택한다.
- 어느 단계도 만족하지 못하면 분할 모드로 전환한다.

### 4.4 인코딩과 재시도

- H.264(`libx264`), preset `medium`, profile `high`, `yuv420p`, 2-pass ABR.
- 오디오: 원본이 AAC이고 비트레이트가 128kbps 이하이면 복사, 아니면
  AAC 96kbps(스테레오) / 64kbps(모노)로 인코딩. 샘플레이트는 유지하고
  채널은 최대 2개로 제한한다.
- 자막·데이터 스트림은 v1에서 제외한다. 첫 비디오/첫 오디오만 매핑한다.
- ffmpeg의 자동 회전을 신뢰하고, 출력에 회전 메타데이터를 남기지 않는다.
- 단일 출력은 `-movflags +faststart`를 적용한다.

**재시도 규칙**

1. 계산된 `videoKbps`로 인코딩하고 실제 크기를 측정한다.
2. 목표 초과 시
   `videoKbps = max(100, floor(videoKbps × 목표바이트 ÷ 실제바이트 × 0.95))`로
   최대 2회 재인코딩한다(단계당 최대 3회 인코딩).
3. 그래도 초과하면 다음 낮은 해상도 단계로 내려가 1~2를 반복한다.
4. 480p에서도 초과하면 분할 모드로 전환한다.
5. 목표보다 작게 나온 경우 위로 재시도하지 않는다. 첫 성공 결과를 채택한다.

### 4.5 분할

- 분할 해상도는 `min(원본, 720p 박스)`다. 원본이 더 작으면 원본을 유지한다.
- 화질 하한은 결과 해상도의 짧은 변 기준 `≥700 → 800`, 그 외 `400`.
- 조각 길이 후보 `[120, 60, 30, 15]`초를 긴 것부터 검사한다.

```
조각 videoKbps = floor(조각바이트 × 8 ÷ 조각길이 ÷ 1000 × 0.95) − audioKbps
```

- 하한을 만족하는 가장 긴 조각 길이를 선택한다(파일 수 최소화).
- 모두 만족하지 못하면 15초를 사용하고 저화질 가능성을 결과에 기록한다.
- 분할 모드의 오디오는 항상 AAC 96/64kbps로 재인코딩해 조각 경계를 깨끗하게 한다.
- **전체 영상을 한 번만 2-pass 인코딩**하면서
  `-force_key_frames "expr:gte(t,n_forced*조각길이)"`로 경계마다 키프레임을
  강제하고, segment muxer로 `-segment_time`에 맞춰 분할한다.
  `-reset_timestamps 1`로 각 조각의 타임스탬프를 0부터 시작시킨다.
- 분할 후 **조각별 크기를 검사**한다. 초과 조각은 원본의 해당 구간을
  `videoKbps × 조각바이트 ÷ 실제바이트 × 0.95`로 최대 2회 재인코딩한다.
  그래도 초과하면 해당 조각을 반으로 나누어 다시 인코딩한다.
- 각 조각을 `-c copy -movflags +faststart`로 리먹싱한 뒤 최종 검증한다.
- 마지막 조각은 짧을 수 있다.

### 4.6 검증

- 단일: 크기 ≤ 목표, 비디오 스트림 존재, duration이 원본과 ±2초 이내.
- 분할: 각 조각 크기 ≤ 조각 크기, 비디오 스트림 존재, 조각 duration 합이
  원본과 ±2초 이내, 첫 조각이 0초에서 시작.
- 검증 실패 시 해당 작업은 `failed`가 되며 **어떤 출력도 최종 위치로 이동하지 않는다.**

## 5. FFmpeg 준비

탐색 순서:

1. CLI `--ffmpeg` 또는 환경 변수 `VC_FFMPEG`
2. 설정에 저장된 경로
3. 앱 실행 파일 옆 `bin/ffmpeg.exe`
4. `%LOCALAPPDATA%\VideoCompress\ffmpeg\bin\ffmpeg.exe`
5. `PATH`

각 후보는 `ffmpeg -version`과 `ffprobe -version` 실행으로 검증한다.
ffprobe는 ffmpeg와 같은 폴더에서 찾는다.

자동 다운로드:

- URL: `https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip`
- HTTPS, 최대 300MB, 30분 타임아웃, `Content-Length` 기반 진행률.
- ZIP 안에서 `ffmpeg.exe`, `ffprobe.exe`만 재귀적으로 찾아 추출한다.
  Zip-slip을 방지하기 위해 기본 파일명만 사용한다.
- 임시 폴더에 내려받아 검증 후 `ffmpeg/bin/`으로 원자적 이동한다.
- 실패 시 GUI는 재시도와 "직접 선택"을 제공하고, CLI는 종료 코드 2를 반환한다.
- 앱은 FFmpeg를 별도 프로세스로 호출한다. `THIRD_PARTY_NOTICES.md`에
  출처와 라이선스(GPL) 링크를 기록하고, About 화면에서 확인할 수 있게 한다.

## 6. 파일과 경로

### 출력 이름

- 단일: `<원본이름>_compressed.mp4`
- 분할: `<원본이름>_part01.mp4`, `<원본이름>_part02.mp4`, …
  - 번호 자릿수는 `max(2, 전체 조각 수의 자릿수)`.
- 같은 이름이 이미 있으면 덮어쓰지 않는다.
  - 단일: `_compressed (2).mp4`처럼 번호를 올린다.
  - 분할: `<원본이름> (2)_part01.mp4`처럼 그룹 번호를 올린다.
- 기본 저장 위치는 원본과 같은 폴더다. `--out-dir`이 있으면 그곳을 사용한다.

### 임시 파일

- 작업별 임시 폴더: `%LOCALAPPDATA%\VideoCompress\temp\<jobID>\`
- 2-pass 로그 파일도 이 폴더에 둔다.
- 성공·실패·취소 시 폴더를 삭제한다. 시작 시 24시간 지난 잔여 폴더를 정리한다.
- 최종 이동은 `os.Rename`을 사용하고, 다른 드라이브면 복사 후 삭제로 대체한다.

## 7. GUI 상세

- Wails 네이티브 드래그 앤 드롭(`runtime.OnFileDrop`)을 사용한다.
- 레이아웃: 상단 설정 바(목표/조각 MB, FFmpeg 상태), 중앙 드롭 영역 + 큐,
  하단 시작/취소/완료 항목 지우기.
- 큐 항목: 파일명, 원본 크기, 상태 배지, 진행률 바, 단계 텍스트,
  선택 해상도, 결과 파일 목록(폴더 열기).
- 상태 색상: 대기(회색), 진행(파랑), 성공(초록), 건너뜀(노랑), 실패(빨강).
- 설정 값은 즉시 검증하고 잘못된 값이면 시작 버튼을 비활성화한다.
- 창을 닫아도 진행 중인 인코딩은 확인 후 취소하고 종료한다.
- 한국어 UI. 다크 테마 기본.

## 8. CLI 상세

- 사람이 읽는 출력 예:
  `[1/3] movie.mp4: 120.4MB → 4.8MB (1080p→720p, single)`
  `[2/3] long.mp4: 300.1MB → 12.0MB (720p, 3 parts, 30s each)`
- `--json` 결과 예:

```json
[
  {
    "input": "C:\\v\\movie.mp4",
    "status": "done",
    "mode": "single",
    "outputs": [{"path": "C:\\v\\movie_compressed.mp4", "bytes": 4800000}],
    "width": 1280,
    "height": 720,
    "videoKbps": 640,
    "segmentSeconds": 0,
    "error": ""
  }
]
```

- 진행률은 stderr에 `파일명 42% (compressing)` 형태로 출력한다.
- `--verbose`는 ffmpeg 명령과 stderr 마지막 20줄을 출력한다.
- 파일 하나가 실패해도 나머지를 계속 처리하고, 마지막에 요약한다.

## 9. 오류 처리

| 상황 | 동작 |
|---|---|
| FFmpeg 없음/다운로드 실패 | 작업 시작 불가, 안내 + 재시도/직접 선택, CLI 종료 코드 2 |
| 비디오 스트림 없음 | 해당 작업 실패, 큐는 계속 |
| duration 0/미상 | 해당 작업 실패, 명확한 메시지 |
| 목표 달성 불가 | 분할로 폴백, 분할도 불가하면 실패(종료 코드 3) |
| 인코딩 중 ffmpeg 비정상 종료 | 작업 실패, stderr 마지막 20줄 기록 |
| 검증 실패 | 작업 실패, 임시 출력 삭제, 최종 파일 미생성 |
| 취소 | ffmpeg 프로세스 종료, 임시물 삭제, `canceled` 표시 |
| 출력 이름 충돌 | 덮어쓰지 않고 새 이름 사용 |
| 디스크 공간 부족 | 작업 실패, 원본·기존 출력 보존 |

- ffmpeg는 `exec.CommandContext`로 실행하고 셸을 거치지 않는다.
- 로그: `%LOCALAPPDATA%\VideoCompress\logs\app.log`, 5MB에서 회전, 3개 보관.

## 10. 테스트 전략

### 단위 테스트 (Go)

- 비트레이트 계산(오디오 복사/재인코딩/없음, 극단적 duration)
- 해상도 사다리 선택(가로/세로/정사각/저해상도 원본, 업스케일 금지)
- 조각 길이 선택과 조각 수 계산
- 파일명 생성과 충돌 회피
- 설정 검증(`조각 < 목표`, 범위)
- ffprobe JSON 파싱, 진행률 파싱
- ZIP 추출 경로 안전성

### 통합 테스트 (ffmpeg 필요, 없으면 skip)

ffmpeg의 `testsrc2`와 `sine`으로 합성 영상을 만든다.

1. 20초 720p + 오디오 → 단일 출력 ≤ 5MB, 재생 가능
2. 180초 1080p → 분할, 모든 조각 ≤ 4MB, duration 합 ±2초, 각 조각 재생 가능
3. 오디오 없는 영상
4. 이미 목표 이하인 파일 → `skipped`, 출력 없음
5. 1MB 목표의 극단 케이스
6. 취소 → 임시물 정리, 최종 파일 없음
7. 한글 파일명 경로

`VC_TEST_FFMPEG` 환경 변수로 ffmpeg 경로를 지정할 수 있다.

### 프론트엔드 테스트

- 바이트/MB 포맷, 상태 라벨, 설정 검증 함수
- 큐 항목 렌더링(상태별 배지·진행률)
- `npm run build` 성공

### 수동 체크리스트

- 실제 파일 드래그 앤 드롭, 다중 파일 순차 처리
- FFmpeg 최초 다운로드와 재시도
- 창 닫기/취소
- 출력 폴더 열기

### 검증 명령

```
go vet ./...
go test ./...
npm --prefix frontend run test -- --run
npm --prefix frontend run build
wails build
```

## 11. 프로젝트 구조

```
.
├── main.go, app.go          Wails 진입점과 바인딩
├── wails.json
├── go.mod
├── cmd/vc/main.go           CLI
├── internal/
│   ├── config/              설정, 경로
│   ├── ffmpeg/              탐색, 다운로드, 실행, 진행률
│   ├── media/               ffprobe 분석, 계획 계산
│   ├── engine/              압축/분할/검증/저장
│   └── jobs/                큐, 상태, 이벤트, 취소
├── frontend/                React + TypeScript + Vite
├── build/                   Wails 빌드 자산
├── docs/superpowers/specs/  설계 문서
└── THIRD_PARTY_NOTICES.md
```

## 12. 범위 밖 (v1)

- H.265/HEVC, 하드웨어 인코더(NVENC/QSV)
- 자막·챕터·다중 오디오 트랙 보존
- 동시 다중 인코딩
- GUI에서 출력 폴더 지정(CLI는 지원)
- macOS/Linux 패키징, 자동 업데이트, 코드 서명
- 원본보다 큰 목표에서의 업스케일/고화질 재인코딩

## 13. 위험과 완화

| 위험 | 완화 |
|---|---|
| 2-pass ABR도 VBR 편차로 목표를 넘김 | 5% 안전 계수 + 실제 크기 측정 후 최대 2회 재시도 |
| 분할 조각 일부가 4MB 초과 | 조각별 검증, 해당 구간만 재인코딩, 필요 시 반으로 분할 |
| 긴 영상의 인코딩 시간 | 순차 처리, 진행률 표시, 언제든 취소 가능 |
| FFmpeg 다운로드 실패/차단 | 수동 경로 선택, `--no-download`, 재시도 |
| 720p/30초 조각의 화질 저하 | 사용자가 선택한 트레이드오프. 결과에 해상도·비트레이트 표시 |
| 한글/공백 경로 | Go `exec` 인자 전달, 통합 테스트로 검증 |
| 임시물 누적으로 디스크 소모 | 작업 종료 시 삭제, 시작 시 24시간 지난 폴더 정리 |

## 14. 구현 순서 개요

1. 프로젝트 스캐폴딩(Wails + React, `cmd/vc`)
2. `media` 계획 계산 + 단위 테스트
3. `ffmpeg` 탐색/실행/진행률 + 단위 테스트
4. `engine` 단일 압축 + 검증 + 통합 테스트
5. `engine` 분할 + 검증 + 통합 테스트
6. `jobs` 큐/취소/이벤트 + CLI
7. Wails 바인딩과 React UI
8. FFmpeg 자동 다운로드
9. 전체 검증과 빌드

세부 단계는 별도의 구현 계획 문서에서 다룬다.
