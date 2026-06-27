import { describe, expect, it } from 'vitest'

import {
  formatMongoJson,
  formatMongoShell,
  escapeMongoJsonString,
  normalizeMongoForCompare,
  parseMongoValue,
  repairStandardJson,
  unescapeMongoJsonString,
  validateMongoQueryPart,
} from './index'

describe('mongodb-core facade', () => {
  it('parses shell BSON values and exposes canonical Extended JSON', () => {
    const parsed = parseMongoValue('{ _id: ObjectId("507f1f77bcf86cd799439011"), createdAt: ISODate("2024-01-01T00:00:00.000Z") }')

    expect(parsed.ok).toBe(true)
    if (!parsed.ok) throw new Error('expected parsed value')
    expect(parsed.parser).toBe('shell-bson')

    const formatted = formatMongoJson('{ _id: ObjectId("507f1f77bcf86cd799439011"), createdAt: ISODate("2024-01-01T00:00:00.000Z") }')
    expect(formatted.ok).toBe(true)
    if (!formatted.ok) throw new Error('expected formatted value')
    expect(formatted.text).toContain('ObjectId("507f1f77bcf86cd799439011")')
    expect(formatted.extendedJson).toContain('"$oid"')
    expect(formatted.extendedJson).toContain('"$date"')
  })

  it('keeps legacy AST formatting as a compatibility fallback', () => {
    const formatted = formatMongoJson('{ _id: ObjectId("abc") }')

    expect(formatted.ok).toBe(true)
    if (!formatted.ok) throw new Error('expected fallback formatting')
    expect(formatted.diagnostics.map((item) => item.code)).toContain('legacy-parser-fallback')
    expect(formatted.text).toContain('ObjectId("abc")')
  })

  it('repairs broken JSON explicitly into standard JSON', () => {
    const repaired = repairStandardJson('{ name: "Ada", trailing: true, }')

    expect(repaired.ok).toBe(true)
    if (!repaired.ok) throw new Error('expected repaired JSON')
    expect(JSON.parse(repaired.text)).toEqual({ name: 'Ada', trailing: true })
    expect(repaired.diagnostics[0]).toMatchObject({ source: 'repair' })
  })

  it('normalizes Mongo values for compare using the core AST adapter', () => {
    const normalized = normalizeMongoForCompare('{ b: 2, _id: ObjectId("507f1f77bcf86cd799439011"), a: { z: ISODate("2024-01-01T00:00:00.000Z") } }')

    expect(normalized.error).toBeNull()
    expect(normalized.text.indexOf('"_id"')).toBeLessThan(normalized.text.indexOf('"a"'))
    expect(normalized.text).toContain('ObjectId("507f1f77bcf86cd799439011")')
    expect(normalized.text).toContain('ISODate("2024-01-01T00:00:00.000Z")')
    expect(normalized.keyLineMap['a.z']).toBeGreaterThan(0)
  })

  it('escapes Mongo JSON through canonical Extended JSON and restores readable Mongo notation', () => {
    const escaped = escapeMongoJsonString('{ _id: ObjectId("507f1f77bcf86cd799439011") }')

    expect(escaped.error).toBeUndefined()
    const inner = JSON.parse(escaped.output ?? '')
    expect(JSON.parse(inner)).toEqual({ _id: { $oid: '507f1f77bcf86cd799439011' } })

    const restored = unescapeMongoJsonString(escaped.output ?? '')
    expect(restored.output).toContain('ObjectId("507f1f77bcf86cd799439011")')
  })

  it('validates MongoDB query parts through mongodb-query-parser', () => {
    expect(validateMongoQueryPart('filter', '{ active: true }')).toEqual([])
    expect(validateMongoQueryPart('sort', '{ createdAt: -1 }')).toEqual([])
    expect(validateMongoQueryPart('filter', '{ active: }')[0]).toMatchObject({
      code: 'filter-invalid',
      source: 'query-validator',
    })
  })

  it('formats shell and adds query parser validation diagnostics', () => {
    const formatted = formatMongoShell('db.users.find({ active: true }).sort({ createdAt: -1 }).limit(10)')

    expect(formatted.ok).toBe(true)
    if (!formatted.ok) throw new Error('expected shell formatting')
    expect(formatted.text).toContain('db.getCollection("users").find')
    expect(formatted.diagnostics).toEqual([])

    const invalid = formatMongoShell('db.users.find({ active: })')
    expect(invalid.ok).toBe(true)
    if (!invalid.ok) throw new Error('expected shell formatting with diagnostics')
    expect(invalid.diagnostics.some((item) => item.source === 'query-validator')).toBe(true)
  })
})
