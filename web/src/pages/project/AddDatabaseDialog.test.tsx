import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { makeProject } from '../../test/projects'
import AddDatabaseDialog from './AddDatabaseDialog'

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })
const URL = '/api/projects/1/databases'

afterEach(() => {
  vi.unstubAllGlobals()
})

/** open renders the dialog on a project with two services; fetch answers the POST with answer. */
function open(answer: () => Response | Promise<Response> = () => json(404, { error: 'no' })) {
  const stub = vi.fn(async (_url: string, _init?: RequestInit) => answer())
  vi.stubGlobal('fetch', stub)
  const onAdded = vi.fn()
  const onClose = vi.fn()
  render(
    <AddDatabaseDialog
      project={makeProject({ serviceNames: ['db', 'cache'] })}
      onAdded={onAdded}
      onClose={onClose}
    />,
  )
  return {
    stub,
    onAdded,
    onClose,
    dialog: screen.getByRole('dialog', { name: 'Add database manually' }),
  }
}

const fill = (dialog: HTMLElement, name = 'ledger') =>
  fireEvent.change(within(dialog).getByLabelText('Database name'), { target: { value: name } })
const submit = (dialog: HTMLElement) =>
  fireEvent.click(within(dialog).getByRole('button', { name: 'Check and add' }))

describe('add database dialog', () => {
  it('offers the types, the project services and the standard variables of the type', () => {
    const { dialog } = open()
    const type = within(dialog).getByRole('radiogroup', { name: 'Type' })
    expect(
      within(type)
        .getAllByRole('radio')
        .map((r) => (r as HTMLInputElement).value),
    ).toEqual(['postgresql', 'mysql', 'mongodb'])
    expect(within(type).getByRole('radio', { name: 'PostgreSQL' })).toBeChecked()

    const service = within(dialog).getByLabelText('Service')
    expect(
      within(service)
        .getAllByRole('option')
        .map((o) => o.textContent),
    ).toEqual(['db', 'cache'])

    expect(
      within(dialog).getByRole('radio', { name: /The image's standard variables/ }),
    ).toBeChecked()
    expect(dialog).toHaveTextContent('POSTGRES_USER / POSTGRES_PASSWORD')
    fireEvent.click(within(type).getByRole('radio', { name: 'MySQL / MariaDB' }))
    expect(dialog).toHaveTextContent(
      'MYSQL_USER / MYSQL_PASSWORD (or MARIADB_*, or the root password)',
    )
    fireEvent.click(within(type).getByRole('radio', { name: 'MongoDB' }))
    expect(dialog).toHaveTextContent('MONGO_INITDB_ROOT_USERNAME / MONGO_INITDB_ROOT_PASSWORD')

    expect(within(dialog).queryByLabelText('User variable')).toBeNull()
    expect(dialog).toHaveTextContent(
      'The panel never asks for the password: it is read inside the container.',
    )
  })

  it('needs the name, and both variables under Other variables, before posting', () => {
    const { dialog, stub } = open()
    submit(dialog)
    expect(within(dialog).getByText('Enter the database name.')).toBeInTheDocument()

    fill(dialog)
    fireEvent.click(within(dialog).getByRole('radio', { name: 'Other variables' }))
    fireEvent.change(within(dialog).getByLabelText('User variable'), {
      target: { value: 'APP_USER' },
    })
    submit(dialog)
    expect(within(dialog).getByText('Enter the password variable.')).toBeInTheDocument()
    expect(within(dialog).queryByText('Enter the user variable.')).toBeNull()
    expect(stub).not.toHaveBeenCalled()
  })

  it('posts the form with Checking… while it waits, then reports the name added', async () => {
    let answer: (r: Response) => void = () => {}
    const { dialog, stub, onAdded } = open(
      () =>
        new Promise<Response>((resolve) => {
          answer = resolve
        }),
    )
    fill(dialog)
    fireEvent.change(within(dialog).getByLabelText('Service'), { target: { value: 'cache' } })
    fireEvent.click(within(dialog).getByRole('radio', { name: 'Other variables' }))
    fireEvent.change(within(dialog).getByLabelText('User variable'), {
      target: { value: 'APP_USER' },
    })
    fireEvent.change(within(dialog).getByLabelText('Password variable'), {
      target: { value: 'APP_PASSWORD' },
    })
    submit(dialog)

    expect(await within(dialog).findByRole('button', { name: 'Checking…' })).toBeDisabled()
    const [url, init] = stub.mock.calls[0]
    expect(url).toBe(URL)
    expect(init?.method).toBe('POST')
    expect(JSON.parse(String(init?.body))).toEqual({
      type: 'postgresql',
      service: 'cache',
      name: 'ledger',
      userVar: 'APP_USER',
      passwordVar: 'APP_PASSWORD',
    })

    answer(json(201, makeProject()))
    await waitFor(() => expect(onAdded).toHaveBeenCalledWith('ledger'))
  })

  it('sends empty variable names for the standard credentials', async () => {
    const { dialog, stub } = open(() => json(201, makeProject()))
    fill(dialog)
    submit(dialog)
    await waitFor(() => expect(stub).toHaveBeenCalled())
    const init = stub.mock.calls[0][1]
    expect(JSON.parse(String(init?.body))).toMatchObject({ userVar: '', passwordVar: '' })
  })

  it('keeps the dialog open and shows a failed check under the form', async () => {
    const words = "Access denied. Check APP_PASSWORD in the container's environment."
    const { dialog, onAdded, onClose } = open(() =>
      json(422, { error: words, field: 'check', code: 'check_failed' }),
    )
    fill(dialog)
    submit(dialog)
    expect(await within(dialog).findByText(words)).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Check and add' })).toBeEnabled()
    expect(onAdded).not.toHaveBeenCalled()
    expect(onClose).not.toHaveBeenCalled()
  })

  it('puts a field error under its field', async () => {
    const words =
      'Use letters, digits and _ . $ -, starting with a letter, digit or _, 64 characters at most.'
    const { dialog } = open(() => json(422, { error: words, field: 'name', code: 'invalid' }))
    fill(dialog, 'bad name')
    submit(dialog)
    const shown = await within(dialog).findByText(words)
    const input = within(dialog).getByLabelText('Database name')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(input.getAttribute('aria-describedby')).toBe(shown.closest('p')?.id)
  })

  it('says to start the service first when it does not run', async () => {
    const { dialog } = open(() =>
      json(409, { error: 'db_not_running', message: 'Start db first.', service: 'db' }),
    )
    fill(dialog)
    submit(dialog)
    expect(await within(dialog).findByText('Start db first')).toBeInTheDocument()
  })

  it('says when Docker is not responding', async () => {
    const { dialog } = open(() =>
      json(503, { error: 'docker_down', message: 'Docker is not responding.' }),
    )
    fill(dialog)
    submit(dialog)
    expect(await within(dialog).findByText('Docker is not responding.')).toBeInTheDocument()
  })

  it('cannot be closed while the check runs', async () => {
    const { dialog, onClose } = open(() => new Promise<Response>(() => {}))
    fill(dialog)
    submit(dialog)
    await within(dialog).findByRole('button', { name: 'Checking…' })
    expect(within(dialog).getByRole('button', { name: 'Cancel' })).toBeDisabled()
    fireEvent.keyDown(dialog, { key: 'Escape' })
    expect(onClose).not.toHaveBeenCalled()
  })
})
