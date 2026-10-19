import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import SettingsPanel from './SettingsPanel'

describe('SettingsPanel', () => {
  it('shows the current settings', () => {
    render(<SettingsPanel settings={{ targetMB: 5, partMB: 4 }} running={false} onSave={vi.fn()} />)
    expect(screen.getByLabelText('목표 크기 (MB)')).toHaveValue('5')
    expect(screen.getByLabelText('조각 크기 (MB)')).toHaveValue('4')
  })

  it('rejects a part size that is not smaller than the target', async () => {
    const onSave = vi.fn()
    render(<SettingsPanel settings={{ targetMB: 5, partMB: 4 }} running={false} onSave={onSave} />)
    fireEvent.change(screen.getByLabelText('조각 크기 (MB)'), { target: { value: '6' } })
    fireEvent.click(screen.getByRole('button', { name: '설정 저장' }))
    expect(await screen.findByText(/조각 크기는/)).toBeInTheDocument()
    expect(onSave).not.toHaveBeenCalled()
  })

  it('saves valid values as numbers', async () => {
    const onSave = vi.fn(async () => {})
    render(<SettingsPanel settings={{ targetMB: 5, partMB: 4 }} running={false} onSave={onSave} />)
    fireEvent.change(screen.getByLabelText('목표 크기 (MB)'), { target: { value: '8' } })
    fireEvent.change(screen.getByLabelText('조각 크기 (MB)'), { target: { value: '3' } })
    fireEvent.click(screen.getByRole('button', { name: '설정 저장' }))
    await waitFor(() => expect(onSave).toHaveBeenCalledWith(8, 3))
  })

  it('disables the form while running', () => {
    render(<SettingsPanel settings={{ targetMB: 5, partMB: 4 }} running onSave={vi.fn()} />)
    expect(screen.getByRole('button', { name: '설정 저장' })).toBeDisabled()
  })

  it('surfaces save failures', async () => {
    const onSave = vi.fn(async () => {
      throw new Error('설정을 저장하지 못했습니다: 권한 없음')
    })
    render(<SettingsPanel settings={{ targetMB: 5, partMB: 4 }} running={false} onSave={onSave} />)
    fireEvent.click(screen.getByRole('button', { name: '설정 저장' }))
    expect(await screen.findByText(/권한 없음/)).toBeInTheDocument()
  })
})
