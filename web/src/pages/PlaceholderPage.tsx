import { LiveDim } from '../app/live'
import type { AppRoute } from '../app/routes'
import IssueLinks from '../components/IssueLinks'
import PageHeader from '../components/PageHeader'

export default function PlaceholderPage({ route }: { route: AppRoute }) {
  return (
    <section className="flex flex-col gap-3">
      <PageHeader title={route.title} />
      <LiveDim>
        <p className="text-muted">
          Coming in <IssueLinks issues={route.issues} />
        </p>
      </LiveDim>
    </section>
  )
}
