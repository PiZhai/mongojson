import { describe, expect, it } from 'vitest'
import { workspaceMetadata } from './definitions'

describe('workspace definitions', () => {
  it('keeps steward as the first and default product workspace', () => {
    expect(workspaceMetadata[0]).toMatchObject({ id: 'steward', defaultModuleId: 'steward' })
  })
})
