import type {
  GeneratedSchema,
  GeneratedSchemaTarget,
  JsonNode,
  SchemaProfile,
  SchemaProfileField,
  TableData,
} from '../../types/tooling'
import { astNodeToDisplay, buildTableFromAst } from './jsonFormatter'

function sanitizeName(path: string) {
  const raw = path
    .replace(/\[\d+]/g, 'Item')
    .split('.')
    .map((part) => part.replace(/[^a-zA-Z0-9_]/g, '_'))
    .join('_')
  return /^[a-zA-Z_]/.test(raw) ? raw : `field_${raw}`
}

function toPascalCase(value: string) {
  return sanitizeName(value)
    .split('_')
    .filter(Boolean)
    .map((part) => `${part[0]?.toUpperCase() ?? ''}${part.slice(1)}`)
    .join('')
}

function typeScriptType(type: string) {
  if (type === 'number') return 'number'
  if (type === 'bool') return 'boolean'
  if (type === 'array') return 'unknown[]'
  if (type === 'object') return 'Record<string, unknown>'
  if (type === 'null') return 'null'
  return 'string'
}

function zodType(type: string) {
  if (type === 'number') return 'z.number()'
  if (type === 'bool') return 'z.boolean()'
  if (type === 'array') return 'z.array(z.unknown())'
  if (type === 'object') return 'z.record(z.string(), z.unknown())'
  if (type === 'null') return 'z.null()'
  return 'z.string()'
}

function goType(type: string) {
  if (type === 'number') return 'float64'
  if (type === 'bool') return 'bool'
  if (type === 'array') return '[]any'
  if (type === 'object') return 'map[string]any'
  return 'string'
}

function examplesForColumn(table: TableData, columnIndex: number) {
  const seen = new Set<string>()
  table.rows.forEach((row) => {
    const value = astNodeToDisplay(row[columnIndex])
    if (value !== 'NULL' && seen.size < 3) seen.add(value)
  })
  return Array.from(seen)
}

export function buildSchemaProfile(ast: JsonNode | null): SchemaProfile | null {
  const table = buildTableFromAst(ast)
  if (!table) return null

  const fields: SchemaProfileField[] = table.schema.map((column, index) => {
    const presenceRatio = 1 - column.nullRatio
    const risks: string[] = []
    if (column.isMixed) risks.push('类型不稳定')
    if (column.nullRatio > 0.5) risks.push('缺失率高')
    if (column.nullRatio === 1) risks.push('字段全为空')
    if (column.path.includes('password') || column.path.includes('token') || column.path.includes('secret')) risks.push('疑似敏感字段')

    return {
      path: column.path,
      dominantType: column.dominantType,
      optional: column.nullRatio > 0,
      nullRatio: column.nullRatio,
      presenceRatio,
      isMixed: column.isMixed,
      typeCounts: column.typeCounts,
      examples: examplesForColumn(table, index),
      risks,
    }
  })

  return {
    docCount: table.docCount,
    fieldCount: fields.length,
    nullableFieldCount: fields.filter((field) => field.nullRatio > 0).length,
    mixedFieldCount: fields.filter((field) => field.isMixed).length,
    riskFieldCount: fields.filter((field) => field.risks.length > 0).length,
    fields,
  }
}

export function generateSchema(profile: SchemaProfile, target: GeneratedSchemaTarget): GeneratedSchema {
  if (target === 'typescript') {
    const lines = profile.fields.map((field) => {
      const optional = field.optional ? '?' : ''
      const type = field.isMixed
        ? Object.keys(field.typeCounts)
            .map(typeScriptType)
            .join(' | ')
        : typeScriptType(field.dominantType)
      return `  ${JSON.stringify(field.path)}${optional}: ${type}`
    })
    return { target, code: `export interface MongoDocument {\n${lines.join('\n')}\n}` }
  }

  if (target === 'zod') {
    const lines = profile.fields.map((field) => {
      const optional = field.optional ? '.optional()' : ''
      return `  ${JSON.stringify(field.path)}: ${zodType(field.dominantType)}${optional},`
    })
    return { target, code: `import { z } from 'zod'\n\nexport const MongoDocumentSchema = z.object({\n${lines.join('\n')}\n})` }
  }

  const lines = profile.fields.map((field) => {
    const name = toPascalCase(field.path)
    const pointer = field.optional ? '*' : ''
    return `  ${name} ${pointer}${goType(field.dominantType)} \`json:"${field.path}${field.optional ? ',omitempty' : ''}"\``
  })
  return { target, code: `type MongoDocument struct {\n${lines.join('\n')}\n}` }
}
