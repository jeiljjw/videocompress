import type { FFmpegStatus } from '../lib/api'

interface Props {
  status: FFmpegStatus
  onDownload: () => void
  /** Opens a file picker so a manually installed ffmpeg.exe can be used. */
  onPickFFmpeg?: () => void
}

export default function FFmpegBanner({ status, onDownload, onPickFFmpeg }: Props) {
  if (status.found) return null
  return (
    <section className="banner" role="alert">
      <div className="banner__text">
        <strong>FFmpeg가 필요합니다.</strong>
        <p>최초 1회 자동으로 내려받습니다(약 115MB). 이미 설치되어 있으면 자동으로 찾아 사용합니다.</p>
        {status.error && <p className="banner__error">{status.error}</p>}
      </div>
      <div className="banner__actions">
        <button className="btn btn--primary" onClick={onDownload} disabled={status.downloading}>
          {status.downloading ? `내려받는 중 ${Math.round(status.percent)}%` : 'FFmpeg 내려받기'}
        </button>
        {onPickFFmpeg && (
          <button className="btn" onClick={onPickFFmpeg} disabled={status.downloading}>
            FFmpeg 직접 선택
          </button>
        )}
      </div>
    </section>
  )
}
