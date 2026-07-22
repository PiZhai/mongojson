import { moduleCatalog } from '../../modules/catalog'
import { workspaceMetadata, type WorkspaceDefinition } from '../workspaces/definitions'
import { getEnvironmentModuleSelection, selectEnabledModules, type ModuleSelection } from './featureGates'
import type { CapabilityId, CapabilityProvider, ToolModuleManifest, WorkspaceId } from '../../platform/contracts/modules'

export type ModuleRegistry = {
  modules: ToolModuleManifest[]
  workspaces: WorkspaceDefinition[]
  legacyRoutes: Array<{ from: string; to: string }>
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
  const legacyRoutes = new Set<string>()
  const capabilities = new Map<CapabilityId, CapabilityProvider>()

  for (const module of enabledModules) {
    if (ids.has(module.id)) throw new Error(`Duplicate frontend module id: ${module.id}`)
    if (routes.has(module.route.path)) throw new Error(`Duplicate frontend module route: ${module.route.path}`)
    if (legacyRoutes.has(module.route.path)) throw new Error(`Frontend module route conflicts with a legacy route: ${module.route.path}`)
    ids.add(module.id)
    routes.add(module.route.path)

    for (const legacyPath of module.route.legacyPaths ?? []) {
      if (routes.has(legacyPath) || legacyRoutes.has(legacyPath)) {
        throw new Error(`Duplicate frontend legacy route: ${legacyPath}`)
      }
      legacyRoutes.add(legacyPath)
    }

    const routePrefix = `/${module.workspace}${module.workspace === 'steward' ? '' : '/'}`
    const routeMatchesWorkspace = module.workspace === 'steward'
      ? module.route.path === routePrefix
      : module.route.path.startsWith(routePrefix)
    if (!routeMatchesWorkspace) {
      throw new Error(`Module ${module.id} route ${module.route.path} does not belong to workspace ${module.workspace}`)
    }

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

  const workspaces = workspaceMetadata
    .map<WorkspaceDefinition>((workspace) => {
      const modules = enabledModules.filter((module) => module.workspace === workspace.id)
      const { defaultModuleId, ...metadata } = workspace
      const defaultPath = modules.find((module) => module.id === defaultModuleId)?.route.path
        ?? modules[0]?.route.path
        ?? ''
      return { ...metadata, defaultPath, modules }
    })
    .filter((workspace) => workspace.modules.length > 0)

  const defaultPath = enabledModules.find((module) => module.workspace === 'steward')?.route.path
    ?? enabledModules.find((module) => module.default)?.route.path
    ?? enabledModules[0].route.path

  return {
    modules: enabledModules,
    workspaces,
    legacyRoutes: enabledModules.flatMap((module) =>
      (module.route.legacyPaths ?? []).map((from) => ({ from, to: module.route.path }))),
    defaultPath,
    capabilities,
  }
}

export const moduleRegistry = createModuleRegistry(moduleCatalog, getEnvironmentModuleSelection())

export function resolveCapability(capabilityId: CapabilityId) {
  return moduleRegistry.capabilities.get(capabilityId)
}

export function getWorkspace(id: WorkspaceId) {
  return moduleRegistry.workspaces.find((workspace) => workspace.id === id)
}
