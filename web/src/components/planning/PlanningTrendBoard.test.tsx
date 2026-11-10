import { cleanup, render as renderComponent, screen, fireEvent, waitFor } from '@testing-library/react'
import { afterEach, describe, it, expect, vi } from 'vitest'
import { SWRConfig } from 'swr'
import type { ReactElement } from 'react'
function render(ui: ReactElement) {
  return renderComponent(ui, {
    wrapper: ({ children }) => <SWRConfig value={{ provider: () => new Map() }}>{children}</SWRConfig>,
  })
}
import { PlanningTrendBoard } from './PlanningTrendBoard'
const mocks = vi.hoisted(() => ({ api: vi.fn() }))
vi.mock('../../lib/planning', () => ({ planningAPI: mocks.api }))
afterEach(() => {
  cleanup()
  mocks.api.mockReset()
})
describe('free trend board', () => {
  it('loads all returned markets and lets the user search and select', async () => {
    mocks.api.mockResolvedValue({
      coins: [
        { pair: 'BTCUSDT', score: 80, return_7d: 0.05 },
        { pair: 'ETHUSDT', score: 88, return_7d: 0.12 },
        { pair: 'XMRUSDT', score: 82 },
      ],
      scanned: 60,
      fetched_at: new Date().toISOString(),
    })
    const select = vi.fn()
    const markets = vi.fn()
    render(
      <PlanningTrendBoard
        venue="binance"
        disabled={false}
        onSelect={select}
        onMarkets={markets}
      />
    )
    await screen.findByRole('button', { name: /ETH/ })
    expect(mocks.api).toHaveBeenCalledWith('ai500')
    fireEvent.change(screen.getByLabelText('Search trend board'), {
      target: { value: 'eth' },
    })
    expect(screen.queryByRole('button', { name: /XMR/ })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: /ETH/ }))
    expect(select).toHaveBeenCalledWith('ETHUSDT')
    expect(mocks.api).toHaveBeenCalledTimes(1)
    expect(markets).toHaveBeenCalledWith(['BTCUSDT', 'ETHUSDT', 'XMRUSDT'])
    expect(
      screen.getByText(
        'Free · computed from public Binance candles · cached 5 min'
      )
    ).toBeVisible()
    expect(screen.queryByText(/wallet|Claw402|paid/i)).toBeNull()
  })
  it('does not request the Binance board on another venue', () => {
    render(
      <PlanningTrendBoard
        venue="hyperliquid"
        disabled={false}
        onSelect={() => {}}
      />
    )
    expect(mocks.api).not.toHaveBeenCalled()
  })
  it('handles an invalid response without crashing or retrying', async () => {
    mocks.api.mockResolvedValue({})
    render(
      <PlanningTrendBoard
        venue="binance"
        disabled={false}
        onSelect={() => {}}
      />
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Invalid trend board response'
    )
    expect(mocks.api).toHaveBeenCalledTimes(1)
  })
  it('says so when no market is in an uptrend', async () => {
    mocks.api.mockResolvedValue({
      coins: [],
      scanned: 60,
      fetched_at: new Date().toISOString(),
    })
    render(
      <PlanningTrendBoard
        venue="binance"
        disabled={false}
        onSelect={() => {}}
      />
    )
    expect(
      await screen.findByText('No market is currently in an uptrend.')
    ).toBeVisible()
  })
  it('reuses the scan across rerenders and keeps markets visible during refresh failures', async () => {
    mocks.api.mockResolvedValue({
      coins: [{ pair: 'BTCUSDT', score: 80 }],
      fetched_at: new Date().toISOString(),
    })
    const view = render(
      <PlanningTrendBoard venue="binance" disabled={false} onSelect={() => {}} onMarkets={() => {}} />
    )
    await screen.findByRole('button', { name: /BTC/ })
    view.rerender(
      <PlanningTrendBoard venue="binance" disabled={false} onSelect={() => {}} onMarkets={() => {}} />
    )
    expect(mocks.api).toHaveBeenCalledTimes(1)
    let reject!: (reason: Error) => void
    mocks.api.mockReturnValue(new Promise((_, fail) => { reject = fail }))
    fireEvent.click(screen.getByRole('button', { name: 'Refresh list' }))
    await waitFor(() => expect(mocks.api).toHaveBeenCalledTimes(2))
    expect(screen.getByRole('button', { name: /BTC/ })).toBeVisible()
    reject(new Error('Scan unavailable'))
    expect(await screen.findByRole('alert')).toHaveTextContent('Scan unavailable')
    expect(screen.getByRole('button', { name: /BTC/ })).toBeVisible()
  })
})
