import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SWRConfig } from 'swr'
import { CoinsTab, sortCoins } from './CoinsTab'
import type { SymbolSummary } from '../../lib/api/copyTrade'

const mocks = vi.hoisted(() => ({
  symbols: vi.fn(),
  detail: vi.fn(),
  klines: vi.fn(),
  hitRate: vi.fn(),
}))
vi.mock('../../lib/api', () => ({
  api: {
    getCopyTradeSymbols: mocks.symbols,
    getCopyTradeSymbol: mocks.detail,
    getMarketKlines: mocks.klines,
    getHitRate: mocks.hitRate,
  },
}))
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
    symbol,
    summary: null,
    holders: [],
    events: [],
    traders: {},
  }))
  mocks.klines.mockResolvedValue([])
  mocks.hitRate.mockResolvedValue({ days: 30, rows: [] })
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
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })
    expect(mocks.symbols).toHaveBeenCalledTimes(1)
    expect(mocks.detail).toHaveBeenCalledWith('BTCUSDT')
    expect(mocks.klines).toHaveBeenCalledTimes(1)
    await act(async () => {
      await vi.advanceTimersByTimeAsync(4100)
    })
    expect(mocks.symbols).toHaveBeenCalledTimes(2)
    expect(mocks.detail).toHaveBeenCalledTimes(2)
    expect(mocks.klines).toHaveBeenCalledTimes(1)
  })

  it('does not hold coin details behind a slow summary list', async () => {
    mocks.symbols.mockReturnValue(new Promise(() => {}))
    mount()
    await act(async () => {
      await vi.advanceTimersByTimeAsync(4100)
    })
    expect(mocks.symbols).toHaveBeenCalledTimes(1)
    expect(mocks.detail).toHaveBeenCalledTimes(2)
    expect(mocks.klines).toHaveBeenCalledTimes(1)
  })
})

describe('copy-trade holder identity', () => {
  it('shows namesakes with their own portfolio links and incomplete-history warning', async () => {
    const holder = (id: string, entry: number) => ({
      portfolio_id: id,
      nickname: 'RocketTrader',
      symbol: 'BTCUSDT',
      side: 'long',
      qty: 1,
      entry_price: entry,
      entry_value: entry,
      source: 'inferred',
      history_complete: false,
      opened_at: 1000,
    })
    let holders = [
      holder('signal-id', 100),
      holder('public-id', 110),
      holder('signal-id', 120),
    ]
    mocks.detail.mockImplementation(async () => ({
      summary: null,
      holders,
      events: [],
      traders: {},
    }))
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      mount()
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0)
      })
      expect(
        screen.getAllByRole('link', { name: 'RocketTrader' })
      ).toHaveLength(3)
      expect(screen.getByText('ID: public-id')).toBeTruthy()
      const link = screen.getAllByRole('link', { name: 'RocketTrader' })[1]
      expect(link.getAttribute('href')).toBe(
        'https://www.binance.com/en/copy-trading/lead-details/public-id'
      )
      expect(screen.getAllByText('Backfilling')).toHaveLength(3)
      holders = [holder('public-id', 110)]
      await act(async () => {
        await vi.advanceTimersByTimeAsync(4100)
      })
      expect(
        screen.getAllByRole('link', { name: 'RocketTrader' })
      ).toHaveLength(1)
      expect(screen.queryByText('ID: signal-id')).toBeNull()
      expect(
        errors.mock.calls.some((args) => args.join(' ').includes('same key'))
      ).toBe(false)
    } finally {
      errors.mockRestore()
    }
  })
})

describe('coin ordering', () => {
  const coin = (
    symbol: string,
    rank: number | undefined,
    confidence: number
  ) =>
    ({
      symbol,
      long: { traders: 1, value: 0 },
      short: { traders: 0, value: 0 },
      confidence: { score: confidence, direction: 'long' },
      rank: rank === undefined ? undefined : { score: rank, direction: 'long' },
    }) as unknown as SymbolSummary

  it('orders by the cross-coin score, not by holdings confidence', () => {
    const out = sortCoins(
      [coin('OLDUSDT', 15, 90), coin('NEWUSDT', 72, 20)],
      'rank',
      'all'
    )
    expect(out.map((c) => c.symbol)).toEqual(['NEWUSDT', 'OLDUSDT'])
  })

  it('keeps coins without a score last', () => {
    const out = sortCoins(
      [coin('NONEUSDT', undefined, 50), coin('SCOREDUSDT', 30, 10)],
      'rank',
      'all'
    )
    expect(out.map((c) => c.symbol)).toEqual(['SCOREDUSDT', 'NONEUSDT'])
  })
})
