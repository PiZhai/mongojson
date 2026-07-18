import { Suspense, type ReactNode } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import './App.css'
import { moduleRegistry, resolveCapability } from './app/modules/registry'
import { getModulePage } from './app/modules/pageLoader'
import { ModuleProviders } from './app/modules/runtime'
import { ManagementAuthGate } from './components/auth/ManagementAuthGate'
import { AppShell } from './components/layout/AppShell'
import { CapabilityNavigationProvider } from './platform/workspace/CapabilityNavigationProvider'

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
    <ManagementAuthGate>
      <BrowserRouter>
        <CapabilityNavigationProvider resolveCapability={resolveCapability}>
          <ModuleProviders>
            <Routes>
              <Route path="/" element={<AppShell />}>
                <Route index element={<Navigate to={moduleRegistry.defaultPath} replace />} />
                {moduleRegistry.modules.map((module) => {
                  const Page = getModulePage(module)
                  return <Route element={withSuspense(<Page />)} key={module.id} path={module.route.path} />
                })}
                <Route path="*" element={<Navigate to={moduleRegistry.defaultPath} replace />} />
              </Route>
            </Routes>
          </ModuleProviders>
        </CapabilityNavigationProvider>
      </BrowserRouter>
    </ManagementAuthGate>
  )
}

export default App
