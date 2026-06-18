import type {
  DiffSummary,
  JsonFormatResponse,
  JsonNode,
  ShellValidation,
  TableData,
  TableValidation,
} from '../../types/tooling'

const MONGO_FUNCTIONS = new Set([
  'ObjectId',
  'ISODate',
  'NumberLong',
  'NumberInt',
  'NumberDecimal',
  'BinData',
  'Timestamp',
  'Date',
  'RegExp',
  'UUID',
  'Code',
  'DBRef',
])

const MONGO_KEYWORDS = new Set(['MinKey', 'MaxKey'])
const LITERALS = new Set(['true', 'false', 'null', 'undefined', 'NaN', 'Infinity'])

type Token =
  | { type: '{' | '}' | '[' | ']' | ':' | ','; pos: number }
  | { type: 'STRING'; value: string; pos: number }
  | { type: 'NUMBER'; value: string; pos: number }
  | { type: 'LITERAL'; value: string; pos: number }
  | { type: 'MONGO'; func: string; args: string | null; pos: number }
  | { type: 'IDENT'; value: string; pos: number }

type FormatMeta = {
  text: string
  error: string | null
  lineCount: number
  charCount: number
  maxDepth?: number
  ast: JsonNode | null
  keyLineMap: Record<string, number>
}

type ShellArg = {
  text: string
  start: number
  end: number
}

type ShellMethod = {
  name: string
  nameStart: number
  nameEnd: number
  openParen: number
  closeParen: number
  argsRaw: ShellArg[]
}

type ShellStatement = {
  collection: string
  collectionStart: number
  collectionEnd: number
  methods: ShellMethod[]
  operators: Array<{ name: string; pos: number }>
}

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

function tokenize(input: string): Token[] {
  const tokens: Token[] = []
  let i = 0

  while (i < input.length) {
    const ch = input[i]
    const startPos = i

    if (ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r') {
      i += 1
      continue
    }

    if (ch === '/' && input[i + 1] === '/') {
      while (i < input.length && input[i] !== '\n') i += 1
      continue
    }

    if (ch === '/' && input[i + 1] === '*') {
      i += 2
      while (i < input.length - 1 && !(input[i] === '*' && input[i + 1] === '/')) i += 1
      if (i < input.length) i += 2
      continue
    }

    if ('{}[]:,'.includes(ch)) {
      tokens.push({ type: ch as '{' | '}' | '[' | ']' | ':' | ',', pos: startPos })
      i += 1
      continue
    }

    if (ch === '"' || ch === "'") {
      const quote = ch
      i += 1
      let str = ''
      while (i < input.length) {
        if (input[i] === '\\' && i + 1 < input.length) {
          str += input[i] + input[i + 1]
          i += 2
        } else if (input[i] === quote) {
          i += 1
          break
        } else {
          str += input[i]
          i += 1
        }
      }
      tokens.push({ type: 'STRING', value: str, pos: startPos })
      continue
    }

    if (ch === '-' && /[0-9]/.test(input[i + 1] ?? '')) {
      let value = '-'
      i += 1
      while (/[0-9]/.test(input[i] ?? '')) {
        value += input[i]
        i += 1
      }
      if (input[i] === '.') {
        value += '.'
        i += 1
        while (/[0-9]/.test(input[i] ?? '')) {
          value += input[i]
          i += 1
        }
      }
      if (input[i] === 'e' || input[i] === 'E') {
        value += input[i]
        i += 1
        if (input[i] === '+' || input[i] === '-') {
          value += input[i]
          i += 1
        }
        while (/[0-9]/.test(input[i] ?? '')) {
          value += input[i]
          i += 1
        }
      }
      tokens.push({ type: 'NUMBER', value, pos: startPos })
      continue
    }

    if (/[0-9]/.test(ch)) {
      let value = ''
      while (/[0-9]/.test(input[i] ?? '')) {
        value += input[i]
        i += 1
      }
      if (input[i] === '.') {
        value += '.'
        i += 1
        while (/[0-9]/.test(input[i] ?? '')) {
          value += input[i]
          i += 1
        }
      }
      if (input[i] === 'e' || input[i] === 'E') {
        value += input[i]
        i += 1
        if (input[i] === '+' || input[i] === '-') {
          value += input[i]
          i += 1
        }
        while (/[0-9]/.test(input[i] ?? '')) {
          value += input[i]
          i += 1
        }
      }
      tokens.push({ type: 'NUMBER', value, pos: startPos })
      continue
    }

    if (/[a-zA-Z_$]/.test(ch)) {
      let word = ''
      while (/[a-zA-Z0-9_$]/.test(input[i] ?? '')) {
        word += input[i]
        i += 1
      }

      if (word === 'new') {
        while (/\s/.test(input[i] ?? '')) i += 1
        const typeStart = i
        let typeWord = ''
        while (/[a-zA-Z_]/.test(input[i] ?? '')) {
          typeWord += input[i]
          i += 1
        }
        while (/\s/.test(input[i] ?? '')) i += 1
        if (input[i] === '(') {
          i += 1
          let args = ''
          let depth = 1
          while (i < input.length && depth > 0) {
            if (input[i] === '(') depth += 1
            else if (input[i] === ')') depth -= 1
            if (depth > 0) args += input[i]
            i += 1
          }
          tokens.push({ type: 'MONGO', func: `new ${typeWord}`, args: args.trim(), pos: startPos })
        } else {
          tokens.push({ type: 'IDENT', value: word, pos: startPos })
          i = typeStart
        }
        continue
      }

      if (MONGO_FUNCTIONS.has(word)) {
        while (/\s/.test(input[i] ?? '')) i += 1
        if (input[i] === '(') {
          i += 1
          let args = ''
          let depth = 1
          while (i < input.length && depth > 0) {
            if (input[i] === '(') depth += 1
            else if (input[i] === ')') depth -= 1
            if (depth > 0) args += input[i]
            i += 1
          }
          tokens.push({ type: 'MONGO', func: word, args: args.trim(), pos: startPos })
        } else {
          tokens.push({ type: 'IDENT', value: word, pos: startPos })
        }
        continue
      }

      if (MONGO_KEYWORDS.has(word)) {
        tokens.push({ type: 'MONGO', func: word, args: null, pos: startPos })
        continue
      }

      if (LITERALS.has(word)) {
        tokens.push({ type: 'LITERAL', value: word, pos: startPos })
        continue
      }

      tokens.push({ type: 'IDENT', value: word, pos: startPos })
      continue
    }

    i += 1
  }

  return tokens
}

