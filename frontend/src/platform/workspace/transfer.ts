const WORKSPACE_TRANSFER_KEY = 'mongojson-workspace-transfer'
let cleanupScheduled = false

export type WorkspaceTransferPayload = {
  target: string
  mode?: string
  input: string
  rightInput?: string
  createdAt: number
}

export function saveWorkspaceTransfer(payload: Omit<WorkspaceTransferPayload, 'createdAt'>) {
  if (typeof window === 'undefined') return
  window.sessionStorage.setItem(
    WORKSPACE_TRANSFER_KEY,
    JSON.stringify({
      ...payload,
      createdAt: Date.now(),
    }),
  )
}

export function readWorkspaceTransfer(target: string, mode?: string): WorkspaceTransferPayload | null {
  if (typeof window === 'undefined') return null
  const raw = window.sessionStorage.getItem(WORKSPACE_TRANSFER_KEY)
  if (!raw) return null

  try {
    const payload = JSON.parse(raw) as WorkspaceTransferPayload
    const isFresh = Date.now() - payload.createdAt < 10 * 60 * 1000
    const matchesTarget = payload.target === target
    const matchesMode = !mode || !payload.mode || payload.mode === mode
    if (!isFresh || !matchesTarget || !matchesMode) return null
    if (!cleanupScheduled) {
      cleanupScheduled = true
      window.setTimeout(() => {
        window.sessionStorage.removeItem(WORKSPACE_TRANSFER_KEY)
        cleanupScheduled = false
      }, 0)
    }
    return payload
  } catch {
    window.sessionStorage.removeItem(WORKSPACE_TRANSFER_KEY)
    return null
  }
}
