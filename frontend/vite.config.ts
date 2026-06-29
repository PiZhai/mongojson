import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
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
})