function parse(tokens: Token[]): JsonNode {
  let pos = 0

  const peek = () => tokens[pos] ?? null
  const next = () => {
    if (pos >= tokens.length) {
      const error = new Error('Unexpected end of input') as Error & { position?: number }
      error.position = -1
      throw error
    }
    return tokens[pos++]
  }

  const errorAt = (message: string, token: Token | null) => {
    const error = new Error(message) as Error & { position?: number }
    error.position = token?.pos ?? tokens[pos]?.pos ?? -1
    return error
  }

  const expect = (type: Token['type']) => {
    const token = next()
    if (token.type !== type) {
      throw errorAt(`Expected ${type} but got '${token.type}'`, token)
    }
    return token
  }

  const parseValue = (): JsonNode => {
    const token = peek()
    if (!token) throw errorAt('Unexpected end of input', null)

    switch (token.type) {
      case '{':
        return parseObject()
      case '[':
        return parseArray()
      case 'STRING':
        next()
        return { type: 'string', value: token.value }
      case 'NUMBER':
        next()
        return { type: 'number', value: token.value }
      case 'LITERAL':
        next()
        return { type: 'literal', value: token.value }
      case 'MONGO':
        next()
        return { type: 'mongo', func: token.func, args: token.args }
      case 'IDENT':
        next()
        return { type: 'string', value: token.value }
      default:
        throw errorAt(`Unexpected token '${token.type}'`, token)
    }
  }

  const parseObject = (): JsonNode => {
    expect('{')
    const entries: Array<{ key: string; value: JsonNode }> = []

    if (peek()?.type === '}') {
      next()
      return { type: 'object', entries }
    }

    while (true) {
      const token = peek()
      let key = ''
      if (token?.type === 'STRING' || token?.type === 'IDENT') {
        key = token.value
        next()
      } else {
        throw errorAt(`Expected property key but got ${token ? `'${token.type}'` : 'EOF'}`, token)
      }

      if (peek()?.type === ':') next()
      else throw errorAt(`Expected ':' after key '${key}'`, peek())

      entries.push({ key, value: parseValue() })

      const tail = peek()
      if (tail?.type === ',') {
        next()
        if (peek()?.type === '}') {
          next()
          break
        }
      } else if (tail?.type === '}') {
        next()
        break
      } else {
        throw errorAt(`Expected ',' or '}' but got ${tail ? `'${tail.type}'` : 'EOF'}`, tail)
      }
    }

    return { type: 'object', entries }
  }

  const parseArray = (): JsonNode => {
    expect('[')
    const items: JsonNode[] = []

    if (peek()?.type === ']') {
      next()
      return { type: 'array', items }
    }

    while (true) {
      items.push(parseValue())
      const tail = peek()
      if (tail?.type === ',') {
        next()
        if (peek()?.type === ']') {
          next()
          break
        }
      } else if (tail?.type === ']') {
        next()
        break
      } else {
        throw errorAt(`Expected ',' or ']' but got ${tail ? `'${tail.type}'` : 'EOF'}`, tail)
      }
    }

    return { type: 'array', items }
  }

  const ast = parseValue()
  if (pos < tokens.length) {
    const token = tokens[pos]
    throw errorAt(`Unexpected tokens after end of JSON: ${token.type}`, token)
  }

  return ast
}

