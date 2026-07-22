import type { ToolModuleManifest } from '../../platform/contracts/modules'

export const inspectModule = {
  id: 'inspect',
  version: '1.0.0',
  title: '智能诊断',
  workspace: 'tools',
  order: 10,
  default: true,
  route: {
    path: '/tools/inspect',
    load: () => import('./InspectWorkspace').then((module) => ({ default: module.InspectWorkspace })),
  },
  navigation: { label: '智能诊断', icon: 'inspect' },
  requires: [
    { id: 'json.format', optional: true },
    { id: 'mongo-json.format', optional: true },
    { id: 'mongo-json.repair', optional: true },
    { id: 'mongo-json.unescape', optional: true },
    { id: 'mongo-json.diff', optional: true },
    { id: 'mongo-json.table', optional: true },
    { id: 'mongo-json.shell', optional: true },
  ],
  standalone: { supported: true },
} satisfies ToolModuleManifest
