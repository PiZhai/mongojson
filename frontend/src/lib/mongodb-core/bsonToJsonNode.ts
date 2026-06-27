import { Binary, BSONRegExp, Decimal128, Double, Int32, Long, MaxKey, MinKey, ObjectId, Timestamp, UUID } from 'bson'

import { compareFieldKeys } from '../tooling/jsonFormatter/ast'
import type { JsonNode } from '../../types/tooling'

function quoteArg(value: string) {
  return JSON.stringify(value)
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return Object.prototype.toString.call(value) === '[object Object]'
}

function bsonType(value: unknown) {
  return typeof value === 'object' && value !== null && '_bsontype' in value ? String((value as { _bsontype: unknown })._bsontype) : ''
}

function bsonValueToNode(value: unknown): JsonNode | null {
  const type = bsonType(value)

  if (value instanceof ObjectId || type === 'ObjectId') {
    const hex = typeof (value as { toHexString?: unknown }).toHexString === 'function'
      ? (value as { toHexString: () => string }).toHexString()
      : String(value)
    return { type: 'mongo', func: 'ObjectId', args: quoteArg(hex) }
  }
  if (value instanceof Date) {
    return { type: 'mongo', func: 'ISODate', args: quoteArg(value.toISOString()) }
  }
  if (value instanceof Long || type === 'Long') {
    return { type: 'mongo', func: 'NumberLong', args: quoteArg(String(value)) }
  }
  if (value instanceof Int32 || type === 'Int32') {
    return { type: 'mongo', func: 'NumberInt', args: quoteArg(String(value)) }
  }
  if (value instanceof Decimal128 || type === 'Decimal128') {
    return { type: 'mongo', func: 'NumberDecimal', args: quoteArg(String(value)) }
  }
  if (value instanceof Double || type === 'Double') {
    return { type: 'number', value: String(value) }
  }
  if (value instanceof Timestamp || type === 'Timestamp') {
    return { type: 'mongo', func: 'Timestamp', args: quoteArg(String(value)) }
  }
  if (value instanceof MinKey || type === 'MinKey') {
    return { type: 'mongo', func: 'MinKey', args: null }
  }
  if (value instanceof MaxKey || type === 'MaxKey') {
    return { type: 'mongo', func: 'MaxKey', args: null }
  }
  if (value instanceof BSONRegExp || type === 'BSONRegExp') {
    return { type: 'mongo', func: 'RegExp', args: quoteArg((value as BSONRegExp).pattern) }
  }
  if (value instanceof RegExp) {
    return { type: 'mongo', func: 'RegExp', args: quoteArg(value.source) }
  }
  if (value instanceof Binary || type === 'Binary') {
    const binary = value as Binary
    return { type: 'mongo', func: 'BinData', args: `${binary.sub_type}, ${quoteArg(binary.toString('base64'))}` }
  }
  if (value instanceof UUID || type === 'UUID') {
    return { type: 'mongo', func: 'UUID', args: quoteArg(String(value)) }
  }

  return null
}

export function mongoValueToJsonNode(value: unknown): JsonNode {
  const bsonNode = bsonValueToNode(value)
  if (bsonNode) return bsonNode

  if (value === null) return { type: 'literal', value: 'null' }
  if (value === undefined) return { type: 'literal', value: 'undefined' }
  if (typeof value === 'string') return { type: 'string', value }
  if (typeof value === 'number') {
    if (Number.isNaN(value)) return { type: 'literal', value: 'NaN' }
    if (value === Infinity) return { type: 'literal', value: 'Infinity' }
    if (value === -Infinity) return { type: 'literal', value: '-Infinity' }
    return { type: 'number', value: String(value) }
  }
  if (typeof value === 'bigint') return { type: 'mongo', func: 'NumberLong', args: quoteArg(String(value)) }
  if (typeof value === 'boolean') return { type: 'literal', value: String(value) }
  if (Array.isArray(value)) {
    return {
      type: 'array',
      items: value.map((item) => mongoValueToJsonNode(item)),
    }
  }
  if (isPlainObject(value)) {
    return {
      type: 'object',
      entries: Object.entries(value)
        .map(([key, item]) => ({ key, value: mongoValueToJsonNode(item) }))
        .sort((a, b) => compareFieldKeys(a.key, b.key)),
    }
  }

  return { type: 'string', value: String(value) }
}
