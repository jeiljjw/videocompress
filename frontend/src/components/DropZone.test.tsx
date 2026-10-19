import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import DropZone from './DropZone'

describe('DropZone', () => {
  it('calls onPick when clicked', () => {
    const onPick = vi.fn()
    render(<DropZone disabled={false} onPick={onPick} />)
    fireEvent.click(screen.getByRole('button'))
    expect(onPick).toHaveBeenCalledTimes(1)
  })

  it('ignores clicks while disabled', () => {
    const onPick = vi.fn()
    render(<DropZone disabled onPick={onPick} />)
    fireEvent.click(screen.getByRole('button'))
    expect(onPick).not.toHaveBeenCalled()
  })

  it('highlights on drag over and clears on drag leave', () => {
    render(<DropZone disabled={false} onPick={() => {}} />)
    const zone = screen.getByRole('button')
    fireEvent.dragOver(zone)
    expect(zone.className).toContain('dropzone--hover')
    fireEvent.dragLeave(zone)
    expect(zone.className).not.toContain('dropzone--hover')
  })

  it('does not highlight while disabled', () => {
    render(<DropZone disabled onPick={() => {}} />)
    const zone = screen.getByRole('button')
    fireEvent.dragOver(zone)
    expect(zone.className).not.toContain('dropzone--hover')
  })
})
