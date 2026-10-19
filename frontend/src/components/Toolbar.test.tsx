import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import Toolbar from './Toolbar'

const counts = { total: 3, queued: 1, finished: 2 }

describe('Toolbar', () => {
  it('enables start only with queued jobs and no run in progress', () => {
    const onStart = vi.fn()
    render(<Toolbar running={false} counts={counts} onStart={onStart} onCancel={() => {}} onClear={() => {}} />)
    fireEvent.click(screen.getByRole('button', { name: '압축 시작' }))
    expect(onStart).toHaveBeenCalled()
  })

  it('disables start with an empty queue', () => {
    render(<Toolbar running={false} counts={{ total: 0, queued: 0, finished: 0 }} onStart={() => {}} onCancel={() => {}} onClear={() => {}} />)
    expect(screen.getByRole('button', { name: '압축 시작' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '완료 항목 지우기' })).toBeDisabled()
  })

  it('enables cancel only while running', () => {
    const onCancel = vi.fn()
    render(<Toolbar running counts={counts} onStart={() => {}} onCancel={onCancel} onClear={() => {}} />)
    expect(screen.getByRole('button', { name: '압축 시작' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: '취소' }))
    expect(onCancel).toHaveBeenCalled()
  })
})