function formatAst(node: JsonNode, indent = 0, indentSize = 2): string {
  const pad = ' '.repeat(indent)
  const inner = ' '.repeat(indent + indentSize)

  switch (node.type) {
    case 'object': {
      if (node.entries.length === 0) return `${pad}{}`
      const parts = node.entries.map((entry) => {
        const value = formatAst(entry.value, indent + indentSize, indentSize)
        return `${inner}"${entry.key}" : ${value.trimStart()}`
      })
      return `${pad}{\n${parts.join(',\n')}\n${pad}}`
    }
    case 'array': {
      if (node.items.length === 0) return `${pad}[]`
      const parts = node.items.map((item) => formatAst(item, indent + indentSize, indentSize))
      const allSimple = node.items.every(
        (item) =>
          item.type === 'string' ||
          item.type === 'number' ||
          item.type === 'literal' ||
          item.type === 'mongo' ||
          (item.type === 'object' && item.entries.length === 0) ||
          (item.type === 'array' && item.items.length === 0),
      )
      if (allSimple) {
        return `${pad}[ ${parts.map((part) => part.trim()).join(', ')} ]`
      }
      return `${pad}[\n${parts.join(',\n')}\n${pad}]`
    }
    case 'string':
      return `${pad}"${node.value}"`
    case 'number':
      return `${pad}${node.value}`
    case 'literal':
      return `${pad}${node.value}`
    case 'mongo':
      return node.args != null ? `${pad}${node.func}(${node.args})` : `${pad}${node.func}`
  }
}

function formatAstCompact(node: JsonNode): string {
  switch (node.type) {
    case 'object':
      return `{${node.entries.map((entry) => `"${entry.key}":${formatAstCompact(entry.value)}`).join(',')}}`
    case 'array':
      return `[${node.items.map((item) => formatAstCompact(item)).join(',')}]`
    case 'string':
      return `"${node.value}"`
    case 'number':
      return node.value
    case 'literal':
      return node.value
    case 'mongo':
      return node.args != null ? `${node.func}(${node.args})` : node.func
  }
}

function compareFieldKeys(a: string, b: string) {
  return a.localeCompare(b, 'en', {
    numeric: true,
    sensitivity: 'base',
  })
}

function computeMaxDepth(node: JsonNode | null): number {
  if (!node) return 0
  switch (node.type) {
    case 'object':
      return node.entries.length === 0
        ? 1
        : 1 + Math.max(...node.entries.map((entry) => computeMaxDepth(entry.value)))
    case 'array':
      return node.items.length === 0 ? 1 : 1 + Math.max(...node.items.map((item) => computeMaxDepth(item)))
    default:
      return 0
  }
}

export function formatJson(input: string, compact = false): JsonFormatResponse {
  if (!input.trim()) {
    return { error: '输入为空' }
  }

  try {
    const tokens = tokenize(input)
    const ast = parse(tokens)
    const formatted = compact ? formatAstCompact(ast) : formatAst(ast, 0, 2)
    return {
      formatted,
      ast,
      lineCount: formatted.split('\n').length,
      charCount: formatted.length,
      maxDepth: computeMaxDepth(ast),
    }
  } catch (error) {
    const detail = error as Error & { position?: number }
    return {
      error: detail.message,
      position: detail.position,
    }
  }
}

