import type { Container } from '../api/types.gen'
import { type ContainerTone, containerTone } from '../lib/containers'

const looks: Record<ContainerTone, { text: string; dot: string }> = {
  ok: { text: 'text-success', dot: 'bg-success' },
  failed: { text: 'text-[#ff8c8f]', dot: 'bg-danger' },
  stopped: { text: 'text-stopped', dot: 'bg-stopped' },
}

/** ContainerStatus is the small dot and word of design 02's containers table. */
export default function ContainerStatus({ container }: { container: Container }) {
  const look = looks[containerTone(container)]
  return (
    <span className={`inline-flex items-center gap-[7px] font-sans whitespace-nowrap ${look.text}`}>
      <span aria-hidden="true" className={`size-[7px] flex-none rounded-full ${look.dot}`} />
      {container.status || 'Unknown'}
    </span>
  )
}
