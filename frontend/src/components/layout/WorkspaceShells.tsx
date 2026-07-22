import { NavLink, Outlet } from 'react-router-dom'
import { getWorkspace } from '../../app/modules/registry'
import type { WorkspaceId } from '../../platform/contracts/modules'
import { WorkspaceLauncher } from './WorkspaceLauncher'

function WorkspaceNav({ workspace }: { workspace: WorkspaceId }) {
  const definition = getWorkspace(workspace)
  if (!definition || definition.modules.length < 2) return null
  return (
    <nav aria-label={`${definition.label}页面`} className="workspace-local-nav">
      {definition.modules.map((module) => (
        <NavLink className={({ isActive }) => `workspace-local-link${isActive ? ' is-active' : ''}`} key={module.id} to={module.route.path}>
          {module.navigation.label}
        </NavLink>
      ))}
    </nav>
  )
}

function BrandedWorkspaceShell({
  eyebrow,
  title,
  workspace,
}: {
  eyebrow: string
  title: string
  workspace: Exclude<WorkspaceId, 'tools'>
}) {
  return (
    <div className={`workspace-shell ${workspace}-workspace-shell`} data-workspace={workspace}>
      <a className="skip-link" href="#main-content">跳到主内容</a>
      <WorkspaceLauncher currentWorkspace={workspace} />
      <header className="workspace-shell-header">
        <div className="workspace-shell-brand">
          <span>{eyebrow}</span>
          <strong>{title}</strong>
        </div>
        <WorkspaceNav workspace={workspace} />
      </header>
      <main className="workspace-shell-content" id="main-content">
        <Outlet />
      </main>
    </div>
  )
}

export function StewardWorkspaceShell() {
  return <BrandedWorkspaceShell eyebrow="PERSONAL AI" title="智能管家" workspace="steward" />
}

export function DocumentsWorkspaceShell() {
  return <BrandedWorkspaceShell eyebrow="WRITING SPACE" title="文档" workspace="documents" />
}

export function EntertainmentWorkspaceShell() {
  return <BrandedWorkspaceShell eyebrow="MEDIA ROOM" title="娱乐" workspace="entertainment" />
}
