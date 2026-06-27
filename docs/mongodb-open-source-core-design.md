# MongoDB Tool Open Source Core Design

```yaml
doc_type: agent_design_spec
status: implemented_with_fallback
audience:
  - coding-agent
  - human-maintainer
created_at: 2026-06-26
scope:
  - frontend/src/components/tooling/MongoJsonWorkspace.tsx
  - frontend/src/components/tooling/mongo-json
  - frontend/src/lib/tooling/jsonFormatter
  - frontend/src/lib/tooling/mongoInspector.ts
decision_summary: >
  Use Monaco as the editor shell and interaction surface. Move parsing,
  formatting, repairing, and base validation into open source libraries behind
  a project-owned facade. Keep project-specific MongoDB risk rules and product
  summaries in local code.
```

## 1. Agent Reading Contract

This document is written as an implementation contract for future coding agents.
Read these sections before editing code:

1. `2. Target Decision`
2. `4. Layer Responsibilities`
3. `7. Facade API Contract`
4. `10. Migration Plan`
5. `11. Acceptance Criteria`

If any dependency API has changed, verify its current package docs before coding.
The package versions below are a planning snapshot from 2026-06-26, not a lockfile
requirement.

## 2. Target Decision

Adopt this architecture:

```text
Monaco = editor shell and interaction layer
Open source libraries = parsing, formatting, repairing, and base validation core
Project code = orchestration, MongoDB risk rules, summaries, tables, diffs, UX states
```

Do not keep growing a hand-written MongoDB parser inside React state hooks.
Do not replace Monaco. Monaco remains the best fit for editor rendering, model
markers, hover, actions, focus, theme, and keyboard behavior.

## 3. Current State

Current implementation:

- `frontend/src/lib/mongodb-core` is now the third-party parser facade. It wraps
  `bson` / `EJSON`, `@mongodb-js/shell-bson-parser`, `jsonrepair`, and
  `mongodb-query-parser`.
- MongoDB JSON format mode validates through the facade and can produce Canonical
  Extended JSON. Parsed BSON/JS values are converted into project `JsonNode`
  values for shell-notation output and downstream table/diff/schema workflows.
- Repair mode is explicit and outputs standard JSON through `jsonrepair`.
- Shell mode keeps the project method-chain summarizer and now adds query-part
  validation through `mongodb-query-parser`.
- `CodeEditor` / `MonacoEditorHost` accept facade diagnostics and render Monaco
  markers for parser, repair, and query-validation issues.
- `useMongoJsonWorkspaceState.ts` owns mode state, live validation, formatting
  actions, Shell checks, and risk inspection orchestration.
- `jsonFormatter/parser.ts` is a hand-written tokenizer/parser for relaxed
  MongoDB JSON-like input.
- `jsonFormatter/shell.ts` is a hand-written parser for `db.collection.method()`
  style chains.
- `mongoInspector.ts` contains useful project-specific query risk rules.
- Monaco currently provides editor UI through `CodeEditor`, `MonacoEditorHost`,
  and `MONGO_LANGUAGE_ID`.

Remaining issue:

- The old parser is still retained as a compatibility fallback when the open
  source parser rejects input that the existing UI has historically accepted.
  Rich hover/actions and full fallback retirement remain future hardening work.

## 4. Layer Responsibilities

| Layer | Owns | Must Not Own |
| --- | --- | --- |
| Monaco layer | Text editor UI, syntax theme, markers, hover, code actions, format action entrypoints, focus and line navigation | MongoDB semantic parsing, query risk decisions, BSON/EJSON conversion rules |
| Open source core | MongoDB shell value parsing, EJSON/BSON normalization, JSON repair, base query parameter validation, generic code formatting | Product risk policy, UI state, user-facing workflow decisions |
| Project rules | Risk scoring, query/pipeline summaries, method-chain outline, schema/table/diff workflows, localized messages | Low-level BSON parsing, generic JSON repair, generic JS formatting |
| React workspace | State composition, mode routing, command wiring, render data | Direct third-party parser calls scattered through components |

