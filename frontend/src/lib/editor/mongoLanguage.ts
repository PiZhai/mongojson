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
      { token: 'key', foreground: '172033' },
      { token: 'type.identifier', foreground: '0f9f8f', fontStyle: 'bold' },
      { token: 'operator.keyword', foreground: '7c3aed', fontStyle: 'bold' },
      { token: 'identifier', foreground: '007aff' },
      { token: 'string', foreground: 'c56a05' },
      { token: 'number', foreground: '2f9e44' },
      { token: 'keyword', foreground: '005ecb' },
      { token: 'comment', foreground: '6f7d91' },
      { token: 'delimiter.curly', foreground: 'f97316' },
      { token: 'delimiter.square', foreground: 'af52de' },
      { token: 'delimiter.parenthesis', foreground: '0f9f8f' },
      { token: 'delimiter.comma', foreground: '6f7d91' },
      { token: 'delimiter.colon', foreground: '6f7d91' },
    ],
    colors: {
      'editor.background': '#fbfdff',
      'editor.foreground': '#243044',
      'editorLineNumber.foreground': '#9aa8bb',
      'editorCursor.foreground': '#007aff',
      'editor.selectionBackground': '#d8ecff',
      'editor.inactiveSelectionBackground': '#e8eef6',
      'editor.lineHighlightBackground': '#f1f7ff',
    },
  })
}
