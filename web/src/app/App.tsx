import { BrowserRouter, Route, Routes } from 'react-router'
import NotFoundPage from '../pages/NotFoundPage'
import PlaceholderPage from '../pages/PlaceholderPage'
import AppShell from './AppShell'
import { appRoutes } from './routes'

/** The route tree without a router, so tests can wrap it in a MemoryRouter. */
export function AppRoutes() {
  return (
    <Routes>
      <Route element={<AppShell />}>
        {appRoutes.map((route) => (
          <Route key={route.path} path={route.path} element={<PlaceholderPage route={route} />} />
        ))}
        <Route path="*" element={<NotFoundPage />} />
      </Route>
    </Routes>
  )
}

export default function App() {
  return (
    <BrowserRouter>
      <AppRoutes />
    </BrowserRouter>
  )
}
