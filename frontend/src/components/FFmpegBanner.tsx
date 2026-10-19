import type { FFmpegStatus } from '../lib/api'

interface Props {
  status: FFmpegStatus
  onDownload: () => void
}

export default function FFmpegBanner({ status, onDownload }: Props) {
  if (status.found) return null
  return (
    <section className="banner" role="alert">
      <div className="banner__text">
        <strong>FFmpeg가 필요합니다.</strong>
        <p>최초 1회 자동으로 내려받습니다(약 115MB). 이미 설치되어 있으면 자동으로 찾아 사용합니다.</p>
        {status.error && <p className="banner__error">{status.error}</p>}
      </div>
      <button className="btn btn--primary" onClick={onDownload} disabled={status.downloading}>
        {status.downloading ? `내려받는 중 ${Math.round(status.percent)}%` : 'FFmpeg 내려받기'}
      </button>
    </section>
  )
}
