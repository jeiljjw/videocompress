# 서드파티 고지 (Third-party notices)

이 프로젝트는 아래 소프트웨어를 사용하거나 사용자가 직접 내려받도록 안내합니다.
배포물에는 포함되지 않는 항목은 그렇게 표시했습니다.

## FFmpeg (런타임에 사용자가 내려받음, 배포물에 미포함)

- 프로젝트: FFmpeg — <https://ffmpeg.org/>
- 라이선스: FFmpeg는 기본적으로 LGPL v2.1 이상이며, 특정 빌드는 GPL이 적용됩니다.
  자세한 내용은 <https://ffmpeg.org/legal.html> 참고.
- 이 앱은 `https://www.gyan.dev/ffmpeg/builds/`의 Windows 릴리스(essentials) 빌드를
  내려받습니다. 이 빌드는 **GPL v3**로 배포되며, FFmpeg 바이너리는 별도 실행 파일로만
  호출됩니다(라이브러리 링크 없음). 따라서 이 저장소의 소스 코드에는 FFmpeg의
  라이선스가 전파되지 않습니다.
- H.264 인코딩은 `libx264`(GPL)를, AAC 인코딩은 FFmpeg 내장 `aac` 인코더를 사용합니다.
  내려받는 빌드에 포함된 라이선스 조건을 따릅니다.
- 이미 설치된 FFmpeg를 사용하도록 지정할 수도 있습니다(`settings.json`의 `ffmpegPath`,
  CLI `--ffmpeg`, GUI의 "FFmpeg 직접 선택"). 이 경우 해당 바이너리의 라이선스를 따릅니다.

## Go

- [Wails v2](https://github.com/wailsapp/wails) — MIT License
- Wails가 함께 가져오는 간접 의존성(예: `go-webview2`, `go-ole`, `gorilla/websocket`,
  `labstack/echo`, `golang.org/x/*` 등)은 각 프로젝트의 라이선스를 따릅니다.
  전체 목록은 `go.mod`와 `go.sum`에서 확인할 수 있습니다.

## 프런트엔드 (빌드 시에만 사용, 결과물에 번들됨)

- [React](https://react.dev/) — MIT License
- [Vite](https://vitejs.dev/) — MIT License
- [TypeScript](https://www.typescriptlang.org/) — Apache License 2.0
- [Vitest](https://vitest.dev/) — MIT License
- [@testing-library/react](https://testing-library.com/) — MIT License
- [jsdom](https://github.com/jsdom/jsdom) — MIT License
- `@vitejs/plugin-react`, `@types/react`, `@types/react-dom` — MIT License

전체 의존성 목록과 버전은 `frontend/package.json`을 참고하세요.
