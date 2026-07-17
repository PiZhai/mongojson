import type { ToolModuleManifest } from '../../platform/contracts/modules'

export const visualizeModule = {
  id: 'visualize',
  version: '1.0.0',
  title: '数据可视化',
  group: 'data',
  order: 40,
  route: {
    path: '/tools/visualize',
    load: () => import('./VisualizationWorkspace').then((module) => ({ default: module.VisualizationWorkspace })),
  },
  navigation: { label: '数据可视化', icon: 'visualize' },
  backend: [{ id: 'preset-api', required: false, endpoints: ['/api/presets'] }],
  standalone: { supported: true },
} satisfies ToolModuleManifest
