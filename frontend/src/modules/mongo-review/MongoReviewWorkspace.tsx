import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { CodeEditor } from '../../components/editor/CodeEditor'
import { MONGO_LANGUAGE_ID } from '../../lib/editor/mongoLanguage'
import type { MongoDiagnostic } from '../../lib/mongodb-core'
import * as reviewApi from './api'
import type {
  Environment,
  EnvironmentName,
  ParseResult,
  QueryRule,
  RepositoryProject,
  RepositoryStatement,
  RepositoryTask,
  RepositoryTaskSummary,
  Review,
  ScriptRecord,
} from './types'
import './styles.css'

const environmentNames: EnvironmentName[] = ['demo', 'test', 'stag', 'prod']
const initialSource = `// 新增活动配置
db.getCollection("activity").insertOne({
  code: "ACTIVITY_2026",
  enabled: true,
  priority: 10
})

// 停用历史配置
db.getCollection("activity").updateMany(
  { code: "ACTIVITY_OLD", enabled: true },
  { $set: { enabled: false } }
)`

type Tab = 'review' | 'environments' | 'rules'

function errorText(error: unknown) {
  if (!(error instanceof Error)) return '操作失败'
  try {
    const payload = JSON.parse(error.message) as { error?: string }
    return payload.error ?? error.message
  } catch {
    return error.message
  }
}

function diagnosticsForEditor(parsed: ParseResult | null): MongoDiagnostic[] {
  if (!parsed) return []
  return [...parsed.diagnostics, ...parsed.operations.flatMap((operation) => operation.diagnostics)]
    .map((item) => ({ ...item, source: 'parser' }))
}

function EnvironmentSettings({
  environments,
  reload,
}: {
  environments: Environment[]
  reload: () => Promise<void>
}) {
  const [forms, setForms] = useState<Record<string, { uri: string; database: string }>>({})
  const [status, setStatus] = useState<Record<string, string>>({})

  const update = (name: string, key: 'uri' | 'database', value: string) => {
    setForms((current) => ({
      ...current,
      [name]: { uri: current[name]?.uri ?? '', database: current[name]?.database ?? '', [key]: value },
    }))
  }

  return (
    <section className="mongo-review-settings-grid" aria-label="MongoDB 环境连接">
      {environmentNames.map((name) => {
        const current = environments.find((item) => item.environment === name)
        const form = forms[name] ?? { uri: '', database: current?.database_name ?? '' }
        const hasDraftConnection = Boolean(form.uri.trim() && form.database.trim())
        const canTestConnection = Boolean(current?.configured || hasDraftConnection)
        return (
          <article className="mongo-review-card" key={name}>
            <div className="mongo-review-card-heading">
              <div>
                <span className={`mongo-review-env-dot ${current?.configured ? 'is-ready' : ''}`} />
                <strong>{name.toUpperCase()}</strong>
              </div>
              <span className="mongo-review-muted">{current?.configured ? '已配置' : '未配置'}</span>
            </div>
            <label>
              MongoDB 连接串
              <input
                autoComplete="new-password"
                onChange={(event) => update(name, 'uri', event.target.value)}
                placeholder={current?.configured ? '输入新连接串以覆盖现有配置' : 'mongodb://readonly:***@host:27017'}
                type="password"
                value={form.uri}
              />
            </label>
            <label>
              数据库名
              <input
                onChange={(event) => update(name, 'database', event.target.value)}
                placeholder="业务数据库"
                value={form.database}
              />
            </label>
            <div className="mongo-review-actions">
              <button
                className="secondary-button"
                disabled={!canTestConnection}
                onClick={async () => {
                  setStatus((value) => ({ ...value, [name]: '正在测试...' }))
                  try {
                    await reviewApi.testEnvironment(
                      name,
                      hasDraftConnection ? form.uri.trim() : undefined,
                      hasDraftConnection ? form.database.trim() : undefined,
                    )
                    setStatus((value) => ({ ...value, [name]: '连接正常' }))
                  } catch (error) {
                    setStatus((value) => ({ ...value, [name]: errorText(error) }))
                  }
                }}
                type="button"
              >
                测试连接
              </button>
              <button
                className="primary-button"
                disabled={!form.uri || !form.database}
                onClick={async () => {
                  setStatus((value) => ({ ...value, [name]: '正在保存...' }))
                  try {
                    await reviewApi.saveEnvironment(name, form.uri, form.database)
                    setForms((value) => ({ ...value, [name]: { ...form, uri: '' } }))
                    await reload()
                    setStatus((value) => ({ ...value, [name]: '已加密保存' }))
                  } catch (error) {
                    setStatus((value) => ({ ...value, [name]: errorText(error) }))
                  }
                }}
                type="button"
              >
                保存
              </button>
            </div>
            {status[name] ? <p className="mongo-review-inline-status">{status[name]}</p> : null}
          </article>
        )
      })}
    </section>
  )
}

