import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { ServerInfo } from '../../api/types.gen'
import { GB, makeServerInfo } from '../../test/server'
import SystemInfoCard from './SystemInfoCard'

function renderCard(info: ServerInfo) {
  render(<SystemInfoCard info={info} />)
  return screen.getByRole('region', { name: 'System info' })
}

/** fact is the value of a definition term. */
const fact = (card: HTMLElement, term: string) =>
  within(card).getByText(term, { selector: 'dt' }).nextElementSibling

describe('SystemInfoCard', () => {
  it('lists the operating system, kernel, CPU, RAM and Docker version', () => {
    const card = renderCard(makeServerInfo())
    expect(fact(card, 'Operating system')).toHaveTextContent('Ubuntu 24.04 LTS')
    expect(fact(card, 'Kernel')).toHaveTextContent('6.8.0-45-generic')
    expect(fact(card, 'CPU')).toHaveTextContent('4 vCPU')
    expect(fact(card, 'RAM')).toHaveTextContent('8.0 GB')
    expect(fact(card, 'Docker')).toHaveTextContent('29.3.1')
    expect(card).not.toHaveTextContent(/pm2|Node/)
  })

  it('reads — before Docker has answered once', () => {
    const card = renderCard(
      makeServerInfo({
        hostname: '',
        os: '',
        kernel: '',
        docker: { state: 'not_responding', version: '', containersRunning: 0 },
      }),
    )
    expect(fact(card, 'Operating system')).toHaveTextContent('—')
    expect(fact(card, 'Kernel')).toHaveTextContent('—')
    expect(fact(card, 'Docker')).toHaveTextContent('—')
  })

  it('reads — for cores and RAM before anything answered', () => {
    const card = renderCard(makeServerInfo({ cores: 0, ramBytes: 0 }))
    expect(fact(card, 'CPU')).toHaveTextContent(/^—$/)
    expect(fact(card, 'RAM')).toHaveTextContent(/^—$/)
  })

  it('says Not installed without a Docker socket', () => {
    const card = renderCard(
      makeServerInfo({ docker: { state: 'not_installed', version: '', containersRunning: 0 } }),
    )
    expect(fact(card, 'Docker')).toHaveTextContent('Not installed')
  })

  it('shows each disk against the threshold', () => {
    const card = renderCard(
      makeServerInfo({
        disks: [
          { mount: '/', usedBytes: 93 * GB, totalBytes: 160 * GB },
          { mount: '/var/lib/docker', usedBytes: 450 * GB, totalBytes: 500 * GB },
        ],
      }),
    )
    const root = within(card).getByTestId('disk-/')
    expect(root).toHaveTextContent('/')
    expect(root).toHaveTextContent('93 GB / 160 GB · 58%')
    expect(root.querySelector('[data-bar]')).toHaveStyle({ width: '58%' })
    expect(root.querySelector('[data-bar]')).not.toHaveClass('bg-danger')

    // 90% is over the 85% threshold: coloured.
    const docker = within(card).getByTestId('disk-/var/lib/docker')
    expect(docker).toHaveTextContent('450 GB / 500 GB · 90%')
    expect(docker.querySelector('[data-bar]')).toHaveClass('bg-danger')
    expect(within(docker).getByText('450 GB / 500 GB · 90%')).toHaveClass('text-danger')

    expect(within(card).getByText('Alert threshold 85%')).toBeInTheDocument()
    expect(card).not.toHaveTextContent('Re-run install.sh to list every disk.')
  })

  it('a disk at the threshold is not coloured', () => {
    const card = renderCard(
      makeServerInfo({ disks: [{ mount: '/', usedBytes: 85 * GB, totalBytes: 100 * GB }] }),
    )
    expect(within(card).getByTestId('disk-/').querySelector('[data-bar]')).not.toHaveClass(
      'bg-danger',
    )
  })

  it('shows the fallback row with the note to re-run install.sh', () => {
    const card = renderCard(
      makeServerInfo({
        disks: [{ mount: '/ (panel data)', usedBytes: 20 * GB, totalBytes: 40 * GB }],
        disksPartial: true,
      }),
    )
    expect(within(card).getByTestId('disk-/ (panel data)')).toHaveTextContent('/ (panel data)')
    expect(within(card).getByText('Re-run install.sh to list every disk.')).toBeInTheDocument()
  })
})
