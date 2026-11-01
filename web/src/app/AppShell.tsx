import { Menu, Server, X } from 'lucide-react'
import { useEffect, useId, useState } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router'
import { fetchMe } from '../api/me'
import { getServer } from '../api/metrics'
import Dialog from '../components/Dialog'
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
 * useEmail reads /api/me once at mount and gives the signed-in email, or null
 * until it answers and after a failure. The shell holds it, so the sidebar
 * and the phone drawer share one read.
 */
function useEmail(): string | null {
  const [email, setEmail] = useState<string | null>(null)
  useEffect(() => {
    const ctl = new AbortController()
    fetchMe(ctl.signal).then((me) => {
      if (me && !ctl.signal.aborted) setEmail(me.email)
    })
    return () => ctl.abort()
  }, [])
  return email
}

/** WIDE is where the sidebar shows and the phone drawer has no place (Tailwind's `md`). */
const WIDE = '(min-width: 768px)'

/**
 * NavBody is the content of the nav: the brand with its `hostname · OS` line,
 * the links with their indicators and the user chip. The sidebar (768 px and
 * wider) and the phone drawer both show it; `onNavigate` runs when a link is
 * followed.
 */
function NavBody({
  machine,
  email,
  onNavigate,
}: {
  machine: string
  email: string | null
  onNavigate?: () => void
}) {
  return (
    <>
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
              onClick={onNavigate}
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
      <UserChip email={email} />
    </>
  )
}

/**
 * NavDrawer is the nav as a left drawer (below 768 px): the same links in a
 * modal that traps focus and closes on Escape, a click on the overlay, the
 * Close button and following a link.
 */
function NavDrawer({
  machine,
  email,
  onClose,
}: {
  machine: string
  email: string | null
  onClose: () => void
}) {
  const titleId = useId()
  return (
    <Dialog labelledBy={titleId} onClose={onClose} drawer>
      <h2 id={titleId} className="sr-only">
        Menu
      </h2>
      <button
        type="button"
        aria-label="Close menu"
        onClick={onClose}
        className="absolute top-3 right-3 flex size-11 cursor-pointer items-center justify-center rounded-lg border-0 bg-transparent text-muted hover:text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        <X aria-hidden="true" size={20} />
      </button>
      <nav aria-label="Mobile navigation" className="flex flex-1 flex-col gap-7">
        <NavBody machine={machine} email={email} onNavigate={onClose} />
      </nav>
    </Dialog>
  )
}

/**
 * AppShell is the frame of every page: the nav with its indicators, the
 * brand's `hostname · OS` line (one /api/server read at mount) and the user
 * chip (one /api/me read at mount), the banners and the page. Below 768 px
 * the sidebar gives way to a top bar whose "Open menu" button opens the same
 * nav as a drawer, which closes when the window is widened to 768 px.
 */
export default function AppShell() {
  const machine = useMachineLine()
  const email = useEmail()
  const [menu, setMenu] = useState(false)
  const { pathname } = useLocation()

  // Any navigation closes the drawer, including back and forward.
  // biome-ignore lint/correctness/useExhaustiveDependencies: only a new path re-runs this.
  useEffect(() => setMenu(false), [pathname])

  // A window widened to 768 px or more shows the sidebar: the drawer closes.
  useEffect(() => {
    if (!menu || typeof window.matchMedia !== 'function') return
    const query = window.matchMedia(WIDE)
    const closeIfWide = () => {
      if (query.matches) setMenu(false)
    }
    closeIfWide()
    query.addEventListener('change', closeIfWide)
    return () => query.removeEventListener('change', closeIfWide)
  }, [menu])

  return (
    <LiveProvider>
      <LatestBackupsProvider>
        <DriveReturn />
        <div className="flex min-h-screen flex-wrap bg-bg text-text">
          <div className="sticky top-0 z-30 flex basis-full items-center gap-3 border-b border-border bg-sidebar px-4 py-2 md:hidden">
            <button
              type="button"
              aria-label="Open menu"
              aria-haspopup="dialog"
              aria-expanded={menu}
              onClick={() => setMenu(true)}
              className="flex size-11 flex-none cursor-pointer items-center justify-center rounded-lg border border-border-strong bg-transparent text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
            >
              <Menu aria-hidden="true" size={20} />
            </button>
            <span className="text-sm font-semibold tracking-tight">Server Control Panel</span>
          </div>
          <nav
            aria-label="Main navigation"
            className="flex basis-[220px] grow flex-col gap-7 border-r border-border bg-sidebar px-3.5 py-5 max-md:hidden"
          >
            <NavBody machine={machine} email={email} />
          </nav>
          <main className="min-w-0 basis-[560px] grow-[999] px-8 pt-7 pb-14 max-sm:px-4 max-sm:pt-5">
            <div className="mx-auto max-w-[1600px]">
              <DockerBanner />
              <DriveExpiredBanner />
              <Outlet />
            </div>
          </main>
        </div>
        {menu && <NavDrawer machine={machine} email={email} onClose={() => setMenu(false)} />}
      </LatestBackupsProvider>
    </LiveProvider>
  )
}
