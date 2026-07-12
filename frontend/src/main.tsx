import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'

const chunkReloadKeyPrefix = 'mongojson:chunk-reload:'

window.addEventListener('vite:preloadError', (event) => {
  const entryScript = document.querySelector<HTMLScriptElement>('script[type="module"][src]')?.src ?? 'unknown-build'
  const reloadKey = `${chunkReloadKeyPrefix}${entryScript}`

  if (window.sessionStorage.getItem(reloadKey) === '1') {
    return
  }

  event.preventDefault()
  window.sessionStorage.setItem(reloadKey, '1')
  window.location.reload()
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
