import { describe, expect, it } from 'vitest'
import type { CapabilityId, ToolModuleId, ToolModuleManifest } from '../../platform/contracts/modules'
import { createModuleRegistry } from './registry'

function manifest(
  id: ToolModuleId,
  path: string,
  options: Partial<ToolModuleManifest> = {},
): ToolModuleManifest {
  return {
    id,
    version: '1.0.0',
    title: id,
    group: 'data',
    order: 1,
    route: { path, load: async () => ({ default: () => null }) },
    navigation: { label: id, icon: 'json' },
    standalone: { supported: false },
    ...options,
  }
}

describe('module registry', () => {
  it('selects build-profile modules and applies runtime disabling', () => {
    const modules = [manifest('inspect', '/inspect', { default: true }), manifest('json', '/json')]
    const registry = createModuleRegistry(modules, {
      includedModules: 'inspect,json',
      disabledModules: 'inspect',
    })

    expect(registry.modules.map((module) => module.id)).toEqual(['json'])
    expect(registry.defaultPath).toBe('/json')
  })

  it('rejects unknown module configuration', () => {
    expect(() => createModuleRegistry([manifest('json', '/json')], { disabledModules: 'missing' })).toThrow(
      'Unknown frontend module',
    )
  })

  it('rejects duplicate routes and capability providers', () => {
    const capability: CapabilityId = 'json.format'
    expect(() => createModuleRegistry([
      manifest('inspect', '/same'),
      manifest('json', '/same'),
    ])).toThrow('Duplicate frontend module route')

    expect(() => createModuleRegistry([
      manifest('inspect', '/inspect', { provides: [{ id: capability }] }),
      manifest('json', '/json', { provides: [{ id: capability }] }),
    ])).toThrow('Duplicate frontend capability provider')
  })

  it('rejects missing required capabilities but allows optional ones', () => {
    const required = manifest('inspect', '/inspect', {
      requires: [{ id: 'json.format' }],
    })
    const optional = manifest('inspect', '/inspect', {
      requires: [{ id: 'json.format', optional: true }],
    })

    expect(() => createModuleRegistry([required])).toThrow('requires unavailable capability')
    expect(createModuleRegistry([optional]).modules).toHaveLength(1)
  })
})
