import IssueLinks from '../../components/IssueLinks'

/** TabPlaceholder holds a detail tab's place until the issues that build it land. */
export default function TabPlaceholder({ issues }: { issues: number[] }) {
  return (
    <p className="m-0 text-muted">
      Coming in <IssueLinks issues={issues} />
    </p>
  )
}