function RuleSettings({ rules, reload }: { rules: QueryRule[]; reload: () => Promise<void> }) {
  const [rule, setRule] = useState<QueryRule>({
    name: '',
    collection: '',
    field_mappings: [{ document_path: '', query_field: '' }],
  })
  const [message, setMessage] = useState('')

  return (
    <div className="mongo-review-rule-layout">
      <section className="mongo-review-card">
        <h2>添加查询规则</h2>
        <label>
          规则名称
          <input value={rule.name} onChange={(event) => setRule({ ...rule, name: event.target.value })} />
        </label>
        <label>
          集合名称
          <input
            placeholder="activity"
            value={rule.collection}
            onChange={(event) => setRule({ ...rule, collection: event.target.value })}
          />
        </label>
        <div className="mongo-review-mappings">
          {rule.field_mappings.map((mapping, index) => (
            <div className="mongo-review-mapping-row" key={index}>
              <input
                aria-label={`脚本文档字段 ${index + 1}`}
                placeholder="脚本文档字段，如 code"
                value={mapping.document_path}
                onChange={(event) => {
                  const mappings = [...rule.field_mappings]
                  mappings[index] = { ...mapping, document_path: event.target.value }
                  setRule({ ...rule, field_mappings: mappings })
                }}
              />
              <span>→</span>
              <input
                aria-label={`数据库查询字段 ${index + 1}`}
                placeholder="数据库字段，如 code"
                value={mapping.query_field}
                onChange={(event) => {
                  const mappings = [...rule.field_mappings]
                  mappings[index] = { ...mapping, query_field: event.target.value }
                  setRule({ ...rule, field_mappings: mappings })
                }}
              />
            </div>
          ))}
        </div>
        <div className="mongo-review-actions">
          <button
            className="secondary-button"
            onClick={() => setRule({
              ...rule,
              field_mappings: [...rule.field_mappings, { document_path: '', query_field: '' }],
            })}
            type="button"
          >
            添加字段
          </button>
          <button
            className="primary-button"
            onClick={async () => {
              try {
                await reviewApi.saveRule(rule)
                setRule({ name: '', collection: '', field_mappings: [{ document_path: '', query_field: '' }] })
                await reload()
                setMessage('规则已保存')
              } catch (error) {
                setMessage(errorText(error))
              }
            }}
            type="button"
          >
            保存规则
          </button>
        </div>
        {message ? <p className="mongo-review-inline-status">{message}</p> : null}
      </section>
      <section className="mongo-review-card">
        <h2>已有规则</h2>
        <div className="mongo-review-rule-list">
          {rules.map((item) => (
            <article key={item.id}>
              <div>
                <strong>{item.name}</strong>
                <span>{item.collection}</span>
              </div>
              <code>
                {item.field_mappings.map((mapping) => `${mapping.document_path} → ${mapping.query_field}`).join(', ')}
              </code>
              <button
                className="text-button danger"
                onClick={async () => {
                  if (!item.id) return
                  await reviewApi.deleteRule(item.id)
                  await reload()
                }}
                type="button"
              >
                删除
              </button>
            </article>
          ))}
          {rules.length === 0 ? <p className="mongo-review-empty">还没有查询规则。</p> : null}
        </div>
      </section>
    </div>
  )
}

