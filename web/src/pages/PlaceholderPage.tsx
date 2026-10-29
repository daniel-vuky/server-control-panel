import type { AppRoute } from '../app/routes'
import IssueLinks from '../components/IssueLinks'

export default function PlaceholderPage({ route }: { route: AppRoute }) {
  return (
    <section className="flex flex-col gap-3">
      <h1 className="text-2xl font-semibold tracking-tight">{route.title}</h1>
      <p className="text-muted">
        Coming in <IssueLinks issues={route.issues} />
      </p>
    </section>
  )
}