function sortAstNode(node: JsonNode): JsonNode {
  switch (node.type) {
    case 'object':
      return {
        type: 'object',
        entries: node.entries
          .map((entry) => ({ key: entry.key, value: sortAstNode(entry.value) }))
          .sort((a, b) => compareFieldKeys(a.key, b.key)),
      }
    case 'array':
      return {
        type: 'array',
        items: node.items.map((item) => sortAstNode(item)),
      }
    case 'string':
      return { type: 'string', value: node.value }
    case 'number':
      return { type: 'number', value: node.value }
    case 'literal':
      return { type: 'literal', value: node.value }
    case 'mongo':
      return { type: 'mongo', func: node.func, args: node.args }
  }
}

function joinFieldPath(basePath: string, segment: string | number) {
  if (typeof segment === 'number') {
    return basePath ? `${basePath}[${segment}]` : `[${segment}]`
  }
  return basePath ? `${basePath}.${segment}` : segment
}

function collectFieldPaths(node: JsonNode | null, basePath: string, paths: Array<{ path: string; key: string }>) {
  if (!node) return

  if (node.type === 'object') {
    for (const entry of node.entries) {
      const entryPath = joinFieldPath(basePath, entry.key)
      paths.push({ path: entryPath, key: entry.key })
      collectFieldPaths(entry.value, entryPath, paths)
    }
    return
  }

  if (node.type === 'array') {
    node.items.forEach((item, index) => collectFieldPaths(item, joinFieldPath(basePath, index), paths))
  }
}

function buildKeyLineMap(formattedText: string, ast: JsonNode | null) {
  const keyLineMap: Record<string, number> = {}
  if (!ast) return keyLineMap
  const orderedPaths: Array<{ path: string; key: string }> = []
  collectFieldPaths(ast, '', orderedPaths)
  const lines = formattedText.split('\n')
  let cursor = 0

  for (const item of orderedPaths) {
    const escapedKey = item.key.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    const pattern = new RegExp(`"${escapedKey}"\\s*:`)
    for (let lineIndex = cursor; lineIndex < lines.length; lineIndex += 1) {
      if (pattern.test(lines[lineIndex])) {
        keyLineMap[item.path] = lineIndex + 1
        cursor = lineIndex + 1
        break
      }
    }
  }

  return keyLineMap
}

function compareFieldDiffs(nodeA: JsonNode | undefined, nodeB: JsonNode | undefined, basePath: string, result: DiffSummary) {
  if (!nodeA && !nodeB) return
  if (!nodeA) {
    if (basePath) result.rightOnly.push(basePath)
    return
  }
  if (!nodeB) {
    if (basePath) result.leftOnly.push(basePath)
    return
  }

  if (nodeA.type === 'object' && nodeB.type === 'object') {
    const mapA: Record<string, JsonNode> = {}
    const mapB: Record<string, JsonNode> = {}
    const keys = new Set<string>()
    nodeA.entries.forEach((entry) => {
      mapA[entry.key] = entry.value
      keys.add(entry.key)
    })
    nodeB.entries.forEach((entry) => {
      mapB[entry.key] = entry.value
      keys.add(entry.key)
    })

    Array.from(keys)
      .sort(compareFieldKeys)
      .forEach((key) => {
        const path = joinFieldPath(basePath, key)
        if (!(key in mapB)) {
          result.leftOnly.push(path)
        } else if (!(key in mapA)) {
          result.rightOnly.push(path)
        } else {
          compareFieldDiffs(mapA[key], mapB[key], path, result)
        }
      })
    return
  }

  if (nodeA.type === 'array' && nodeB.type === 'array') {
    const maxLength = Math.max(nodeA.items.length, nodeB.items.length)
    for (let index = 0; index < maxLength; index += 1) {
      compareFieldDiffs(nodeA.items[index], nodeB.items[index], joinFieldPath(basePath, index), result)
    }
    return
  }

  if (formatAstCompact(nodeA) !== formatAstCompact(nodeB) && basePath) {
    result.changed.push(basePath)
  }
}

export function getFieldDiffSummary(astA: JsonNode | null, astB: JsonNode | null): DiffSummary {
  const result: DiffSummary = { leftOnly: [], rightOnly: [], changed: [] }
  if (!astA || !astB) return result
  compareFieldDiffs(astA, astB, '', result)
  return result
}

