import { Server } from 'lucide-react'
import { useEffect, useState } from 'react'
import { NavLink, Outlet } from 'react-router'
import { getServer } from '../api/metrics'
import DockerBanner from '../components/DockerBanner'
import DriveExpiredBanner from '../components/DriveExpiredBanner'
import { plural } from '../lib/plural'
import DriveReturn from './DriveReturn'
import { LatestBackupsProvider, useLatestBackups } from './latestBackups'
import { LiveProvider, useLive } from './live'
import { appRoutes } from './routes'
import UserChip from './UserChip'

const navRoutes = appRoutes.filter((r) => r.nav)

/** The states of a project's latest ended backup that are not a success. */
const FAILED = (state: string) => state !== 'ok'

/**
 * NavIndicator is the trailing mark of a nav item (design 01): the tracked
 * project count on Projects and the red dot on Backups while any tracked
 * project's latest ended backup is not ok.
 */
function NavIndicator({ path }: { path: string }) {
  const { projects } = useLive()
  const { data } = useLatestBackups()
  if (path === '/projects' && projects !== null) {
    return <span className="ml-auto font-mono text-xs text-muted">{projects.length}</span>
  }
  if (path === '/backups' && data !== null) {
    const failed = data.projects.filter((b) => FAILED(b.state)).length
    if (failed > 0) {
      return (
        <span
          role="img"
          aria-label={plural(failed, 'failed backup', 'failed backups')}
          className="ml-auto size-[7px] rounded-full bg-danger"
        />
      )
    }
  }
  return null
}

/**
 * useMachineLine reads /api/server once at mount and gives the brand's
 * `hostname · OS` line, or '' until it answers and after a failure (a failed
 * read changes nothing, so it re-renders nothing).
 */
function useMachineLine(): string {
  const [line, setLine] = useState('')
  useEffect(() => {
    const ctl = new AbortController()
    getServer(ctl.signal).then(
      (s) => {
        if (!ctl.signal.aborted && s.hostname && s.os) setLine(`${s.hostname} · ${s.os}`)
      },
      () => {},
    )
    return () => ctl.abort()
  }, [])
  return line
}

/**
 * AppShell is the frame of every page: the nav with its indicators and the
 * brand's `hostname · OS` line (one /api/server read at mount), the banners
 * and the page.
 */
export default function AppShell() {
  const machine = useMachineLine()
  return (
    <LiveProvider>
      <LatestBackupsProvider>
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
              <div className="flex min-w-0 flex-col gap-0.5">
                <span className="text-sm font-semibold tracking-tight">Server Control Panel</span>
                {machine && <span className="font-mono text-[11.5px] text-muted">{machine}</span>}
              </div>
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
                    <NavIndicator path={path} />
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
      </LatestBackupsProvider>
    </LiveProvider>
  )
}
