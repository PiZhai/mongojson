import type { ToolModuleManifest } from '../../platform/contracts/modules'

export const stewardModule = {
  id: 'steward',
  version: '4.8.0',
  title: '私人管家',
  workspace: 'steward',
  order: 40,
  route: {
    path: '/steward',
    legacyPaths: ['/tools/steward'],
    load: () => import('./StewardWorkspace').then((module) => ({ default: module.StewardWorkspace })),
  },
  navigation: { label: '私人管家', icon: 'steward' },
  backend: [{ id: 'steward-api', required: true, endpoints: ['/api/steward/conversations', '/api/steward/agent/status'] }],
  storage: [{ kind: 'localStorage', namespace: 'mongojson.steward', required: false }],
  standalone: { supported: true },
} satisfies ToolModuleManifest
