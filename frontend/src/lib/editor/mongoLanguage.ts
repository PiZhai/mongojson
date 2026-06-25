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
        [/\b-?(0|[1-9]\d*)(\.\d+)?([eE][-+]?\d+)?\b/, 'number'],
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

  monaco.editor.defineTheme('mongodb-light', {
    base: 'vs',
    inherit: true,
    rules: [
      { token: 'key', foreground: '1f2937' },
      { token: 'type.identifier', foreground: '047857', fontStyle: 'bold' },
      { token: 'operator.keyword', foreground: '7c3aed', fontStyle: 'bold' },
      { token: 'identifier', foreground: '2563eb' },
      { token: 'string', foreground: 'b45309' },
      { token: 'number', foreground: '047857' },
      { token: 'keyword', foreground: '1d4ed8' },
      { token: 'comment', foreground: '6b7280' },
      { token: 'delimiter.curly', foreground: 'ca8a04' },
      { token: 'delimiter.square', foreground: '9333ea' },
      { token: 'delimiter.parenthesis', foreground: '0284c7' },
      { token: 'delimiter.comma', foreground: '64748b' },
      { token: 'delimiter.colon', foreground: '64748b' },
    ],
    colors: {
      'editor.background': '#fbfcfe',
      'editor.foreground': '#1f2937',
      'editorLineNumber.foreground': '#9ca3af',
      'editorCursor.foreground': '#2563eb',
      'editor.selectionBackground': '#dbeafe',
      'editor.inactiveSelectionBackground': '#e5e7eb',
      'editor.lineHighlightBackground': '#f3f6fb',
    },
  })
}
