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
