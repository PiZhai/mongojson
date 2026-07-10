#!/usr/bin/env node
/**
 * Static validator for the "single source of truth" breakpoint architecture.
 *
 * Scans every .css file under frontend/src for @media / @container rules and
 * checks that every max-width / min-width literal used in a rule condition
 * matches a value registered in frontend/src/styles/layout-constants.json.
 * Any breakpoint literal that is not registered causes the script to exit
 * with a non-zero status and a report of file/line/value.
 *
 * It also cross-checks the sizing CSS custom properties declared in the
 * index.css :root block against the `sizeTokens` section of the same JSON
 * file, so the two representations (CSS vars for runtime styling, JSON for
 * tooling/tests) cannot silently drift apart.
 *
 * Usage: node scripts/validate-breakpoints.cjs
 */
'use strict'

const fs = require('fs')
const path = require('path')

const ROOT = path.resolve(__dirname, '..')
const SRC_DIR = path.join(ROOT, 'src')
const CONSTANTS_PATH = path.join(SRC_DIR, 'styles', 'layout-constants.json')

/** @type {{ sizeTokens: Record<string, number>, breakpoints: Record<string, { value: number }> }} */
const constants = JSON.parse(fs.readFileSync(CONSTANTS_PATH, 'utf8'))

const allowedBreakpointValues = new Set(
  Object.values(constants.breakpoints).map((entry) => entry.value),
)

/** Recursively collect all .css files under a directory. */
function collectCssFiles(dir) {
  const results = []
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const fullPath = path.join(dir, entry.name)
    if (entry.isDirectory()) {
      results.push(...collectCssFiles(fullPath))
    } else if (entry.isFile() && entry.name.endsWith('.css')) {
      results.push(fullPath)
    }
  }
  return results
}

/**
 * Matches the full prelude of an @media / @container rule, up to (but not
 * including) the opening `{`, e.g.:
 *   @media (max-width: 1080px)
 *   @container tool-workspace (max-width: 1319px)
 *   @media (min-width: 1081px) and (max-width: 1500px)
 * A prelude may contain more than one max-width/min-width condition (as in
 * the last example above), so every width literal inside it must be
 * extracted individually rather than just the first one.
 */
