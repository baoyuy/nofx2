import { useEffect, useState } from 'react'
import { startVisiblePolling } from '../../lib/visiblePolling'

// Keep the one-second display timer out of the dashboard's expensive panels.
export function TerminalTime({ nextCycleMs, isZh = false }: { nextCycleMs?: number | null; isZh?: boolean }) {
  const [now, setNow] = useState(Date.now)
  useEffect(() => startVisiblePolling(async () => { setNow(Date.now()) }, 1000), [])
  if (nextCycleMs === undefined) return <>{new Date(now).toLocaleTimeString('en-GB', { hour12: false })}</>
  if (!nextCycleMs) return <>—</>
  const remaining = nextCycleMs - now
  if (remaining <= 0) return <>{isZh ? '已到期' : 'due now'}</>
  const seconds = Math.floor(remaining / 1000)
  return <>{Math.floor(seconds / 60)}m {seconds % 60}s</>
}
