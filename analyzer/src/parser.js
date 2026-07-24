import { parse as parseJavaScript } from 'acorn'
import parseShellBSON, { ParseMode } from '@mongodb-js/shell-bson-parser'
import { isFilterValid } from 'mongodb-query-parser'
import { toExtendedJSON } from './ejson.js'

const operationNames = new Set([
  'insertOne',
  'insertMany',
  'updateOne',
  'updateMany',
  'deleteOne',
  'deleteMany',
  'bulkWrite',
])

function diagnostic(code, message, node, severity = 'warning') {
  return {
    code,
    message,
    severity,
    source: 'mongo-script-analyzer',
    offset: node?.start ?? 0,
    line: node?.loc?.start?.line ?? 1,
    column: (node?.loc?.start?.column ?? 0) + 1,
  }
}

function sourceFor(source, node) {
  return source.slice(node.start, node.end)
}

function parseFragment(source, node) {
  return parseShellBSON(sourceFor(source, node), {
    mode: ParseMode.Strict,
    allowComments: true,
    allowMethods: false,
  })
}

function evaluateStatic(source, node, constants, path = '$') {
  if (!node) return { known: false, value: null, unknownPaths: [path] }
  if (node.type === 'Identifier') {
    if (constants.has(node.name)) return constants.get(node.name)
    return { known: false, value: null, unknownPaths: [path] }
  }
  if (node.type === 'ObjectExpression') {
    const result = {}
    const unknownPaths = []
    for (const property of node.properties) {
      if (property.type !== 'Property' || property.computed || property.kind !== 'init') {
        unknownPaths.push(path)
        continue
      }
      const key = property.key.type === 'Identifier' ? property.key.name : String(property.key.value)
      const child = evaluateStatic(source, property.value, constants, `${path}.${key}`)
      result[key] = child.value
      unknownPaths.push(...child.unknownPaths)
    }
    return { known: unknownPaths.length === 0, value: result, unknownPaths }
  }
  if (node.type === 'ArrayExpression') {
    const result = []
    const unknownPaths = []
    node.elements.forEach((element, index) => {
      const child = evaluateStatic(source, element, constants, `${path}[${index}]`)
      result.push(child.value)
      unknownPaths.push(...child.unknownPaths)
    })
    return { known: unknownPaths.length === 0, value: result, unknownPaths }
  }
  if (node.type === 'TemplateLiteral' && node.expressions.length > 0) {
    return { known: false, value: null, unknownPaths: [path] }
  }
  if (node.type === 'CallExpression' && node.callee?.type === 'MemberExpression') {
    return { known: false, value: null, unknownPaths: [path] }
  }
  try {
    return { known: true, value: parseFragment(source, node), unknownPaths: [] }
  } catch {
    return { known: false, value: null, unknownPaths: [path] }
  }
}

function strictCollectionCall(source, node) {
  if (
    node?.type !== 'CallExpression'
    || node.callee?.type !== 'MemberExpression'
    || node.callee.computed
    || node.callee.object?.type !== 'Identifier'
    || node.callee.object.name !== 'db'
    || node.callee.property?.type !== 'Identifier'
    || node.callee.property.name !== 'getCollection'
    || node.arguments.length !== 1
    || node.arguments[0]?.type !== 'Literal'
    || typeof node.arguments[0].value !== 'string'
  ) {
    return null
  }
  const raw = sourceFor(source, node.arguments[0])
  if (!raw.startsWith('"')) return null
  return node.arguments[0].value
}

function operationCall(source, expression) {
  if (
    expression?.type !== 'CallExpression'
    || expression.callee?.type !== 'MemberExpression'
    || expression.callee.computed
    || expression.callee.property?.type !== 'Identifier'
    || !operationNames.has(expression.callee.property.name)
  ) {
    return null
  }
  const collection = strictCollectionCall(source, expression.callee.object)
  return {
    node: expression,
    collectionNode: expression.callee.object,
    collection,
    type: expression.callee.property.name,
  }
}

function descriptionFor(comments, previousEnd, operationStart) {
  return comments
    .filter((comment) => comment.start >= previousEnd && comment.end <= operationStart)
    .map((comment) => comment.value.trim())
    .filter(Boolean)
    .join('\n')
}

function serializeParameter(result) {
  return {
    value: result.known ? toExtendedJSON(result.value) : null,
    unresolvedPaths: result.unknownPaths,
  }
}

function regularOperation(source, call, constants, comments, previousEnd) {
  const args = call.node.arguments.map((arg, index) =>
    serializeParameter(evaluateStatic(source, arg, constants, `$args[${index}]`)),
  )
  const diagnostics = []
  if (!call.collection) {
    diagnostics.push(diagnostic(
      'collection-format',
      '集合必须使用 db.getCollection("collection")，且集合名必须是双引号静态字符串。',
      call.collectionNode,
      'error',
    ))
  }
  args.forEach((arg, index) => {
    if (arg.unresolvedPaths.length > 0) {
      diagnostics.push(diagnostic(
        'dynamic-argument',
        `第 ${index + 1} 个参数包含运行时值，相关字段已标记为“无法确定”。`,
        call.node.arguments[index],
      ))
    }
  })
  if (
    ['updateOne', 'updateMany', 'deleteOne', 'deleteMany'].includes(call.type)
    && call.node.arguments[0]?.type !== 'Identifier'
    && args[0]?.unresolvedPaths.length === 0
  ) {
    try {
      if (!isFilterValid(sourceFor(source, call.node.arguments[0]))) {
        diagnostics.push(diagnostic(
          'invalid-filter',
          '过滤条件未通过 MongoDB 查询解析器校验，操作不会查询数据库。',
          call.node.arguments[0],
          'error',
        ))
      }
    } catch (error) {
      diagnostics.push(diagnostic(
        'invalid-filter',
        error instanceof Error ? error.message : '过滤条件无法解析。',
        call.node.arguments[0],
        'error',
      ))
    }
  }
  return {
    id: `op-${call.node.start}`,
    type: call.type,
    collection: call.collection ?? '',
    queryable: Boolean(call.collection)
      && args.every((arg) => arg.unresolvedPaths.length === 0)
      && !diagnostics.some((item) => item.severity === 'error'),
    description: descriptionFor(comments, previousEnd, call.node.start),
    source: sourceFor(source, call.node),
    range: {
      start: call.node.start,
      end: call.node.end,
      startLine: call.node.loc.start.line,
      startColumn: call.node.loc.start.column + 1,
      endLine: call.node.loc.end.line,
      endColumn: call.node.loc.end.column + 1,
    },
    arguments: args.map((arg) => arg.value),
    unresolvedPaths: args.flatMap((arg) => arg.unresolvedPaths),
    diagnostics,
  }
}

