import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SWRConfig } from 'swr'
import { ExplosivePage } from './ExplosivePage'
import { LanguageProvider } from '../contexts/LanguageContext'

const mocks = vi.hoisted(() => ({
  board: vi.fn(),
  klines: vi.fn(),
}))
vi.mock('../lib/api', () => ({
  api: { getMarketKlines: mocks.klines },
}))
vi.mock('../lib/api/explosive', () => ({
  explosiveApi: { board: mocks.board },
}))
vi.mock('../components/market/BinanceChart', () => ({
  BinanceChart: () => <div>Chart</div>,
  BN: { up: 'green', down: 'red' },
}))

const row = {
  symbol: 'SOLUSDT',
  price: 150,
  change_24h: 18.4,
  change_5m: 1.25,
  change_15m: 2.1,
  volume_24h: 120_000_000,
  vol_ratio: 3.2,
  oi_change_1h: 0.021,
  funding: 0.0001,
  atr_pct: 0.6,
  distance_high: 1.4,
  ema20: 143,
  ema60: 138,
  ema120: 130,
  score: 88,
  speed: 1,
  volume: 0.7,
  open_interest: 0.7,
  structure: 1,
  penalty: 1,
  state: 'starting' as const,
  tags: ['breakout', 'volume', 'oi'],
}

beforeEach(() => {
  mocks.board.mockResolvedValue({
    rows: [row],
    scanned: 400,
    candidates: 30,
    fetched_at: new Date().toISOString(),
  })
  mocks.klines.mockResolvedValue([])
})
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('explosive board', () => {
  it('renders the scored gainer with its signals', async () => {
    render(
      <LanguageProvider>
        <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
          <ExplosivePage />
        </SWRConfig>
      </LanguageProvider>
    )
    expect((await screen.findAllByText('SOL')).length).toBeGreaterThan(0)
    expect(screen.getByText('88')).toBeTruthy()
    expect(screen.getByText('+18.40%')).toBeTruthy()
    expect(screen.getByText('3.2')).toBeTruthy()
    expect(screen.getByText('+2.10%')).toBeTruthy()
  })
})
