import ConfirmDialog from './ConfirmDialog'

type Props = {
  /** name is the database the backup will continue with after connecting. */
  name: string
  /** busy is true while the consent URL is being asked for. */
  busy: boolean
  onLater(): void
  onConnect(): void
}

/** DriveIcon is design 09's Google Drive glyph. */
function DriveIcon() {
  return (
    <svg
      aria-hidden="true"
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinejoin="round"
    >
      <path d="M8.5 3h7l6 10.5-3.5 6h-12L2.5 13.5z" />
      <path d="M8.5 3l6.5 11.5M15.5 3 9 14.5M2.5 13.5h19" />
    </svg>
  )
}

/**
 * DriveRequiredDialog is design 09's "Connect Google Drive first": a backup
 * needs Drive, and after connecting the browser comes back to continue it.
 */
export default function DriveRequiredDialog({ name, busy, onLater, onConnect }: Props) {
  return (
    <ConfirmDialog
      icon={<DriveIcon />}
      title="Connect Google Drive first"
      body={
        <>
          Backups need a storage location. After connecting, you will return here and continue
          backing up <span className="font-mono">{name}</span>.
        </>
      }
      cancelLabel="Later"
      confirmLabel="Connect Google Drive"
      tone="primary"
      busy={busy}
      onCancel={onLater}
      onConfirm={onConnect}
    />
  )
}
