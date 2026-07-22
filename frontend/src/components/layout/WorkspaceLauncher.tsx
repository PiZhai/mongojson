import { Menu } from '@base-ui/react/menu'
import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { moduleRegistry } from '../../app/modules/registry'
import type { WorkspaceId } from '../../platform/contracts/modules'

type Props = {
  currentWorkspace: WorkspaceId
}

function WorkspaceGlyph({ id }: { id: WorkspaceId }) {
  if (id === 'steward') {
    return <svg aria-hidden="true" viewBox="0 0 24 24"><path d="M12 3.5l7 3v5.2c0 4.4-2.8 7.4-7 8.8-4.2-1.4-7-4.4-7-8.8V6.5z" /><path d="M9 12l2 2 4-4" /></svg>
  }
  if (id === 'tools') {
    return <svg aria-hidden="true" viewBox="0 0 24 24"><path d="M5 5h14v5H5zM5 14h6v5H5zM15 14h4v5h-4z" /><path d="M8 7.5h8M7 16.5h2M16.5 16.5h1" /></svg>
  }
  if (id === 'documents') {
    return <svg aria-hidden="true" viewBox="0 0 24 24"><path d="M6 3.5h8.5L19 8v12.5H6z" /><path d="M14.5 3.5V8H19M9 12h7M9 15h7M9 18h4" /></svg>
  }
  return <svg aria-hidden="true" viewBox="0 0 24 24"><path d="M9 18V6l9-2v12" /><circle cx="6.5" cy="18" r="2.5" /><circle cx="15.5" cy="16" r="2.5" /></svg>
}

export function WorkspaceLauncher({ currentWorkspace }: Props) {
  const [open, setOpen] = useState(false)
  const triggerRef = useRef<HTMLButtonElement | null>(null)
  const itemRefs = useRef<Array<HTMLElement | null>>([])
  const navigate = useNavigate()
  const workspaces = moduleRegistry.workspaces

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        setOpen((value) => !value)
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])

  useEffect(() => {
    if (!open) return
    const activeIndex = Math.max(0, workspaces.findIndex((workspace) => workspace.id === currentWorkspace))
    window.requestAnimationFrame(() => itemRefs.current[activeIndex]?.focus())
  }, [currentWorkspace, open, workspaces])

  return (
    <Menu.Root
      onOpenChange={(nextOpen, eventDetails) => {
        setOpen(nextOpen)
        if (!nextOpen && eventDetails.reason === 'escape-key') {
          window.requestAnimationFrame(() => triggerRef.current?.focus())
        }
      }}
      open={open}
    >
      <div className="workspace-launcher" data-current-workspace={currentWorkspace}>
        <Menu.Trigger
          aria-label="切换工作区"
          className="workspace-launcher-trigger"
          ref={triggerRef}
          title="切换工作区（Ctrl+K）"
        >
          <WorkspaceGlyph id={currentWorkspace} />
        </Menu.Trigger>
        <Menu.Portal>
          <Menu.Backdrop className="workspace-launcher-scrim" />
          <Menu.Positioner
            align="start"
            className="workspace-launcher-positioner"
            data-current-workspace={currentWorkspace}
            positionMethod="fixed"
            sideOffset={10}
          >
            <Menu.Popup aria-label="工作区" className="workspace-launcher-menu" id="workspace-launcher-menu">
              <header>
                <strong>切换工作区</strong>
                <span>Ctrl K</span>
              </header>
              <div className="workspace-launcher-list">
                {workspaces.map((workspace, index) => {
                  const active = workspace.id === currentWorkspace
                  return (
                    <Menu.Item
                      aria-current={active ? 'page' : undefined}
                      className={`workspace-launcher-item${active ? ' is-active' : ''}`}
                      key={workspace.id}
                      onClick={() => {
                        if (!active) navigate(workspace.defaultPath)
                      }}
                      ref={(node) => { itemRefs.current[index] = node }}
                    >
                      <span className="workspace-launcher-icon"><WorkspaceGlyph id={workspace.id} /></span>
                      <span className="workspace-launcher-copy"><strong>{workspace.label}</strong><small>{workspace.description}</small></span>
                      <span className="workspace-launcher-state">{active ? '当前' : '进入'}</span>
                    </Menu.Item>
                  )
                })}
              </div>
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </div>
    </Menu.Root>
  )
}
