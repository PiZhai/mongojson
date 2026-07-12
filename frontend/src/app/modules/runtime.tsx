import { lazy, Suspense, type ComponentType, type PropsWithChildren, type ReactNode } from 'react'
import { moduleRegistry } from './registry'
import type { ShellSlotId, ToolModuleManifest } from '../../platform/contracts/modules'

const providerCache = new Map<NonNullable<ToolModuleManifest['runtime']>['provider'], ComponentType<PropsWithChildren>>()
const extensionCache = new Map<() => Promise<{ default: ComponentType }>, ComponentType>()

export function ModuleProviders({ children }: PropsWithChildren) {
  return moduleRegistry.modules.reduceRight<ReactNode>((content, module) => {
    const loader = module.runtime?.provider
    if (!loader) return content

    let Provider = providerCache.get(loader)
    if (!Provider) {
      Provider = lazy(loader)
      providerCache.set(loader, Provider)
    }

    return (
      <Suspense fallback={<div className="route-loading">正在初始化模块...</div>} key={module.id}>
        <Provider>{content}</Provider>
      </Suspense>
    )
  }, children)
}

export function ShellExtensionSlot({ id }: { id: ShellSlotId }) {
  const extensions = moduleRegistry.modules
    .flatMap((module) => (module.shellSlots ?? []).map((extension) => ({ moduleId: module.id, ...extension })))
    .filter((extension) => extension.id === id)
    .sort((left, right) => left.order - right.order)

  return extensions.map((extension) => {
    let Extension = extensionCache.get(extension.load)
    if (!Extension) {
      Extension = lazy(extension.load)
      extensionCache.set(extension.load, Extension)
    }

    return (
      <Suspense fallback={null} key={`${extension.moduleId}:${extension.id}:${extension.order}`}>
        <Extension />
      </Suspense>
    )
  })
}
