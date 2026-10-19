import { useEffect, useState } from 'react'
import type { Settings } from '../lib/api'

interface Props {
  settings: Settings
  running: boolean
  onSave: (targetMB: number, partMB: number) => Promise<void>
}

export default function SettingsPanel({ settings, running, onSave }: Props) {
  const [target, setTarget] = useState(String(settings.targetMB))
  const [part, setPart] = useState(String(settings.partMB))
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    setTarget(String(settings.targetMB))
    setPart(String(settings.partMB))
  }, [settings.targetMB, settings.partMB])

  const validate = (): string => {
    const t = Number(target)
    const p = Number(part)
    if (!Number.isInteger(t) || t < 2 || t > 100) return '목표 크기는 2~100MB 사이의 정수여야 합니다.'
    if (!Number.isInteger(p) || p < 1 || p >= t) return '조각 크기는 1MB 이상, 목표 크기보다 작아야 합니다.'
    return ''
  }

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
          }}
        />
      </label>
      {error && <p className="error">{error}</p>}
      {saved && !error && <p className="ok">저장했습니다.</p>}
      <button
        className="btn"
        disabled={running}
        onClick={async () => {
          const message = validate()
          setError(message)
          setSaved(false)
          if (message) return
          try {
            await onSave(Number(target), Number(part))
            setSaved(true)
          } catch (e) {
            setError(e instanceof Error ? e.message : String(e))
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
