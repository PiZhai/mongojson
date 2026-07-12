import type { ToolModuleManifest } from '../../platform/contracts/modules'

export const memoDocsModule = {
  id: 'memo-docs',
  version: '1.0.0',
  title: '在线备忘录',
  group: 'documents',
  order: 50,
  route: {
    path: '/tools/memo-docs',
    load: () => import('./MemoDocsWorkspace').then((module) => ({ default: module.MemoDocsWorkspace })),
  },
  navigation: { label: '在线备忘录', icon: 'memo' },
  backend: [{ id: 'memo-api', required: true, endpoints: ['/api/memo', '/api/files'] }],
  storage: [{ kind: 'localStorage', namespace: 'mongojson.memoDocs', required: false }],
  standalone: { supported: true },
} satisfies ToolModuleManifest
