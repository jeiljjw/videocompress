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
