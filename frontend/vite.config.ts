import { fileURLToPath, URL } from 'node:url'
import { defineConfig, loadEnv, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import moduleCatalogConfig from './module-catalog.json'

const VIRTUAL_MODULE_ID = 'virtual:tool-module-catalog'
const RESOLVED_VIRTUAL_MODULE_ID = `\0${VIRTUAL_MODULE_ID}`

function moduleCatalogPlugin(includedModulesValue?: string): Plugin {
  const includedIds = new Set(
    (includedModulesValue ?? '')
      .split(',')
      .map((value) => value.trim())
      .filter(Boolean),
  )
  const knownIds = new Set(moduleCatalogConfig.map((entry) => entry.id))

  for (const id of includedIds) {
    if (!knownIds.has(id)) throw new Error(`Unknown module in VITE_INCLUDED_MODULES: ${id}`)
  }

  const entries = moduleCatalogConfig.filter((entry) => includedIds.size === 0 || includedIds.has(entry.id))
  if (entries.length === 0) throw new Error('VITE_INCLUDED_MODULES must include at least one frontend module.')

  return {
    name: 'mongojson-tool-module-catalog',
    resolveId(id) {
      return id === VIRTUAL_MODULE_ID ? RESOLVED_VIRTUAL_MODULE_ID : null
    },
    load(id) {
      if (id !== RESOLVED_VIRTUAL_MODULE_ID) return null
      const imports = entries.map(
        (entry, index) => `import { ${entry.exportName} as module${index} } from '${entry.importPath}'`,
      )
      const modules = entries.map((_, index) => `module${index}`).join(', ')
      return `${imports.join('\n')}\nexport const moduleCatalog = [${modules}]\n`
    },
  }
}

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const includedModules = process.env.VITE_INCLUDED_MODULES ?? env.VITE_INCLUDED_MODULES

  return {
    plugins: [moduleCatalogPlugin(includedModules), react()],
    resolve: {
      alias: {
        '@mongojson/vditor-core': fileURLToPath(new URL('./packages/vditor-core/src/index.ts', import.meta.url)),
      },
    },
    optimizeDeps: {
      exclude: ['@mongojson/vditor-core'],
    },
    server: {
      host: '127.0.0.1',
      port: 4174,
      proxy: {
        '/api': {
          target: 'http://127.0.0.1:18080',
          changeOrigin: true,
          ws: true,
        },
        '/healthz': {
          target: 'http://127.0.0.1:18080',
          changeOrigin: true,
        },
        '/readyz': {
          target: 'http://127.0.0.1:18080',
          changeOrigin: true,
        },
      },
    },
  }
})
