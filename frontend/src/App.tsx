import { lazy, Suspense, type ReactNode } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import './App.css'
import { AppShell } from './components/layout/AppShell'
import { MusicPlayerProvider } from './components/music/MusicPlayerProvider'

const JsonToolPage = lazy(() => import('./pages/JsonToolPage').then((module) => ({ default: module.JsonToolPage })))
const InspectToolPage = lazy(() =>
  import('./pages/InspectToolPage').then((module) => ({ default: module.InspectToolPage })),
)
const MemoDocsPage = lazy(() => import('./pages/MemoDocsPage').then((module) => ({ default: module.MemoDocsPage })))
const MusicToolPage = lazy(() => import('./pages/MusicToolPage').then((module) => ({ default: module.MusicToolPage })))
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
      <MusicPlayerProvider>
        <Routes>
          <Route path="/" element={<AppShell />}>
            <Route index element={<Navigate to="/tools/inspect" replace />} />
            <Route path="tools/inspect" element={withSuspense(<InspectToolPage />)} />
            <Route path="tools/json" element={withSuspense(<JsonToolPage />)} />
            <Route path="tools/mongodb-json" element={withSuspense(<MongoJsonToolPage />)} />
            <Route path="tools/visualize" element={withSuspense(<VisualizeToolPage />)} />
            <Route path="tools/memo-docs" element={withSuspense(<MemoDocsPage />)} />
            <Route path="tools/music" element={withSuspense(<MusicToolPage />)} />
          </Route>
        </Routes>
      </MusicPlayerProvider>
    </BrowserRouter>
  )
}

export default App
