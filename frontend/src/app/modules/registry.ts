import { moduleCatalog } from '../../modules/catalog'
import { getEnvironmentModuleSelection, selectEnabledModules, type ModuleSelection } from './featureGates'
import type { CapabilityId, CapabilityProvider, ToolModuleManifest } from '../../platform/contracts/modules'

export type ModuleRegistry = {
  modules: ToolModuleManifest[]
  defaultPath: string
  capabilities: Map<CapabilityId, CapabilityProvider>
}

export function createModuleRegistry(
  manifests: ToolModuleManifest[],
  selection: ModuleSelection = {},
): ModuleRegistry {
  const enabledModules = selectEnabledModules(manifests, selection).sort((left, right) => left.order - right.order)
  if (enabledModules.length === 0) {
    throw new Error('The frontend build must enable at least one tool module.')
  }

  const ids = new Set<string>()
  const routes = new Set<string>()
  const capabilities = new Map<CapabilityId, CapabilityProvider>()

  for (const module of enabledModules) {
    if (ids.has(module.id)) throw new Error(`Duplicate frontend module id: ${module.id}`)
    if (routes.has(module.route.path)) throw new Error(`Duplicate frontend module route: ${module.route.path}`)
    ids.add(module.id)
    routes.add(module.route.path)

    for (const capability of module.provides ?? []) {
      if (capabilities.has(capability.id)) {
        throw new Error(`Duplicate frontend capability provider: ${capability.id}`)
      }
      capabilities.set(capability.id, capability)
    }
  }

  for (const module of enabledModules) {
    for (const requirement of module.requires ?? []) {
      if (!requirement.optional && !capabilities.has(requirement.id)) {
        throw new Error(`Module ${module.id} requires unavailable capability ${requirement.id}`)
      }
    }
  }

  return {
    modules: enabledModules,
    defaultPath: enabledModules.find((module) => module.default)?.route.path ?? enabledModules[0].route.path,
    capabilities,
  }
}

export const moduleRegistry = createModuleRegistry(moduleCatalog, getEnvironmentModuleSelection())

export function resolveCapability(capabilityId: CapabilityId) {
  return moduleRegistry.capabilities.get(capabilityId)
}
