import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SWRConfig } from 'swr'
import { FollowingTab } from './FollowingTab'
import type {
  FollowedTrader,
  TraderPosition,
  TraderSnapshot,
} from '../../lib/api/copyTrade'

const mocks = vi.hoisted(() => ({
  getAutoFollow: vi.fn(),
  getFollow: vi.fn(),
  unfollow: vi.fn(),
  follow: vi.fn(),
  setAutoFollow: vi.fn(),
  removeAutoExclusion: vi.fn(),
}))
vi.mock('../../lib/api', () => ({ api: { ...mocks } }))
// The panel has its own settings UI and polling; this file is about the cards.
vi.mock('./BoardSyncPanel', () => ({ BoardSyncPanel: () => null }))

const trader = (over: Partial<FollowedTrader> = {}): FollowedTrader => ({
  portfolio_id: 'p1',
  nickname: 'Alpha',
  avatar_url: '',
  status: 'ACTIVE',
  position_show: false,
  aum: 100000,
  copy_count: 0,
  max_copy_count: 0,
  roi: 31.1,
  pnl: 0,
  mdd: 2.9,
  win_rate: 100,
  last_trade_time: 0,
  followed_at: new Date(Date.now() - 3600_000).toISOString(),
  ...over,
})

const position = (over: Partial<TraderPosition> = {}): TraderPosition => ({
  symbol: 'SOLUSDT',
  side: 'long',
  qty: 1,
  entry_price: 71.8,
  entry_value: 62000,
  opened_at: 0,
  last_add_at: 0,
  source: 'inferred',
  return_pct: -12.1,
  pct_of_margin: false,
  priced: true,
  trapped: false,
  liquidated: false,
  fresh: false,
  unrealized_pnl: -496,
  ...over,
})

const snap = (over: Partial<TraderSnapshot> = {}): TraderSnapshot => ({
  trader: trader(),
  positions: [],
  events: [],
  positions_source: 'inferred',
  updated_at: new Date(Date.now() - 60_000).toISOString(),
  history_complete: true,
  hot: false,
  score: {
    score: 90,
    quality: 0.8,
    drawdown: 0.9,
    win_rate: 0.9,
    tenure: 0.5,
    scale: 0.5,
    reliability: 0.9,
  },
  orders_per_day: 2,
  high_freq: false,
  style: {
    kind: 'swing',
    trades: 20,
    median_hold_hours: 10,
    adds_per_trade: 0.2,
    adverse_add_share: 0.1,
    loss_exit_share: 0.3,
  },
  ...over,
})

function mount(snaps: TraderSnapshot[]) {
  return render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <FollowingTab
        language="zh"
        snaps={snaps}
        onChanged={() => {}}
        onSymbol={() => {}}
      />
    </SWRConfig>
  )
}

beforeEach(() => {
  mocks.getAutoFollow.mockResolvedValue(null)
  mocks.getFollow.mockResolvedValue(null)
})
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('trader card', () => {
  it('labels the position columns and shows the open loss as a percentage', () => {
    mount([snap({ positions: [position()] })])
    expect(
      screen.getByRole('columnheader', { name: '浮动盈亏' })
    ).toBeInTheDocument()
    expect(screen.getByText('-$496.00')).toBeInTheDocument()
    expect(screen.getByText('-12.1%')).toBeInTheDocument()
  })

  it('says positions are inferred and tags stock contracts', () => {
    mount([snap({ positions: [position({ tradfi: true })] })])
    expect(screen.getByText('推算持仓')).toBeInTheDocument()
    expect(screen.getByText('股')).toBeInTheDocument()
  })

  it('reports an unfinished backfill instead of claiming there is no position', () => {
    mount([snap({ history_complete: false })])
    expect(screen.getByText('成交未回填完')).toBeInTheDocument()
    expect(
      screen.getByText('成交还在回填，暂时看不到推算的持仓')
    ).toBeInTheDocument()
  })

  it('keeps the trader name as a heading for screen-reader navigation', () => {
    mount([snap()])
    expect(screen.getByRole('heading', { name: /Alpha/ })).toBeInTheDocument()
  })
})

describe('trader list filters', () => {
  it('narrows the cards to the searched name', () => {
    mount([
      snap({ trader: trader({ portfolio_id: 'a', nickname: 'Alpha' }) }),
      snap({ trader: trader({ portfolio_id: 'b', nickname: 'Beta' }) }),
    ])
    expect(screen.getByText('Beta')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('搜索带单员'), {
      target: { value: 'beta' },
    })
    expect(screen.queryByText('Alpha')).not.toBeInTheDocument()
    expect(screen.getByText('Beta')).toBeInTheDocument()
    expect(screen.getByText('显示 1 / 2 人')).toBeInTheDocument()
  })

  it('keeps only traders with positions when that chip is on', () => {
    mount([
      snap({
        trader: trader({ portfolio_id: 'a', nickname: 'Alpha' }),
        positions: [position()],
      }),
      snap({ trader: trader({ portfolio_id: 'b', nickname: 'Beta' }) }),
    ])
    fireEvent.click(screen.getByRole('button', { name: '有持仓' }))
    expect(screen.getByText('Alpha')).toBeInTheDocument()
    expect(screen.queryByText('Beta')).not.toBeInTheDocument()
  })
})
