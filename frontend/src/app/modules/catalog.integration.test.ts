import { describe, expect, it } from 'vitest'
import { moduleCatalog } from '../../modules/catalog'
import { createModuleRegistry } from './registry'

describe('production module catalog', () => {
  it('validates the complete catalog and standalone contract', () => {
    const registry = createModuleRegistry(moduleCatalog)

    expect(registry.modules).toHaveLength(10)
    expect(registry.modules.every((module) => module.standalone.supported)).toBe(true)
  })

  it.each(moduleCatalog.map((module) => module.id))('can disable %s without invalidating the registry', (moduleId) => {
    const registry = createModuleRegistry(moduleCatalog, { disabledModules: moduleId })

    expect(registry.modules.some((module) => module.id === moduleId)).toBe(false)
    expect(registry.modules).toHaveLength(moduleCatalog.length - 1)
  })
})
