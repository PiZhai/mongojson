import { readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, extname, relative, resolve, sep } from 'node:path'
import catalog from '../module-catalog.json' with { type: 'json' }

const projectRoot = resolve(import.meta.dirname, '..')
const sourceRoot = resolve(projectRoot, 'src')
const moduleRoot = resolve(sourceRoot, 'modules')
const sourceExtensions = new Set(['.ts', '.tsx', '.js', '.jsx'])
const violations = []

function walk(directory) {
  return readdirSync(directory).flatMap((name) => {
    const path = resolve(directory, name)
    return statSync(path).isDirectory() ? walk(path) : [path]
  })
}

function sourcePath(path) {
  return relative(sourceRoot, path).split(sep).join('/')
}

function resolveImport(importer, specifier) {
  if (!specifier.startsWith('.')) return null
  const candidate = resolve(dirname(importer), specifier)
  const candidates = [
    candidate,
    ...[...sourceExtensions].map((extension) => `${candidate}${extension}`),
    ...[...sourceExtensions].map((extension) => resolve(candidate, `index${extension}`)),
  ]
  return candidates.find((path) => {
    try {
      return statSync(path).isFile()
    } catch {
      return false
    }
  }) ?? candidate
}

function moduleId(path) {
  const relativePath = relative(moduleRoot, path)
  if (relativePath.startsWith('..')) return null
  const segments = relativePath.split(sep)
  return segments.length > 1 ? segments[0] : null
}

for (const file of walk(sourceRoot).filter((path) => sourceExtensions.has(extname(path)))) {
  const content = readFileSync(file, 'utf8')
  const importerModule = moduleId(file)
  const importerPath = sourcePath(file)
  const imports = [...content.matchAll(/(?:from\s+|import\s*\()\s*['"]([^'"]+)['"]/g)]

  for (const match of imports) {
    const imported = resolveImport(file, match[1])
    if (!imported) continue
    const importedModule = moduleId(imported)
    const importedPath = sourcePath(imported)

    if (importerModule && importedModule && importerModule !== importedModule) {
      violations.push(`${importerPath} imports another feature module: ${importedPath}`)
    }
    if (importerModule && importedPath.startsWith('app/')) {
      violations.push(`${importerPath} imports host implementation: ${importedPath}`)
    }
    if ((importerPath.startsWith('platform/') || importerPath.startsWith('shared/')) && importedModule) {
      violations.push(`${importerPath} imports feature implementation: ${importedPath}`)
    }
    if (!importerModule && importedModule) {
      violations.push(`${importerPath} bypasses the module registry: ${importedPath}`)
    }
  }
}

const forbiddenLegacyPaths = [
  'src/types/tooling.ts',
  'src/lib/api/client.ts',
  'src/components/music/MusicPlayerProvider.tsx',
]
for (const path of forbiddenLegacyPaths) {
  try {
    if (statSync(resolve(projectRoot, path)).isFile()) violations.push(`Legacy architecture file still exists: ${path}`)
  } catch {
    // Missing is the required state.
  }
}

for (const path of ['src/App.tsx', 'src/components/layout/AppShell.tsx']) {
  const content = readFileSync(resolve(projectRoot, path), 'utf8')
  if (content.includes('/tools/')) violations.push(`${path} contains a hard-coded tool route`)
}

for (const module of catalog) {
  for (const requiredFile of ['index.ts', 'manifest.ts', 'styles.css']) {
    const path = `src/modules/${module.id}/${requiredFile}`
    try {
      if (!statSync(resolve(projectRoot, path)).isFile()) violations.push(`Invalid module contract file: ${path}`)
    } catch {
      violations.push(`Missing module contract file: ${path}`)
    }
  }
}

if (violations.length > 0) {
  console.error(violations.map((violation) => `- ${violation}`).join('\n'))
  process.exit(1)
}

console.log('Frontend module boundaries are valid.')
