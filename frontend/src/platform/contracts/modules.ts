import type { ComponentType, PropsWithChildren } from 'react'

export type ToolModuleId =
  | 'inspect'
  | 'json'
  | 'mongo-json'
  | 'visualize'
  | 'memo-docs'
  | 'music'
  | 'watch-party'
  | 'canvas'

export type ToolModuleGroup = 'data' | 'documents' | 'media'

export type ToolModuleIcon = 'inspect' | 'json' | 'mongo' | 'visualize' | 'memo' | 'music' | 'watch' | 'canvas'

export type CapabilityId =
  | 'json.format'
  | 'mongo-json.format'
  | 'mongo-json.repair'
  | 'mongo-json.unescape'
  | 'mongo-json.diff'
  | 'mongo-json.table'
  | 'mongo-json.shell'

export type CapabilityProvider = {
  id: CapabilityId
  navigation?: {
    path: string
    transferTarget: string
    mode?: string
  }
}

export type CapabilityRequirement = {
  id: CapabilityId
  optional?: boolean
}

export type ShellSlotId = 'shell.bottom-player' | 'shell.right-drawer'

export type BackendRequirement = {
  id: string
  required: boolean
  endpoints: string[]
}

export type StorageRequirement = {
  kind: 'localStorage' | 'sessionStorage' | 'indexedDB'
  namespace: string
  required: boolean
}

export type ToolModuleManifest = {
  id: ToolModuleId
  version: string
  title: string
  group: ToolModuleGroup
  order: number
  default?: boolean
  gate?: string
  route: {
    path: string
    load: () => Promise<{ default: ComponentType }>
  }
  navigation: {
    label: string
    icon: ToolModuleIcon
  }
  provides?: CapabilityProvider[]
  requires?: CapabilityRequirement[]
  runtime?: {
    provider?: () => Promise<{ default: ComponentType<PropsWithChildren> }>
  }
  shellSlots?: Array<{
    id: ShellSlotId
    order: number
    load: () => Promise<{ default: ComponentType }>
  }>
  backend?: BackendRequirement[]
  storage?: StorageRequirement[]
  standalone: {
    supported: boolean
  }
}