function ResultPanel({ review }: { review: Review | null }) {
  if (!review) {
    return <div className="mongo-review-empty">运行审查后，这里会按环境和操作展示影响范围与字段差异。</div>
  }
  return (
    <div className="mongo-review-results">
      <div className="mongo-review-review-status">
        <strong>任务状态：{review.status}</strong>
        <span>{review.results.length} 个环境操作结果</span>
      </div>
      {review.error ? <div className="mongo-review-alert is-error">{review.error}</div> : null}
      {review.results.map((result, resultIndex) => (
        <article className="mongo-review-result-card" key={`${result.operation_id}-${result.environment}-${resultIndex}`}>
          <header>
            <div>
              <span className={`mongo-review-status-badge is-${result.status}`}>{result.status}</span>
              <strong>{result.environment.toUpperCase()}</strong>
              <code>{result.operation_id}</code>
            </div>
            <span>匹配 {result.match_count}{result.truncated ? '+' : ''} 条</span>
          </header>
          {result.message ? <p>{result.message}</p> : null}
          {result.documents?.map((document, documentIndex) => (
            <div className="mongo-review-document" key={documentIndex}>
              {document.modified_paths?.length ? (
                <p className="mongo-review-modified">预计修改：{document.modified_paths.join(', ')}</p>
              ) : null}
              {document.uncertain_paths?.length ? (
                <p className="mongo-review-uncertain">无法确定：{document.uncertain_paths.join(', ')}</p>
              ) : null}
              {document.differences?.map((difference) => (
                <div
                  className={`mongo-review-difference is-${difference.kind}`}
                  key={difference.path}
                  title={`数据库实际记录：${JSON.stringify(difference.database, null, 2)}`}
                >
                  <code>{difference.path}</code>
                  <span>{difference.kind === 'changed' ? '字段不同' : difference.kind}</span>
                  <pre>{JSON.stringify(difference.script, null, 2)}</pre>
                </div>
              ))}
              {!document.differences?.length && document.before ? (
                <details>
                  <summary>查看数据库记录</summary>
                  <pre>{JSON.stringify(document.before, null, 2)}</pre>
                </details>
              ) : null}
            </div>
          ))}
        </article>
      ))}
    </div>
  )
}

