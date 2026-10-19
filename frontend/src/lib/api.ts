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
  PickFFmpeg(): Promise<string>
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
