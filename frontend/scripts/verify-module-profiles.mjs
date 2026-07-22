import { readFileSync, readdirSync, statSync } from 'node:fs'
import { resolve } from 'node:path'
import { spawnSync } from 'node:child_process'
import catalog from '../module-catalog.json' with { type: 'json' }

const projectRoot = resolve(import.meta.dirname, '..')
const viteEntry = resolve(projectRoot, 'node_modules/vite/bin/vite.js')
const distRoot = resolve(projectRoot, 'dist')
const routeByModule = {
  inspect: '/tools/inspect',
  json: '/tools/json',
  'mongo-json': '/tools/mongodb-json',
  visualize: '/tools/visualize',
  'memo-docs': '/documents/memo',
  music: '/entertainment/music',
  'watch-party': '/entertainment/watch',
  canvas: '/documents/canvas',
  steward: '/steward',
}
const cssMarkerByModule = {
  inspect: '.inspect-',
  'mongo-json': '.mongo-',
  visualize: '.visualization-',
  'memo-docs': '.memo-',
  music: '.music-',
  'watch-party': '.watch-video-frame-idle',
  canvas: '.canvas-',
}

function walk(directory) {
  return readdirSync(directory).flatMap((name) => {
    const path = resolve(directory, name)
    return statSync(path).isDirectory() ? walk(path) : [path]
  })
}

for (const selected of catalog) {
  const result = spawnSync(process.execPath, [viteEntry, 'build'], {
    cwd: projectRoot,
    env: { ...process.env, VITE_INCLUDED_MODULES: selected.id, VITE_DISABLED_MODULES: '' },
    encoding: 'utf8',
  })
  if (result.status !== 0) {
    process.stderr.write(result.stdout)
    process.stderr.write(result.stderr)
    process.exit(result.status ?? 1)
  }

  const javascript = walk(distRoot)
    .filter((path) => path.endsWith('.js'))
    .map((path) => readFileSync(path, 'utf8'))
    .join('\n')
  const css = walk(distRoot)
    .filter((path) => path.endsWith('.css'))
    .map((path) => readFileSync(path, 'utf8'))
    .join('\n')

  if (!javascript.includes(routeByModule[selected.id])) {
    throw new Error(`Single-module build did not contain its route: ${selected.id}`)
  }
  for (const excluded of catalog.filter((entry) => entry.id !== selected.id)) {
    if (javascript.includes(routeByModule[excluded.id])) {
      throw new Error(`Single-module build ${selected.id} leaked excluded route ${excluded.id}`)
    }
    const excludedCssMarker = cssMarkerByModule[excluded.id]
    if (excludedCssMarker && css.includes(excludedCssMarker)) {
      throw new Error(`Single-module build ${selected.id} leaked CSS from ${excluded.id}`)
    }
  }
  console.log(`Verified standalone build profile: ${selected.id}`)
}
