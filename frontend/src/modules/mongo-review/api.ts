import { apiRequest, API_BASE } from '../../platform/http/client'
import type {
  Environment,
  EnvironmentName,
  ParseResult,
  QueryRule,
  RepositoryFile,
  RepositoryProject,
  RepositoryTask,
  RepositoryTaskSummary,
  Review,
  ScriptRecord,
} from './types'

const base = `${API_BASE}/mongodb-review`

function json(method: string, body?: unknown): RequestInit {
  return {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  }
}

export async function listEnvironments() {
  return apiRequest<{ environments: Environment[] }>(`${base}/environments`)
}

export async function saveEnvironment(
  environment: EnvironmentName,
  connectionURI: string,
  databaseName: string,
) {
  return apiRequest<{ environment: Environment }>(
    `${base}/environments/${environment}`,
    json('PUT', { connection_uri: connectionURI, database_name: databaseName }),
  )
}

export async function testEnvironment(
  environment: EnvironmentName,
  connectionURI?: string,
  databaseName?: string,
) {
  return apiRequest<{ status: string }>(
    `${base}/environments/${environment}/test`,
    json('POST', {
      connection_uri: connectionURI ?? '',
      database_name: databaseName ?? '',
    }),
  )
}

export async function listRules() {
  return apiRequest<{ query_rules: QueryRule[] }>(`${base}/query-rules`)
}

export async function saveRule(rule: QueryRule) {
  return apiRequest<{ query_rule: QueryRule }>(`${base}/query-rules`, json('POST', rule))
}

export async function deleteRule(id: string) {
  const response = await fetch(`${base}/query-rules/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!response.ok) throw new Error(await response.text())
}

export async function listRepositoryFiles() {
  return apiRequest<{ files: RepositoryFile[] }>(`${base}/repository/files`)
}

export async function listRepositoryIndex() {
  return apiRequest<{ projects: RepositoryProject[]; tasks: RepositoryTaskSummary[] }>(
    `${base}/repository/index`,
  )
}

export async function getRepositoryTask(taskKey: string) {
  return apiRequest<{ task: RepositoryTask }>(
    `${base}/repository/tasks/${encodeURIComponent(taskKey)}`,
  )
}

export async function createRepositoryFile(input: {
  project: string
  task_folder: string
  file_path: string
  source: string
}) {
  return apiRequest<{ file: RepositoryFile }>(
    `${base}/repository/files`,
    json('POST', input),
  )
}

export async function readRepositoryFile(path: string) {
  return apiRequest<{ path: string; source: string }>(
    `${base}/repository/content?path=${encodeURIComponent(path)}`,
  )
}

export async function parseScript(source: string) {
  return apiRequest<ParseResult>(`${base}/parse`, json('POST', { source }))
}

export async function listScripts() {
  return apiRequest<{ scripts: ScriptRecord[] }>(`${base}/scripts`)
}

export async function saveScript(script: ScriptRecord) {
  return apiRequest<{ script: ScriptRecord }>(`${base}/scripts`, json('POST', script))
}

export async function startReview(input: {
  script_id?: string
  source: string
  environments: EnvironmentName[]
  rule_ids: Record<string, string>
}) {
  return apiRequest<{ review: Review }>(`${base}/reviews`, json('POST', input))
}

export function subscribeReview(
  reviewID: string,
  onReview: (review: Review) => void,
  onError: () => void,
) {
  const source = new EventSource(`${base}/reviews/${encodeURIComponent(reviewID)}/events`)
  const receive = (event: MessageEvent) => {
    const payload = JSON.parse(event.data) as { review: Review }
    onReview(payload.review)
    if (payload.review.status === 'completed' || payload.review.status === 'failed') source.close()
  }
  for (const event of ['queued', 'running', 'operation', 'completed', 'failed']) {
    source.addEventListener(event, receive as EventListener)
  }
  source.onerror = onError
  return () => source.close()
}
