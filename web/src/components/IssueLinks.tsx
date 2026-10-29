const issueUrl = (n: number) => `https://github.com/daniel-vuky/server-control-panel/issues/${n}`

/** IssueLinks is a comma-separated list of links to GitHub issues. */
export default function IssueLinks({ issues }: { issues: number[] }) {
  return issues.map((n, i) => (
    <span key={n}>
      {i > 0 && ', '}
      <a
        className="font-mono text-accent hover:underline"
        href={issueUrl(n)}
        target="_blank"
        rel="noreferrer"
      >
        #{n}
      </a>
    </span>
  ))
}
