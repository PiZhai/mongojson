import { useMemo, useState } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router-dom'
import { MusicMiniPlayer } from '../music/MusicPlayerProvider'

type NavItem = {
  to: string
  title: string
  icon: 'inspect' | 'json' | 'mongo' | 'visualize' | 'memo' | 'music'
}

const navGroups: Array<{ label: string; items: NavItem[] }> = [
  {
    label: '数据工具',
    items: [
      {
        to: '/tools/inspect',
        title: '智能诊断',
        icon: 'inspect',
      },
      {
        to: '/tools/json',
        title: 'JSON 工具',
        icon: 'json',
      },
      {
        to: '/tools/mongodb-json',
        title: 'MongoDB JSON',
        icon: 'mongo',
      },
      {
        to: '/tools/visualize',
        title: '数据可视化',
        icon: 'visualize',
      },
    ],
  },
  {
    label: '文档能力',
    items: [
      {
        to: '/tools/memo-docs',
        title: '在线备忘录',
        icon: 'memo',
      },
    ],
  },
  {
    label: '媒体工具',
    items: [
      {
        to: '/tools/music',
        title: '音乐播放器',
        icon: 'music',
      },
    ],
  },
]

const pageMeta: Record<string, { title: string }> = {
  '/tools/inspect': {
    title: '智能诊断',
  },
  '/tools/json': {
    title: 'JSON 工具',
  },
  '/tools/mongodb-json': {
    title: 'MongoDB JSON 工具',
  },
  '/tools/visualize': {
    title: '数据可视化',
  },
  '/tools/memo-docs': {
    title: '在线备忘录',
  },
  '/tools/music': {
    title: '音乐播放器',
  },
}

const SIDEBAR_STORAGE_KEY = 'personal-tooling-sidebar-collapsed'

function BrandMark() {
  return (
    <svg aria-hidden="true" className="brand-mark" viewBox="0 0 24 24">
      <rect height="16" rx="3" width="16" x="4" y="4" />
      <path d="M8 9h8M8 12h5M8 15h8" />
    </svg>
  )
}

function NavIcon({ icon }: Pick<NavItem, 'icon'>) {
  if (icon === 'inspect') {
    return (
      <svg aria-hidden="true" className="nav-icon-svg" viewBox="0 0 24 24">
        <path d="M5 5h14v5H5z" />
        <path d="M5 14h6v5H5z" />
        <path d="M15 14h4v5h-4z" />
        <path d="M8 7.5h8M7 16.5h2M16.5 16.5h1" />
      </svg>
    )
  }

  if (icon === 'json') {
    return (
      <svg aria-hidden="true" className="nav-icon-svg" viewBox="0 0 24 24">
        <path d="M9 5C7.2 6.8 6.4 8.8 6.4 12s.8 5.2 2.6 7" />
        <path d="M15 5c1.8 1.8 2.6 3.8 2.6 7s-.8 5.2-2.6 7" />
        <path d="M11 8v8" />
        <path d="M13 8v8" />
      </svg>
    )
  }

  if (icon === 'mongo') {
    return (
      <svg aria-hidden="true" className="nav-icon-svg" viewBox="0 0 24 24">
        <ellipse cx="12" cy="7" rx="5.5" ry="2.5" />
        <path d="M6.5 7v8c0 1.4 2.5 2.5 5.5 2.5s5.5-1.1 5.5-2.5V7" />
        <path d="M12 5v14" />
      </svg>
    )
  }

  if (icon === 'memo') {
    return (
      <svg aria-hidden="true" className="nav-icon-svg" viewBox="0 0 24 24">
        <path d="M7 4.5h8.2L19 8.3V19.5H7z" />
        <path d="M15 4.5v4h4" />
        <path d="M10 12h6M10 15h6M10 18h3.5" />
      </svg>
    )
  }

  if (icon === 'music') {
    return (
      <svg aria-hidden="true" className="nav-icon-svg" viewBox="0 0 24 24">
        <path d="M9 18V6l9-2v12" />
        <circle cx="6.5" cy="18" r="2.5" />
        <circle cx="15.5" cy="16" r="2.5" />
      </svg>
    )
  }

  return (
    <svg aria-hidden="true" className="nav-icon-svg" viewBox="0 0 24 24">
      <path d="M5 16.5h3.5V10H5zM10.25 16.5h3.5V6.5h-3.5zM15.5 16.5H19V12h-3.5z" />
      <path d="M5 19h14" />
    </svg>
  )
}

function SidebarToggleGlyph({ collapsed }: { collapsed: boolean }) {
  return collapsed ? (
    <svg aria-hidden="true" className="theme-icon" viewBox="0 0 24 24">
      <path d="M9 6l6 6-6 6" />
    </svg>
  ) : (
    <svg aria-hidden="true" className="theme-icon" viewBox="0 0 24 24">
      <path d="M15 6l-6 6 6 6" />
    </svg>
  )
}

export function AppShell() {
  const location = useLocation()
  const [sidebarCollapsed, setSidebarCollapsed] = useState(() => {
    if (typeof window === 'undefined') {
      return false
    }

    return window.localStorage.getItem(SIDEBAR_STORAGE_KEY) === 'true'
  })

  const meta = useMemo(() => pageMeta[location.pathname] ?? pageMeta['/tools/json'], [location.pathname])

  const toggleSidebar = () => {
    setSidebarCollapsed((value) => {
      const nextValue = !value
      window.localStorage.setItem(SIDEBAR_STORAGE_KEY, String(nextValue))
      return nextValue
    })
  }

  return (
    <div
      className="app-shell"
      data-sidebar={sidebarCollapsed ? 'collapsed' : 'expanded'}
    >
      <a className="skip-link" href="#main-content">
        跳到主内容
      </a>
      <aside aria-label="主导航" className="app-sidebar">
        <div className="sidebar-topbar">
          <div className="app-brand">
            <div className="app-brand-title">
              <BrandMark />
              <span className="app-brand-name">Personal Tooling</span>
            </div>
            <div className="app-brand-badge">
              <span>Workspace</span>
            </div>
          </div>

          <button
            aria-expanded={!sidebarCollapsed}
            aria-label={sidebarCollapsed ? '展开左侧导航' : '收起左侧导航'}
            className="sidebar-toggle-button"
            onClick={toggleSidebar}
            type="button"
          >
            <SidebarToggleGlyph collapsed={sidebarCollapsed} />
          </button>
        </div>

        {navGroups
          .filter((group) => group.items.length > 0)
          .map((group) => (
            <div className="nav-group" key={group.label}>
              <p className="nav-group-label">{group.label}</p>
              {group.items.map((item) => {
                return (
                  <NavLink
                    className={({ isActive }) => `nav-link${isActive ? ' nav-link-active' : ''}`}
                    data-nav-title={item.title}
                    key={item.to}
                    to={item.to}
                  >
                    <span className="nav-icon" aria-hidden="true">
                      <NavIcon icon={item.icon} />
                    </span>
                    <span className="nav-link-title">{item.title}</span>
                  </NavLink>
                )
              })}
            </div>
          ))}

      </aside>

      <main className="app-main" id="main-content">
        <header className="app-header">
          <div className="app-header-copy">
            <h1 className="app-header-title">{meta.title}</h1>
          </div>
          <div className="app-header-actions">
            <div className="app-header-badge">
              <span className="app-header-badge-dot" />
              <span>工作区在线</span>
            </div>
          </div>
        </header>
        <div className="app-content">
          <Outlet />
        </div>
        <MusicMiniPlayer />
      </main>
    </div>
  )
}
