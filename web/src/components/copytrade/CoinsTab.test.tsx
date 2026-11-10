import { act, cleanup, render } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SWRConfig } from 'swr'
import { CoinsTab } from './CoinsTab'

const mocks = vi.hoisted(() => ({ symbols: vi.fn(), detail: vi.fn(), klines: vi.fn() }))
vi.mock('../../lib/api', () => ({ api: {
  getCopyTradeSymbols: mocks.symbols,
  getCopyTradeSymbol: mocks.detail,
  getMarketKlines: mocks.klines,
} }))
vi.mock('../market/BinanceChart', () => ({
  BinanceChart: () => <div>Chart</div>,
  BN: { up: 'green', down: 'red' },
}))
vi.mock('./ConfidenceUi', () => ({
  ConfidenceBadge: () => null,
  explainConfidence: () => '',
  HealthTag: () => null,
  HitRatePanel: () => null,
}))

beforeEach(() => {
  vi.useFakeTimers()
  mocks.symbols.mockResolvedValue({ symbols: [] })
  mocks.detail.mockImplementation(async (symbol: string) => ({
    symbol, summary: null, holders: [], events: [], traders: {},
  }))
  mocks.klines.mockResolvedValue([])
})
afterEach(() => {
  cleanup()
  vi.useRealTimers()
  vi.clearAllMocks()
})
function mount() {
  return render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <CoinsTab language="en" selected="BTCUSDT" onSelect={() => {}} />
    </SWRConfig>
  )
}

describe('copy-trade coin snapshot polling', () => {
  it('refreshes local summaries and details without repeatedly fetching remote candles', async () => {
    mount()
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(mocks.symbols).toHaveBeenCalledTimes(1)
    expect(mocks.detail).toHaveBeenCalledWith('BTCUSDT')
    expect(mocks.klines).toHaveBeenCalledTimes(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(4100) })
    expect(mocks.symbols).toHaveBeenCalledTimes(2)
    expect(mocks.detail).toHaveBeenCalledTimes(2)
    expect(mocks.klines).toHaveBeenCalledTimes(1)
  })

  it('does not hold coin details behind a slow summary list', async () => {
    mocks.symbols.mockReturnValue(new Promise(() => {}))
    mount()
    await act(async () => { await vi.advanceTimersByTimeAsync(4100) })
    expect(mocks.symbols).toHaveBeenCalledTimes(1)
    expect(mocks.detail).toHaveBeenCalledTimes(2)
    expect(mocks.klines).toHaveBeenCalledTimes(1)
  })
})
