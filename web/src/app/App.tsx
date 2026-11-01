import type { ReactNode } from 'react'
import { createBrowserRouter, Navigate, type RouteObject, RouterProvider } from 'react-router'
import { ToastProvider } from '../components/Toast'
import ActivityPage from '../pages/ActivityPage'
import BackupsPage from '../pages/BackupsPage'
import DashboardPage from '../pages/DashboardPage'
import NotFoundPage from '../pages/NotFoundPage'
import OnboardingPage from '../pages/onboarding/OnboardingPage'
import ProjectDatabase from '../pages/project/ProjectDatabase'
import ProjectLogs from '../pages/project/ProjectLogs'
import ProjectOverview from '../pages/project/ProjectOverview'
import ProjectPage from '../pages/project/ProjectPage'
import ProjectSettings from '../pages/project/ProjectSettings'
import RescanPage from '../pages/RescanPage'
import SettingsPage from '../pages/SettingsPage'
import ServerPage from '../pages/server/ServerPage'
import AppShell from './AppShell'
import { appRoutes } from './routes'

/**
 * The page of every route but the project detail. Projects is the Dashboard's
 * table view: the list of tracked projects lives there, filters included.
 */
const pages: Record<string, ReactNode> = {
  '/': <DashboardPage />,
  '/projects': <Navigate to="/?view=table" replace />,
  '/activity': <ActivityPage />,
  '/backups': <BackupsPage />,
  '/onboarding': <OnboardingPage />,
  '/rescan/:id': <RescanPage />,
  '/server': <ServerPage />,
  '/settings': <SettingsPage />,
}

/** projectRoute is the detail page, one child route per tab (Overview is the index). */
export const projectRoute = {
  path: '/projects/:id',
  element: <ProjectPage />,
  children: [
    { index: true, element: <ProjectOverview /> },
    { path: 'logs', element: <ProjectLogs /> },
    { path: 'database', element: <ProjectDatabase /> },
    { path: 'settings', element: <ProjectSettings /> },
  ],
} satisfies RouteObject

/**
 * The route tree: the shell, every page and Page not found. The browser uses it
 * in a data router (so a page can block navigation); tests use it in a memory one.
 */
export const routeObjects: RouteObject[] = [
  {
    element: (
      <ToastProvider>
        <AppShell />
      </ToastProvider>
    ),
    children: [
      ...appRoutes.map((route) =>
        route.path === projectRoute.path
          ? projectRoute
          : { path: route.path, element: pages[route.path] },
      ),
      { path: '*', element: <NotFoundPage /> },
    ],
  },
]

let browserRouter: ReturnType<typeof createBrowserRouter> | undefined

export default function App() {
  // Created on first render, not on import, so importing routeObjects has no side effect.
  browserRouter ??= createBrowserRouter(routeObjects)
  return <RouterProvider router={browserRouter} />
}
