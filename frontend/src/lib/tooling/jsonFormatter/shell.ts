import type { ShellArg, ShellStatement, ShellValidation } from '../../../shared/data/types'
import { formatJson } from './format'

const KNOWN_MONGO_METHODS: Record<string, number> = {
  find: 1,
  findOne: 1,
  insertOne: 1,
  insertMany: 1,
  updateOne: 1,
  updateMany: 1,
  replaceOne: 1,
  deleteOne: 1,
  deleteMany: 1,
  remove: 1,
  aggregate: 1,
  countDocuments: 1,
  estimatedDocumentCount: 1,
  distinct: 1,
  findOneAndUpdate: 1,
  findOneAndDelete: 1,
  findOneAndReplace: 1,
  bulkWrite: 1,
  createIndex: 1,
  dropIndex: 1,
  getIndexes: 1,
  watch: 1,
  rename: 1,
  drop: 1,
  sort: 1,
  limit: 1,
  skip: 1,
  project: 1,
  hint: 1,
  toArray: 1,
  explain: 1,
  count: 1,
  pretty: 1,
  getCollection: 1,
}

const KNOWN_MONGO_OPS: Record<string, number> = {
  $set: 1,
  $unset: 1,
  $inc: 1,
  $mul: 1,
  $rename: 1,
  $min: 1,
  $max: 1,
  $push: 1,
  $pull: 1,
  $addToSet: 1,
  $pop: 1,
  $pullAll: 1,
  $each: 1,
  $match: 1,
  $project: 1,
  $group: 1,
  $sort: 1,
  $limit: 1,
  $skip: 1,
  $unwind: 1,
  $lookup: 1,
  $addFields: 1,
  $replaceRoot: 1,
  $replaceWith: 1,
  $facet: 1,
  $bucket: 1,
  $sortByCount: 1,
  $count: 1,
  $and: 1,
  $or: 1,
  $not: 1,
  $nor: 1,
  $eq: 1,
  $ne: 1,
  $gt: 1,
  $gte: 1,
  $lt: 1,
  $lte: 1,
  $in: 1,
  $nin: 1,
  $exists: 1,
  $type: 1,
  $regex: 1,
  $mod: 1,
  $elemMatch: 1,
  $all: 1,
  $size: 1,
  $expr: 1,
  $text: 1,
  $sum: 1,
  $avg: 1,
  $first: 1,
  $last: 1,
  $merge: 1,
  $out: 1,
  $sample: 1,
  $redact: 1,
  $geoNear: 1,
  $currentDate: 1,
  $setOnInsert: 1,
  $position: 1,
  $slice: 1,
  $cond: 1,
  $ifNull: 1,
  $switch: 1,
  $arrayElemAt: 1,
  $concat: 1,
  $dateToString: 1,
  $toLower: 1,
  $toUpper: 1,
  $substr: 1,
  $trim: 1,
}

function parseShellArgs(input: string, startPos: number) {
  let i = startPos
  let argStart = startPos
  let depth = 0
  let inDoubleQuote = false
  let inSingleQuote = false
  const args: ShellArg[] = []

  for (; i < input.length; i += 1) {
    const ch = input[i]
    if (inDoubleQuote) {
      if (ch === '\\') i += 1
      else if (ch === '"') inDoubleQuote = false
      continue
    }
    if (inSingleQuote) {
      if (ch === '\\') i += 1
      else if (ch === "'") inSingleQuote = false
      continue
    }
    if (ch === '"') {
      inDoubleQuote = true
      continue
    }
    if (ch === "'") {
      inSingleQuote = true
      continue
    }
    if (ch === '(' || ch === '{' || ch === '[') depth += 1
    else if (ch === ')' || ch === '}' || ch === ']') {
      depth -= 1
      if (depth === -1 && ch === ')') {
        const last = input.slice(argStart, i).trim()
        if (last) args.push({ text: last, start: argStart, end: i })
        return { args, endPos: i + 1 }
      }
    } else if (ch === ',' && depth === 0) {
      const text = input.slice(argStart, i).trim()
      if (text) args.push({ text, start: argStart, end: i })
      argStart = i + 1
    }
  }

  return { args, endPos: i }
}

