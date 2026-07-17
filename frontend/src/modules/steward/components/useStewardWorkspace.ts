import { useEffect, useMemo, useState, type FormEvent } from 'react'
import {
  correctStewardMemory,
  createStewardEvent,
  createStewardIntent,
  createStewardKnowledgeItem,
  createStewardMemory,
  createStewardTag,
  createStewardTask,
  exportStewardData,
  getStewardMemoryVersions,
  getStewardOverview,
  getStewardSourceRefs,
  searchStewardData,
} from '../api'
import type {
  StewardMemory,
  StewardMemoryVersion,
  StewardOverview,
  StewardSearchResult,
  StewardSourceRef,
} from '../types'
import { entityText, isSensitiveLevel } from './model'

export type EventDraft = {
  title: string
  summary: string
  type: string
  dataLevel: string
}

export type TaskDraft = {
  title: string
  description: string
  priority: 'low' | 'normal' | 'high'
  dueAt: string
  dataLevel: string
}

export type IntentDraft = {
  title: string
  summary: string
  reason: string
  suggestedAction: string
  dataLevel: string
}

export type MemoryDraft = {
  title: string
  summary: string
  content: string
  scope: string
  dataLevel: string
}

export type KnowledgeDraft = {
  title: string
  summary: string
  originalUri: string
  type: string
  dataLevel: string
  allowIndex: boolean
}

export type TagDraft = {
  name: string
  type: string
}

export type CorrectionDraft = {
  memory: StewardMemory
  title: string
  summary: string
  content: string
  reason: string
}

const emptyEventDraft: EventDraft = {
  title: '',
  summary: '',
  type: 'manual_note',
  dataLevel: 'D0',
}

const emptyTaskDraft: TaskDraft = {
  title: '',
  description: '',
  priority: 'normal',
  dueAt: '',
  dataLevel: 'D0',
}

const emptyIntentDraft: IntentDraft = {
  title: '',
  summary: '',
  reason: '',
  suggestedAction: '',
  dataLevel: 'D0',
}

const emptyMemoryDraft: MemoryDraft = {
  title: '',
  summary: '',
  content: '',
  scope: 'global',
  dataLevel: 'D0',
}

const emptyKnowledgeDraft: KnowledgeDraft = {
  title: '',
  summary: '',
  originalUri: '',
  type: 'note',
  dataLevel: 'D0',
  allowIndex: true,
}

const emptyTagDraft: TagDraft = {
  name: '',
  type: 'normal',
}

