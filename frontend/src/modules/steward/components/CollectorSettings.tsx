import { useState } from 'react'
import type { StewardCollectorConfig } from '../types'

type Props = {
  busy: boolean
  collector: StewardCollectorConfig
  onSave: (settings: Record<string, unknown>) => Promise<void>
}

export function CollectorSettings({ busy, collector, onSave }: Props) {
  const settings = collector.settings ?? {}
  const configuredPaths = Array.isArray(settings.paths)
    ? settings.paths.filter((item): item is string => typeof item === 'string')
    : []
  const [paths, setPaths] = useState(configuredPaths.join('; '))
  const [maxDepth, setMaxDepth] = useState(Number(settings.max_depth ?? 1))
	const defaultEndpoint = collector.name === 'screenpipe-bridge' ? 'http://127.0.0.1:3030' : 'http://127.0.0.1:5600'
	const [endpoint, setEndpoint] = useState(String(settings.endpoint ?? defaultEndpoint))
	const [pinnedVersion, setPinnedVersion] = useState(String(settings.pinned_version ?? ''))

  if (collector.name === 'screenpipe-bridge' || collector.name === 'activitywatch-bridge') {
		return (
			<div className="steward-collector-settings">
				<input aria-label={`${collector.name} 本地端点`} disabled={busy} onChange={(event) => setEndpoint(event.target.value)} value={endpoint} />
				{collector.name === 'screenpipe-bridge' ? (
					<input aria-label="Screenpipe 固定版本" disabled={busy} onChange={(event) => setPinnedVersion(event.target.value)} placeholder="release 标签或 commit" value={pinnedVersion} />
				) : null}
				<button
					className="steward-icon-button steward-button-secondary"
					disabled={busy || (collector.name === 'screenpipe-bridge' && !pinnedVersion.trim())}
					onClick={() => onSave({ endpoint: endpoint.trim(), limit: 100, ...(collector.name === 'screenpipe-bridge' ? { pinned_version: pinnedVersion.trim(), keyboard_content: false } : {}) })}
					type="button"
				>
					保存连接
				</button>
			</div>
		)
	}

	if (collector.name !== 'watched-directory') return null

  return (
    <div className="steward-collector-settings">
      <input
        aria-label="监控目录"
        disabled={busy}
        onChange={(event) => setPaths(event.target.value)}
        placeholder="C:\Users\me\Documents; D:\Projects"
        value={paths}
      />
      <select aria-label="目录扫描深度" disabled={busy} onChange={(event) => setMaxDepth(Number(event.target.value))} value={maxDepth}>
        <option value={0}>仅当前目录</option>
        <option value={1}>向下一层</option>
        <option value={2}>向下两层</option>
        <option value={3}>向下三层</option>
      </select>
      <button
        className="steward-icon-button steward-button-secondary"
        disabled={busy}
        onClick={() => onSave({
          paths: paths.split(';').map((item) => item.trim()).filter(Boolean),
          max_depth: maxDepth,
        })}
        type="button"
      >
        保存范围
      </button>
    </div>
  )
}
