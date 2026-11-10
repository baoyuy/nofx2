import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { startVisiblePolling } from './visiblePolling'

describe('visible read-only polling', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
    vi.spyOn(navigator, 'onLine', 'get').mockReturnValue(true)
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('does not overlap slow requests and stops after disposal', async () => {
    let resolve!: () => void
    const task = vi.fn(() => new Promise<void>((done) => { resolve = done }))
    const stop = startVisiblePolling(task, 4000)
    expect(task).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(20000)
    expect(task).toHaveBeenCalledTimes(1)
    resolve()
    await vi.advanceTimersByTimeAsync(4000)
    expect(task).toHaveBeenCalledTimes(2)
    stop()
    resolve()
    await vi.advanceTimersByTimeAsync(20000)
    document.dispatchEvent(new Event('visibilitychange'))
    expect(task).toHaveBeenCalledTimes(2)
  })

  it('pauses hidden and offline, then refreshes immediately on return', async () => {
    const visibility = vi.spyOn(document, 'visibilityState', 'get')
    const online = vi.spyOn(navigator, 'onLine', 'get')
    const task = vi.fn().mockResolvedValue(undefined)
    const stop = startVisiblePolling(task, 4000)
    await vi.advanceTimersByTimeAsync(0)
    visibility.mockReturnValue('hidden')
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(20000)
    expect(task).toHaveBeenCalledTimes(1)
    visibility.mockReturnValue('visible')
    document.dispatchEvent(new Event('visibilitychange'))
    expect(task).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(0)
    online.mockReturnValue(false)
    window.dispatchEvent(new Event('offline'))
    await vi.advanceTimersByTimeAsync(20000)
    expect(task).toHaveBeenCalledTimes(2)
    online.mockReturnValue(true)
    window.dispatchEvent(new Event('online'))
    expect(task).toHaveBeenCalledTimes(3)
    stop()
  })

  it('recovers from a rejected request without a tight retry loop', async () => {
    const task = vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValue(undefined)
    const stop = startVisiblePolling(task, 4000)
    await vi.advanceTimersByTimeAsync(3999)
    expect(task).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(task).toHaveBeenCalledTimes(2)
    stop()
  })
})