Invariant:

- React components call project facade functions only. Third-party parser and
  formatter packages stay behind `src/lib/mongodb-core`.

## 5. Selected Libraries

| Package | Planned Role | License Snapshot | Notes |
| --- | --- | --- | --- |
| `bson` | Standard Extended JSON and BSON type handling | Apache-2.0 | Use for EJSON parse/stringify and typed values. Not enough by itself for shell shorthand like `ObjectId(...)`. |
| `@mongodb-js/shell-bson-parser` | Parse MongoDB shell BSON value fragments | Apache-2.0 | Useful for `{ _id: ObjectId("...") }`, `new Date(...)`, comments in loose mode, and safe evaluation of supported BSON shell syntax. |
| `mongodb-query-parser` | Parse and validate filter/project/sort/collation/hint style query parameters | Apache-2.0 | Good for method arguments. Not a full formatter for entire `db.users.find(...).sort(...)` chains. |
| `jsonrepair` | Repair broken JSON into standard JSON | ISC | Use as explicit repair action only. It can convert MongoDB types into plain JSON values, so do not run it implicitly before Mongo formatting. |
| `prettier/standalone` | Format full Shell/JavaScript text | MIT | Use lazily for Shell formatting. It is a printer, not MongoDB semantic validation. |

Rejected for now:

- `mongodb-language-model`: relevant capability but SSPL license snapshot.
- `mongodb-ace-mode`: SSPL snapshot and conflicts with current Monaco direction.
- Plain `acorn` / `meriyah` as primary solution: useful internally, but they do
  not provide MongoDB semantics by themselves.

## 6. Proposed File Layout

Create facade and rule modules without deleting the current implementation first:

```text
frontend/src/lib/mongodb-core/
  index.ts
  types.ts
  parseMongoValue.ts
  formatMongoJson.ts
  formatMongoShell.ts
  repairJson.ts
  validateQuery.ts
  diagnostics.ts

frontend/src/lib/mongodb-rules/
  inspectMongoRisk.ts
  summarizeShell.ts
  summarizePipeline.ts

frontend/src/lib/editor/
  mongoMarkers.ts
  mongoHover.ts
  mongoActions.ts
```

Keep compatibility shims in `frontend/src/lib/tooling/jsonFormatter` during
migration so existing diff/table/schema code can move in small steps.

## 7. Facade API Contract

All UI and workspace state should depend on these stable project-owned APIs.
Names can change during implementation, but the boundary should remain.

```ts
export type MongoInputMode =
  | 'mongo-json'
  | 'mongo-shell'
  | 'standard-json'
  | 'repair-json'

export type DiagnosticSeverity = 'info' | 'warning' | 'error'

export type MongoDiagnostic = {
  code: string
  message: string
  severity: DiagnosticSeverity
  source: 'parser' | 'formatter' | 'repair' | 'query-validator' | 'risk-rule'
  offset?: number
  length?: number
  line?: number
  column?: number
  path?: string
}

export type MongoFormatResult = {
  ok: true
  text: string
  ast?: unknown
  diagnostics: MongoDiagnostic[]
  stats?: {
    chars: number
    lines: number
    maxDepth?: number
  }
} | {
  ok: false
  text?: string
  diagnostics: MongoDiagnostic[]
}

export type MongoShellSummary = {
  collection: string | null
  methods: Array<{
    name: string
    nameOffset?: number
    args: Array<{ text: string; offset?: number }>
  }>
  operators: Array<{ name: string; offset?: number }>
}

export function formatMongoJson(input: string): Promise<MongoFormatResult>
export function formatMongoShell(input: string): Promise<MongoFormatResult>
export function repairStandardJson(input: string): Promise<MongoFormatResult>
export function validateMongoQueryPart(kind: 'filter' | 'project' | 'sort' | 'collation' | 'hint', input: string): MongoFormatResult
export function summarizeMongoShell(input: string): MongoShellSummary
export function analyzeMongoInput(input: string, mode: MongoInputMode): Promise<MongoFormatResult>
```

