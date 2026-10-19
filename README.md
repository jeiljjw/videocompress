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
vc --json --target 5 --part 4 D:\videos\clip.mp4
```

종료 코드: `0` 성공(건너뜀 포함), `1` 처리 실패, `2` 사용법·설정 오류, `3` FFmpeg 없음/다운로드 실패, `130` 취소.

## 빌드

필요 도구: Go 1.23+, Node 20+, [Wails CLI](https://wails.io) v2.11, (Windows) WebView2 런타임.

```powershell
npm --prefix frontend install
wails build -nopackage            # GUI: build/bin/VideoCompress.exe
go build -o bin/vc.exe ./cmd/vc   # CLI
```

`frontend/wailsjs/`는 `wails build`가 생성하는 바인딩이며 저장소에 커밋하지 않습니다.
앱 코드는 `window.go.main.App`을 직접 감싸 쓰므로(`frontend/src/lib/api.ts`) 이 폴더가 없어도 프런트엔드 빌드와 테스트가 동작합니다.

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
