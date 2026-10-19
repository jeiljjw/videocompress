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
