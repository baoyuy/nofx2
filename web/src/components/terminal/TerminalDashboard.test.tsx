import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { SWRConfig } from 'swr'
import { TerminalDashboard } from './TerminalDashboard'
import type { DecisionRecord } from '../../types'

const mocks = vi.hoisted(() => ({ flow: vi.fn(), rank: vi.fn(), stats: vi.fn(), history: vi.fn(), config: vi.fn(), matrix: vi.fn(), links: vi.fn() }))
vi.mock('../../contexts/LanguageContext', () => ({ useLanguage: () => ({ language: 'en' }) }))
vi.mock('../../lib/api', () => ({ api: {
  getFlowMarkets: mocks.flow, getDirectionChangeLeaderboard: mocks.rank,
  getFullStats: mocks.stats, getPositionHistory: mocks.history, getTraderConfig: mocks.config,
  getMirrorLinks: mocks.links,
} }))
vi.mock('../../lib/demo/useDemoEngine', () => ({ useDemoEngine: () => null }))
vi.mock('./OrchestrationTopology', () => ({ OrchestrationTopology: () => null }))
vi.mock('./OrderBook', () => ({ OrderBook: () => null }))
vi.mock('./LiquidationMap', () => ({ LiquidationMap: () => null }))
vi.mock('./KlineChart', () => ({ KlineChart: () => null }))
vi.mock('./ExecutionLog', () => ({ ExecutionLog: () => null }))
vi.mock('./SignalMatrix', () => ({ SignalMatrix: () => { mocks.matrix(); return null } }))
vi.mock('./RiskRadar', () => ({ RiskRadar: () => null }))
vi.mock('./EdgeProfile', () => ({ EdgeProfile: () => null }))
vi.mock('./FlowMarkets', () => ({ FlowMarkets: () => null, FLOW_ROWS: 8 }))

beforeEach(() => {
  vi.useFakeTimers()
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
  mocks.flow.mockResolvedValue({ data: { inflow: [], outflow: [] } })
  mocks.rank.mockResolvedValue({ items: [] })
  mocks.stats.mockResolvedValue({ total_pnl: 0, total_fee: 0, profit_factor: 0, max_drawdown_pct: 0, total_trades: 0, win_trades: 0, loss_trades: 0, win_rate: 0, sharpe_ratio: 0, avg_win: 0, avg_loss: 0 })
  mocks.history.mockResolvedValue({ positions: [] })
  mocks.config.mockResolvedValue({})
  mocks.links.mockResolvedValue([])
})
afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks(); vi.clearAllMocks() })

it('starts public flow before trader discovery and shares it and signal snapshots across traders', async () => {
  const cache = new Map()
  const view = (id?: string) => <SWRConfig value={{ provider: () => cache }}>
    <TerminalDashboard selectedTraderId={id} onTraderSelect={() => {}} decisions={[{ candidate_coins: ['BTCUSDT'], decisions: [] } as unknown as DecisionRecord]} />
  </SWRConfig>
  const ui = render(view())
  await act(async () => { await vi.advanceTimersByTimeAsync(0) })
  expect(mocks.flow).toHaveBeenCalledTimes(1)
  expect(mocks.stats).not.toHaveBeenCalled()
  ui.rerender(view('one'))
  await act(async () => { await vi.advanceTimersByTimeAsync(0) })
  ui.rerender(view('two'))
  await act(async () => { await vi.advanceTimersByTimeAsync(0) })
  expect(mocks.flow).toHaveBeenCalledTimes(1)
  expect(mocks.stats).toHaveBeenCalledWith('two', true)
  expect(mocks.rank).toHaveBeenCalledTimes(1)
  expect(mocks.rank).toHaveBeenCalledWith(30, true, ['BTC'])
})

it('isolates one-second clocks from panel rendering and does not refetch on focus or while hidden', async () => {
  render(<SWRConfig value={{ provider: () => new Map() }}>
    <TerminalDashboard selectedTraderId="one" onTraderSelect={() => {}} />
  </SWRConfig>)
  await act(async () => { await vi.advanceTimersByTimeAsync(0) })
  const renders = mocks.matrix.mock.calls.length
  await act(async () => { await vi.advanceTimersByTimeAsync(5100) })
  expect(mocks.matrix).toHaveBeenCalledTimes(renders)
  fireEvent.focus(window)
  await act(async () => { await vi.advanceTimersByTimeAsync(0) })
  expect(mocks.stats).toHaveBeenCalledTimes(1)
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
  await act(async () => { await vi.advanceTimersByTimeAsync(125000) })
  expect(mocks.flow).toHaveBeenCalledTimes(1)
  expect(mocks.stats).toHaveBeenCalledTimes(1)
  expect(screen.getByText('Dashboard')).toBeVisible()
})

it('shows copy trading for the selected trader', async () => {
  render(<SWRConfig value={{ provider: () => new Map() }}>
    <TerminalDashboard selectedTraderId="one" onTraderSelect={() => {}} />
  </SWRConfig>)
  await act(async () => { await vi.advanceTimersByTimeAsync(0) })
  expect(screen.getByText('Copy trading')).toBeVisible()
  expect(screen.getByText('+ New copy')).toBeVisible()
  expect(mocks.links).toHaveBeenCalledWith('one')
})
