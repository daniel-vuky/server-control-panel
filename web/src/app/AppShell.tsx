import { Server } from 'lucide-react'
import { NavLink, Outlet } from 'react-router'
import DockerBanner from '../components/DockerBanner'
import DriveExpiredBanner from '../components/DriveExpiredBanner'
import DriveReturn from './DriveReturn'
import { LiveProvider } from './live'
import { appRoutes } from './routes'
import UserChip from './UserChip'

const navRoutes = appRoutes.filter((r) => r.nav)

export default function AppShell() {
  return (
    <LiveProvider>
      <DriveReturn />
      <div className="flex min-h-screen flex-wrap bg-bg text-text">
        <nav
          aria-label="Main navigation"
          className="flex basis-[220px] grow flex-col gap-7 border-r border-border bg-sidebar px-3.5 py-5"
        >
          <div className="flex items-center gap-2.5 px-1.5">
            <div
              aria-hidden="true"
              className="flex size-[30px] flex-none items-center justify-center rounded-[7px] bg-text text-bg"
            >
              <Server size={18} strokeWidth={2} />
            </div>
            <span className="text-sm font-semibold tracking-tight">Server Control Panel</span>
          </div>
          <div className="flex flex-col gap-0.5">
            {navRoutes.map(({ path, nav }) => {
              if (!nav) return null
              const Icon = nav.icon
              return (
                <NavLink
                  key={path}
                  to={path}
                  end={path === '/'}
                  className={({ isActive }) =>
                    `flex items-center gap-2.5 rounded-md px-2.5 py-[9px] no-underline ${
                      isActive
                        ? 'bg-nav-active font-medium text-white'
                        : 'text-nav-text hover:bg-surface'
                    }`
                  }
                >
                  <Icon aria-hidden="true" size={18} strokeWidth={1.7} />
                  {nav.label}
                </NavLink>
              )
            })}
          </div>
          <UserChip />
        </nav>
        <main className="min-w-0 basis-[560px] grow-[999] px-8 pt-7 pb-14">
          <div className="mx-auto max-w-[1600px]">
            <DockerBanner />
            <DriveExpiredBanner />
            <Outlet />
          </div>
        </main>
      </div>
    </LiveProvider>
  )
}
