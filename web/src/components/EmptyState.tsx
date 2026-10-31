import type { LucideIcon } from 'lucide-react'
import type { ButtonHTMLAttributes, ReactNode } from 'react'
import { Link, type LinkProps } from 'react-router'

const secondary =
  'inline-flex h-9 cursor-pointer items-center justify-center rounded-[7px] border border-border-strong bg-surface-2 px-3.5 text-text disabled:cursor-not-allowed disabled:opacity-60'

/** EmptyState is the dashed card of design 10: a muted icon, a title, one line and an optional action. */
export default function EmptyState({
  icon: Icon,
  title,
  children,
  action,
  titleAs: Title = 'h2',
}: {
  icon: LucideIcon
  title: string
  children?: ReactNode
  action?: ReactNode
  /** The heading level of the title; a page that has no other h1 passes 'h1'. */
  titleAs?: 'h1' | 'h2'
}) {
  return (
    <div className="flex min-h-[220px] flex-col items-center justify-center gap-2.5 rounded-xl border border-dashed border-border-strong p-5 text-center">
      <Icon aria-hidden="true" size={44} strokeWidth={1.4} className="text-[#4a515c]" />
      <Title className="m-0 text-sm font-semibold">{title}</Title>
      {children ? <p className="m-0 text-[13px] leading-normal text-muted">{children}</p> : null}
      {action}
    </div>
  )
}

/** EmptyStateButton is the 36 px secondary button of an empty state. */
export function EmptyStateButton({
  className = '',
  type = 'button',
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement>) {
  return <button type={type} className={`${secondary} ${className}`} {...rest} />
}

/** EmptyStateLink is the same button as a router link. */
export function EmptyStateLink({ className = '', ...rest }: LinkProps) {
  return <Link className={`${secondary} no-underline ${className}`} {...rest} />
}
