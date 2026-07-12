import { lazy, type ComponentType } from 'react'
import type { ToolModuleManifest } from '../../platform/contracts/modules'

const pageCache = new Map<ToolModuleManifest, ComponentType>()

export function getModulePage(module: ToolModuleManifest) {
  const cached = pageCache.get(module)
  if (cached) return cached
  const Page = lazy(module.route.load)
  pageCache.set(module, Page)
  return Page
}
