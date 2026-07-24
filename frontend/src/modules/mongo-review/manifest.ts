import type { ToolModuleManifest } from '../../platform/contracts/modules'

export const mongoReviewModule = {
  id: 'mongo-review',
  version: '1.0.0',
  title: 'MongoDB 脚本审查',
  workspace: 'tools',
  order: 35,
  route: {
    path: '/tools/mongodb-review',
    load: () => import('./MongoReviewWorkspace').then((module) => ({ default: module.MongoReviewWorkspace })),
  },
  navigation: { label: '脚本审查', icon: 'mongo' },
  backend: [{
    id: 'mongodb-review-api',
    required: true,
    endpoints: ['/api/mongodb-review'],
  }],
  standalone: { supported: true },
} satisfies ToolModuleManifest
