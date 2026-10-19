import { useCallback, useState } from 'react'
import type { DragEvent } from 'react'

interface Props {
  disabled: boolean
  onPick: () => void
}

export default function DropZone({ disabled, onPick }: Props) {
  const [hover, setHover] = useState(false)

  const handleDragOver = useCallback(
    (e: DragEvent<HTMLDivElement>) => {
      e.preventDefault()
      if (!disabled) setHover(true)
    },
    [disabled],
  )

  return (
    <div
      className={`dropzone${hover ? ' dropzone--hover' : ''}${disabled ? ' dropzone--disabled' : ''}`}
      role="button"
      tabIndex={0}
      aria-disabled={disabled}
      onDragOver={handleDragOver}
      onDragLeave={() => setHover(false)}
      onDrop={(e) => {
        e.preventDefault()
        setHover(false)
      }}
      onClick={() => {
        if (!disabled) onPick()
      }}
      onKeyDown={(e) => {
        if (!disabled && (e.key === 'Enter' || e.key === ' ')) onPick()
      }}
    >
      <p className="dropzone__title">비디오 파일을 여기로 끌어다 놓으세요</p>
      <p className="dropzone__hint">또는 클릭해서 선택하세요 · 여러 개를 한 번에 추가할 수 있습니다</p>
    </div>
  )
}
