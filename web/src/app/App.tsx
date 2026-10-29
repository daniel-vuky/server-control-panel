import type { ReactNode } from 'react'
import { BrowserRouter, Route, Routes } from 'react-router'
import { ToastProvider } from '../components/Toast'
import DashboardPage from '../pages/DashboardPage'
import NotFoundPage from '../pages/NotFoundPage'
import OnboardingPage from '../pages/onboarding/OnboardingPage'
import PlaceholderPage from '../pages/PlaceholderPage'
import SettingsPage from '../pages/SettingsPage'
import AppShell from './AppShell'
import { appRoutes } from './routes'

/** The pages built so far; every other route shows its placeholder. */
const pages: Record<string, ReactNode> = {
  '/': <DashboardPage />,
  '/onboarding': <OnboardingPage />,
  '/settings': <SettingsPage />,
}

/** The route tree without a router, so tests can wrap it in a MemoryRouter. */
export function AppRoutes() {
  return (
    <ToastProvider>
      <Routes>
        <Route element={<AppShell />}>
          {appRoutes.map((route) => (
            <Route
              key={route.path}
              path={route.path}
              element={pages[route.path] ?? <PlaceholderPage route={route} />}
            />
          ))}
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
    </ToastProvider>
  )
}

export default function App() {
  return (
    <BrowserRouter>
      <AppRoutes />
    </BrowserRouter>
  )
}