function bulkOperations(source, operation, call, constants, comments, previousEnd) {
  const bulkArg = call.node.arguments[0]
  if (bulkArg?.type !== 'ArrayExpression') {
    operation.diagnostics.push(diagnostic(
      'bulk-dynamic',
      'bulkWrite 的操作列表必须是静态数组。',
      bulkArg ?? call.node,
      'error',
    ))
    operation.queryable = false
    return [operation]
  }
  const options = call.node.arguments[1]
    ? evaluateStatic(source, call.node.arguments[1], constants, '$options')
    : { known: true, value: { ordered: true }, unknownPaths: [] }
  operation.bulkOrdered = options.known ? options.value?.ordered !== false : true
  operation.children = []
  for (let index = 0; index < bulkArg.elements.length; index += 1) {
    const element = bulkArg.elements[index]
    const property = element?.type === 'ObjectExpression' && element.properties.length === 1
      ? element.properties[0]
      : null
    const type = property?.key?.type === 'Identifier' ? property.key.name : property?.key?.value
    if (!operationNames.has(type) || type === 'bulkWrite' || property?.value?.type !== 'ObjectExpression') {
      operation.diagnostics.push(diagnostic(
        'bulk-operation',
        `bulkWrite 第 ${index + 1} 项不是受支持的静态操作。`,
        element ?? bulkArg,
        'error',
      ))
      if (operation.bulkOrdered) break
      continue
    }
    const payload = evaluateStatic(source, property.value, constants, `$bulk[${index}]`)
    operation.children.push({
      id: `${operation.id}-${index}`,
      index,
      type,
      arguments: [payload.known ? toExtendedJSON(payload.value) : null],
      unresolvedPaths: payload.unknownPaths,
      queryable: operation.queryable && payload.known,
      diagnostics: payload.known ? [] : [
        diagnostic('dynamic-argument', `bulkWrite 第 ${index + 1} 项包含运行时值。`, property.value),
      ],
    })
    if (!payload.known && operation.bulkOrdered) break
  }
  operation.description = descriptionFor(comments, previousEnd, operation.range.start)
  return [operation]
}

export function parseMongoScript(source) {
  if (typeof source !== 'string' || source.length === 0) {
    return { operations: [], diagnostics: [diagnostic('empty-script', '脚本不能为空。', null, 'error')] }
  }
  const comments = []
  let program
  try {
    program = parseJavaScript(source, {
      ecmaVersion: 'latest',
      sourceType: 'script',
      locations: true,
      ranges: true,
      onComment: comments,
    })
  } catch (error) {
    return {
      operations: [],
      diagnostics: [{
        code: 'syntax-error',
        message: error.message,
        severity: 'error',
        source: 'acorn',
        offset: error.pos ?? 0,
        line: error.loc?.line ?? 1,
        column: (error.loc?.column ?? 0) + 1,
      }],
    }
  }

  const constants = new Map()
  const preamble = program.body
    .filter((statement) => statement.type === 'VariableDeclaration' && ['const', 'let'].includes(statement.kind))
    .map((statement) => sourceFor(source, statement))
    .join('\n')
  const diagnostics = []
  for (const statement of program.body) {
    if (statement.type !== 'VariableDeclaration' || !['const', 'let'].includes(statement.kind)) continue
    for (const declaration of statement.declarations) {
      if (declaration.id.type !== 'Identifier' || !declaration.init) continue
      constants.set(declaration.id.name, evaluateStatic(source, declaration.init, constants, `$const.${declaration.id.name}`))
    }
  }

  const operations = []
  let previousEnd = 0
  for (const statement of program.body) {
    if (statement.type === 'VariableDeclaration' || statement.type === 'EmptyStatement') continue
    const call = operationCall(source, statement.type === 'ExpressionStatement' ? statement.expression : null)
    if (!call) {
      diagnostics.push(diagnostic(
        'unsupported-statement',
        '仅支持顶层静态 MongoDB 操作；循环、函数、分支和其他运行时代码不会执行。',
        statement,
      ))
      continue
    }
    const parsed = regularOperation(source, call, constants, comments, previousEnd)
    if (parsed.type === 'bulkWrite') {
      operations.push(...bulkOperations(source, parsed, call, constants, comments, previousEnd))
    } else {
      operations.push(parsed)
    }
    const latest = operations[operations.length - 1]
    if (latest) latest.contextSource = preamble ? `${preamble}\n\n${latest.source}` : latest.source
    previousEnd = statement.end
  }
  return { operations, diagnostics }
}
