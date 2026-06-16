import type * as Monaco from 'monaco-editor'

export const MONGO_LANGUAGE_ID = 'mongodb-json'

let registered = false

export function ensureMongoLanguage(monaco: typeof Monaco) {
  if (registered) {
    return
  }

  registered = true

  monaco.languages.register({ id: MONGO_LANGUAGE_ID })
  monaco.languages.setLanguageConfiguration(MONGO_LANGUAGE_ID, {
    autoClosingPairs: [
      { open: '{', close: '}' },
      { open: '[', close: ']' },
      { open: '(', close: ')' },
      { open: '"', close: '"' },
      { open: "'", close: "'" },
    ],
    brackets: [
      ['{', '}'],
      ['[', ']'],
      ['(', ')'],
    ],
    surroundingPairs: [
      { open: '{', close: '}' },
      { open: '[', close: ']' },
      { open: '(', close: ')' },
      { open: '"', close: '"' },
      { open: "'", close: "'" },
    ],
  })

  monaco.languages.setMonarchTokensProvider(MONGO_LANGUAGE_ID, {
    brackets: [
      { open: '{', close: '}', token: 'delimiter.curly' },
      { open: '[', close: ']', token: 'delimiter.square' },
      { open: '(', close: ')', token: 'delimiter.parenthesis' },
    ],
    defaultToken: '',
    keywords: [
      'ObjectId',
      'ISODate',
      'NumberInt',
      'NumberLong',
      'NumberDecimal',
      'Timestamp',
      'BinData',
      'MinKey',
      'MaxKey',
      'DBRef',
      'RegExp',
      'Code',
      'UUID',
      'Date',
      'new',
      'true',
      'false',
      'null',
      'undefined',
      'NaN',
      'Infinity',
    ],
    tokenizer: {
      root: [
        [/"([^"\\]|\\.)*"(?=\s*:)/, 'key'],
        [/\$[A-Za-z_][\w$]*/, 'operator.keyword'],
        [/[{}[\]()]/, '@brackets'],
        [/,/, 'delimiter.comma'],
        [/:/, 'delimiter.colon'],
        [/\b-?(0|[1-9]\d*)(\.\d+)?([eE][\-+]?\d+)?\b/, 'number'],
        [/\b(true|false|null|undefined|NaN|Infinity)\b/, 'keyword'],
        [/\b[A-Za-z_][\w$]*\b/, {
          cases: {
            '@keywords': 'type.identifier',
            '@default': 'identifier',
          },
        }],
        [/"([^"\\]|\\.)*"/, 'string'],
        [/'([^'\\]|\\.)*'/, 'string'],
        [/\/\/.*$/, 'comment'],
        [/\/\*/, 'comment', '@comment'],
        [/\s+/, 'white'],
      ],
      comment: [
        [/[^/*]+/, 'comment'],
        [/\*\//, 'comment', '@pop'],
        [/./, 'comment'],
      ],
    },
  })

  monaco.editor.defineTheme('mongodb-vs-dark', {
    base: 'vs-dark',
    inherit: true,
    rules: [
      { token: 'key', foreground: 'D4D4D4' },
      { token: 'type.identifier', foreground: '4EC9B0', fontStyle: 'bold' },
      { token: 'operator.keyword', foreground: 'C586C0', fontStyle: 'bold' },
      { token: 'identifier', foreground: '9CDCFE' },
      { token: 'string', foreground: 'CE9178' },
      { token: 'number', foreground: 'B5CEA8' },
      { token: 'keyword', foreground: '569CD6' },
      { token: 'comment', foreground: '6A9955' },
      { token: 'delimiter.curly', foreground: 'FFD700' },
      { token: 'delimiter.square', foreground: 'DA70D6' },
      { token: 'delimiter.parenthesis', foreground: '4FC1FF' },
      { token: 'delimiter.comma', foreground: '808080' },
      { token: 'delimiter.colon', foreground: '808080' },
    ],
    colors: {},
  })
}
