import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SWRConfig } from 'swr'
import { EventsPage } from './EventsPage'
import type { EventsResponse } from '../lib/api/events'

const mocks = vi.hoisted(() => ({
  getEvents: vi.fn(),
  labelEvent: vi.fn(),
  getModels: vi.fn().mockResolvedValue([]),
  userId: 'user-a',
  language: 'en',
}))
vi.mock('../lib/api/events', () => ({
  getEvents: mocks.getEvents,
  labelEvent: mocks.labelEvent,
}))
vi.mock('../lib/api', () => ({ api: { getModelConfigs: mocks.getModels } }))
vi.mock('../contexts/AuthContext', () => ({
  useAuth: () => ({ user: { id: mocks.userId }, token: 'session' }),
}))
vi.mock('../contexts/LanguageContext', () => ({
  useLanguage: () => ({ language: mocks.language }),
}))

const alpha = {
  trader_id: 'a',
  trader_name: 'Alpha',
  strategy_id: 's1',
  strategy_name: 'Momentum',
}
const beta = {
  trader_id: 'b',
  trader_name: 'Beta',
  strategy_id: 's2',
  strategy_name: 'Range',
}
const empty: EventsResponse = {
  generated_at: '2026-06-01T10:00:00Z',
  running_traders: 0,
  watches: [],
  events: [],
  warnings: [],
}
const board: EventsResponse = {
  ...empty,
  running_traders: 2,
  watches: [
    { coin: 'BTC', symbol: 'BTCUSDT', traders: [alpha] },
    { coin: 'ETH', symbol: 'ETHUSDT', traders: [beta] },
  ],
  events: [
    {
      id: '1',
      title: 'Bitcoin above $100k?',
      url: 'https://polymarket.com/event/btc',
      source: 'Polymarket',
      published_at: empty.generated_at,
      received_at: empty.generated_at,
      end_at: '2026-12-31T23:59:00Z',
      markets: [
        {
          id: 'm1',
          question: 'BTC above $100k at year end?',
          outcomes: [
            { name: 'Yes', price: 0.625 },
            { name: 'No', price: 0.375 },
          ],
          price_basis: 'Outcome price',
          change_24h: 0.025,
        },
      ],
      coins: ['BTC'],
      traders: [alpha],
    },
    {
      id: '2',
      title: 'Ethereum above $5k?',
      url: 'javascript:alert(1)',
      source: 'Kalshi',
      published_at: empty.generated_at,
      received_at: empty.generated_at,
      markets: [],
      coins: ['ETH'],
      traders: [beta],
    },
  ],
  warnings: ['One source unavailable'],
}
function View() {
  return (
    <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
      <EventsPage />
    </SWRConfig>
  )
}
beforeEach(() => {
  mocks.userId = 'user-a'
  mocks.language = 'en'
  mocks.getEvents.mockResolvedValue(empty)
})
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('EventsPage', () => {
  it('shows loading and a distinct no-running state with the disclaimer', async () => {
    render(<View />)
    expect(screen.getByText('Loading news and events…')).toBeTruthy()
    expect(await screen.findByText(/No running traders\./)).toBeTruthy()
    expect(
      screen.getByText(/Market-price implied probabilities/).textContent
    ).toContain('No automatic orders')
  })
  it('distinguishes no coins and no prediction market events', async () => {
    mocks.getEvents.mockResolvedValue({ ...empty, running_traders: 1 })
    const view = render(<View />)
    expect(
      await screen.findByText(/No coins in the running strategies/)
    ).toBeTruthy()
    view.unmount()
    mocks.getEvents.mockResolvedValue({ ...board, events: [] })
    render(<View />)
    expect(
      await screen.findByText(/No news or events in your watch scope/)
    ).toBeTruthy()
  })
  it('shows errors and retries successfully', async () => {
    mocks.getEvents
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValue(empty)
    render(<View />)
    expect(await screen.findByRole('alert')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText(/No running traders\./)).toBeTruthy()
    expect(mocks.getEvents).toHaveBeenCalledTimes(2)
  })
  it('renders associations, warnings, source/time and only safe external links', async () => {
    mocks.getEvents.mockResolvedValue(board)
    render(<View />)
    const link = await screen.findByRole('link', {
      name: 'Bitcoin above $100k?',
    })
    expect(link.getAttribute('href')).toBe('https://polymarket.com/event/btc')
    expect(link.getAttribute('rel')).toBe('noopener noreferrer')
    expect(
      screen.queryByRole('link', { name: 'Ethereum above $5k?' })
    ).toBeNull()
    expect(screen.getAllByText('Alpha → Momentum')).toHaveLength(2)
    expect(screen.getAllByText('Beta → Range')).toHaveLength(2)
    expect(screen.getByText('One source unavailable')).toBeTruthy()
    expect(
      screen
        .getByText(/Polymarket · Created/)
        .querySelector('time')
        ?.getAttribute('datetime')
    ).toBe(empty.generated_at)
  })
  it('combines coin/trader filters and shows a separate no-match state', async () => {
    mocks.getEvents.mockResolvedValue(board)
    render(<View />)
    await screen.findByText('Bitcoin above $100k?')
    fireEvent.change(screen.getByLabelText('Coin'), {
      target: { value: 'BTC' },
    })
    expect(screen.queryByText('Ethereum above $5k?')).toBeNull()
    fireEvent.change(screen.getByLabelText('Trader'), {
      target: { value: 'b' },
    })
    expect(
      screen.getByText('No news or events match these filters.')
    ).toBeTruthy()
    fireEvent.change(screen.getByLabelText('Coin'), { target: { value: '' } })
    expect(screen.getByText('Ethereum above $5k?')).toBeTruthy()
    expect(screen.queryByText('Bitcoin above $100k?')).toBeNull()
  })
  it('clamps filters when the watched scope changes on refresh', async () => {
    mocks.getEvents.mockResolvedValueOnce(board).mockResolvedValue({
      ...board,
      watches: [board.watches[1]],
      events: [board.events[1]],
    })
    render(<View />)
    await screen.findByText('Bitcoin above $100k?')
    fireEvent.change(screen.getByLabelText('Coin'), {
      target: { value: 'BTC' },
    })
    fireEvent.change(screen.getByLabelText('Trader'), {
      target: { value: 'a' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await screen.findByText('Ethereum above $5k?')
    expect((screen.getByLabelText('Coin') as HTMLSelectElement).value).toBe('')
    expect((screen.getByLabelText('Trader') as HTMLSelectElement).value).toBe(
      ''
    )
  })
  it('does not retain previous user data during an account change', async () => {
    mocks.getEvents
      .mockResolvedValueOnce(board)
      .mockImplementation(() => new Promise(() => {}))
    const view = render(<View />)
    await screen.findByText('Bitcoin above $100k?')
    mocks.userId = 'user-b'
    view.rerender(<View />)
    await waitFor(() => expect(mocks.getEvents).toHaveBeenCalledTimes(2))
    expect(screen.queryByText('Bitcoin above $100k?')).toBeNull()
    expect(screen.queryByText('Alpha → Momentum')).toBeNull()
  })
  it('renders Chinese labels and empty guidance', async () => {
    mocks.language = 'zh'
    render(<View />)
    expect(await screen.findByText(/没有运行中的交易员/)).toBeTruthy()
    expect(screen.getByRole('heading', { name: '资讯与事件' })).toBeTruthy()
    expect(screen.getByText(/市场价格隐含概率，非事实或收益保证/)).toBeTruthy()
  })
  it('renders outcome probabilities, deadline and price differences in percentage points', async () => {
    mocks.getEvents.mockResolvedValue(board)
    render(<View />)
    await screen.findByText('Bitcoin above $100k?')
    expect(screen.getByText('Yes: 62.5%')).toBeTruthy()
    expect(screen.getByText('No: 37.5%')).toBeTruthy()
    expect(screen.getByText('Price basis: Outcome price')).toBeTruthy()
    expect(
      screen.getByText('First outcome 24h price change: +2.5 percentage points')
    ).toBeTruthy()
    const deadline = screen.getAllByText(/Deadline:/, { selector: 'p' })[0]
    expect(deadline.querySelector('time')?.getAttribute('datetime')).toBe(
      board.events[0].end_at
    )
    expect(screen.getByText('Deadline: Not provided')).toBeTruthy()
    expect(screen.getByText('No market prices available.')).toBeTruthy()
  })
  it('combines platform with the coin filter', async () => {
    mocks.getEvents.mockResolvedValue(board)
    render(<View />)
    await screen.findByText('Bitcoin above $100k?')
    fireEvent.change(screen.getByLabelText('Platform'), {
      target: { value: 'Kalshi' },
    })
    expect(screen.queryByText('Bitcoin above $100k?')).toBeNull()
    expect(screen.getByText('Ethereum above $5k?')).toBeTruthy()
    fireEvent.change(screen.getByLabelText('Coin'), {
      target: { value: 'BTC' },
    })
    expect(
      screen.getByText('No news or events match these filters.')
    ).toBeTruthy()
  })
  it('shows only the first eight markets', async () => {
    mocks.getEvents.mockResolvedValue({
      ...board,
      events: [
        {
          ...board.events[0],
          markets: Array.from({ length: 9 }, (_, i) => ({
            ...board.events[0].markets[0],
            id: String(i),
            question: `Market ${i + 1}`,
          })),
        },
      ],
    })
    render(<View />)
    await screen.findByText('Market 8')
    expect(screen.queryByText('Market 9')).toBeNull()
    expect(screen.getByText(/Showing the first 8 markets/)).toBeTruthy()
  })
  it('filters deterministic event views and renders local evidence separately from platform change', async () => {
    mocks.getEvents.mockResolvedValue({
      ...board,
      events: [
        {
          ...board.events[0],
          is_new: true,
          significant_change: true,
          first_seen_at: empty.generated_at,
          markets: [
            {
              ...board.events[0].markets[0],
              history_status: 'available',
              changes: [
                {
                  outcome: 'Yes',
                  previous_price: 0.5,
                  current_price: 0.625,
                  delta_pp: 12.5,
                  from_at: '2026-06-01T09:00:00Z',
                  to_at: empty.generated_at,
                  significant: true,
                },
              ],
            },
          ],
        },
        { ...board.events[1], ending_soon: true },
      ],
    })
    render(<View />)
    await screen.findByText('Bitcoin above $100k?')
    expect(
      screen.getByText('Yes: 50% → 62.5% · +12.5 percentage points')
    ).toBeTruthy()
    expect(screen.getByText(/Reason: absolute change/)).toBeTruthy()
    expect(
      screen.getByText('First outcome 24h price change: +2.5 percentage points')
    ).toBeTruthy()
    fireEvent.click(
      screen.getByRole('button', { name: 'Significant changes · 1' })
    )
    expect(screen.queryByText('Ethereum above $5k?')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Ending soon · 1' }))
    expect(screen.queryByText('Bitcoin above $100k?')).toBeNull()
    expect(screen.getByText('Ethereum above $5k?')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'New events · 1' }))
    expect(screen.getByText('Bitcoin above $100k?')).toBeTruthy()
    expect(screen.queryByText('Ethereum above $5k?')).toBeNull()
  })

  it('requests selected comparison settings and explains insufficient history', async () => {
    mocks.getEvents.mockResolvedValue(board)
    render(<View />)
    await screen.findByText('Bitcoin above $100k?')
    expect(
      screen.getByText('Insufficient history; waiting for a valid baseline')
    ).toBeTruthy()
    expect(mocks.getEvents).toHaveBeenLastCalledWith({
      windowMinutes: 60,
      thresholdPP: 5,
      endingHours: 24,
    })
    fireEvent.change(screen.getByLabelText('Comparison window'), {
      target: { value: '15' },
    })
    await waitFor(() =>
      expect(mocks.getEvents).toHaveBeenLastCalledWith({
        windowMinutes: 15,
        thresholdPP: 5,
        endingHours: 24,
      })
    )
    await screen.findByText('Bitcoin above $100k?')
    fireEvent.change(screen.getByLabelText('Change threshold'), {
      target: { value: '10' },
    })
    await waitFor(() =>
      expect(mocks.getEvents).toHaveBeenLastCalledWith({
        windowMinutes: 15,
        thresholdPP: 10,
        endingHours: 24,
      })
    )
    await screen.findByText('Bitcoin above $100k?')
    fireEvent.change(screen.getByLabelText('Deadline window'), {
      target: { value: '6' },
    })
    await waitFor(() =>
      expect(mocks.getEvents).toHaveBeenLastCalledWith({
        windowMinutes: 15,
        thresholdPP: 10,
        endingHours: 6,
      })
    )
  })

  it('does not present stale or unavailable history as unchanged', async () => {
    mocks.getEvents.mockResolvedValue({
      ...board,
      events: [
        {
          ...board.events[0],
          markets: [
            {
              ...board.events[0].markets[0],
              history_status: 'stale',
              changes: [],
            },
            {
              ...board.events[0].markets[0],
              id: 'unavailable',
              history_status: 'unavailable',
              changes: [],
            },
          ],
        },
      ],
    })
    render(<View />)
    expect(
      await screen.findByText('Stale snapshot; change detection paused')
    ).toBeTruthy()
    expect(
      screen.getByText('History unavailable; changes cannot be verified')
    ).toBeTruthy()
    fireEvent.click(
      screen.getByRole('button', { name: 'Significant changes · 0' })
    )
    expect(
      screen.getByText('No news or events match these filters.')
    ).toBeTruthy()
  })
})

it('requires explicit model selection and never auto-labels on refresh', async () => {
  mocks.getModels.mockResolvedValueOnce([
    { id: 'm', name: 'Enabled model', enabled: true },
    { id: 'off', name: 'Disabled model', enabled: false },
  ])
  mocks.getEvents.mockResolvedValue(board)
  render(<View />)
  await screen.findByText('Bitcoin above $100k?')
  await screen.findByRole('option', { name: 'Enabled model' })
  expect(screen.queryByRole('option', { name: 'Disabled model' })).toBeNull()
  expect(
    (screen.getByLabelText('Labeling model') as HTMLSelectElement).value
  ).toBe('')
  expect(mocks.labelEvent).not.toHaveBeenCalled()
  fireEvent.change(screen.getByLabelText('Labeling model'), {
    target: { value: 'm' },
  })
  await waitFor(() =>
    expect(mocks.getEvents).toHaveBeenLastCalledWith({
      windowMinutes: 60,
      thresholdPP: 5,
      endingHours: 24,
      labelModelId: 'm',
    })
  )
  await screen.findByText('Bitcoin above $100k?')
  fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  await waitFor(() => expect(mocks.getEvents).toHaveBeenCalledTimes(3))
  expect(mocks.labelEvent).not.toHaveBeenCalled()
})

it('renders news and community provenance without prediction prices or deadlines', async () => {
  const mixed: EventsResponse = {
    ...board,
    events: [
      ...board.events,
      {
        ...board.events[0],
        id: 'free-crypto-news:1',
        source: 'Free Crypto News',
        kind: 'news',
        title: 'Bitcoin regulatory report',
        url: 'https://publisher.test/btc',
        publisher: 'Original Media',
        summary: 'A reported policy development, not verified.',
        article_type: 'report',
        markets: [],
        is_new: true,
      },
      {
        ...board.events[0],
        id: 'free-crypto-news:2',
        source: 'Free Crypto News',
        kind: 'news',
        title: 'Bitcoin technical opinion',
        publisher: 'TradingView Crypto Ideas',
        article_type: 'opinion',
        markets: [],
      },
      {
        ...board.events[0],
        id: 'hacker-news:1',
        source: 'Hacker News',
        kind: 'community',
        title: 'Bitcoin protocol discussion',
        summary: 'Community submitted discussion.',
        publisher: 'Hacker News',
        discussion_url: 'https://news.ycombinator.com/item?id=1',
        score: 12,
        comments: 3,
        markets: [],
        significant_change: true,
        ending_soon: true,
      },
    ],
  }
  mocks.getEvents.mockResolvedValue(mixed)
  render(<View />)
  await screen.findByText('Bitcoin regulatory report')
  expect(screen.getByText('Original source: Original Media')).toBeTruthy()
  expect(screen.getByText('Opinion / analysis · unverified')).toBeTruthy()
  expect(screen.getByText('Community score: 12 · Comments: 3')).toBeTruthy()
  expect(
    screen
      .getByRole('link', { name: 'Read HN discussion' })
      .getAttribute('href')
  ).toBe('https://news.ycombinator.com/item?id=1')
  fireEvent.change(screen.getByLabelText('Information type'), {
    target: { value: 'news' },
  })
  expect(screen.queryByText('Bitcoin protocol discussion')).toBeNull()
  expect(screen.queryByText('Bitcoin above $100k?')).toBeNull()
  expect(screen.queryByText(/Deadline:/)).toBeNull()
  expect(screen.queryByText('No market prices available.')).toBeNull()
  expect(screen.getByText('Bitcoin regulatory report')).toBeTruthy()
  fireEvent.change(screen.getByLabelText('Platform'), {
    target: { value: 'Hacker News' },
  })
  expect(
    screen.getByText('No news or events match these filters.')
  ).toBeTruthy()
  fireEvent.change(screen.getByLabelText('Information type'), {
    target: { value: 'community' },
  })
  expect(screen.getByText('Bitcoin protocol discussion')).toBeTruthy()
  expect(
    screen.getByRole('button', { name: 'Significant changes · 0' })
  ).toBeTruthy()
  expect(screen.getByRole('button', { name: 'Ending soon · 0' })).toBeTruthy()
  expect(screen.queryByText(/Deadline:/)).toBeNull()
})
