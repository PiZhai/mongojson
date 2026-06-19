import { lazy, Suspense, type ReactNode } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import './App.css'
import { AppShell } from './components/layout/AppShell'

const JsonToolPage = lazy(() => import('./pages/JsonToolPage').then((module) => ({ default: module.JsonToolPage })))
const MongoJsonToolPage = lazy(() =>
  import('./pages/MongoJsonToolPage').then((module) => ({ default: module.MongoJsonToolPage })),
)
const VisualizeToolPage = lazy(() =>
  import('./pages/VisualizeToolPage').then((module) => ({ default: module.VisualizeToolPage })),
)

function PageFallback() {
  return (
    <div className="route-loading" role="status">
      正在加载工具...
    </div>
  )
}

function withSuspense(node: ReactNode) {
  return <Suspense fallback={<PageFallback />}>{node}</Suspense>
}

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<AppShell />}>
          <Route index element={<Navigate to="/tools/json" replace />} />
          <Route path="tools/json" element={withSuspense(<JsonToolPage />)} />
          <Route path="tools/mongodb-json" element={withSuspense(<MongoJsonToolPage />)} />
          <Route path="tools/visualize" element={withSuspense(<VisualizeToolPage />)} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}

export default App
