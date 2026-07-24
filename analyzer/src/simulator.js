import { Query, update } from 'mingo'
import { fromExtendedJSON, toExtendedJSON } from './ejson.js'

const allowedUpdateOperators = new Set([
  '$set',
  '$unset',
  '$inc',
  '$rename',
  '$push',
  '$addToSet',
  '$pull',
  '$pop',
  '$min',
  '$max',
  '$mul',
])

function queryOptions() {
  return {
    scriptEnabled: false,
    useStrictMode: true,
  }
}

function toMingoValue(value) {
  if (Array.isArray(value)) return value.map(toMingoValue)
  if (!value || typeof value !== 'object') return value
  if (value._bsontype === 'Int32' || value._bsontype === 'Double') return value.value
  if (value._bsontype === 'Long') {
    const number = value.toNumber()
    if (!Number.isSafeInteger(number)) throw new Error('Long 超出 JavaScript 安全整数范围，无法确定更新结果。')
    return number
  }
  if (value._bsontype === 'Decimal128') {
    const number = Number(value.toString())
    if (!Number.isFinite(number)) throw new Error('Decimal128 无法安全转换，无法确定更新结果。')
    return number
  }
  if (value._bsontype) return value
  return Object.fromEntries(Object.entries(value).map(([key, child]) => [key, toMingoValue(child)]))
}

export function matchDocuments({ documents = [], filter = {} }) {
  const bsonFilter = toMingoValue(fromExtendedJSON(filter))
  const query = new Query(bsonFilter, queryOptions())
  const matches = documents.map((document, index) => ({
    index,
    matches: query.test(toMingoValue(fromExtendedJSON(document))),
  }))
  return { matches }
}

export function simulateUpdate({ document, update: modifier, arrayFilters = [] }) {
  const bsonDocument = toMingoValue(fromExtendedJSON(document))
  const bsonModifier = toMingoValue(fromExtendedJSON(modifier))
  const unsupportedOperators = Object.keys(bsonModifier).filter((key) => !allowedUpdateOperators.has(key))
  if (unsupportedOperators.length > 0) {
    return {
      before: toExtendedJSON(bsonDocument),
      after: null,
      modifiedPaths: [],
      uncertainPaths: unsupportedOperators,
      diagnostics: unsupportedOperators.map((operator) => ({
        code: 'unsupported-update-operator',
        message: `${operator} 不在可验证的更新操作符白名单中。`,
        severity: 'warning',
        source: 'mingo',
      })),
    }
  }
  const before = toExtendedJSON(bsonDocument)
  const working = toMingoValue(fromExtendedJSON(before))
  const modifiedPaths = update(
    working,
    bsonModifier,
    arrayFilters.map((item) => toMingoValue(fromExtendedJSON(item))),
    undefined,
    { queryOptions: queryOptions() },
  )
  return {
    before,
    after: toExtendedJSON(working),
    modifiedPaths: modifiedPaths.flat ? modifiedPaths.flat() : modifiedPaths,
    uncertainPaths: [],
    diagnostics: [],
  }
}
