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
          {job.outputs.map((p) => {
            const bytes = outputBytes(job, p)
            return (
              <li key={p}>
                <button className="link" onClick={() => onReveal(p)}>
                  {basename(p)}
                </button>
                {bytes !== undefined && <span className="job__outmeta">{formatBytes(bytes)}</span>}
              </li>
            )
          })}
        </ul>
      )}
      {job.status === 'failed' && job.message && <p className="job__error">{job.message}</p>}
    </li>
  )
}
