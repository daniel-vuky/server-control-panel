import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { ServerInfo } from '../../api/types.gen'
import { makeServerInfo } from '../../test/server'
import ServicesCard from './ServicesCard'

function renderCard(info: ServerInfo, dockerUp: boolean | null = true) {
  render(<ServicesCard info={info} dockerUp={dockerUp} />)
  return screen.getByRole('region', { name: 'System services' })
}

const row = (card: HTMLElement, name: string) => within(card).getByTestId(`service-${name}`)

describe('ServicesCard', () => {
  it('shows Docker running with its containers and cloudflared connected with its hostnames', () => {
    const card = renderCard(makeServerInfo())
    const docker = row(card, 'Docker daemon')
    expect(docker).toHaveTextContent('Docker daemon')
    expect(within(docker).getByText('Running')).toHaveClass('text-success')
    expect(docker).toHaveTextContent('7 containers')

    const tunnel = row(card, 'cloudflared')
    expect(within(tunnel).getByText('Connected')).toHaveClass('text-success')
    expect(tunnel).toHaveTextContent('5 hostnames')
    expect(card).not.toHaveTextContent('pm2')
  })

  it('counts one container and one hostname in the singular', () => {
    const card = renderCard(
      makeServerInfo({
        docker: { state: 'running', version: '29', containersRunning: 1 },
        cloudflared: { state: 'connected', hostnames: 1 },
      }),
    )
    expect(row(card, 'Docker daemon')).toHaveTextContent('1 container')
    expect(row(card, 'cloudflared')).toHaveTextContent('1 hostname')
  })

  it('leaves the hostnames out when the configuration could not be read', () => {
    const card = renderCard(
      makeServerInfo({ cloudflared: { state: 'connected', hostnames: null } }),
    )
    expect(row(card, 'cloudflared')).not.toHaveTextContent('hostname')
  })

  it('says Not responding when the live state says Docker is down', () => {
    const card = renderCard(makeServerInfo(), false)
    const docker = row(card, 'Docker daemon')
    expect(within(docker).getByText('Not responding')).toHaveClass('text-danger')
    expect(docker).not.toHaveTextContent('container')
  })

  it('says Running when the live state says Docker is back before the next read', () => {
    const card = renderCard(
      makeServerInfo({ docker: { state: 'not_responding', version: '29', containersRunning: 3 } }),
      true,
    )
    expect(within(row(card, 'Docker daemon')).getByText('Running')).toBeInTheDocument()
  })

  it('follows the server until the live state arrives', () => {
    const card = renderCard(
      makeServerInfo({ docker: { state: 'not_responding', version: '29', containersRunning: 0 } }),
      null,
    )
    expect(row(card, 'Docker daemon')).toHaveTextContent('Not responding')
  })

  it('says Not installed without a Docker socket', () => {
    const card = renderCard(
      makeServerInfo({ docker: { state: 'not_installed', version: '', containersRunning: 0 } }),
      false,
    )
    expect(within(row(card, 'Docker daemon')).getByText('Not installed')).toHaveClass('text-danger')
  })

  it('shows cloudflared disconnected and not running in red', () => {
    const disconnected = renderCard(
      makeServerInfo({ cloudflared: { state: 'disconnected', hostnames: 5 } }),
    )
    expect(within(row(disconnected, 'cloudflared')).getByText('Disconnected')).toHaveClass(
      'text-danger',
    )
  })

  it('shows cloudflared not running in red', () => {
    const card = renderCard(
      makeServerInfo({ cloudflared: { state: 'not_running', hostnames: null } }),
    )
    expect(within(row(card, 'cloudflared')).getByText('Not running')).toHaveClass('text-danger')
  })

  it('shows a running cloudflared without metrics in grey', () => {
    const card = renderCard(makeServerInfo({ cloudflared: { state: 'unknown', hostnames: null } }))
    expect(within(row(card, 'cloudflared')).getByText('Running · connection unknown')).toHaveClass(
      'text-muted',
    )
  })
})
