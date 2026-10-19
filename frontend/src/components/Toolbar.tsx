interface Props {
  running: boolean
  counts: { total: number; queued: number; finished: number }
  /** The size inputs are valid; start is blocked while they are not (spec §7). */
  settingsValid?: boolean
  onStart: () => void
  onCancel: () => void
  onClear: () => void
}

export default function Toolbar({ running, counts, settingsValid = true, onStart, onCancel, onClear }: Props) {
  return (
    <div className="toolbar">
      <button className="btn btn--primary" onClick={onStart} disabled={running || counts.queued === 0 || !settingsValid}>
        압축 시작
      </button>
      <button className="btn" onClick={onCancel} disabled={!running}>
        취소
      </button>
      <button className="btn" onClick={onClear} disabled={running || counts.finished === 0}>
        완료 항목 지우기
      </button>
      <span className="toolbar__count">
        대기 {counts.queued} · 전체 {counts.total}
      </span>
    </div>
  )
}
