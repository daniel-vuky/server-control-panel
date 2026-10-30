import { render } from '@testing-library/react'
import {
  createMemoryRouter,
  type InitialEntry,
  Outlet,
  type RouteObject,
  RouterProvider,
} from 'react-router'
import { routeObjects } from '../app/App'
import { LiveProvider } from '../app/live'
import { ToastProvider } from '../components/Toast'

type Options = {
  /** The history index to start on; defaults to the last entry. */
  index?: number
  /**
   * Routes to render instead of the whole app. They are wrapped in a layout
   * route with the providers the app shell gives (toasts, live state).
   */
  routes?: RouteObject[]
}

/**
 * renderApp renders the app (or the given routes) in a memory data router, so
 * tests exercise the same router the browser uses. entry is one history entry
 * (a path, or {pathname, state}) or the whole history stack.
 */
export function renderApp(entry: InitialEntry | InitialEntry[] = '/', opts: Options = {}) {
  const initialEntries = Array.isArray(entry) ? entry : [entry]
  const routes: RouteObject[] = opts.routes
    ? [
        {
          element: (
            <ToastProvider>
              <LiveProvider>
                <Outlet />
              </LiveProvider>
            </ToastProvider>
          ),
          children: opts.routes,
        },
      ]
    : routeObjects
  const router = createMemoryRouter(routes, {
    initialEntries,
    initialIndex: opts.index ?? initialEntries.length - 1,
  })
  return { ...render(<RouterProvider router={router} />), router }
}
