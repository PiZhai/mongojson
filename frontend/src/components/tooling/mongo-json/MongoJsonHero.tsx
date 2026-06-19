import { modeDescriptions } from './modeMeta'
import type { MongoMode } from './types'

type MongoJsonHeroProps = {
  activeModeLabel: string
  mode: MongoMode
}

export function MongoJsonHero({ activeModeLabel, mode }: MongoJsonHeroProps) {
  return (
    <div className="page-hero">
      <div className="page-hero-main">
        <h2 className="page-hero-title">MongoDB JSON 数据调试工作台</h2>
        <p className="page-hero-copy">
          覆盖扩展类型格式化、结构对比、表格化校验、Shell 语句整理和字符串转义，保留了现有工具最有价值的能力。
        </p>
        <div className="page-hero-meta">
          <span className="meta-chip">Extended JSON</span>
          <span className="meta-chip">结构对比</span>
          <span className="meta-chip">表格视图</span>
          <span className="meta-chip">Shell</span>
        </div>
      </div>
      <div className="page-hero-side">
        <div className="hero-stat-grid">
          <article className="hero-stat">
            <span className="hero-stat-label">当前模块</span>
            <strong className="hero-stat-value">{activeModeLabel}</strong>
          </article>
          <article className="hero-stat">
            <span className="hero-stat-label">可用能力</span>
            <strong className="hero-stat-value">6</strong>
          </article>
          <article className="hero-stat hero-stat-wide">
            <span className="hero-stat-label">当前工作说明</span>
            <strong className="hero-stat-value">{modeDescriptions[mode]}</strong>
          </article>
        </div>
      </div>
    </div>
  )
}
