#!/usr/bin/env node
'use strict'

const fs = require('fs')
const path = require('path')

const ROOT = path.resolve(__dirname, '..')
const SRC_DIR = path.join(ROOT, 'src')
const CONSTANTS_PATH = path.join(SRC_DIR, 'styles', 'layout-constants.json')
const constants = JSON.parse(fs.readFileSync(CONSTANTS_PATH, 'utf8'))

const allowedQueries = new Map(
  Object.values(constants.breakpoints).map((entry) => [
    `${entry.queryType}:${entry.value}`,
    entry,
  ]),
)

function collectCssFiles(dir) {
  const results = []
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const fullPath = path.join(dir, entry.name)
    if (entry.isDirectory()) results.push(...collectCssFiles(fullPath))
    else if (entry.isFile() && entry.name.endsWith('.css')) results.push(fullPath)
  }
  return results
}

function stripCssComments(content) {
  return content.replace(/\/\*[\s\S]*?\*\//g, (match) => match.replace(/[^\n]/g, ' '))
}

function lineNumber(content, offset) {
  return content.slice(0, offset).split('\n').length
}

const AT_RULE_RE = /@(media|container)\b([^{]*)\{/g
const WIDTH_RE = /(?:max|min)-width:\s*(\d+)px/g

function findQueryViolations(file, content) {
  const violations = []
  let rule
  AT_RULE_RE.lastIndex = 0
  while ((rule = AT_RULE_RE.exec(content)) !== null) {
    const kind = rule[1]
    const prelude = rule[2]
    let width
    WIDTH_RE.lastIndex = 0
    while ((width = WIDTH_RE.exec(prelude)) !== null) {
      const value = Number(width[1])
      const registered = allowedQueries.get(`${kind}:${value}`)
      if (!registered) {
        violations.push({ file, line: lineNumber(content, rule.index), message: `@${kind}${prelude.trim()} is not registered` })
        continue
      }
      if (kind === 'media' && value === 768 && !/orientation:\s*portrait/.test(prelude)) {
        violations.push({ file, line: lineNumber(content, rule.index), message: 'the 768px layout media query must also require orientation: portrait' })
      }
    }
  }
  return violations
}

function camelToKebab(value) {
  return value.replace(/[A-Z]/g, (letter) => `-${letter.toLowerCase()}`)
}

function findTokenViolations(cssFiles) {
  const declarations = new Map()
  const declarationRe = /--layout-([a-z0-9-]+):\s*([\d.]+)px/g
  for (const file of cssFiles) {
    const content = stripCssComments(fs.readFileSync(file, 'utf8'))
    let match
    while ((match = declarationRe.exec(content)) !== null) {
      declarations.set(match[1], { file: path.relative(ROOT, file), value: Number(match[2]) })
    }
  }

  const violations = []
  for (const [key, expected] of Object.entries(constants.sizeTokens)) {
    const cssName = camelToKebab(key)
    const declaration = declarations.get(cssName)
    if (!declaration) {
      violations.push(`missing --layout-${cssName} for sizeTokens.${key}`)
    } else if (declaration.value !== expected) {
      violations.push(`${declaration.file}: --layout-${cssName} is ${declaration.value}px, expected ${expected}px`)
    }
  }
  return violations
}

function main() {
  const cssFiles = collectCssFiles(SRC_DIR)
  const queryViolations = []
  for (const file of cssFiles) {
    const content = stripCssComments(fs.readFileSync(file, 'utf8'))
    queryViolations.push(...findQueryViolations(path.relative(ROOT, file), content))
  }
  const tokenViolations = findTokenViolations(cssFiles)

  if (queryViolations.length || tokenViolations.length) {
    if (queryViolations.length) {
      console.error('\n[validate-breakpoints] Invalid responsive queries:')
      for (const item of queryViolations) console.error(`  ${item.file}:${item.line} ${item.message}`)
    }
    if (tokenViolations.length) {
      console.error('\n[validate-breakpoints] Layout token mismatches:')
      for (const item of tokenViolations) console.error(`  ${item}`)
    }
    process.exit(1)
  }

  console.log(`[validate-breakpoints] OK — ${cssFiles.length} CSS file(s) use the project-wide minimum-grid contract.`)
}

main()
