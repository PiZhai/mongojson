import type { ToolModuleId, ToolModuleManifest } from '../../platform/contracts/modules'

export type ModuleSelection = {
  includedModules?: string
  disabledModules?: string
}

function parseModuleIds(value?: string) {
  return new Set(
    (value ?? '')
      .split(',')
      .map((item) => item.trim())
      .filter(Boolean),
  )
}

export function selectEnabledModules(modules: ToolModuleManifest[], selection: ModuleSelection = {}) {
  const included = parseModuleIds(selection.includedModules)
  const disabled = parseModuleIds(selection.disabledModules)
  const knownIds = new Set<ToolModuleId>(modules.map((module) => module.id))

  for (const configuredId of [...included, ...disabled]) {
    if (!knownIds.has(configuredId as ToolModuleId)) {
      throw new Error(`Unknown frontend module in feature configuration: ${configuredId}`)
    }
  }

  return modules.filter((module) => {
    const isIncluded = included.size === 0 || included.has(module.id)
    return isIncluded && !disabled.has(module.id)
  })
}

export function getEnvironmentModuleSelection(): ModuleSelection {
  return {
    includedModules: import.meta.env.VITE_INCLUDED_MODULES,
    disabledModules: import.meta.env.VITE_DISABLED_MODULES,
  }
}
