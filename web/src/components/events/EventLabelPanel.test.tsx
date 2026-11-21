import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { EventLabelPanel } from './EventLabelPanel'
import type { EventLabel } from '../../lib/api/events'
const mocks = vi.hoisted(() => ({ labelEvent: vi.fn() }))
vi.mock('../../lib/api/events', () => ({ labelEvent: mocks.labelEvent }))
vi.mock('../../contexts/LanguageContext', () => ({
  useLanguage: () => ({ language: 'en' }),
}))
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})
const label: EventLabel = {
  model_id: 'm',
  model_name: 'Configured model',
  input_hash: 'hash',
  analyzed_at: '2026-11-15T06:00:00Z',
  stale: false,
  cached: true,
  analysis: {
    event_type: 'price_target',
    summary: 'Conditional price event',
    associations: [
      { coin: 'BTC', relation: 'direct', reason: 'The question names BTC' },
    ],
    attention_reason: 'Observed price movement',
    impact_conditions: ['If the condition is met, review the rules'],
    limitations: ['Not a fact or trade signal'],
    evidence_ids: ['polymarket:1'],
  },
}
describe('EventLabelPanel', () => {
  it('never calls on mount and requires a model', () => {
    render(<EventLabelPanel eventId="e" modelId="" onLabeled={vi.fn()} />)
    expect((screen.getByRole('button') as HTMLButtonElement).disabled).toBe(
      true
    )
    expect(mocks.labelEvent).not.toHaveBeenCalled()
  })
  it('runs only on explicit click, suppresses duplicates and refreshes saved labels', async () => {
    let finish!: () => void
    mocks.labelEvent.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve
        })
    )
    const refresh = vi.fn().mockResolvedValue(undefined)
    render(<EventLabelPanel eventId="e" modelId="m" onLabeled={refresh} />)
    const button = screen.getByRole('button')
    fireEvent.click(button)
    fireEvent.click(button)
    expect(mocks.labelEvent).toHaveBeenCalledExactlyOnceWith(
      'e',
      'm',
      undefined
    )
    finish()
    await waitFor(() => expect(refresh).toHaveBeenCalledTimes(1))
  })
  it('renders cached labels with evidence and does not call again', () => {
    render(
      <EventLabelPanel
        eventId="e"
        modelId="m"
        label={label}
        onLabeled={vi.fn()}
      />
    )
    expect(screen.getByText('Conditional price event')).toBeTruthy()
    expect(screen.getByText('polymarket:1')).toBeTruthy()
    expect(
      (screen.getByRole('button', { name: 'Cached' }) as HTMLButtonElement)
        .disabled
    ).toBe(true)
    expect(mocks.labelEvent).not.toHaveBeenCalled()
  })
  it('marks stale labels and never renders another model result', () => {
    const view = render(
      <EventLabelPanel
        eventId="e"
        modelId="m"
        label={{ ...label, stale: true }}
        onLabeled={vi.fn()}
      />
    )
    expect(screen.getByRole('status').textContent).toContain('historical only')
    expect((screen.getByRole('button') as HTMLButtonElement).disabled).toBe(
      false
    )
    view.rerender(
      <EventLabelPanel
        eventId="e"
        modelId="other"
        label={label}
        onLabeled={vi.fn()}
      />
    )
    expect(screen.queryByText('Conditional price event')).toBeNull()
  })
  it('shows safe failure without automatic retry', async () => {
    mocks.labelEvent.mockRejectedValue(new Error('Rate limit reached'))
    const refresh = vi.fn()
    render(<EventLabelPanel eventId="e" modelId="m" onLabeled={refresh} />)
    fireEvent.click(screen.getByRole('button'))
    expect(await screen.findByRole('alert')).toHaveProperty(
      'textContent',
      'Rate limit reached'
    )
    expect(mocks.labelEvent).toHaveBeenCalledTimes(1)
    expect(refresh).not.toHaveBeenCalled()
  })
})
