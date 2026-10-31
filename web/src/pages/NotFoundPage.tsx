import { FileQuestion } from 'lucide-react'
import EmptyState, { EmptyStateLink } from '../components/EmptyState'

export default function NotFoundPage() {
  return (
    <EmptyState
      icon={FileQuestion}
      title="Page not found"
      action={<EmptyStateLink to="/">Back to Dashboard</EmptyStateLink>}
    >
      That page does not exist.
    </EmptyState>
  )
}
