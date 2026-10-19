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
import { jobListPayload, objectPayload } from './lib/payload'

const EMPTY_STATUS: FFmpegStatus = { found: false, path: '', downloading: false, percent: 0, error: '' }
const FINISHED = ['done', 'failed', 'skipped', 'canceled']

/** mergeJobs appends only jobs we do not already show, so an optimistic add
 * cannot duplicate rows the queue:update event already delivered. */
function mergeJobs(prev: Job[], added: Job[]): Job[] {
  const seen = new Set(prev.map((j) => j.id))
  const fresh = added.filter((j) => !seen.has(j.id))
  return fresh.length > 0 ? [...prev, ...fresh] : prev
}

export default function App() {
  const api = app()
  const [settings, setSettings] = useState<Settings>({ targetMB: 5, partMB: 4 })
  const [jobs, setJobs] = useState<Job[]>([])
  const [status, setStatus] = useState<FFmpegStatus>(EMPTY_STATUS)
  const [notice, setNotice] = useState('')
  const [running, setRunning] = useState(false)
  const [settingsValid, setSettingsValid] = useState(true)

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
      const list = jobListPayload(data)
      if (list) setJobs(list)
      void api
        .IsRunning()
        .then(setRunning)
        .catch(() => {})
    })
    const offStatus = on(EVENT_FFMPEG_STATUS, (data) => {
      const next = objectPayload<FFmpegStatus>(data)
      if (next) setStatus(next)
    })
    const offNotice = on(EVENT_NOTICE, (data) => {
      const n = objectPayload<Notice>(data)
      if (n && typeof n.message === 'string') setNotice(n.message)
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
      if (added?.length) setJobs((prev) => mergeJobs(prev, added))
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

  const onValidChange = useCallback((valid: boolean) => setSettingsValid(valid), [])

  const onPickFFmpeg = useCallback(async () => {
    if (!api) return
    try {
      await api.PickFFmpeg()
      setStatus(await api.GetFFmpegStatus())
      setNotice('')
    } catch (e) {
      setNotice(e instanceof Error ? e.message : String(e))
    }
  }, [api])

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

      <FFmpegBanner
        status={status}
        onDownload={() => void api.DownloadFFmpeg().catch(() => {})}
        onPickFFmpeg={() => void onPickFFmpeg()}
      />

      <DropZone disabled={running} onPick={() => void onPick()} />

      <Toolbar
        running={running}
        counts={counts}
        settingsValid={settingsValid}
        onStart={() => void onStart()}
        onCancel={() => void onCancel()}
        onClear={() => void onClear()}
      />

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

      <SettingsPanel settings={settings} running={running} onSave={onSaveSettings} onValidChange={onValidChange} />

      <footer className="app__footer">
        결과는 원본과 같은 폴더에 저장됩니다. CLI는 <code>vc --help</code>로 확인하세요.
      </footer>
    </main>
  )
}
