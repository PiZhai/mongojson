import { useMemo, useState } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router-dom'

type NavItem = {
  to: string
  title: string
  description: string
  icon: 'json' | 'mongo' | 'visualize'
}

const navGroups: Array<{ label: string; items: NavItem[] }> = [
  {
    label: '数据工具',
    items: [
      {
        to: '/tools/json',
        title: 'JSON 工具',
        description: '格式化、压缩、校验与树视图',
        icon: 'json',
      },
      {
        to: '/tools/mongodb-json',
        title: 'MongoDB JSON',
        description: '扩展类型、对比、表格与 Shell',
        icon: 'mongo',
      },
      {
        to: '/tools/visualize',
        title: '数据可视化',
        description: '将 JSON 表格数据映射成图表',
        icon: 'visualize',
      },
    ],
  },
  {
    label: '文档能力',
    items: [],
  },
]

const pageMeta: Record<string, { title: string; subtitle: string }> = {
  '/tools/json': {
    title: 'JSON 工具',
    subtitle: '处理标准 JSON 的格式化、压缩、校验与结构浏览，适合作为所有数据工作的起点。',
  },
  '/tools/mongodb-json': {
    title: 'MongoDB JSON 工具',
    subtitle: '处理扩展类型、Shell 语句、结构对比和表格化浏览，面向更真实的数据排查场景。',
  },
  '/tools/visualize': {
    title: '数据可视化',
    subtitle: '把 JSON 或表格结构映射成图表视图，快速判断分布、趋势和重点字段。',
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

  return (
    <svg aria-hidden="true" className="nav-icon-svg" viewBox="0 0 24 24">
      <path d="M5 16.5h3.5V10H5zM10.25 16.5h3.5V6.5h-3.5zM15.5 16.5H19V12h-3.5z" />
      <path d="M5 19h14" />
    </svg>
  )
}

function ThemeGlyph({ dark }: { dark: boolean }) {
  return dark ? (
    <svg aria-hidden="true" className="theme-icon" viewBox="0 0 24 24">
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2.5v2.2M12 19.3v2.2M21.5 12h-2.2M4.7 12H2.5M18.7 5.3l-1.6 1.6M6.9 17.1l-1.6 1.6M18.7 18.7l-1.6-1.6M6.9 6.9L5.3 5.3" />
    </svg>
  ) : (
    <svg aria-hidden="true" className="theme-icon" viewBox="0 0 24 24">
      <path d="M18.5 14.6A7.5 7.5 0 0 1 9.4 5.5a8.5 8.5 0 1 0 9.1 9.1z" />
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
  const [dark, setDark] = useState(true)
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
      data-theme={dark ? 'dark' : 'light'}
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
              <span>v1</span>
              <span>Browser Workspace</span>
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
                    <span className="nav-link-copy">
                      <span className="nav-link-title">{item.title}</span>
                      <span className="nav-link-desc">{item.description}</span>
                    </span>
                  </NavLink>
                )
              })}
            </div>
          ))}

        <div className="sidebar-footer">
          <p className="sidebar-footer-title">运行策略</p>
          <p className="sidebar-footer-text">
            前端负责交互和轻量解析，后端负责平台底座、存储和可扩展能力。首期按单人在线工具站设计。
          </p>
        </div>
      </aside>

      <main className="app-main" id="main-content">
        <header className="app-header">
          <div className="app-header-copy">
            <h1 className="app-header-title">{meta.title}</h1>
            <p className="app-header-subtitle">{meta.subtitle}</p>
          </div>
          <div className="app-header-actions">
            <div className="app-header-badge">
              <span className="app-header-badge-dot" />
              <span>Local Workspace</span>
            </div>
            <button
              aria-label="Toggle theme"
              className="theme-button"
              onClick={() => setDark((value) => !value)}
              type="button"
            >
              <ThemeGlyph dark={dark} />
            </button>
          </div>
        </header>
        <div className="app-content">
          <Outlet />
        </div>
      </main>
    </div>
  )
}
