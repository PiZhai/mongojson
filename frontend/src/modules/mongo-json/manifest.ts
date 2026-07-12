import type { CapabilityProvider, ToolModuleManifest } from '../../platform/contracts/modules'

const modes = ['format', 'repair', 'unescape', 'diff', 'table', 'shell'] as const

const provides = modes.map<CapabilityProvider>((mode) => ({
  id: `mongo-json.${mode}`,
  navigation: {
    path: `/tools/mongodb-json?mode=${mode}`,
    transferTarget: 'mongodb-json',
    mode,
  },
}))

export const mongoJsonModule = {
  id: 'mongo-json',
  version: '1.0.0',
  title: 'MongoDB JSON 工具',
  group: 'data',
  order: 30,
  route: {
    path: '/tools/mongodb-json',
    load: () => import('./MongoJsonWorkspace').then((module) => ({ default: module.MongoJsonWorkspace })),
  },
  navigation: { label: 'MongoDB JSON', icon: 'mongo' },
  provides,
  standalone: { supported: true },
} satisfies ToolModuleManifest
