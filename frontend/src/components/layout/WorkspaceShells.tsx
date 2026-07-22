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
  return (
    <div className="workspace-shell entertainment-workspace-shell" data-workspace="entertainment">
      <a className="skip-link" href="#main-content">跳到主内容</a>
      <WorkspaceLauncher currentWorkspace="entertainment" />
      <header className="workspace-shell-header entertainment-shell-header">
        <div className="workspace-shell-brand entertainment-shell-brand">
          <span className="entertainment-brand-mark" aria-hidden="true">
            <svg viewBox="0 0 24 24"><path d="M9 18V6l9-2v12" /><circle cx="6.5" cy="18" r="2.5" /><circle cx="15.5" cy="16" r="2.5" /></svg>
          </span>
          <span className="entertainment-brand-copy"><small>MIDNIGHT LOUNGE</small><strong>午夜客厅</strong></span>
        </div>
        <WorkspaceNav workspace="entertainment" />
        <span className="entertainment-shell-status"><i /> 本机媒体空间</span>
      </header>
      <main className="workspace-shell-content" id="main-content"><Outlet /></main>
    </div>
  )
}
