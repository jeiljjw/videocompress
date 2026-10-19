import { useEffect, useState } from 'react'
import type { Settings } from '../lib/api'

interface Props {
  settings: Settings
  running: boolean
  onSave: (targetMB: number, partMB: number) => Promise<void>
  /** Notified whenever the inputs become valid or invalid (spec: start is gated on this). */
  onValidChange?: (valid: boolean) => void
}

/** validate returns the first problem with the entered values, or ''. */
export function validateSettings(target: string, part: string): string {
  const t = Number(target)
  const p = Number(part)
  if (!Number.isInteger(t) || t < 2 || t > 100) return '목표 크기는 2~100MB 사이의 정수여야 합니다.'
  if (!Number.isInteger(p) || p < 1 || p >= t) return '조각 크기는 1MB 이상, 목표 크기보다 작아야 합니다.'
  return ''
}

export default function SettingsPanel({ settings, running, onSave, onValidChange }: Props) {
  const [target, setTarget] = useState(String(settings.targetMB))
  const [part, setPart] = useState(String(settings.partMB))
  const [saveError, setSaveError] = useState('')
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    setTarget(String(settings.targetMB))
    setPart(String(settings.partMB))
  }, [settings.targetMB, settings.partMB])

  // Validate as the user types (spec §7) and let the parent gate the start button.
  const error = validateSettings(target, part)
  useEffect(() => {
    onValidChange?.(error === '')
  }, [error, onValidChange])

  return (
    <section className="panel" aria-label="크기 설정">
      <h2 className="panel__title">크기 설정</h2>
      <label className="field">
        <span>목표 크기 (MB)</span>
        <input
          value={target}
          inputMode="numeric"
          disabled={running}
          onChange={(e) => {
            setTarget(e.target.value)
            setSaved(false)
            setSaveError('')
          }}
        />
      </label>
      <label className="field">
        <span>조각 크기 (MB)</span>
        <input
          value={part}
          inputMode="numeric"
          disabled={running}
          onChange={(e) => {
            setPart(e.target.value)
            setSaved(false)
            setSaveError('')
          }}
        />
      </label>
      {error && <p className="error">{error}</p>}
      {saveError && <p className="error">{saveError}</p>}
      {saved && !error && !saveError && <p className="ok">저장했습니다.</p>}
      <button
        className="btn"
        disabled={running || error !== ''}
        onClick={async () => {
          setSaveError('')
          setSaved(false)
          try {
            await onSave(Number(target), Number(part))
            setSaved(true)
          } catch (e) {
            setSaveError(e instanceof Error ? e.message : String(e))
          }
        }}
      >
        설정 저장
      </button>
      <p className="panel__hint">
        목표 크기를 넘기지 못하면 480p까지 내려가고, 그래도 안 되면 조각 크기 이하로 재생 가능한 조각을 만듭니다.
      </p>
    </section>
  )
}
