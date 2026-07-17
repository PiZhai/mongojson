export type ToolStatusKind = 'idle' | 'success' | 'warning' | 'error'

export type ToolStatus = {
  kind: ToolStatusKind
  message: string
}
