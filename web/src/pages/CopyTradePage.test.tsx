import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { SWRConfig } from 'swr'
import { CopyTradePage } from './CopyTradePage'

const mocks = vi.hoisted(() => ({ status: vi.fn(), follows: vi.fn(), feed: vi.fn(), discover: vi.fn() }))
vi.mock('../contexts/LanguageContext', () => ({ useLanguage: () => ({ language: 'en' }) }))
vi.mock('../lib/api', () => ({ api: {
  getCopyTradeStatus: mocks.status,
  getFollows: mocks.follows,
  getFeed: mocks.feed,
  getLeaderboard: mocks.discover,
} }))
vi.mock('../components/copytrade/FollowingTab', () => ({
  FollowingTab: ({ snaps }: { snaps: unknown[] }) => <p>Loaded follows: {snaps.length}</p>,
}))
vi.mock('../components/copytrade/DiscoverTab', () => ({ DiscoverTab: () => <p>Discover</p> }))
vi.mock('../components/copytrade/CoinsTab', () => ({ CoinsTab: () => <p>Coins</p> }))

function mount() {
  return render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <MemoryRouter><CopyTradePage /></MemoryRouter>
    </SWRConfig>
  )
}
beforeEach(() => {
  mocks.status.mockReturnValue(new Promise(() => {}))
  mocks.follows.mockResolvedValue({ follows: [{ trader: { portfolio_id: 'one', nickname: 'Alice' } }] })
  mocks.feed.mockResolvedValue({ items: [], total: 0 })
})
afterEach(() => {
  cleanup()
  vi.useRealTimers()
  vi.restoreAllMocks()
  vi.clearAllMocks()
})

describe('copy-trade snapshot responsiveness', () => {
  it('renders follows and starts the feed without waiting for a stalled status request', async () => {
    mount()
    expect(await screen.findByText('Loaded follows: 1')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Live feed' }))
    await waitFor(() => expect(mocks.feed).toHaveBeenCalledTimes(1))
    // Remounting the active tab reuses the same query, not a second fetch.
    fireEvent.click(screen.getByRole('button', { name: 'Following (1)' }))
    fireEvent.click(screen.getByRole('button', { name: 'Live feed' }))
    expect(mocks.feed).toHaveBeenCalledTimes(1)
    expect(mocks.status).toHaveBeenCalledTimes(1)
    expect(mocks.discover).not.toHaveBeenCalled()
  })

  it('refreshes only local snapshots every 4s, pausing hidden pages and pending reads', async () => {
    vi.useFakeTimers()
    const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
    mount()
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(mocks.follows).toHaveBeenCalledTimes(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(4100) })
    expect(mocks.follows).toHaveBeenCalledTimes(2)
    expect(mocks.status).toHaveBeenCalledTimes(1)
    visibility.mockReturnValue('hidden')
    await act(async () => { await vi.advanceTimersByTimeAsync(12000) })
    expect(mocks.follows).toHaveBeenCalledTimes(2)
    expect(mocks.discover).not.toHaveBeenCalled()
  })

  it('shows component readiness without claiming that a strategy enabled AI context', async () => {
    mocks.status.mockResolvedValue({
      client: { paused: false, requests_per_min: 2, proxy: '' },
      follows: 10, max_follows: 100, hot: 1, failing: 0,
      ai_ready: 1, ai_total: 10, positions_fresh: 10, fills_fresh: 1, profiles_fresh: 8,
    })
    mount()
    expect(await screen.findByText('Data sync incomplete')).toBeVisible()
    const ready = screen.getByText('AI data ready 1/10 · Positions 10 · Fills 1 · Profiles 8')
    expect(ready).toHaveAttribute('title', expect.stringContaining('must enable copy-trade context'))
    expect(screen.queryByText('OK')).not.toBeInTheDocument()
  })
})