const AT_RULE_PRELUDE_RE = /@(media|container)\b([^{]*)\{/g
const WIDTH_LITERAL_RE = /(?:max|min)-width:\s*(\d+)px/g

function findBreakpointViolations(filePath, content) {
  const violations = []
  const lines = content.split('\n')
  const lineStartOffsets = []
  let offset = 0
  for (const line of lines) {
    lineStartOffsets.push(offset)
    offset += line.length + 1
  }

  function lineNumberForOffset(charOffset) {
    let lineNo = 1
    for (let i = 0; i < lineStartOffsets.length; i += 1) {
      if (lineStartOffsets[i] <= charOffset) {
        lineNo = i + 1
      } else {
        break
      }
    }
    return lineNo
  }

  let ruleMatch
  AT_RULE_PRELUDE_RE.lastIndex = 0
  while ((ruleMatch = AT_RULE_PRELUDE_RE.exec(content)) !== null) {
    const prelude = ruleMatch[2]
    const preludeStartOffset = ruleMatch.index

    let widthMatch
    WIDTH_LITERAL_RE.lastIndex = 0
    while ((widthMatch = WIDTH_LITERAL_RE.exec(prelude)) !== null) {
      const value = Number(widthMatch[1])
      if (!allowedBreakpointValues.has(value)) {
        violations.push({
          file: filePath,
          line: lineNumberForOffset(preludeStartOffset + widthMatch.index),
          value,
          snippet: `@${ruleMatch[1]}${prelude.trim()}`,
        })
      }
    }
  }
  return violations
}

/** Extract --workspace-* / --sidebar-* size-related custom properties from :root. */
const ROOT_VAR_RE = /--(workspace-[a-z-]+|sidebar-[a-z-]+):\s*([\d.]+)px/g

const KNOWN_CSS_VAR_MAP = {
  'workspace-gap': 'workspaceGap',
  'workspace-primary-min': 'workspacePrimaryMin',
  'workspace-primary-max': 'workspacePrimaryMax',
  'workspace-secondary-min': 'workspaceSecondaryMin',
  'workspace-secondary-max': 'workspaceSecondaryMax',
  'workspace-outline-min': 'workspaceOutlineMin',
  'workspace-outline-max': 'workspaceOutlineMax',
  'sidebar-expanded-width': 'sidebarExpandedWidth',
  'sidebar-collapsed-width': 'sidebarCollapsedWidth',
}

function findSizeTokenMismatches(filePath, content) {
  const mismatches = []
  let match
  ROOT_VAR_RE.lastIndex = 0
  while ((match = ROOT_VAR_RE.exec(content)) !== null) {
    const cssVarName = match[1]
    const cssValue = Number(match[2])
    const jsonKey = KNOWN_CSS_VAR_MAP[cssVarName]
    if (!jsonKey) {
      continue // not a size token we track (e.g. workspace-secondary-dock-min is intentionally unmapped)
    }
    const expected = constants.sizeTokens[jsonKey]
    if (expected !== undefined && expected !== cssValue) {
      mismatches.push({
        file: filePath,
        cssVar: `--${cssVarName}`,
        cssValue,
        jsonKey,
        expected,
      })
    }
  }
  return mismatches
}

/**
 * Strips /* ... *\/ comment blocks from CSS source, replacing their content
 * with spaces (preserving line/column positions and newlines so the
 * line-number lookup used by findBreakpointViolations stays accurate).
 * This prevents breakpoint literals mentioned only in prose inside a
 * comment (e.g. explaining a past migration) from being mistaken for a
 * real @media/@container rule.
 */
function stripCssComments(content) {
  return content.replace(/\/\*[\s\S]*?\*\//g, (match) =>
    match.replace(/[^\n]/g, ' '),
  )
}

function main() {
  const cssFiles = collectCssFiles(SRC_DIR)
  const allViolations = []
  const allMismatches = []

  for (const filePath of cssFiles) {
    const rawContent = fs.readFileSync(filePath, 'utf8')
    const content = stripCssComments(rawContent)
    const relPath = path.relative(ROOT, filePath)
    allViolations.push(...findBreakpointViolations(relPath, content))
    allMismatches.push(...findSizeTokenMismatches(relPath, content))
  }

  let hasError = false

  if (allViolations.length > 0) {
    hasError = true
    console.error('\n[validate-breakpoints] Unregistered breakpoint literals found:\n')
    for (const v of allViolations) {
      console.error(`  ${v.file}:${v.line}  ${v.snippet}  -> ${v.value}px is not registered in layout-constants.json`)
    }
    console.error(
      `\nRegistered breakpoint values: ${[...allowedBreakpointValues].sort((a, b) => a - b).join(', ')}px`,
    )
    console.error(
      'Add a new entry to frontend/src/styles/layout-constants.json (with a formula) before using a new breakpoint value.\n',
    )
  }

  if (allMismatches.length > 0) {
    hasError = true
    console.error('\n[validate-breakpoints] CSS variable / JSON sizeTokens mismatch:\n')
    for (const m of allMismatches) {
      console.error(
        `  ${m.file}  ${m.cssVar}: ${m.cssValue}px  !=  sizeTokens.${m.jsonKey}: ${m.expected}px`,
      )
    }
    console.error('')
  }

  if (!hasError) {
    console.log(
      `[validate-breakpoints] OK — scanned ${cssFiles.length} CSS file(s), all breakpoints and size tokens are consistent with layout-constants.json.`,
    )
    process.exit(0)
  }

  process.exit(1)
}

main()
