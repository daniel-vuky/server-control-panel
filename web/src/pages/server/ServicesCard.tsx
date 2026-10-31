import type { ServerInfo } from '../../api/types.gen'
import { plural } from '../../lib/plural'

type Tone = 'ok' | 'bad' | 'neutral'

const DOT: Record<Tone, string> = { ok: 'bg-success', bad: 'bg-danger', neutral: 'bg-stopped' }
const WORD: Record<Tone, string> = { ok: 'text-success', bad: 'text-danger', neutral: 'text-muted' }

type Service = { name: string; tone: Tone; word: string; detail: string | null }

/**
 * dockerService is the Docker daemon row (spec decision 13). The live state
 * (`dockerUp`, pushed by the hub) wins over the server's read when they
 * disagree, so the card turns at once, like the Docker banner; only the
 * server can tell that there is no Docker socket at all.
 */
function dockerService(info: ServerInfo, dockerUp: boolean | null): Service {
  const name = 'Docker daemon'
  const state = info.docker.state
  if (state === 'not_installed' && dockerUp !== true) {
    return { name, tone: 'bad', word: 'Not installed', detail: null }
  }
  const up = dockerUp ?? state === 'running'
  if (!up) return { name, tone: 'bad', word: 'Not responding', detail: null }
  const n = info.docker.containersRunning
  return { name, tone: 'ok', word: 'Running', detail: plural(n, 'container', 'containers') }
}

/** tunnelService is the cloudflared row (spec decision 14). */
function tunnelService(info: ServerInfo): Service {
  const name = 'cloudflared'
  const { state, hostnames } = info.cloudflared
  switch (state) {
    case 'connected':
      return {
        name,
        tone: 'ok',
        word: 'Connected',
        detail: hostnames === null ? null : plural(hostnames, 'hostname', 'hostnames'),
      }
    case 'disconnected':
      return { name, tone: 'bad', word: 'Disconnected', detail: null }
    case 'not_running':
      return { name, tone: 'bad', word: 'Not running', detail: null }
    default:
      return { name, tone: 'neutral', word: 'Running · connection unknown', detail: null }
  }
}

/**
 * ServicesCard is design 05's System services: the Docker daemon and
 * cloudflared (pm2 is dropped: v1 manages compose projects only).
 */
export default function ServicesCard({
  info,
  dockerUp,
}: {
  info: ServerInfo
  dockerUp: boolean | null
}) {
  const services = [dockerService(info, dockerUp), tunnelService(info)]
  return (
    <section
      aria-labelledby="svc-title"
      className="flex flex-col gap-3 rounded-[10px] border border-border bg-surface p-[18px]"
    >
      <h2 id="svc-title" className="m-0 text-[15px] font-semibold">
        System services
      </h2>
      {services.map((s) => (
        <div
          key={s.name}
          data-testid={`service-${s.name}`}
          className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-lg border border-border bg-[#0F1215] p-3"
        >
          <span aria-hidden="true" className={`size-2 flex-none rounded-full ${DOT[s.tone]}`} />
          <span className="font-medium">{s.name}</span>
          <span className={`ml-auto text-[12.5px] ${WORD[s.tone]}`}>{s.word}</span>
          {s.detail && <span className="text-[12.5px] text-muted">{s.detail}</span>}
        </div>
      ))}
    </section>
  )
}
