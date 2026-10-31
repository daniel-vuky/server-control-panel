import type { LucideIcon } from 'lucide-react'
import {
  Activity,
  DatabaseBackup,
  FolderKanban,
  LayoutDashboard,
  Server,
  Settings,
} from 'lucide-react'

export type AppRoute = {
  path: string
  title: string
  /** GitHub issues that will deliver this page. */
  issues: number[]
  /** Present only for pages that appear in the sidebar. */
  nav?: { label: string; icon: LucideIcon }
}

export const appRoutes: AppRoute[] = [
  {
    path: '/',
    title: 'Dashboard',
    issues: [4, 11, 23, 24, 25, 33, 39, 40, 41, 42, 43, 44, 72],
    nav: { label: 'Dashboard', icon: LayoutDashboard },
  },
  {
    path: '/projects',
    title: 'Projects',
    issues: [4],
    nav: { label: 'Projects', icon: FolderKanban },
  },
  {
    path: '/projects/:id',
    title: 'Project detail',
    issues: [4, 5, 31, 32, 34, 35, 37, 38, 48, 56],
  },
  { path: '/onboarding', title: 'Onboarding', issues: [17, 18, 23, 26] },
  { path: '/rescan/:id', title: 'Rescan', issues: [24] },
  {
    path: '/backups',
    title: 'Backups',
    issues: [6, 45, 49, 52, 57],
    nav: { label: 'Backups', icon: DatabaseBackup },
  },
  {
    path: '/server',
    title: 'Server',
    issues: [7, 59, 60, 61, 62, 63],
    nav: { label: 'Server', icon: Server },
  },
  { path: '/activity', title: 'Activity', issues: [8], nav: { label: 'Activity', icon: Activity } },
  {
    path: '/settings',
    title: 'Settings',
    issues: [3, 6, 8, 17, 19],
    nav: { label: 'Settings', icon: Settings },
  },
]
