import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SWRConfig } from 'swr'
import { ExplosivePage } from './ExplosivePage'
import { LanguageProvider } from '../contexts/LanguageContext'

const mocks = vi.hoisted(() => ({
  board: vi.fn(),
  accuracy: vi.fn(),
  klines: vi.fn(),
}))
vi.mock('../lib/api', () => ({
  api: { getMarketKlines: mocks.klines },
}))
vi.mock('../lib/api/explosive', () => ({
  explosiveApi: { board: mocks.board, accuracy: mocks.accuracy },
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
  day_gain: 0.184,
  elapsed_hours: 3.2,
  volume_slope: 0.12,
  predicted_remaining: 12.5,
  tradability: 'good',
  oi_change_1h: 0.021,
  funding: 0.0001,
  atr_pct: 0.6,
  distance_high: 1.4,
  ema20: 143,
  ema60: 138,
  ema120: 130,
  strength: 88,
  risk: 1,
  score: 88,
  speed: 1,
  volume: 0.7,
  open_interest: 0.7,
  structure: 1,
  rel_strength: 0.4,
  state: 'starting' as const,
  tags: ['breakout', 'volume', 'oi'],
}

const noTags = {
  ...row,
  symbol: 'NULLUSDT',
  score: 40,
  strength: 40,
  // The API used to send null here and the table crashed on tags.slice.
  tags: null as unknown as string[],
}

beforeEach(() => {
  mocks.board.mockResolvedValue({
    rows: [row, noTags],
    scanned: 400,
    candidates: 30,
    fetched_at: new Date().toISOString(),
  })
  mocks.klines.mockResolvedValue([])
  mocks.accuracy.mockResolvedValue({
    days: 30,
    total: 3,
    bands: [
      { band: '≥80', samples: 3, up_1h: 0.66, up_4h: 0.66, up_24h: 0.33, beat_4h: 0.66, avg_excess_4h: 0.01, avg_dd_4h: -0.01, sustained: 0.66 },
    ],
    top: [
      { band: 'top5', samples: 3, up_1h: 0.66, up_4h: 0.66, up_24h: 0.33, beat_4h: 0.66, avg_excess_4h: 0.01, avg_dd_4h: -0.01, sustained: 0.66 },
    ],
    tags: [],
    updated_at: new Date().toISOString(),
  })
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
    expect(screen.getAllByText('+12.5%').length).toBeGreaterThan(0)
    expect(screen.getAllByText('+18.40%').length).toBeGreaterThan(0)
    // The null-tags row still rendered instead of taking the page down.
    expect((await screen.findAllByText('NULL')).length).toBeGreaterThan(0)
  })

  it('shows the measured record on the accuracy tab', async () => {
    render(
      <LanguageProvider>
        <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
          <ExplosivePage />
        </SWRConfig>
      </LanguageProvider>
    )
    await screen.findAllByText('SOL')
    fireEvent.click(screen.getByText(/Accuracy|准确率/))
    expect(await screen.findByText('≥80')).toBeTruthy()
    expect(screen.getAllByText('3').length).toBeGreaterThan(0)
  })
})
