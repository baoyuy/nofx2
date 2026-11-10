import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { TerminalTime } from './TerminalTime'

afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks() })

it('ticks the countdown locally, pauses hidden and catches up immediately', async () => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-01-01T00:00:00Z'))
  const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
  render(<TerminalTime nextCycleMs={Date.now() + 65000} />)
  expect(screen.getByText('1m 5s')).toBeVisible()
  await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
  expect(screen.getByText('1m 4s')).toBeVisible()
  visibility.mockReturnValue('hidden')
  act(() => document.dispatchEvent(new Event('visibilitychange')))
  await act(async () => { await vi.advanceTimersByTimeAsync(10000) })
  expect(screen.getByText('1m 4s')).toBeVisible()
  visibility.mockReturnValue('visible')
  await act(async () => { document.dispatchEvent(new Event('visibilitychange')) })
  expect(screen.getByText('0m 54s')).toBeVisible()
  await act(async () => { await vi.advanceTimersByTimeAsync(54000) })
  expect(screen.getByText('due now')).toBeVisible()
})