export function normalizeForCompare(input: string): FormatMeta {
  if (!input.trim()) {
    return {
      text: '',
      error: null,
      lineCount: 0,
      charCount: 0,
      maxDepth: 0,
      ast: null,
      keyLineMap: {},
    }
  }

  const result = formatJson(input, false)
  if ('error' in result) {
    return {
      text: input,
      error: result.error,
      lineCount: input.split('\n').length,
      charCount: input.length,
      ast: null,
      keyLineMap: {},
    }
  }

  const sortedAst = sortAstNode(result.ast)
  const text = formatAst(sortedAst, 0, 2)
  return {
    text,
    error: null,
    lineCount: text.split('\n').length,
    charCount: text.length,
    maxDepth: computeMaxDepth(sortedAst),
    ast: sortedAst,
    keyLineMap: buildKeyLineMap(text, sortedAst),
  }
}

function astValueToDisplay(node: JsonNode | null | undefined): string | null {
  if (!node) return null
  switch (node.type) {
    case 'string':
      return node.value
    case 'number':
      return node.value
    case 'literal':
      return node.value === 'null' || node.value === 'undefined' ? null : node.value
    case 'mongo':
      return node.args != null ? `${node.func}(${node.args})` : node.func
    case 'object':
      return node.entries.length === 0 ? '{}' : `{…${node.entries.length}}`
    case 'array':
      return node.items.length === 0 ? '[]' : `[…${node.items.length}]`
  }
}

function astValueToType(node: JsonNode | null | undefined): string {
  if (!node) return 'null'
  switch (node.type) {
    case 'string':
      return 'string'
    case 'number':
      return 'number'
    case 'literal':
      if (node.value === 'true' || node.value === 'false') return 'bool'
      if (node.value === 'null' || node.value === 'undefined') return 'null'
      return 'string'
    case 'mongo':
      if (node.func === 'ObjectId') return 'oid'
      if (node.func === 'ISODate' || node.func === 'new Date' || node.func === 'Date') return 'date'
      return 'mongo'
    case 'object':
      return 'object'
    case 'array':
      return 'array'
  }
}

function flattenAst(node: JsonNode | null | undefined, prefix: string, result: Record<string, JsonNode | null>) {
  if (!node) {
    result[prefix] = null
    return
  }

  if (node.type === 'object') {
    if (node.entries.length === 0) {
      result[prefix] = node
      return
    }
    for (const entry of node.entries) {
      flattenAst(entry.value, prefix ? `${prefix}.${entry.key}` : entry.key, result)
    }
    return
  }

  if (node.type === 'array') {
    result[prefix] = node
    node.items.slice(0, 10).forEach((item, index) => {
      flattenAst(item, `${prefix}[${index}]`, result)
    })
    return
  }

  result[prefix] = node
}

function buildValidation(schema: TableData['schema'], docCount: number): TableValidation[] {
  const checks: TableValidation[] = []
  const fullyNull = schema.filter((item) => item.nullCount === item.totalCount)
  if (fullyNull.length > 0) {
    checks.push({
      level: 'warn',
      msg: `${fullyNull.length} 个字段全为 null: ${fullyNull
        .slice(0, 3)
        .map((item) => item.path)
        .join(', ')}${fullyNull.length > 3 ? '…' : ''}`,
    })
  }

  const mixed = schema.filter((item) => item.isMixed)
  if (mixed.length > 0) {
    checks.push({
      level: 'warn',
      msg: `${mixed.length} 个字段类型不一致: ${mixed
        .slice(0, 3)
        .map((item) => `${item.path}[${Object.keys(item.typeCounts).join('/')}]`)
        .join(', ')}${mixed.length > 3 ? '…' : ''}`,
    })
  }

  const highNull = schema.filter((item) => item.nullRatio > 0.5 && item.nullRatio < 1)
  if (highNull.length > 0) {
    checks.push({
      level: 'warn',
      msg: `${highNull.length} 个字段缺失率>50%: ${highNull
        .slice(0, 3)
        .map((item) => `${item.path} (${Math.round(item.nullRatio * 100)}%)`)
        .join(', ')}${highNull.length > 3 ? '…' : ''}`,
    })
  }

  let maxDepth = 0
  schema.forEach((item) => {
    const depth = (item.path.match(/\./g) ?? []).length
    if (depth > maxDepth) maxDepth = depth
  })
  checks.unshift({ level: 'ok', msg: `${docCount} 条文档, ${schema.length} 个字段, 最大嵌套深度 ${maxDepth}` })
  return checks
}

