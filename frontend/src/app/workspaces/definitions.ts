import type {
  ToolModuleManifest,
  WorkspaceId,
  WorkspaceTheme,
} from '../../platform/contracts/modules'

export type WorkspaceDefinition = {
  id: WorkspaceId
  label: string
  description: string
  defaultPath: string
  theme: WorkspaceTheme
  modules: ToolModuleManifest[]
}

export type WorkspaceMetadata = Omit<WorkspaceDefinition, 'defaultPath' | 'modules'> & {
  defaultModuleId: ToolModuleManifest['id']
}

export const workspaceMetadata: readonly WorkspaceMetadata[] = [
  {
    id: 'steward',
    label: '智能管家',
    description: '对话、执行与自动收集',
    defaultModuleId: 'steward',
    theme: 'steward-soft',
  },
  {
    id: 'tools',
    label: '工具',
    description: '诊断、转换与数据处理',
    defaultModuleId: 'inspect',
    theme: 'tooling-light',
  },
  {
    id: 'documents',
    label: '文档',
    description: '文稿与无限画布',
    defaultModuleId: 'memo-docs',
    theme: 'documents-warm',
  },
  {
    id: 'entertainment',
    label: '娱乐',
    description: '音乐与同步观影',
    defaultModuleId: 'music',
    theme: 'entertainment-dark',
  },
] as const

export function getWorkspaceMetadata(id: WorkspaceId) {
  const workspace = workspaceMetadata.find((item) => item.id === id)
  if (!workspace) throw new Error(`Unknown workspace: ${id}`)
  return workspace
}
