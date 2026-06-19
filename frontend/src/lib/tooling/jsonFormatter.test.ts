import { describe, expect, it } from 'vitest'

import {
  astNodeToDisplay,
  buildTableFromAst,
  escapeJsonString,
  formatJson,
  getFieldDiffSummary,
  normalizeForCompare,
  parseShellStatement,
  unescapeJsonString,
} from './jsonFormatter'

function expectFormatted(input: string, compact = false) {
  const result = formatJson(input, compact)
  if ('error' in result) {
    throw new Error(result.error)
  }
  return result
}

describe('jsonFormatter', () => {
  it('formats relaxed Mongo JSON and reports parser errors', () => {
    const result = expectFormatted(`{
      _id: ObjectId("507f1f77bcf86cd799439011"),
      createdAt: ISODate("2024-01-01T00:00:00.000Z"),
      name: 'Ada',
      flags: [true, null]
    }`)

    expect(result.formatted).toContain('"_id" : ObjectId("507f1f77bcf86cd799439011")')
    expect(result.formatted).toContain('"createdAt" : ISODate("2024-01-01T00:00:00.000Z")')
    expect(result.formatted).toContain('"name" : "Ada"')
    expect(result.lineCount).toBeGreaterThan(1)
    expect(result.maxDepth).toBe(2)

    const compact = expectFormatted('{ name: "Ada", tags: ["admin", "active"] }', true)
    expect(compact.formatted).toBe('{"name":"Ada","tags":["admin","active"]}')

    expect(formatJson('{ name: "Ada"')).toMatchObject({
      error: expect.stringContaining("Expected ',' or '}'"),
    })
  })

  it('normalizes documents into a stable compare order', () => {
    const first = normalizeForCompare('{ b: 2, a: { z: 3, y: 1 } }')
    const second = normalizeForCompare('{ a: { y: 1, z: 3 }, b: 2 }')

    expect(first.error).toBeNull()
    expect(first.text).toBe(second.text)
    expect(first.text.indexOf('"a"')).toBeLessThan(first.text.indexOf('"b"'))
    expect(first.text.indexOf('"y"')).toBeLessThan(first.text.indexOf('"z"'))
    expect(first.keyLineMap['a.y']).toBeGreaterThan(0)
    expect(first.maxDepth).toBe(2)
  })

  it('sorts numeric suffixes naturally when normalizing compare order', () => {
    const result = normalizeForCompare('{ field10: 1, field2: 2, field1: 3 }')

    expect(result.error).toBeNull()
    expect(result.text.indexOf('"field1"')).toBeLessThan(result.text.indexOf('"field2"'))
    expect(result.text.indexOf('"field2"')).toBeLessThan(result.text.indexOf('"field10"'))
  })

  it('builds table schema and rows from parsed document arrays', () => {
    const { ast } = expectFormatted(`[
      {
        _id: ObjectId("507f1f77bcf86cd799439011"),
        name: "Ada",
        age: 37,
        profile: { city: "Hangzhou" },
        score: null
      },
      {
        _id: ObjectId("507f191e810c19729de860ea"),
        name: "Grace",
        age: "unknown",
        profile: { city: "Shanghai" },
        extra: true
      }
    ]`)

    const table = buildTableFromAst(ast)
    expect(table).not.toBeNull()
    if (!table) throw new Error('expected table data')

    expect(table.docCount).toBe(2)
    expect(table.validation[0]).toMatchObject({ level: 'ok' })

    const byPath = Object.fromEntries(table.schema.map((column, index) => [column.path, { column, index }]))
    expect(byPath._id.column).toMatchObject({
      dominantType: 'oid',
      isMixed: false,
      totalCount: 2,
    })
    expect(byPath.age.column).toMatchObject({
      dominantType: 'number',
      isMixed: true,
      typeCounts: { number: 1, string: 1 },
      nullCount: 0,
    })
    expect(byPath.extra.column).toMatchObject({
      dominantType: 'bool',
      nullCount: 1,
      totalCount: 2,
    })
    expect(byPath['profile.city'].column).toMatchObject({
      dominantType: 'string',
      isMixed: false,
    })

    expect(table.rows[0][byPath.age.index]).toMatchObject({ type: 'number', value: '37' })
    expect(table.rows[1][byPath.age.index]).toMatchObject({ type: 'string', value: 'unknown' })
    expect(table.rows[0][byPath.extra.index]).toBeNull()
  })

  it('parses Mongo shell collection references, method chains, args, and operators', () => {
    const chained = parseShellStatement(`// find expensive open orders
      db.orders.find({ status: "open", total: { $gte: 100 } }, { _id: 0 })
        .sort({ createdAt: -1 })
        .limit(10)`)

    expect(chained).not.toBeNull()
    if (!chained) throw new Error('expected parsed shell statement')
    expect(chained.collection).toBe('orders')
    expect(chained.methods.map((method) => method.name)).toEqual(['find', 'sort', 'limit'])
    expect(chained.methods[0].argsRaw.map((arg) => arg.text)).toEqual([
      '{ status: "open", total: { $gte: 100 } }',
      '{ _id: 0 }',
    ])
    expect(chained.operators.map((operator) => operator.name)).toContain('$gte')

    const getCollection = parseShellStatement(
      'db.getCollection("order-items").updateMany({ sku: { $in: ["A", "B"] } }, { $set: { active: true } })',
    )

    expect(getCollection).not.toBeNull()
    if (!getCollection) throw new Error('expected getCollection shell statement')
    expect(getCollection.collection).toBe('order-items')
    expect(getCollection.methods.map((method) => method.name)).toEqual(['updateMany'])
    expect(getCollection.operators.map((operator) => operator.name)).toEqual(['$in', '$set'])
    expect(parseShellStatement('print("hello")')).toBeNull()
  })

  it('summarizes field-level diffs between normalized ASTs', () => {
    const left = normalizeForCompare('{ a: 1, b: 2, nested: { value: "old" } }')
    const right = normalizeForCompare('{ a: 1, c: 3, nested: { value: "new" } }')

    const diff = getFieldDiffSummary(left.ast, right.ast)

    expect(diff.leftOnly).toEqual(['b'])
    expect(diff.rightOnly).toEqual(['c'])
    expect(diff.changed).toEqual(['nested.value'])
  })

  it('escapes and restores JSON string payloads', () => {
    const escaped = escapeJsonString('{ name: "Ada", active: true }')

    expect(escaped.error).toBeUndefined()
    expect(escaped.output).toBe('"{\\"name\\":\\"Ada\\",\\"active\\":true}"')

    const restored = unescapeJsonString(escaped.output ?? '')
    expect(restored.error).toBeUndefined()
    expect(restored.output).toContain('"name" : "Ada"')
    expect(restored.output).toContain('"active" : true')

    expect(unescapeJsonString('{ not: "a string" }')).toMatchObject({
      error: '输入不是转义后的 JSON 字符串',
    })
  })

  it('renders AST cells for table and chart consumers', () => {
    expect(astNodeToDisplay({ type: 'literal', value: 'null' })).toBe('NULL')
    expect(astNodeToDisplay({ type: 'mongo', func: 'ObjectId', args: '"abc"' })).toBe('ObjectId("abc")')
    expect(astNodeToDisplay({ type: 'array', items: [{ type: 'number', value: '1' }] })).toBe('[…1]')
    expect(astNodeToDisplay({ type: 'object', entries: [{ key: 'a', value: { type: 'number', value: '1' } }] })).toBe('{…1}')
  })
})
