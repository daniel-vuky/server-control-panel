/** CloudUploadIcon is design 04's cloud with an up arrow: Back up all now and the empty Backups page. */
export default function CloudUploadIcon({
  size,
  strokeWidth,
  stroke = 'currentColor',
}: {
  size: number
  strokeWidth: number
  stroke?: string
}) {
  return (
    <svg
      aria-hidden="true"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke={stroke}
      strokeWidth={strokeWidth}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M7 18a5 5 0 0 1-.9-9.9A6 6 0 0 1 17.7 7 4.5 4.5 0 0 1 18 18z" />
      <path d="M12 11v6M9.5 13.5 12 11l2.5 2.5" />
    </svg>
  )
}