Design constraints:

- Return diagnostics instead of throwing across the facade boundary.
- Preserve original input when formatting fails.
- Include offsets when a library can provide them. If not, return a diagnostic
  without location and let the UI show it in the status area.
- Keep all user-facing Chinese messages outside low-level library adapters when
  practical. Adapter diagnostics may use stable English `code` values.

## 8. Mode Behavior

### MongoDB JSON Format Mode

Preferred flow:

```text
input
  -> try shell-bson-parser for relaxed shell value fragments
  -> normalize via bson/EJSON where possible
  -> print via project formatter preserving Mongo type display
  -> return diagnostics + stats
```

Fallback:

- If open source parser cannot preserve a current feature, call the existing
  `formatJson()` implementation as a temporary compatibility fallback.
- Emit a diagnostic with `source: 'parser'` only when all parsers fail.

Do not:

- Automatically run `jsonrepair` before MongoDB formatting.
- Silently convert `ObjectId(...)` to a plain string in normal MongoDB JSON mode.

### Shell Mode

Preferred flow:

```text
input
  -> parse/summarize method chain with project shell summarizer
  -> parse method arguments with mongodb-query-parser or shell-bson-parser
  -> format full text with prettier/standalone when available
  -> run project risk rules
  -> return formatted text + summary + diagnostics
```

Why keep a project summarizer:

- `mongodb-query-parser` validates query parts, not full `db.collection.find().sort()`
  chains.
- Product UI needs collection/method/operator offsets for clickable summaries.

### Repair Mode

Preferred flow:

```text
input
  -> jsonrepair
  -> JSON.parse
  -> JSON.stringify with readable indentation
```

This mode should be explicit: button text should make clear that the output is
standard JSON, not MongoDB shell notation.

### Diff/Table/Schema Modes

These modes currently depend on AST shape from `jsonFormatter`.
Migration should use an adapter:

```ts
type ProjectJsonNode = existing JsonNode
function toProjectJsonNode(parsed: unknown): ProjectJsonNode
```

Only remove the old AST after diff/table/schema tests pass against the adapter.

## 9. Monaco Integration

Monaco remains the editor shell:

- `mongoMarkers.ts`: converts `MongoDiagnostic[]` into Monaco markers through
  `monaco.editor.setModelMarkers`.
- `mongoHover.ts`: describes known MongoDB methods, operators, BSON values, and
  project risk hints.
- `mongoActions.ts`: registers actions:
  - format MongoDB JSON
  - format Shell
  - repair as standard JSON
  - copy as Extended JSON
  - jump to first diagnostic

Marker severity mapping:

| Diagnostic Severity | Monaco Severity |
| --- | --- |
| `error` | `MarkerSeverity.Error` |
| `warning` | `MarkerSeverity.Warning` |
| `info` | `MarkerSeverity.Info` |

Monaco must not import `bson`, `jsonrepair`, `mongodb-query-parser`, or
`@mongodb-js/shell-bson-parser` directly. It receives facade results.

## 10. Migration Plan

### Phase 0: Baseline Protection

Tasks:

- Add focused tests around current behavior before replacing internals.
- Cover relaxed Mongo JSON, Shell method chain parsing, risk checks, diff/table
  compatibility, and error messages.

Acceptance:

- `npm run test` passes.
- Existing fixtures document current behavior and known limitations.

### Phase 1: Add Facade Without Behavior Change

Tasks:

- Add `mongodb-core` facade.
- Implement facade by delegating to current `jsonFormatter` and `mongoInspector`.
- Point `useMongoJsonWorkspaceState` at the facade only where low risk.

Acceptance:

- No user-visible behavior change.
- React components do not call new third-party libraries directly.

### Phase 2: Introduce Open Source Parsers Behind Facade

