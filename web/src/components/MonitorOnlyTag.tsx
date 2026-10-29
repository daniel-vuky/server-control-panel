/** MonitorOnlyTag marks the panel's own project: it can be watched, never controlled. */
export default function MonitorOnlyTag() {
  return (
    <span className="inline-flex h-6 flex-none items-center rounded-[5px] border border-border-strong px-2 text-xs whitespace-nowrap text-text-2">
      Monitor only
    </span>
  )
}
