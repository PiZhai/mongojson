import { mongoModes } from './modeMeta'
import type { MongoMode } from './types'

type ModeSwitchProps = {
  mode: MongoMode
  onModeChange: (mode: MongoMode) => void
}

export function ModeSwitch({ mode, onModeChange }: ModeSwitchProps) {
  return (
    <section className="mode-strip" aria-label="MongoDB JSON 模块切换">
      <div className="mode-strip-copy">
        <p className="mode-strip-label">Modules</p>
        <p className="mode-strip-text">主操作区只展示当前模块，避免对比、表格、Shell 和字符串处理互相串场。</p>
      </div>
      <div className="mode-switch" role="tablist" aria-label="工具模式">
        {mongoModes.map(([nextMode, label]) => (
          <button
            aria-selected={mode === nextMode}
            className={`mode-switch-button${mode === nextMode ? ' mode-switch-button-active' : ''}`}
            key={nextMode}
            onClick={() => onModeChange(nextMode)}
            role="tab"
            type="button"
          >
            {label}
          </button>
        ))}
      </div>
    </section>
  )
}
