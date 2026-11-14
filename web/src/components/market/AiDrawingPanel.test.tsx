import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { AiDrawingPanel } from './AiDrawingPanel'
import type { AIModel } from '../../types'
import type { AIChartDrawingResult, ChartViewport } from '../../utils/aiChartDrawings'
const mocks = vi.hoisted(() => ({ analyze: vi.fn() }))
vi.mock('../../lib/api', () => ({ api: { analyzeChartDrawings: mocks.analyze } }))
vi.mock('../../contexts/LanguageContext', () => ({ useLanguage: () => ({ language: 'en' }) }))
const bars = Array.from({ length: 40 }, (_, i) => ({ time: 1700000000 + i * 900, open: 100 + i, close: 101 + i, high: 105 + i, low: 95 + i, volume: 100 }))
const viewport: ChartViewport = { candles: bars, from: bars[0].time, to: bars[39].time }
const model = { id: 'configured-model', name: 'Configured model', enabled: true } as AIModel
const response: AIChartDrawingResult = { symbol: 'BTCUSDT', interval: '15m', from: viewport.from, to: viewport.to, analyzed_at: 1701000000, summary: 'Selected support', drawings: [{ kind: 'support', anchors: [{ index: 4, field: 'low' }], reason: 'Repeated low', invalidation: 'Close below low' }], warnings: [] }
beforeEach(() => vi.clearAllMocks())
afterEach(cleanup)
it('only calls on click, draws validated geometry, toggles and clears without another call', async () => {
  mocks.analyze.mockResolvedValue(structuredClone(response))
  const draw = vi.fn()
  render(<AiDrawingPanel symbol="BTCUSDT" interval="15m" models={[model]} viewport={viewport} onDraw={draw} />)
  expect(mocks.analyze).not.toHaveBeenCalled()
  fireEvent.click(screen.getByText('Analyze current window'))
  await screen.findByText('Selected support')
  expect(mocks.analyze).toHaveBeenCalledOnce()
  expect(mocks.analyze.mock.calls[0][0].candles).toEqual(bars)
  expect(draw.mock.calls[0][1][0].a.price).toBe(bars[4].low)
  fireEvent.click(screen.getByLabelText('支撑线'))
  expect(draw).toHaveBeenLastCalledWith(expect.any(String), [])
  fireEvent.click(screen.getByText('Clear drawings'))
  expect(screen.queryByText('Selected support')).not.toBeInTheDocument()
  expect(mocks.analyze).toHaveBeenCalledOnce()
})
it('does not draw a delayed result into another viewport', async () => {
  let resolve!: (value: AIChartDrawingResult) => void
  mocks.analyze.mockReturnValue(new Promise<AIChartDrawingResult>(r => { resolve = r }))
  const draw = vi.fn()
  const view = (v: ChartViewport) => <AiDrawingPanel symbol="BTCUSDT" interval="15m" models={[model]} viewport={v} onDraw={draw} />
  const ui = render(view(viewport))
  fireEvent.click(screen.getByText('Analyze current window'))
  ui.rerender(view({ ...viewport, from: bars[1].time, candles: bars.slice(1) }))
  await act(async () => resolve(response))
  expect(draw).not.toHaveBeenCalled()
  expect(screen.getByRole('status')).toHaveTextContent('Old drawings are hidden')
})
it('shows failures and does not retry or draw invalid anchors', async () => {
  mocks.analyze.mockResolvedValue({ ...response, drawings: [{ ...response.drawings[0], anchors: [{ index: 999, field: 'low' }] }] })
  const draw = vi.fn()
  render(<AiDrawingPanel symbol="BTCUSDT" interval="15m" models={[model]} viewport={viewport} onDraw={draw} />)
  fireEvent.click(screen.getByText('Analyze current window'))
  await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Invalid drawing anchor'))
  expect(draw).not.toHaveBeenCalled()
  expect(mocks.analyze).toHaveBeenCalledOnce()
})
it('disables paid requests without a configured model or sufficient bars', () => {
  render(<AiDrawingPanel symbol="BTCUSDT" interval="15m" models={[]} viewport={null} onDraw={vi.fn()} />)
  expect(screen.getByText('Analyze current window')).toBeDisabled()
})
