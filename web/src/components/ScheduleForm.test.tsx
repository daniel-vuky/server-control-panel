import { fireEvent, render, screen, within } from '@testing-library/react'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import type { ScheduleDraft } from '../lib/schedule'
import ScheduleForm from './ScheduleForm'

const start: ScheduleDraft = {
  frequency: 'daily',
  time: '02:00',
  weekday: 0,
  keep: '14',
  compress: true,
}

function Host({
  layout,
  errors = {},
  onRef,
}: {
  layout?: 'segmented' | 'grid'
  errors?: Parameters<typeof ScheduleForm>[0]['errors']
  onRef?: Parameters<typeof ScheduleForm>[0]['fieldRef']
}) {
  const [value, setValue] = useState(start)
  return (
    <ScheduleForm
      idPrefix="t"
      value={value}
      onChange={(p) => setValue((v) => ({ ...v, ...p }))}
      errors={errors}
      zone={null}
      layout={layout}
      fieldRef={onRef}
    />
  )
}

describe('ScheduleForm layout="grid"', () => {
  it('drives the frequency from a Schedule select, Daily / Weekly / Off, and has no radiogroup', () => {
    render(<Host layout="grid" />)
    const select = screen.getByRole('combobox', { name: 'Schedule' })
    expect(select).toHaveValue('daily')
    expect(select).toHaveClass('h-[38px]')
    expect(
      within(select)
        .getAllByRole('option')
        .map((o) => o.textContent),
    ).toEqual(['Daily', 'Weekly', 'Off'])
    expect(screen.queryByRole('radiogroup')).toBeNull()
    expect(screen.queryByLabelText('Day')).toBeNull()
  })

  it('shows Day under the three fields for Weekly and hides it again for Off', () => {
    render(<Host layout="grid" />)
    fireEvent.change(screen.getByLabelText('Schedule'), { target: { value: 'weekly' } })
    const day = screen.getByLabelText('Day')
    expect(day).toHaveValue('0')
    expect(day.parentElement).toHaveClass('col-start-1')
    const grid = screen.getByLabelText('Run time').closest('.grid')
    expect(grid).toHaveClass('grid-cols-[repeat(auto-fit,minmax(min(200px,100%),1fr))]')
    expect(grid).toContainElement(day)
    fireEvent.change(screen.getByLabelText('Schedule'), { target: { value: 'off' } })
    expect(screen.queryByLabelText('Day')).toBeNull()
  })

  it('fieldRef resolves frequency to the select; a 422 on it marks the select', () => {
    const refs: Record<string, HTMLElement | null> = {}
    render(
      <Host
        layout="grid"
        errors={{ frequency: 'Pick one.' }}
        onRef={(f, el) => {
          refs[f] = el
        }}
      />,
    )
    const select = screen.getByLabelText('Schedule')
    expect(refs.frequency).toBe(select)
    expect(select).toHaveAttribute('aria-invalid', 'true')
    expect(select).toHaveClass('border-field-error')
    expect(screen.getByText('Pick one.')).toBeInTheDocument()
  })

  it('keeps the Gzip switch under the fields', () => {
    render(<Host layout="grid" />)
    const gzip = screen.getByRole('switch', { name: 'Gzip compression' })
    expect(gzip).toBeChecked()
    const grid = screen.getByLabelText('Run time').closest('.grid') as HTMLElement
    expect(grid.compareDocumentPosition(gzip) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(grid).not.toContainElement(gzip)
  })
})

describe('ScheduleForm default layout', () => {
  it('keeps the segmented Frequency radiogroup', () => {
    render(<Host />)
    expect(screen.getByRole('radiogroup', { name: 'Frequency' })).toBeInTheDocument()
    expect(screen.queryByRole('combobox', { name: 'Schedule' })).toBeNull()
    fireEvent.click(screen.getByRole('radio', { name: 'Weekly' }))
    expect(screen.getByLabelText('Day')).toBeInTheDocument()
  })
})
