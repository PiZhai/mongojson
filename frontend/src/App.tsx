import { Suspense, type ComponentType, type ReactNode } from 'react'
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import './App.css'
import './components/layout/WorkspaceShells.css'
import { moduleRegistry, resolveCapability } from './app/modules/registry'
import { getModulePage } from './app/modules/pageLoader'
import { ModuleProviders, ShellExtensionSlot } from './app/modules/runtime'
import { ManagementAuthGate } from './components/auth/ManagementAuthGate'
import { ToolsWorkspaceShell } from './components/layout/AppShell'
import {
  DocumentsWorkspaceShell,
  EntertainmentWorkspaceShell,
  StewardWorkspaceShell,
} from './components/layout/WorkspaceShells'
import type { WorkspaceId } from './platform/contracts/modules'
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

const workspaceShells: Record<WorkspaceId, ComponentType> = {
  tools: ToolsWorkspaceShell,
  entertainment: EntertainmentWorkspaceShell,
  documents: DocumentsWorkspaceShell,
  steward: StewardWorkspaceShell,
}

function LegacyRedirect({ to }: { to: string }) {
  const location = useLocation()
  return <Navigate replace to={`${to}${location.search}${location.hash}`} />
}

function App() {
  return (
    <ManagementAuthGate>
      <BrowserRouter>
        <CapabilityNavigationProvider resolveCapability={resolveCapability}>
          <ModuleProviders>
            <Routes>
              <Route path="/" element={<Navigate to={moduleRegistry.defaultPath} replace />} />
              {moduleRegistry.workspaces.map((workspace) => {
                const Shell = workspaceShells[workspace.id]
                return (
                  <Route element={<Shell />} key={workspace.id}>
                    {workspace.modules.map((module) => {
                      const Page = getModulePage(module)
                      return <Route element={withSuspense(<Page />)} key={module.id} path={module.route.path} />
                    })}
                  </Route>
                )
              })}
              {moduleRegistry.legacyRoutes.map(({ from, to }) => (
                <Route element={<LegacyRedirect to={to} />} key={from} path={from} />
              ))}
              <Route path="*" element={<Navigate to={moduleRegistry.defaultPath} replace />} />
            </Routes>
            <ShellExtensionSlot id="shell.bottom-player" />
            <ShellExtensionSlot id="shell.right-drawer" />
          </ModuleProviders>
        </CapabilityNavigationProvider>
      </BrowserRouter>
    </ManagementAuthGate>
  )
}

export default App
