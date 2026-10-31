import { CircleAlert } from 'lucide-react'

/** FieldMessage is a form field's error, under the field; give the field aria-describedby={id}. */
export default function FieldMessage({ id, message }: { id: string; message: string }) {
  return (
    <p id={id} role="alert" className="m-0 flex items-start gap-[7px] text-[13px] text-danger-text">
      <CircleAlert aria-hidden="true" size={15} strokeWidth={2} className="mt-0.5 flex-none" />
      <span>{message}</span>
    </p>
  )
}