export function useStewardWorkspace() {
  const [overview, setOverview] = useState<StewardOverview | null>(null)
  const [eventDraft, setEventDraft] = useState<EventDraft>(emptyEventDraft)
  const [taskDraft, setTaskDraft] = useState<TaskDraft>(emptyTaskDraft)
  const [intentDraft, setIntentDraft] = useState<IntentDraft>(emptyIntentDraft)
  const [memoryDraft, setMemoryDraft] = useState<MemoryDraft>(emptyMemoryDraft)
  const [knowledgeDraft, setKnowledgeDraft] = useState<KnowledgeDraft>(emptyKnowledgeDraft)
  const [tagDraft, setTagDraft] = useState<TagDraft>(emptyTagDraft)
  const [correctionDraft, setCorrectionDraft] = useState<CorrectionDraft | null>(null)
  const [sourceRefs, setSourceRefs] = useState<StewardSourceRef[] | null>(null)
  const [sourceTarget, setSourceTarget] = useState('最近来源')
  const [memoryVersions, setMemoryVersions] = useState<StewardMemoryVersion[]>([])
  const [searchQuery, setSearchQuery] = useState('')
  const [searchType, setSearchType] = useState('')
  const [searchResults, setSearchResults] = useState<StewardSearchResult[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  const refresh = async () => {
    setError(null)
    const result = await getStewardOverview()
    setOverview(result.overview)
  }

  useEffect(() => {
    let alive = true
    getStewardOverview()
      .then((result) => {
        if (alive) {
          setOverview(result.overview)
        }
      })
      .catch((err: unknown) => {
        if (alive) {
          setError(err instanceof Error ? err.message : '加载私人管家数据失败')
        }
      })
      .finally(() => {
        if (alive) {
          setLoading(false)
        }
      })
    return () => {
      alive = false
    }
  }, [])

  const counts = useMemo(() => overview?.counts ?? {}, [overview])
  const collectors = overview?.collectors ?? []
  const events = overview?.events ?? []
  const timelineSegments = overview?.timeline_segments ?? []
  const tasks = overview?.tasks ?? []
  const intents = overview?.intents ?? []
  const memories = overview?.memories ?? []
  const knowledgeItems = overview?.knowledge_items ?? []
  const displayedSourceRefs = sourceRefs ?? overview?.source_refs ?? []
  const tags = overview?.tags ?? []
  const auditLogs = overview?.audit_logs ?? []
  const sync = overview?.sync ?? null
  const autonomy = overview?.autonomy ?? null
  const candidateProposalCount = (autonomy?.proposals ?? []).filter((proposal) => proposal.status === 'candidate').length
  const devicesById = useMemo(() => new Map((sync?.devices ?? []).map((device) => [device.id, device])), [sync?.devices])
  const capabilitiesByKey = useMemo(
    () =>
      new Map(
        (sync?.capabilities ?? []).map((capability) => [
          `${capability.device_id}:${capability.capability}`,
          capability,
        ]),
      ),
    [sync?.capabilities],
  )
  const permissionRows = useMemo(
    () =>
      [...(sync?.permissions ?? [])].sort((left, right) => {
        const byDevice = left.device_id.localeCompare(right.device_id)
        return byDevice === 0 ? left.capability.localeCompare(right.capability) : byDevice
      }),
    [sync?.permissions],
  )

  const runAction = async (label: string, action: () => Promise<unknown>) => {
    setBusy(label)
    setError(null)
    try {
      await action()
      await refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : `${label}失败`)
    } finally {
      setBusy(null)
    }
  }

  const submitEvent = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!eventDraft.title.trim()) {
      setError('事件标题不能为空')
      return
    }
    await runAction('创建事件', async () => {
      await createStewardEvent({
        type: eventDraft.type,
        title: eventDraft.title.trim(),
        summary: eventDraft.summary.trim(),
        source: 'manual',
        data_level: eventDraft.dataLevel,
        permission_level: 'A3',
        user_confirmed: true,
      })
      setEventDraft(emptyEventDraft)
    })
  }

  const submitTask = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!taskDraft.title.trim()) {
      setError('任务标题不能为空')
      return
    }
    await runAction('创建任务', async () => {
      await createStewardTask({
        type: 'manual',
        title: taskDraft.title.trim(),
        description: taskDraft.description.trim(),
        priority: taskDraft.priority,
        due_at: taskDraft.dueAt ? new Date(taskDraft.dueAt).toISOString() : null,
        source: 'manual',
        data_level: taskDraft.dataLevel,
        permission_level: 'A3',
        risk_level: isSensitiveLevel(taskDraft.dataLevel) ? 'medium' : 'low',
        user_confirmed: true,
      })
      setTaskDraft(emptyTaskDraft)
    })
  }

  const submitIntent = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!intentDraft.title.trim()) {
      setError('意图标题不能为空')
      return
    }
    await runAction('创建意图', async () => {
      await createStewardIntent({
        type: 'follow_up',
        title: intentDraft.title.trim(),
        summary: intentDraft.summary.trim(),
        reason: intentDraft.reason.trim(),
        suggested_action: intentDraft.suggestedAction.trim() || '确认后转任务',
        risk_level: isSensitiveLevel(intentDraft.dataLevel) ? 'medium' : 'low',
        source: 'manual',
        data_level: intentDraft.dataLevel,
        permission_level: 'A3',
        confidence: 0.7,
        user_confirmed: false,
      })
      setIntentDraft(emptyIntentDraft)
    })
  }

  const submitMemory = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!memoryDraft.title.trim()) {
      setError('记忆标题不能为空')
      return
    }
    await runAction('创建记忆', async () => {
      await createStewardMemory({
        type: 'project_fact',
        title: memoryDraft.title.trim(),
        summary: memoryDraft.summary.trim(),
        content: memoryDraft.content.trim() || memoryDraft.summary.trim(),
        scope: memoryDraft.scope.trim() || 'global',
        source: 'manual',
        data_level: memoryDraft.dataLevel,
        permission_level: 'A3',
        confidence: 1,
        user_confirmed: true,
      })
      setMemoryDraft(emptyMemoryDraft)
    })
  }

  const submitKnowledge = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!knowledgeDraft.title.trim()) {
      setError('知识标题不能为空')
      return
    }
    await runAction('导入知识', async () => {
      await createStewardKnowledgeItem({
        type: knowledgeDraft.type,
        title: knowledgeDraft.title.trim(),
        summary: knowledgeDraft.summary.trim(),
        source: 'manual',
        original_uri: knowledgeDraft.originalUri.trim(),
        import_method: 'manual',
        data_level: knowledgeDraft.dataLevel,
        permission_level: 'A3',
        allow_index: knowledgeDraft.allowIndex && !isSensitiveLevel(knowledgeDraft.dataLevel),
        user_confirmed: true,
      })
      setKnowledgeDraft(emptyKnowledgeDraft)
    })
  }

  const submitTag = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!tagDraft.name.trim()) {
      setError('标签名不能为空')
      return
    }
    await runAction('保存标签', async () => {
      await createStewardTag({ name: tagDraft.name.trim(), type: tagDraft.type })
      setTagDraft(emptyTagDraft)
    })
  }

  const submitCorrection = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!correctionDraft) {
      return
    }
    await runAction('纠正记忆', async () => {
      await correctStewardMemory(correctionDraft.memory.id, {
        title: correctionDraft.title,
        summary: correctionDraft.summary,
        content: correctionDraft.content,
        reason: correctionDraft.reason || '用户纠正',
      })
      setCorrectionDraft(null)
      const versions = await getStewardMemoryVersions(correctionDraft.memory.id)
      setMemoryVersions(versions.versions)
    })
  }

  const showSources = async (entityType: string, id: string, title: string) => {
    setBusy('加载来源')
    setError(null)
    try {
      const result = await getStewardSourceRefs(entityType, id)
      setSourceRefs(result.source_refs)
      setSourceTarget(`${entityText(entityType)} · ${title}`)
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载来源失败')
    } finally {
      setBusy(null)
    }
  }

  const showMemoryVersions = async (memory: StewardMemory) => {
    setBusy('加载版本')
    setError(null)
    try {
      const result = await getStewardMemoryVersions(memory.id)
      setMemoryVersions(result.versions)
      setCorrectionDraft({ memory, title: memory.title, summary: memory.summary, content: memory.content, reason: '' })
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载记忆版本失败')
    } finally {
      setBusy(null)
    }
  }

  const runSearch = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setBusy('搜索')
    setError(null)
    try {
      const result = await searchStewardData({ q: searchQuery, entity_type: searchType, limit: 40 })
      setSearchResults(result.results)
    } catch (err) {
      setError(err instanceof Error ? err.message : '搜索失败')
    } finally {
      setBusy(null)
    }
  }

  const downloadExport = async () => {
    await runAction('导出数据', async () => {
      const result = await exportStewardData(false)
      const blob = new Blob([JSON.stringify(result.export, null, 2)], { type: 'application/json' })
      const url = URL.createObjectURL(blob)
      const anchor = document.createElement('a')
      anchor.href = url
      anchor.download = `steward-s2-export-${new Date().toISOString().slice(0, 10)}.json`
      anchor.click()
      URL.revokeObjectURL(url)
    })
  }

  return {
    overview,
    counts,
    collectors,
    events,
    timelineSegments,
    tasks,
    intents,
    memories,
    knowledgeItems,
    displayedSourceRefs,
    tags,
    auditLogs,
    sync,
    autonomy,
    candidateProposalCount,
    devicesById,
    capabilitiesByKey,
    permissionRows,
    eventDraft,
    setEventDraft,
    taskDraft,
    setTaskDraft,
    intentDraft,
    setIntentDraft,
    memoryDraft,
    setMemoryDraft,
    knowledgeDraft,
    setKnowledgeDraft,
    tagDraft,
    setTagDraft,
    correctionDraft,
    setCorrectionDraft,
    sourceTarget,
    memoryVersions,
    searchQuery,
    setSearchQuery,
    searchType,
    setSearchType,
    searchResults,
    loading,
    busy,
    error,
    refresh,
    runAction,
    submitEvent,
    submitTask,
    submitIntent,
    submitMemory,
    submitKnowledge,
    submitTag,
    submitCorrection,
    showSources,
    showMemoryVersions,
    runSearch,
    downloadExport,
  }
}
