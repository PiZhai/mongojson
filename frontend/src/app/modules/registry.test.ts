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
    workspace: 'tools',
    order: 1,
    route: { path, load: async () => ({ default: () => null }) },
    navigation: { label: id, icon: 'json' },
    standalone: { supported: false },
    ...options,
  }
}

describe('module registry', () => {
  it('selects build-profile modules and applies runtime disabling', () => {
    const modules = [manifest('inspect', '/tools/inspect', { default: true }), manifest('json', '/tools/json')]
    const registry = createModuleRegistry(modules, {
      includedModules: 'inspect,json',
      disabledModules: 'inspect',
    })

    expect(registry.modules.map((module) => module.id)).toEqual(['json'])
    expect(registry.defaultPath).toBe('/tools/json')
  })

  it('rejects unknown module configuration', () => {
    expect(() => createModuleRegistry([manifest('json', '/tools/json')], { disabledModules: 'missing' })).toThrow(
      'Unknown frontend module',
    )
  })

  it('rejects duplicate routes and capability providers', () => {
    const capability: CapabilityId = 'json.format'
    expect(() => createModuleRegistry([
      manifest('inspect', '/tools/same'),
      manifest('json', '/tools/same'),
    ])).toThrow('Duplicate frontend module route')

    expect(() => createModuleRegistry([
      manifest('inspect', '/tools/inspect', { provides: [{ id: capability }] }),
      manifest('json', '/tools/json', { provides: [{ id: capability }] }),
    ])).toThrow('Duplicate frontend capability provider')
  })

  it('rejects missing required capabilities but allows optional ones', () => {
    const required = manifest('inspect', '/tools/inspect', {
      requires: [{ id: 'json.format' }],
    })
    const optional = manifest('inspect', '/tools/inspect', {
      requires: [{ id: 'json.format', optional: true }],
    })

    expect(() => createModuleRegistry([required])).toThrow('requires unavailable capability')
    expect(createModuleRegistry([optional]).modules).toHaveLength(1)
  })

  it('uses steward as the product entry and validates workspace route ownership', () => {
    const registry = createModuleRegistry([
      manifest('inspect', '/tools/inspect', { default: true }),
      manifest('steward', '/steward', { workspace: 'steward', route: { path: '/steward', legacyPaths: ['/tools/steward'], load: async () => ({ default: () => null }) } }),
    ])

    expect(registry.defaultPath).toBe('/steward')
    expect(registry.workspaces.map((workspace) => workspace.id)).toEqual(['steward', 'tools'])
    expect(registry.legacyRoutes).toEqual([{ from: '/tools/steward', to: '/steward' }])
    expect(() => createModuleRegistry([
      manifest('music', '/tools/music', { workspace: 'entertainment' }),
    ])).toThrow('does not belong to workspace entertainment')
  })
})
