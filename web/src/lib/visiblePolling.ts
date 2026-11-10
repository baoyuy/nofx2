// Read-only polling: wait for settlement before scheduling again, pause in
// background/offline, and catch up immediately when the user returns.
export function startVisiblePolling(task: () => Promise<unknown>, interval: number) {
  let stopped = false
  let running = false
  let timer: ReturnType<typeof setTimeout> | undefined
  const visible = () => document.visibilityState !== 'hidden' && navigator.onLine !== false
  const clear = () => {
    clearTimeout(timer)
    timer = undefined
  }
  const run = async () => {
    if (stopped || running || !visible()) return
    clear()
    running = true
    try {
      await task()
    } catch {
      // Keep polling after transient failures; callers own error presentation.
    } finally {
      running = false
      if (!stopped && visible()) timer = setTimeout(() => void run(), interval)
    }
  }
  const resume = () => {
    clear()
    if (visible()) void run()
  }
  document.addEventListener('visibilitychange', resume)
  window.addEventListener('online', resume)
  window.addEventListener('offline', resume)
  void run()
  return () => {
    stopped = true
    clear()
    document.removeEventListener('visibilitychange', resume)
    window.removeEventListener('online', resume)
    window.removeEventListener('offline', resume)
  }
}
