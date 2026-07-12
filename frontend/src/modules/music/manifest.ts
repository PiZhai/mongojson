import type { ToolModuleManifest } from '../../platform/contracts/modules'

export const musicModule = {
  id: 'music',
  version: '1.0.0',
  title: '音乐播放器',
  group: 'media',
  order: 60,
  route: {
    path: '/tools/music',
    load: () => import('./MusicWorkspace').then((module) => ({ default: module.MusicWorkspace })),
  },
  navigation: { label: '音乐播放器', icon: 'music' },
  runtime: {
    provider: () =>
      import('./MusicPlayerProvider').then((module) => ({ default: module.MusicPlayerProvider })),
  },
  shellSlots: [
    {
      id: 'shell.bottom-player',
      order: 10,
      load: () =>
        import('./MusicPlayerProvider').then((module) => ({ default: module.MusicMiniPlayer })),
    },
    {
      id: 'shell.right-drawer',
      order: 10,
      load: () =>
        import('./MusicPlayerProvider').then((module) => ({ default: module.MusicQueueDrawer })),
    },
  ],
  storage: [
    { kind: 'localStorage', namespace: 'personal-tooling-music', required: true },
    { kind: 'indexedDB', namespace: 'personal-tooling-music-files', required: false },
  ],
  backend: [{ id: 'music-catalog-api', required: true, endpoints: ['/api/music/tracks', '/api/music/tracks/:id/content'] }],
  standalone: { supported: true },
} satisfies ToolModuleManifest