Tasks:

- Install `bson`, `@mongodb-js/shell-bson-parser`, and `mongodb-query-parser`.
- Add parser adapters with fallback to current parser.
- Add tests for BSON types and filter/project/sort validation.

Acceptance:

- Current examples still format.
- Mongo shell BSON values parse through open source adapter where supported.
- Diagnostics include stable `code` values.

### Phase 3: Add Explicit Repair and Standard EJSON Output

Tasks:

- Install `jsonrepair`.
- Add repair action and UI copy: "修复为标准 JSON".
- Add "复制为 Extended JSON" or "转为 Extended JSON" action using `bson`.

Acceptance:

- Repair mode does not run automatically in MongoDB JSON format mode.
- Mongo type preserving output remains available.

### Phase 4: Shell Formatting and Monaco Diagnostics

Tasks:

- Install or lazy import `prettier/standalone` and needed parser plugins.
- Format full Shell text through Prettier.
- Convert facade diagnostics to Monaco markers.
- Add hover/actions after markers are stable.

Acceptance:

- Formatting large Shell input does not block initial page load; use lazy import.
- Risk checks still appear in the Shell workspace.
- Monaco markers point to useful locations when offsets are known.

### Phase 5: Retire Old Parser Internals

Tasks:

- Remove obsolete tokenizer/parser code only after all consumers use adapters.
- Keep small project printers or converters if they express product-specific
  display choices.

Acceptance:

- Diff/table/schema workflows pass tests.
- Bundle size impact is measured and acceptable.
- No React component imports parser packages directly.

## 11. Acceptance Criteria

Functional:

- MongoDB JSON mode formats common relaxed MongoDB values and reports parse
  diagnostics without crashing.
- Shell mode formats method chains, summarizes collection/method/operator data,
  and keeps project risk checks.
- Repair action fixes common broken standard JSON without mutating MongoDB JSON
  mode behavior.
- Diff/table/schema workflows keep working through adapter output.

Architecture:

- Third-party parsing/formatting packages are imported only in `mongodb-core`
  or narrowly scoped adapter modules.
- Monaco integration consumes diagnostics and commands from project-owned APIs.
- Project risk rules live under `mongodb-rules` or equivalent local modules.

Quality:

- Unit tests cover parser fallback, facade result shapes, Shell summaries,
  diagnostics mapping, and risk rules.
- Build passes with Vite.
- Lazy-loaded heavy formatters do not inflate the initial route more than
  necessary.

## 12. Test Strategy

Add or update tests in `frontend/src/lib`:

```text
mongodb-core/
  formatMongoJson.test.ts
  formatMongoShell.test.ts
  repairJson.test.ts
  validateQuery.test.ts
  diagnostics.test.ts

mongodb-rules/
  inspectMongoRisk.test.ts
  summarizeShell.test.ts
```

Test fixtures should include:

- `{ _id: ObjectId("507f1f77bcf86cd799439011"), createdAt: ISODate("2024-01-01") }`
- `db.users.find({ active: true }).sort({ createdAt: -1 }).limit(20)`
- `db.users.deleteMany({})`
- aggregation with `$lookup`, `$unwind`, `$group`, `$sort`
- malformed JSON with missing quotes, trailing comma, comments, and missing bracket
- input that must not be auto-repaired in MongoDB JSON mode

## 13. Open Questions

1. Should MongoDB JSON default output preserve shell notation, canonical Extended
   JSON, or offer both as separate output modes?
2. Should Prettier be used for all Shell formatting, or only when the input is
   valid JavaScript according to its parser?
3. Should project risk rules be configurable by user preference later?
4. Should diagnostics be stored as localized messages immediately, or as stable
   codes plus UI-level localization?

Recommended defaults:

- Preserve shell notation in current format mode.
- Add explicit Extended JSON output action.
- Keep risk rule thresholds hard-coded until there is a real settings surface.
- Store stable diagnostic codes and format localized messages at UI boundary.

