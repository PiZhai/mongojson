import type { ToolModuleManifest } from '../../platform/contracts/modules'

export const canvasModule = {
  id: 'canvas',
  version: '1.0.0',
  title: '无界画布',
  group: 'documents',
  order: 30,
  route: {
    path: '/tools/canvas',
    load: () => import('./CanvasWorkspace').then((module) => ({ default: module.CanvasWorkspace })),
  },
  navigation: { label: '无界画布', icon: 'canvas' },
  backend: [{ id: 'canvas-api', required: true, endpoints: ['/api/canvas/boards', '/api/canvas/assets'] }],
  storage: [{ kind: 'localStorage', namespace: 'mongojson.canvas', required: false }],
  standalone: { supported: true },
} satisfies ToolModuleManifest