export function MongoReviewWorkspace() {
  const [tab, setTab] = useState<Tab>('review')
  const [source, setSource] = useState(initialSource)
  const [title, setTitle] = useState('活动配置审查')
  const [script, setScript] = useState<ScriptRecord | null>(null)
  const [parsed, setParsed] = useState<ParseResult | null>(null)
  const [review, setReview] = useState<Review | null>(null)
  const [environments, setEnvironments] = useState<Environment[]>([])
  const [selectedEnvironments, setSelectedEnvironments] = useState<EnvironmentName[]>([])
  const [rules, setRules] = useState<QueryRule[]>([])
  const [ruleSelections, setRuleSelections] = useState<Record<string, string>>({})
  const [operationDescriptions, setOperationDescriptions] = useState<Record<string, string>>({})
  const [repositoryProjects, setRepositoryProjects] = useState<RepositoryProject[]>([])
  const [repositoryTasks, setRepositoryTasks] = useState<RepositoryTaskSummary[]>([])
  const [selectedProject, setSelectedProject] = useState('')
  const [selectedTaskKey, setSelectedTaskKey] = useState('')
  const [taskSearch, setTaskSearch] = useState('')
  const [repositoryTask, setRepositoryTask] = useState<RepositoryTask | null>(null)
  const [repositoryBusy, setRepositoryBusy] = useState(false)
  const [activeStatementIndex, setActiveStatementIndex] = useState(0)
  const workspaceRef = useRef<HTMLDivElement | null>(null)
  const statementRefs = useRef<Array<HTMLElement | null>>([])
  const [manualOpen, setManualOpen] = useState(false)
  const [manualProject, setManualProject] = useState('')
  const [manualTaskFolder, setManualTaskFolder] = useState('')
  const [manualNewTaskFolder, setManualNewTaskFolder] = useState('')
  const [manualFilePath, setManualFilePath] = useState('prod/script.js')
  const [savedScripts, setSavedScripts] = useState<ScriptRecord[]>([])
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)

  const reloadEnvironments = useCallback(async () => {
    const response = await reviewApi.listEnvironments()
    const items = response.environments ?? []
    setEnvironments(items)
    setSelectedEnvironments((current) => current.length
      ? current
      : items.filter((item) => item.configured).map((item) => item.environment))
  }, [])
  const reloadRules = useCallback(async () => {
    const response = await reviewApi.listRules()
    setRules(response.query_rules ?? [])
  }, [])
  const reloadScripts = useCallback(async () => {
    const response = await reviewApi.listScripts()
    setSavedScripts(response.scripts ?? [])
  }, [])
  const reloadRepositoryIndex = useCallback(async () => {
    const response = await reviewApi.listRepositoryIndex()
    const projects = response.projects ?? []
    setRepositoryProjects(projects)
    setRepositoryTasks(response.tasks ?? [])
    setManualProject((current) => current || projects[0]?.name || '')
  }, [])

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void Promise.all([
        reloadEnvironments(),
        reloadRules(),
        reloadScripts(),
        reloadRepositoryIndex(),
      ]).catch((error) => setMessage(errorText(error)))
    }, 0)
    return () => window.clearTimeout(timer)
  }, [reloadEnvironments, reloadRepositoryIndex, reloadRules, reloadScripts])

  const activeReviewID = review?.id
  const activeReviewStatus = review?.status
  useEffect(() => {
    if (!activeReviewID || !activeReviewStatus || !['queued', 'running'].includes(activeReviewStatus)) return
    return reviewApi.subscribeReview(
      activeReviewID,
      setReview,
      () => setMessage('实时连接中断，可重新发起审查。'),
    )
  }, [activeReviewID, activeReviewStatus])

  const editorDiagnostics = useMemo(() => diagnosticsForEditor(parsed), [parsed])
  const collectionRules = (collection: string) => rules.filter((rule) => rule.collection === collection)
  const visibleTasks = useMemo(
    () => repositoryTasks
      .filter((task) =>
        !selectedProject || task.locations.some((location) => location.project === selectedProject))
      .sort((left, right) => right.key.localeCompare(left.key, 'en', {
        numeric: true,
        sensitivity: 'base',
      })),
    [repositoryTasks, selectedProject],
  )
  const manualTaskFolders = useMemo(
    () => repositoryProjects.find((project) => project.name === manualProject)?.task_folders ?? [],
    [manualProject, repositoryProjects],
  )
  const repositoryStatements = useMemo(
    () => repositoryTask?.files.flatMap((file) => file.statements) ?? [],
    [repositoryTask],
  )

  useEffect(() => {
    statementRefs.current = statementRefs.current.slice(0, repositoryStatements.length)
    if (!selectedTaskKey || repositoryStatements.length === 0) return

    let animationFrame: number | null = null
    const syncActiveStatement = () => {
      animationFrame = null
      const scrollContainer = workspaceRef.current
      if (!scrollContainer) return
      const containerBounds = scrollContainer.getBoundingClientRect()
      const viewportCenter = containerBounds.top + scrollContainer.clientHeight / 2
      let closestIndex = 0
      let closestDistance = Number.POSITIVE_INFINITY
      statementRefs.current.forEach((element, index) => {
        if (!element) return
        const bounds = element.getBoundingClientRect()
        const distance = Math.abs(bounds.top + bounds.height / 2 - viewportCenter)
        if (distance < closestDistance) {
          closestDistance = distance
          closestIndex = index
        }
      })
      setActiveStatementIndex((current) => current === closestIndex ? current : closestIndex)
    }
    const scheduleSync = () => {
      if (animationFrame !== null) return
      animationFrame = window.requestAnimationFrame(syncActiveStatement)
    }

    scheduleSync()
    const scrollContainer = workspaceRef.current
    scrollContainer?.addEventListener('scroll', scheduleSync)
    window.addEventListener('resize', scheduleSync)
    return () => {
      scrollContainer?.removeEventListener('scroll', scheduleSync)
      window.removeEventListener('resize', scheduleSync)
      if (animationFrame !== null) window.cancelAnimationFrame(animationFrame)
    }
  }, [repositoryStatements.length, selectedTaskKey])

  const scrollToAdjacentStatement = (direction: -1 | 1) => {
    const scrollContainer = workspaceRef.current
    if (!scrollContainer) return
    const containerBounds = scrollContainer.getBoundingClientRect()
    const viewportCenter = containerBounds.top + scrollContainer.clientHeight / 2
    let currentIndex = activeStatementIndex
    let closestDistance = Number.POSITIVE_INFINITY
    statementRefs.current.forEach((element, index) => {
      if (!element) return
      const bounds = element.getBoundingClientRect()
      const distance = Math.abs(bounds.top + bounds.height / 2 - viewportCenter)
      if (distance < closestDistance) {
        closestDistance = distance
        currentIndex = index
      }
    })
    const targetIndex = Math.min(
      Math.max(currentIndex + direction, 0),
      repositoryStatements.length - 1,
    )
    const target = statementRefs.current[targetIndex]
    if (!target) return
    const targetBounds = target.getBoundingClientRect()
    scrollContainer.scrollTop = Math.max(
      0,
      scrollContainer.scrollTop + targetBounds.top - containerBounds.top - 18,
    )
    setActiveStatementIndex(targetIndex)
  }

  const loadRepositoryTask = async (taskKey: string) => {
    setTaskSearch(taskKey)
    setSelectedTaskKey(taskKey)
    setRepositoryTask(null)
    setActiveStatementIndex(0)
    if (!taskKey) return
    setRepositoryBusy(true)
    setMessage(`正在聚合 ${taskKey} 在各项目中的脚本...`)
    try {
      const response = await reviewApi.getRepositoryTask(taskKey)
      setRepositoryTask(response.task)
      const statementCount = response.task.files.reduce((total, file) => total + file.statements.length, 0)
      setMessage(`${taskKey}：${response.task.locations.length} 个项目位置，${statementCount} 条独立语句。`)
    } catch (error) {
      setMessage(errorText(error))
    } finally {
      setRepositoryBusy(false)
    }
  }

  const loadRepositoryStatement = async (statement: RepositoryStatement) => {
    const filename = statement.file_path.split('/').pop()?.replace(/\.js$/i, '') ?? 'script'
    setSource(statement.source)
    setTitle(`${selectedTaskKey}-${statement.project}-${filename}-${statement.index + 1}`)
    setScript({ title: filename, source: statement.source, origin_path: statement.file_path })
    setReview(null)
    setBusy(true)
    try {
      const result = await reviewApi.parseScript(statement.source)
      setParsed(result)
      setOperationDescriptions(Object.fromEntries(
        result.operations.map((operation) => [operation.id, operation.description]),
      ))
      setMessage(`已载入 ${statement.file_path} 的第 ${statement.index + 1} 条独立语句。`)
    } catch (error) {
      setParsed(null)
      setMessage(errorText(error))
    } finally {
      setBusy(false)
    }
  }

  const openRepositoryStatement = async (statement: RepositoryStatement) => {
    await loadRepositoryStatement(statement)
    setSelectedTaskKey('')
    setRepositoryTask(null)
  }

  const runParse = async () => {
    setBusy(true)
    setMessage('正在解析脚本...')
    try {
      const result = await reviewApi.parseScript(source)
      setParsed(result)
      setOperationDescriptions((current) => Object.fromEntries(
        result.operations.map((operation) => [operation.id, current[operation.id] ?? operation.description]),
      ))
      setMessage(`识别到 ${result.operations.length} 条操作，${diagnosticsForEditor(result).length} 条提示。`)
      return result
    } catch (error) {
      setMessage(errorText(error))
      return null
    } finally {
      setBusy(false)
    }
  }

  return (
    <div
      className="page-shell mongo-review-page"
      data-layout-region="mongo-review-workspace"
      ref={workspaceRef}
    >
      <nav className="mongo-review-tabs" aria-label="脚本审查模块">
        {([
          ['review', '脚本审查'],
          ['environments', '环境连接'],
          ['rules', '查询规则'],
        ] as Array<[Tab, string]>).map(([value, label]) => (
          <button
            className={tab === value ? 'is-active' : ''}
            key={value}
            onClick={() => setTab(value)}
            type="button"
          >
            {label}
          </button>
        ))}
      </nav>

      {tab === 'environments' ? (
        <EnvironmentSettings environments={environments} reload={reloadEnvironments} />
      ) : null}
      {tab === 'rules' ? <RuleSettings rules={rules} reload={reloadRules} /> : null}

      {tab === 'review' ? (
        <>
          <section className="mongo-review-source-bar">
            <label>
              已保存脚本
              <select
                value={script?.id ?? ''}
                onChange={(event) => {
                  const selected = savedScripts.find((item) => item.id === event.target.value)
                  if (!selected) return
                  setScript(selected)
                  setTitle(selected.title)
                  setSource(selected.source)
                  setParsed(selected.operations ? { operations: selected.operations, diagnostics: [] } : null)
                  setOperationDescriptions(Object.fromEntries(
                    (selected.operations ?? []).map((operation) => [operation.id, operation.description]),
                  ))
                }}
              >
                <option value="">新脚本</option>
                {savedScripts.map((item) => <option key={item.id} value={item.id}>{item.title}</option>)}
              </select>
            </label>
            <label>
              项目范围
              <select
                value={selectedProject}
                onChange={(event) => {
                  setSelectedProject(event.target.value)
                  setTaskSearch('')
                  setSelectedTaskKey('')
                  setRepositoryTask(null)
                }}
              >
                <option value="">全部项目</option>
                {repositoryProjects.map((project) => (
                  <option key={project.name} value={project.name}>{project.name}</option>
                ))}
              </select>
            </label>
            <label>
              MCC 任务
              <input
                autoComplete="off"
                className="mongo-review-task-search"
                list="mongo-review-task-options"
                onChange={(event) => {
                  const value = event.target.value.toUpperCase()
                  setTaskSearch(value)
                  const matchedTask = visibleTasks.find((task) => task.key === value)
                  if (matchedTask) {
                    if (matchedTask.key !== selectedTaskKey) void loadRepositoryTask(matchedTask.key)
                    return
                  }
                  setSelectedTaskKey('')
                  setRepositoryTask(null)
                }}
                placeholder="输入或选择 MCC 任务"
                value={taskSearch}
              />
              <datalist id="mongo-review-task-options">
                {visibleTasks.map((task) => (
                  <option
                    key={task.key}
                    label={`${task.locations.length} 个项目 · ${task.file_count} 个文件`}
                    value={task.key}
                  />
                ))}
              </datalist>
            </label>
            <button className="secondary-button" onClick={() => setManualOpen((value) => !value)} type="button">
              {manualOpen ? '收起手动创建' : '手动创建仓库脚本'}
            </button>
          </section>

          {manualOpen ? (
            <section className="mongo-review-manual-create">
              <div>
                <h2>手动创建 JS 文件</h2>
                <p>选择项目，选择已有 MCC 任务文件夹或输入新任务文件夹，再把当前编辑器内容写入新文件。</p>
              </div>
              <label>
                项目文件夹
                <select
                  value={manualProject}
                  onChange={(event) => {
                    setManualProject(event.target.value)
                    setManualTaskFolder('')
                  }}
                >
                  {repositoryProjects.map((project) => (
                    <option key={project.name} value={project.name}>{project.name}</option>
                  ))}
                </select>
              </label>
              <label>
                已有任务文件夹
                <select value={manualTaskFolder} onChange={(event) => setManualTaskFolder(event.target.value)}>
                  <option value="">创建新任务文件夹</option>
                  {manualTaskFolders.map((folder) => <option key={folder} value={folder}>{folder}</option>)}
                </select>
              </label>
              {!manualTaskFolder ? (
                <label>
                  新任务文件夹
                  <input
                    onChange={(event) => setManualNewTaskFolder(event.target.value)}
                    placeholder="例如 MCC-15200@activity"
                    value={manualNewTaskFolder}
                  />
                </label>
              ) : null}
              <label>
                JS 文件相对路径
                <input
                  onChange={(event) => setManualFilePath(event.target.value)}
                  placeholder="例如 prod/activity.js"
                  value={manualFilePath}
                />
              </label>
              <button
                className="primary-button"
                disabled={
                  busy || !manualProject || !(manualTaskFolder || manualNewTaskFolder.trim()) ||
                  !manualFilePath.trim() || !source.trim()
                }
                onClick={async () => {
                  const taskFolder = manualTaskFolder || manualNewTaskFolder.trim()
                  setBusy(true)
                  try {
                    const response = await reviewApi.createRepositoryFile({
                      project: manualProject,
                      task_folder: taskFolder,
                      file_path: manualFilePath.trim(),
                      source,
                    })
                    await reloadRepositoryIndex()
                    const taskKey = taskFolder.match(/^MCC-\d+/i)?.[0].toUpperCase() ?? ''
                    setSelectedProject(manualProject)
                    setScript({ title, source, origin_path: response.file.path })
                    setManualOpen(false)
                    if (taskKey) await loadRepositoryTask(taskKey)
                    setMessage(`已创建 ${response.file.path}，不会覆盖已有文件。`)
                  } catch (error) {
                    setMessage(errorText(error))
                  } finally {
                    setBusy(false)
                  }
                }}
                type="button"
              >
                创建 JS 并写入当前脚本
              </button>
            </section>
          ) : null}

          {selectedTaskKey ? (
            <section className="mongo-review-statement-list" aria-label={`${selectedTaskKey} 独立脚本`}>
              <header className="mongo-review-statement-list-heading">
                <div>
                  <h2>{selectedTaskKey} · {repositoryStatements.length} 条独立脚本</h2>
                  <p>按项目文件路径和文件内语句位置依次展示。</p>
                </div>
                {repositoryBusy ? <span>正在解析...</span> : null}
              </header>
              {repositoryStatements.map((statement, index) => {
                const operation = statement.operation
                const availableRules = collectionRules(operation.collection)
                return (
                  <article
                    className="mongo-review-statement-row"
                    data-statement-index={index}
                    key={statement.id}
                    ref={(element) => {
                      statementRefs.current[index] = element
                    }}
                  >
                    <div className="mongo-review-statement-code">
                      <header>
                        <strong>独立脚本 {index + 1}</strong>
                        <span>{statement.project} · 文件内第 {statement.index + 1} 条</span>
                        <code title={statement.file_path}>{statement.file_path}</code>
                      </header>
                      <CodeEditor
                        allowParentWheelScroll
                        height="100%"
                        language={MONGO_LANGUAGE_ID}
                        readOnly
                        value={statement.source}
                      />
                    </div>
                    <aside className="mongo-review-statement-detail">
                      <div className="mongo-review-statement-operation">
                        <span>操作类型</span>
                        <strong>{operation.type}</strong>
                      </div>
                      <div className="mongo-review-statement-operation">
                        <span>目标集合</span>
                        <code>{operation.collection || '集合格式不合规'}</code>
                      </div>
                      <section>
                        <h3>操作说明</h3>
                        <p>{operation.description || '未从附近注释中解析到操作说明'}</p>
                      </section>
                      {operation.type.startsWith('insert') ? (
                        <section>
                          <h3>可用查询规则</h3>
                          <p>
                            {availableRules.length
                              ? availableRules.map((rule) => rule.name).join('、')
                              : '该集合尚未配置查询规则'}
                          </p>
                        </section>
                      ) : null}
                      {operation.unresolvedPaths.length ? (
                        <p className="mongo-review-uncertain">
                          无法确定：{operation.unresolvedPaths.join(', ')}
                        </p>
                      ) : null}
                      {operation.diagnostics.map((diagnostic) => (
                        <p className="mongo-review-diagnostic" key={`${statement.id}-${diagnostic.code}-${diagnostic.offset}`}>
                          {diagnostic.message}
                        </p>
                      ))}
                      <button
                        className="primary-button mongo-review-open-statement"
                        onClick={() => void openRepositoryStatement(statement)}
                        type="button"
                      >
                        单独编辑与审查
                      </button>
                    </aside>
                  </article>
                )
              })}
              {!repositoryBusy && repositoryStatements.length === 0 ? (
                <p className="mongo-review-empty">该任务没有识别到独立 MongoDB 操作。</p>
              ) : null}
              {repositoryStatements.length > 0 ? (
                <nav className="mongo-review-statement-navigation" aria-label="独立脚本快速切换">
                  <button
                    aria-label="上一个脚本"
                    disabled={activeStatementIndex === 0}
                    onClick={() => scrollToAdjacentStatement(-1)}
                    title="上一个脚本"
                    type="button"
                  >
                    <svg aria-hidden="true" viewBox="0 0 24 24">
                      <path d="m6 15 6-6 6 6" />
                    </svg>
                  </button>
                  <button
                    aria-label="下一个脚本"
                    disabled={activeStatementIndex === repositoryStatements.length - 1}
                    onClick={() => scrollToAdjacentStatement(1)}
                    title="下一个脚本"
                    type="button"
                  >
                    <svg aria-hidden="true" viewBox="0 0 24 24">
                      <path d="m6 9 6 6 6-6" />
                    </svg>
                  </button>
                </nav>
              ) : null}
            </section>
          ) : (
            <>
              <section className="mongo-review-main-grid">
            <div className="mongo-review-editor-card">
              <div className="mongo-review-editor-toolbar">
                <input
                  aria-label="脚本标题"
                  className="mongo-review-title-input"
                  onChange={(event) => setTitle(event.target.value)}
                  value={title}
                />
                <div className="mongo-review-actions">
                  <button className="secondary-button" disabled={busy} onClick={runParse} type="button">
                    解析
                  </button>
                  <button
                    className="secondary-button"
                    disabled={busy || !title || !source}
                    onClick={async () => {
                      setBusy(true)
                      try {
                        const response = await reviewApi.saveScript({
                          id: script?.id,
                          title,
                          source,
                          origin_path: script?.origin_path,
                          operation_descriptions: operationDescriptions,
                        })
                        setScript(response.script)
                        setParsed({ operations: response.script.operations ?? [], diagnostics: [] })
                        await reloadScripts()
                        setMessage('脚本和操作说明已保存。')
                      } catch (error) {
                        setMessage(errorText(error))
                      } finally {
                        setBusy(false)
                      }
                    }}
                    type="button"
                  >
                    保存脚本
                  </button>
                </div>
              </div>
              <CodeEditor
                diagnostics={editorDiagnostics}
                height="560px"
                language={MONGO_LANGUAGE_ID}
                onChange={(value) => {
                  setSource(value)
                  setParsed(null)
                  setReview(null)
                }}
                value={source}
              />
            </div>

            <aside className="mongo-review-side-panel">
              <section>
                <h2>审查环境</h2>
                <div className="mongo-review-env-options">
                  {environmentNames.map((name) => {
                    const configured = environments.some((item) => item.environment === name && item.configured)
                    return (
                      <label className={!configured ? 'is-disabled' : ''} key={name}>
                        <input
                          checked={selectedEnvironments.includes(name)}
                          disabled={!configured}
                          onChange={(event) => setSelectedEnvironments((current) =>
                            event.target.checked ? [...current, name] : current.filter((item) => item !== name))}
                          type="checkbox"
                        />
                        {name.toUpperCase()}
                      </label>
                    )
                  })}
                </div>
              </section>
              <section>
                <h2>解析结果</h2>
                {!parsed ? <p className="mongo-review-empty">点击“解析”识别脚本操作。</p> : null}
                {parsed?.operations.map((operation) => (
                  <article className={`mongo-review-operation ${operation.queryable ? '' : 'is-uncertain'}`} key={operation.id}>
                    <div>
                      <strong>{operation.type}</strong>
                      <code>{operation.collection || '集合格式不合规'}</code>
                    </div>
                    <p>{operation.description || '未填写操作说明'}</p>
                    <input
                      aria-label={`${operation.type} 操作说明`}
                      onChange={(event) => setOperationDescriptions((current) => ({
                        ...current,
                        [operation.id]: event.target.value,
                      }))}
                      placeholder="填写这条操作的目的说明"
                      value={operationDescriptions[operation.id] ?? operation.description}
                    />
                    {operation.type.startsWith('insert') || operation.children?.some((child) => child.type.startsWith('insert')) ? (
                      <select
                        aria-label={`${operation.type} 查询规则`}
                        value={ruleSelections[operation.type === 'bulkWrite' ? operation.collection : operation.id] ?? ''}
                        onChange={(event) => setRuleSelections((current) => ({
                          ...current,
                          [operation.type === 'bulkWrite' ? operation.collection : operation.id]: event.target.value,
                        }))}
                      >
                        <option value="">不查询</option>
                        {collectionRules(operation.collection).map((rule) => (
                          <option key={rule.id} value={rule.id}>{rule.name}</option>
                        ))}
                      </select>
                    ) : null}
                    {operation.unresolvedPaths.length ? (
                      <p className="mongo-review-uncertain">无法确定：{operation.unresolvedPaths.join(', ')}</p>
                    ) : null}
                    {operation.diagnostics.map((item) => (
                      <p className="mongo-review-diagnostic" key={`${item.code}-${item.offset}`}>{item.message}</p>
                    ))}
                  </article>
                ))}
              </section>
              <button
                className="primary-button mongo-review-run-button"
                disabled={busy || selectedEnvironments.length === 0}
                onClick={async () => {
                  setBusy(true)
                  setMessage('正在创建只读审查任务...')
                  try {
                    const ensured = parsed ?? await runParse()
                    if (!ensured) return
                    const response = await reviewApi.startReview({
                      script_id: script?.id,
                      source,
                      environments: selectedEnvironments,
                      rule_ids: ruleSelections,
                    })
                    setReview(response.review)
                    setMessage('审查已开始，生产数据库不会收到写命令。')
                  } catch (error) {
                    setMessage(errorText(error))
                  } finally {
                    setBusy(false)
                  }
                }}
                type="button"
              >
                开始只读审查
              </button>
            </aside>
              </section>
              <section className="mongo-review-result-panel">
                <h2>受影响范围与字段差异</h2>
                <ResultPanel review={review} />
              </section>
            </>
          )}
          {message ? <div className="mongo-review-message" role="status">{message}</div> : null}
        </>
      ) : null}
    </div>
  )
}
