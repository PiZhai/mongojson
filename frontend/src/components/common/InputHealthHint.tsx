type InputHealthHintProps = {
  tone: 'ok' | 'warn' | 'error'
  text: string
}

export function InputHealthHint({ tone, text }: InputHealthHintProps) {
  return (
    <div className={`input-health-hint input-health-hint-${tone}`} role="status">
      <span className="input-health-dot" aria-hidden="true" />
      <span className="input-health-text">{text}</span>
    </div>
  )
}
