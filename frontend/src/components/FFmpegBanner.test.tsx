import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import FFmpegBanner from './FFmpegBanner'

describe('FFmpegBanner', () => {
  it('renders nothing when FFmpeg is ready', () => {
    const { container } = render(
      <FFmpegBanner status={{ found: true, path: 'ff', downloading: false, percent: 0, error: '' }} onDownload={() => {}} />,
    )
    expect(container).toBeEmptyDOMElement()
  })

  it('offers a download button when missing', () => {
    const onDownload = vi.fn()
    render(<FFmpegBanner status={{ found: false, path: '', downloading: false, percent: 0, error: '찾을 수 없습니다' }} onDownload={onDownload} />)
    fireEvent.click(screen.getByRole('button', { name: 'FFmpeg 내려받기' }))
    expect(onDownload).toHaveBeenCalled()
    expect(screen.getByText('찾을 수 없습니다')).toBeInTheDocument()
  })

  it('shows download progress and disables the button', () => {
    render(<FFmpegBanner status={{ found: false, path: '', downloading: true, percent: 42, error: '' }} onDownload={() => {}} />)
    expect(screen.getByRole('button', { name: /내려받는 중 42%/ })).toBeDisabled()
  })
})
