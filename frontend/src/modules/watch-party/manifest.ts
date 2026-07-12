import type { ToolModuleManifest } from '../../platform/contracts/modules'

export const watchPartyModule = {
  id: 'watch-party',
  version: '1.0.0',
  title: '视频同步',
  group: 'media',
  order: 70,
  route: {
    path: '/tools/watch-party',
    load: () => import('./WatchPartyWorkspace').then((module) => ({ default: module.WatchPartyWorkspace })),
  },
  navigation: { label: '视频同步', icon: 'watch' },
  backend: [{ id: 'watch-websocket', required: true, endpoints: ['/api/watch/rooms/:roomId/ws'] }],
  storage: [{ kind: 'localStorage', namespace: 'watch-party', required: false }],
  standalone: { supported: true },
} satisfies ToolModuleManifest
