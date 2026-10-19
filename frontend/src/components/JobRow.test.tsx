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