export function parseShellStatement(input: string): ShellStatement | null {
  if (!input.trim()) return null
  let i = 0

  const skipWhitespace = () => {
    while (i < input.length) {
      if (/\s/.test(input[i])) {
        i += 1
        continue
      }
      if (input[i] === '/' && input[i + 1] === '/') {
        while (i < input.length && input[i] !== '\n') i += 1
        continue
      }
      break
    }
  }

  skipWhitespace()
  if (!/^db/i.test(input.slice(i, i + 2))) return null
  i += 2
  skipWhitespace()
  if (input[i] !== '.') return null
  i += 1
  skipWhitespace()

  let collection = ''
  let collectionStart: number
  if (/^getCollection/i.test(input.slice(i, i + 13))) {
    collectionStart = i
    i += 13
    skipWhitespace()
    if (input[i] === '(') {
      i += 1
      skipWhitespace()
      const quote = input[i]
      if (quote === '"' || quote === "'") {
        i += 1
        collectionStart = i
        while (i < input.length && input[i] !== quote) {
          collection += input[i]
          i += 1
        }
        i += 1
      }
      skipWhitespace()
      if (input[i] === ')') i += 1
    }
  } else {
    collectionStart = i
    while (/[a-zA-Z0-9_-]/.test(input[i] ?? '')) {
      collection += input[i]
      i += 1
    }
  }

  if (!collection) return null
  const collectionEnd = i

  const methods: ShellStatement['methods'] = []
  const operators: Array<{ name: string; pos: number }> = []
  while (i < input.length) {
    skipWhitespace()
    if (input[i] !== '.') break
    i += 1
    skipWhitespace()
    const nameStart = i
    let method = ''
    while (/[a-zA-Z0-9_]/.test(input[i] ?? '')) {
      method += input[i]
      i += 1
    }
    const nameEnd = i
    skipWhitespace()
    if (input[i] === '(') {
      const openParen = i
      i += 1
      const parsed = parseShellArgs(input, i)
      i = parsed.endPos
      const closeParen = Math.max(openParen, i - 1)
      methods.push({ name: method, nameStart, nameEnd, openParen, closeParen, argsRaw: parsed.args })
    } else {
      methods.push({ name: method, nameStart, nameEnd, openParen: nameEnd, closeParen: nameEnd, argsRaw: [] })
    }
  }

  const opRegex = /\$[a-zA-Z][a-zA-Z0-9]*/g
  let match = opRegex.exec(input)
  while (match) {
    operators.push({ name: match[0], pos: match.index })
    match = opRegex.exec(input)
  }

  return { collection, collectionStart, collectionEnd, methods, operators }
}

export function formatShellStatement(input: string): string | null {
  const parsed = parseShellStatement(input)
  if (!parsed || parsed.methods.length === 0) return null

  const collectionRef = `db.getCollection("${parsed.collection}")`
  const formatArg = (raw: ShellArg) => {
    const result = formatJson(raw.text, false)
    return 'error' in result ? raw.text : result.formatted
  }

  return parsed.methods
    .map((method, index) => {
      const prefix = `${index === 0 ? collectionRef : ''}.${method.name}(`
      const args = method.argsRaw.map(formatArg)
      if (args.length === 0) return `${prefix})`
      if (args.length === 1 && !args[0].includes('\n')) return `${prefix}${args[0]})`
      const body = args
        .map((arg, argIndex) => {
          const indented = arg
            .split('\n')
            .map((line) => `  ${line}`)
            .join('\n')
          return `${indented}${argIndex < args.length - 1 ? ',' : ''}`
        })
        .join('\n')
      return `${prefix}\n${body}\n)`
    })
    .join('\n')
}

export function validateShellStatement(input: string): ShellValidation[] {
  const parsed = parseShellStatement(input)
  if (!parsed) {
    return [{ level: 'err', msg: '未识别为有效的 MongoDB Shell 语句（期望 db.xxx）' }]
  }

  const checks: ShellValidation[] = [{ level: 'ok', msg: `集合: ${parsed.collection}, ${parsed.methods.length} 个方法调用` }]

  parsed.methods.forEach((method) => {
    if (!KNOWN_MONGO_METHODS[method.name]) {
      checks.push({ level: 'warn', msg: `未知方法: .${method.name}()` })
    }
  })

  const foundOps: Record<string, number> = {}
  const opRegex = /\$[a-zA-Z][a-zA-Z0-9]*/g
  let match = opRegex.exec(input)
  while (match) {
    foundOps[match[0]] = (foundOps[match[0]] ?? 0) + 1
    match = opRegex.exec(input)
  }

  const unknownOps = Object.keys(foundOps).filter((item) => !KNOWN_MONGO_OPS[item])
  if (unknownOps.length > 0) {
    checks.push({
      level: 'warn',
      msg: `可能未定义的操作符: ${unknownOps.slice(0, 5).join(', ')}${unknownOps.length > 5 ? '…' : ''}`,
    })
  }

  let depth = 0
  let inString = false
  let stringChar = ''
  for (let i = 0; i < input.length; i += 1) {
    const ch = input[i]
    if (inString) {
      if (ch === '\\') i += 1
      else if (ch === stringChar) inString = false
      continue
    }
    if (ch === '"' || ch === "'") {
      inString = true
      stringChar = ch
      continue
    }
    if (ch === '(' || ch === '{' || ch === '[') depth += 1
    if (ch === ')' || ch === '}' || ch === ']') depth -= 1
  }
  if (depth !== 0) {
    checks.push({ level: 'err', msg: `括号不匹配 (深度: ${depth})` })
  }

  return checks
}
