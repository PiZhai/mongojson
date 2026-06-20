import { describe, expect, it } from 'vitest'

import { formatJson } from './jsonFormatter'
import { inspectInput } from './inputInspector'
import { inspectMongoQuery } from './mongoInspector'
import { buildSchemaProfile, generateSchema } from './schemaProfile'
import { getSemanticDiff } from './semanticDiff'

function astOf(input: string) {
  const result = formatJson(input, false)
  if ('error' in result) throw new Error(result.error)
  return result.ast
}

describe('diagnostics tooling', () => {
  it('classifies common pasted input shapes', () => {
    expect(inspectInput('{"a":1}')).toMatchObject({ kind: 'standard-json' })
    expect(inspectInput('{ _id: ObjectId("abc"), a: 1 }')).toMatchObject({ kind: 'mongo-json' })
    expect(inspectInput('"{\\"a\\":1}"')).toMatchObject({ kind: 'escaped-json-string' })
    expect(inspectInput('db.users.find({ active: true })')).toMatchObject({ kind: 'mongo-shell' })
    expect(inspectInput('{"a":1}\n{"a":2}')).toMatchObject({ kind: 'ndjson' })
    expect(inspectInput('INFO payload={"a":1,"b":2} done')).toMatchObject({ kind: 'log-json-fragment' })
    expect(inspectInput(`curl -X POST http://local -d '{"a":1}'`)).toMatchObject({ kind: 'curl' })
  })

  it('produces semantic diff with ignored paths and json patch', () => {
    const left = astOf('[{ id: 2, name: "old", updatedAt: ISODate("2024-01-01") }]')
    const right = astOf('[{ id: 2, name: "new", updatedAt: ISODate("2025-01-01"), extra: true }]')

    const diff = getSemanticDiff(left, right, { ignorePaths: ['[id=2].updatedAt'], arrayMatchKey: 'id' })

    expect(diff.added.map((item) => item.path)).toEqual(['[id=2].extra'])
    expect(diff.valueChanged.map((item) => item.path)).toEqual(['[id=2].name'])
    expect(diff.patch).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ op: 'replace', path: '/id=2/name' }),
        expect.objectContaining({ op: 'add', path: '/id=2/extra' }),
      ]),
    )
  })

  it('profiles schema stability and generates target types', () => {
    const profile = buildSchemaProfile(astOf('[{ name: "Ada", age: 37 }, { name: "Grace", age: "unknown", token: "secret" }]'))

    expect(profile).not.toBeNull()
    if (!profile) throw new Error('expected schema profile')
    expect(profile.mixedFieldCount).toBe(1)
    expect(profile.riskFieldCount).toBeGreaterThan(0)
    expect(generateSchema(profile, 'typescript').code).toContain('export interface MongoDocument')
    expect(generateSchema(profile, 'zod').code).toContain('z.object')
    expect(generateSchema(profile, 'go').code).toContain('type MongoDocument struct')
  })

  it('flags risky mongo shell queries and describes aggregation stages', () => {
    const inspection = inspectMongoQuery(`db.orders.aggregate([
      { $match: { status: { $nin: ["closed"] } } },
      { $lookup: { from: "users", localField: "userId", foreignField: "_id", as: "user" } },
      { $sort: { createdAt: -1 } }
    ])`)

    expect(inspection).not.toBeNull()
    if (!inspection) throw new Error('expected inspection result')
    expect(inspection.risks.map((risk) => risk.code)).toContain('nin-scan-risk')
    expect(inspection.stages.map((stage) => stage.operator)).toEqual(['$match', '$lookup', '$sort'])

    const writeRisk = inspectMongoQuery('db.users.deleteMany({})')
    expect(writeRisk?.risks.map((risk) => risk.code)).toContain('wide-write')
  })
})
