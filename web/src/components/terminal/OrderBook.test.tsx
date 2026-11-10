import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { OrderBook } from './OrderBook'
vi.mock('../../contexts/LanguageContext', () => ({ useLanguage: () => ({ language: 'en' }) }))

beforeEach(() => {
  vi.useFakeTimers()
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
})
afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

it('polls depth without unrelated universe discovery, pauses hidden, catches up on return and aborts on unmount', async () => {
  const fetcher = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({ symbol: 'BTCUSDT', bids: [[99, 2]], asks: [[101, 3]] }) })
  vi.stubGlobal('fetch', fetcher)
  const ui = render(<OrderBook symbol="BTC" />)
  await act(async () => { await vi.advanceTimersByTimeAsync(0) })
  expect(fetcher).toHaveBeenCalledTimes(1)
  expect(fetcher.mock.calls[0][0]).toContain('/api/market/depth?symbol=BTCUSDT')
  const signal = fetcher.mock.calls[0][1].signal as AbortSignal
  await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
  expect(fetcher).toHaveBeenCalledTimes(2)
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
  act(() => document.dispatchEvent(new Event('visibilitychange')))
  await act(async () => { await vi.advanceTimersByTimeAsync(20000) })
  expect(fetcher).toHaveBeenCalledTimes(2)
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
  await act(async () => { document.dispatchEvent(new Event('visibilitychange')) })
  expect(fetcher).toHaveBeenCalledTimes(3)
  expect(screen.getByText('Order book')).toBeVisible()
  ui.unmount()
  expect(signal.aborted).toBe(true)
  await vi.advanceTimersByTimeAsync(10000)
  expect(fetcher).toHaveBeenCalledTimes(3)
})
