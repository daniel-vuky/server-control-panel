import { Server } from 'lucide-react'
import type { ReactNode } from 'react'
import { NavLink, Outlet } from 'react-router'
import ConnectionBadge from '../components/ConnectionBadge'
import DockerBanner from '../components/DockerBanner'
import DriveExpiredBanner from '../components/DriveExpiredBanner'
import DriveReturn from './DriveReturn'
import { LiveProvider, useLive } from './live'
import { appRoutes } from './routes'
import UserChip from './UserChip'

const navRoutes = appRoutes.filter((r) => r.nav)

/** The dim of design 10 while the values on screen may be stale. */
const STALE_STYLE = { opacity: 0.55, filter: 'saturate(0.6)' }

/** LiveContent is the page under the badge; it dims while the connection is lost or expired. */
function LiveContent({ children }: { children: ReactNode }) {
  const { connection } = useLive()
  const stale = connection === 'lost' || connection === 'expired'
  return (
    <div data-live-content="" style={stale ? STALE_STYLE : undefined}>
      {children}
    </div>
  )
}

export default function AppShell() {
  return (
    <LiveProvider>
      <DriveReturn />
      <div className="flex min-h-screen bg-bg text-text">
        <nav
          aria-label="Main navigation"
          className="flex w-[220px] flex-none flex-col gap-7 border-r border-border bg-sidebar px-3.5 py-5"
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
        <main className="min-w-0 flex-1 px-8 pt-3 pb-14">
          {/* The badge keeps its own row in the top-right corner, outside the dim. */}
          <div className="mb-2 min-h-[30px]">
            <ConnectionBadge />
          </div>
          <LiveContent>
            <DockerBanner />
            <DriveExpiredBanner />
            <Outlet />
          </LiveContent>
        </main>
      </div>
    </LiveProvider>
  )
}
