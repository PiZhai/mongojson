import { useCallback, useEffect, useState } from 'react'
import { decideStewardTool, getStewardTool, getStewardTools } from '../api'
import type { StewardCatalogTool, StewardToolHostStatus } from '../types'

type Props = { open: boolean; onClose: () => void }

export function ToolLibraryDialog({ open, onClose }: Props) {
  const [items, setItems] = useState<StewardCatalogTool[]>([])
  const [selected, setSelected] = useState<StewardCatalogTool | null>(null)
  const [hosts, setHosts] = useState<StewardToolHostStatus[]>([])
  const [query, setQuery] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async (search = query) => {
    setBusy(true); setError('')
    try {
      const result = await getStewardTools(search)
      setItems(result.tools)
      setHosts(result.hosts || [])
      if (selected && !result.tools.some((item) => item.name === selected.name)) setSelected(null)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '工具目录加载失败')
    } finally { setBusy(false) }
  }, [query, selected])

  useEffect(() => {
    if (!open) return
    const timer = window.setTimeout(() => void load(''), 0)
    return () => window.clearTimeout(timer)
  }, [open]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (!open) return
    const listener = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    window.addEventListener('keydown', listener)
    return () => window.removeEventListener('keydown', listener)
  }, [onClose, open])
  if (!open) return null

  const inspect = async (name: string) => {
    setBusy(true); setError('')
    try { setSelected((await getStewardTool(name)).tool) }
    catch (reason) { setError(reason instanceof Error ? reason.message : '工具详情加载失败') }
    finally { setBusy(false) }
  }
  const decide = async (decision: 'enable' | 'disable' | 'test' | 'rollback' | 'delete') => {
    if (!selected) return
    setBusy(true); setError('')
    try {
      const result = await decideStewardTool(selected.name, decision)
      setSelected(result.tool)
      await load(query)
    } catch (reason) { setError(reason instanceof Error ? reason.message : '工具操作失败') }
    finally { setBusy(false) }
  }
  const strategy = selected?.active?.manifest?.dependency_strategy as Record<string, unknown> | undefined

  return <div className="steward-archive-modal" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose() }}>
    <section aria-modal="true" className="steward-tool-dialog" role="dialog">
      <header className="steward-archive-dialog-header">
        <div><h2>工具库</h2><small>持久化工具、不可变版本、依赖、测试和执行位置</small></div>
        <button className="steward-archive-close" onClick={onClose} type="button">关闭</button>
      </header>
      <form className="steward-tool-search" onSubmit={(event) => { event.preventDefault(); void load(query) }}>
        <input aria-label="搜索工具" onChange={(event) => setQuery(event.target.value)} placeholder="按名称或能力搜索" value={query} />
        <button className="steward-button steward-button-secondary" disabled={busy} type="submit">搜索</button>
      </form>
      <div className="steward-tool-hosts" aria-label="工具执行主机状态">
        {hosts.map((host) => <span className={host.online ? 'is-online' : 'is-offline'} key={host.target} title={host.summary}>
          <i aria-hidden="true" />{host.name}：{host.online ? '在线' : '离线'} · {host.transport}
        </span>)}
      </div>
      {error ? <div className="steward-tool-error" role="alert">{error}</div> : null}
      <div className="steward-tool-layout" aria-busy={busy}>
        <div className="steward-tool-list">
          {items.map((item) => <button className={selected?.name === item.name ? 'is-active' : ''} key={item.name} onClick={() => void inspect(item.name)} type="button">
            <strong>{item.name}</strong><span>{item.description}</span><small>{item.origin} · {item.active_version || '无版本'} · {item.execution_target} · {item.health_status}</small>
          </button>)}
          {!busy && items.length === 0 ? <p>没有匹配的工具。</p> : null}
        </div>
        <div className="steward-tool-detail">
          {!selected ? <p>选择一个工具查看 Manifest、依赖策略、测试与证据。</p> : <>
            <h3>{selected.name}</h3><p>{selected.description}</p>
            <dl><dt>来源 / 版本</dt><dd>{selected.origin} / {selected.active_version}</dd><dt>运行时 / 位置</dt><dd>{selected.active?.runtime || '—'} / {selected.execution_target}</dd><dt>健康</dt><dd>{selected.health_status} {selected.health_summary}</dd><dt>依赖策略</dt><dd>{String(strategy?.selected || 'none')}：{String(strategy?.selection_reason || '')}</dd><dt>内容哈希</dt><dd className="is-code">{selected.active?.content_sha256 || '—'}</dd></dl>
            <div className="steward-tool-actions">
              <button onClick={() => void decide(selected.enabled ? 'disable' : 'enable')} disabled={busy} type="button">{selected.enabled ? '停用' : '启用'}</button>
              <button onClick={() => void decide('test')} disabled={busy} type="button">重新测试</button>
              <button onClick={() => void decide('rollback')} disabled={busy || (selected.versions?.length || 0) < 2} type="button">回滚</button>
              {selected.origin !== 'builtin' && selected.origin !== 'platform' ? <button onClick={() => void decide('delete')} disabled={busy} type="button">删除</button> : null}
            </div>
            <details><summary>Manifest</summary><pre>{JSON.stringify(selected.active?.manifest || {}, null, 2)}</pre></details>
            <details><summary>SBOM 与来源</summary><pre>{JSON.stringify({ sbom: selected.active?.sbom, provenance: selected.active?.provenance }, null, 2)}</pre></details>
            <details><summary>依赖变化与测试</summary><pre>{JSON.stringify({ dependencies: selected.dependency_changes, tests: selected.recent_tests }, null, 2)}</pre></details>
          </>}
        </div>
      </div>
    </section>
  </div>
}
