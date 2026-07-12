import type { JsonNode } from '../../../shared/data/types'

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

export function tokenize(input: string): Token[] {
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

export function parse(tokens: Token[]): JsonNode {
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
      let key: string
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