export function buildTableFromAst(ast: JsonNode | null): TableData | null {
  if (!ast) return null
  const docs: JsonNode[] =
    ast.type === 'array'
      ? ast.items.filter((item): item is Extract<JsonNode, { type: 'object' }> => item.type === 'object')
      : ast.type === 'object'
        ? [ast]
        : []

  if (docs.length === 0) return null

  const allPathsSet: Record<string, boolean> = {}
  const pathOrder: string[] = []
  const docMaps: Array<Record<string, JsonNode | null>> = []

  for (const doc of docs) {
    const flat: Record<string, JsonNode | null> = {}
    flattenAst(doc, '', flat)
    docMaps.push(flat)
    Object.keys(flat).forEach((key) => {
      if (!allPathsSet[key]) {
        allPathsSet[key] = true
        pathOrder.push(key)
      }
    })
  }

  pathOrder.sort((a, b) => {
    const depthA = (a.match(/\./g) ?? []).length
    const depthB = (b.match(/\./g) ?? []).length
    if (depthA !== depthB) return depthA - depthB
    return a.localeCompare(b)
  })

  const schema = pathOrder.map((path) => {
    const typeCounts: Record<string, number> = {}
    let nullCount = 0
    for (const map of docMaps) {
      const node = map[path]
      if (node == null) {
        nullCount += 1
        continue
      }
      const type = astValueToType(node)
      typeCounts[type] = (typeCounts[type] ?? 0) + 1
    }

    let dominantType = 'null'
    let maxCount = 0
    Object.entries(typeCounts).forEach(([type, count]) => {
      if (count > maxCount) {
        dominantType = type
        maxCount = count
      }
    })

    return {
      path,
      dominantType,
      isMixed: Object.keys(typeCounts).length > 1,
      typeCounts,
      nullCount,
      totalCount: docMaps.length,
      nullRatio: nullCount / docMaps.length,
    }
  })

  const rows = docMaps.map((map) => schema.map((column) => (map[column.path] !== undefined ? map[column.path] : null)))

  return {
    schema,
    rows,
    validation: buildValidation(schema, docs.length),
    docCount: docs.length,
  }
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
  let collectionStart = i
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
    else if (ch === ')' || ch === '}' || ch === ']') depth -= 1
  }

  if (depth !== 0) {
    checks.push({ level: 'err', msg: `括号不匹配 (深度: ${depth})` })
  }

  return checks
}

export function escapeJsonString(rawInput: string): { output?: string; error?: string } {
  let trimmed = rawInput.trim()
  if (trimmed.startsWith('"') && trimmed.endsWith('"')) {
    try {
      const inner = JSON.parse(trimmed)
      if (typeof inner === 'string') trimmed = inner
    } catch {
      // noop
    }
  }

  let jsonText = trimmed
  try {
    JSON.parse(trimmed)
  } catch {
    const result = formatJson(trimmed, false)
    if ('error' in result) return { error: '无法解析为有效 JSON' }
    jsonText = result.formatted
  }

  try {
    const parsed = JSON.parse(jsonText)
    const compact = JSON.stringify(parsed)
    return { output: JSON.stringify(compact) }
  } catch {
    return { error: '无法解析为有效 JSON' }
  }
}

export function unescapeJsonString(rawInput: string): { output?: string; error?: string } {
  const trimmed = rawInput.trim()
  let inner: string | undefined

  try {
    const parsed = JSON.parse(trimmed)
    if (typeof parsed === 'string') inner = parsed
  } catch {
    inner = undefined
  }

  if (!inner && ((trimmed.startsWith('"') && trimmed.endsWith('"')) || (trimmed.startsWith("'") && trimmed.endsWith("'")))) {
    try {
      const parsed = JSON.parse(trimmed.slice(1, -1))
      inner = typeof parsed === 'string' ? parsed : trimmed.slice(1, -1)
    } catch {
      inner = trimmed.slice(1, -1)
    }
  }

  if (!inner) return { error: '输入不是转义后的 JSON 字符串' }

  const result = formatJson(inner, false)
  if ('error' in result) return { error: '转义内容无法解析为有效 JSON' }
  return { output: result.formatted }
}

export function astNodeToDisplay(node: JsonNode | null | undefined): string {
  return astValueToDisplay(node) ?? 'NULL'
}
