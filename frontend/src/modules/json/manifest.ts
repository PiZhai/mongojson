import type { ToolModuleManifest } from '../../platform/contracts/modules'

export const jsonModule = {
  id: 'json',
  version: '1.0.0',
  title: 'JSON 工具',
  group: 'data',
  order: 20,
  route: {
    path: '/tools/json',
    load: () => import('./JsonWorkspace').then((module) => ({ default: module.JsonWorkspace })),
  },
  navigation: { label: 'JSON 工具', icon: 'json' },
  provides: [{ id: 'json.format', navigation: { path: '/tools/json', transferTarget: 'json', mode: 'format' } }],
  standalone: { supported: true },
} satisfies ToolModuleManifest
