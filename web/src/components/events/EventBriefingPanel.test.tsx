import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SWRConfig } from 'swr'
import { EventBriefingPanel } from './EventBriefingPanel'
import type { EventBriefing } from '../../lib/api/eventBriefings'
const mocks = vi.hoisted(() => ({ get: vi.fn(), generate: vi.fn() }))
vi.mock('../../lib/api/eventBriefings', () => ({
  getEventBriefing: mocks.get,
  generateEventBriefing: mocks.generate,
}))
vi.mock('../../contexts/LanguageContext', () => ({
  useLanguage: () => ({ language: 'en' }),
}))
const result: EventBriefing = {
  model_id: 'm',
  model_name: 'Model',
  input_hash: 'hash',
  analyzed_at: '2026-11-15T06:00:00Z',
  stale: false,
  cached: true,
  coverage: {
    total_events: 100,
    included_events: 60,
    omitted_events: 40,
    coins: ['BTC'],
  },
  briefings: [
    {
      coin: 'BTC',
      summary: 'Conditional summary',
      groups: [
        {
          stance: 'favorable',
          title: 'Policy development',
          reason: 'Several reports discuss a change',
          conditions: ['If confirmed'],
          limitations: ['Unverified'],
          evidence_ids: ['e'],
        },
      ],
      watch_points: ['Read the official announcement'],
    },
  ],
}
function View({
  model = 'm',
  session = 'a',
}: {
  model?: string
  session?: string
}) {
  return (
    <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
      <EventBriefingPanel
        key={`${session}:${model}`}
        modelId={model}
        sessionKey={session}
        enabled
        events={[
          {
            id: 'e',
            source: 'Free Crypto News',
            title: 'Original report',
            url: 'https://example.com/report',
            published_at: '',
            received_at: '',
            markets: [],
            coins: ['BTC'],
            traders: [],
          },
        ]}
      />
    </SWRConfig>
  )
}
beforeEach(() => {
  mocks.get.mockResolvedValue({ status: 'empty' })
  mocks.generate.mockResolvedValue(result)
})
afterEach(() => {
  cleanup()
  vi.resetAllMocks()
})
describe('EventBriefingPanel', () => {
  it('does not generate on mount and requires a model', () => {
    render(<View model="" />)
    expect((screen.getByRole('button') as HTMLButtonElement).disabled).toBe(
      true
    )
    expect(mocks.get).not.toHaveBeenCalled()
    expect(mocks.generate).not.toHaveBeenCalled()
  })
  it('loads cached briefing without calling the model and shows grouped evidence', async () => {
    mocks.get.mockResolvedValue(result)
    render(<View />)
    expect(await screen.findByText('Conditional summary')).toBeTruthy()
    expect(screen.getByText('Potentially favorable')).toBeTruthy()
    expect(
      screen.getByText(/Analyzed 60 \/ 100 items; 40 omitted/)
    ).toBeTruthy()
    expect(
      screen
        .getByRole('link', { name: 'Original report', hidden: true })
        .getAttribute('href')
    ).toBe('https://example.com/report')
    expect(mocks.generate).not.toHaveBeenCalled()
  })
  it('generates once on click and suppresses duplicate clicks', async () => {
    let finish!: (r: EventBriefing) => void
    mocks.generate.mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = resolve
        })
    )
    render(<View />)
    await screen.findByText(/No saved briefing/)
    const button = screen.getByRole('button', {
      name: 'Generate watched coin briefings',
    })
    fireEvent.click(button)
    fireEvent.click(button)
    expect(mocks.generate).toHaveBeenCalledExactlyOnceWith('m')
    mocks.get.mockResolvedValue(result)
    finish(result)
    expect(await screen.findByText('Conditional summary')).toBeTruthy()
  })
  it('marks old briefings and never auto-reruns', async () => {
    mocks.get.mockResolvedValue({ ...result, stale: true })
    render(<View />)
    expect(
      await screen.findByText(/older briefing for historical reference/)
    ).toBeTruthy()
    expect(mocks.generate).not.toHaveBeenCalled()
  })
  it('retains readable error without auto retry', async () => {
    mocks.generate.mockRejectedValue(new Error('Budget reached'))
    render(<View />)
    await screen.findByText(/No saved briefing/)
    fireEvent.click(
      screen.getByRole('button', { name: 'Generate watched coin briefings' })
    )
    expect((await screen.findByRole('alert')).textContent).toContain(
      'Budget reached'
    )
    await waitFor(() => expect(mocks.generate).toHaveBeenCalledTimes(1))
  })
})
