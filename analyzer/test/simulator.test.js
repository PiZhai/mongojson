import test from 'node:test'
import assert from 'node:assert/strict'
import { matchDocuments, simulateUpdate } from '../src/simulator.js'

test('matches documents without script execution', () => {
  const result = matchDocuments({
    documents: [{ code: 'A', count: 2 }, { code: 'B', count: 1 }],
    filter: { code: 'A', count: { $gte: 2 } },
  })
  assert.deepEqual(result.matches, [{ index: 0, matches: true }, { index: 1, matches: false }])
})

test('simulates whitelisted updates and returns paths', () => {
  const result = simulateUpdate({
    document: { code: 'A', count: 2, tags: ['old'] },
    update: { $inc: { count: 3 }, $addToSet: { tags: 'new' } },
  })
  assert.equal(result.after.count.$numberInt, '5')
  assert.deepEqual(result.modifiedPaths.sort(), ['count', 'tags'])
})

test('marks unsupported operators as uncertain', () => {
  const result = simulateUpdate({
    document: { code: 'A' },
    update: { $currentDate: { updatedAt: true } },
  })
  assert.deepEqual(result.uncertainPaths, ['$currentDate'])
  assert.equal(result.after, null)
})
