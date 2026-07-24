import test from 'node:test'
import assert from 'node:assert/strict'
import { parseMongoScript } from '../src/parser.js'

test('parses strict getCollection operations and associates comments', () => {
  const result = parseMongoScript(`
const id = ObjectId("507f1f77bcf86cd799439011")
// 创建活动
db.getCollection("activity").insertOne({ _id: id, enabled: true })

// 停用已有活动
db.getCollection("activity").updateMany({ enabled: true }, { $set: { enabled: false } })
`)
  assert.equal(result.operations.length, 2)
  assert.equal(result.operations[0].collection, 'activity')
  assert.equal(result.operations[0].description, '创建活动')
  assert.equal(result.operations[0].queryable, true)
  assert.match(result.operations[0].contextSource, /const id = ObjectId/)
  assert.equal(result.operations[1].description, '停用已有活动')
})

test('rejects non-strict collection access without executing it', () => {
  const result = parseMongoScript(`db.activity.deleteMany({ enabled: false })`)
  assert.equal(result.operations.length, 1)
  assert.equal(result.operations[0].queryable, false)
  assert.equal(result.operations[0].diagnostics[0].code, 'collection-format')
})

test('marks runtime values as uncertain', () => {
  const result = parseMongoScript(`db.getCollection("activity").insertOne({ createdAt: Date.now() })`)
  assert.equal(result.operations[0].queryable, false)
  assert.deepEqual(result.operations[0].unresolvedPaths, ['$args[0].createdAt'])
})

test('parses ordered bulkWrite children', () => {
  const result = parseMongoScript(`
db.getCollection("activity").bulkWrite([
  { insertOne: { document: { code: "A" } } },
  { deleteOne: { filter: { code: "B" } } }
], { ordered: false })
`)
  assert.equal(result.operations[0].children.length, 2)
  assert.equal(result.operations[0].bulkOrdered, false)
})
