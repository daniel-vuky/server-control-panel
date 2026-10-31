import { FileQuestion } from 'lucide-react'
import { LiveDim } from '../app/live'
import EmptyState, { EmptyStateLink } from '../components/EmptyState'
import { BadgeRow } from '../components/PageHeader'

export default function NotFoundPage() {
  return (
    <div className="flex flex-col gap-5">
      <BadgeRow />
      <LiveDim>
        <EmptyState
          icon={FileQuestion}
          title="Page not found"
          titleAs="h1"
          action={<EmptyStateLink to="/">Back to Dashboard</EmptyStateLink>}
        >
          That page does not exist.
        </EmptyState>
      </LiveDim>
    </div>
  )
}
