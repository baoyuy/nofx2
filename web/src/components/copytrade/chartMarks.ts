import type { FeedItem, Holder, TradeSide } from '../../lib/api/copyTrade'

// Avatars on the K-line chart: one per open/add at the candle it happened in,
// pinned to the left edge for held positions whose entry is not on the chart,
// and small arrows for reductions and closes.

export interface EntryMark {
  key: string
  kind: 'entry' | 'pinned'
  portfolioId: string
  side: TradeSide
  /** candle time, unix seconds; 0 for pinned */
  bar: number
  price: number
  event?: FeedItem
  /** set only while this entry's position is still held */
  holder?: Holder
  /** the position this entry opened or added to has been closed, or was
   * likely liquidated */
  closed: boolean
}

export interface ExitMark {
  key: string
  bar: number
  price: number
  event: FeedItem
}

/** More avatars than this make the chart unreadable. */
export const MAX_ENTRY_MARKS = 80

/** Candle (open time in seconds) containing ms, or null when off the chart. */
export function barFor(times: number[], ms: number): number | null {
  if (!times.length) return null
  const t = ms / 1000
  const step = times.length > 1 ? times[1] - times[0] : 3600
  if (t < times[0] || t >= times[times.length - 1] + step) return null
  let lo = 0
  let hi = times.length - 1
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1
    if (times[mid] <= t) lo = mid
    else hi = mid - 1
  }
  return times[lo]
}

export function buildMarks(
  events: FeedItem[],
  holders: Holder[],
  times: number[]
): { entries: EntryMark[]; exits: ExitMark[] } {
  const entries: EntryMark[] = []
  const exits: ExitMark[] = []
  // events are newest first; keep the newest when capping.
  for (const e of events) {
    const bar = barFor(times, e.time)
    if (bar === null) continue
    const key = `${e.portfolio_id}-${e.time}-${e.action}-${e.side}`
    if (e.action === 'open' || e.action === 'add') {
      if (entries.length >= MAX_ENTRY_MARKS) continue
      // Still held only if the trader holds that side and has not closed it
      // since this entry (a later re-entry is a different position).
      const closedSince = events.some(
        (x) =>
          x.action === 'close' &&
          x.portfolio_id === e.portfolio_id &&
          x.side === e.side &&
          x.time > e.time
      )
      const holder = closedSince
        ? undefined
        : holders.find(
            (h) => h.portfolio_id === e.portfolio_id && h.side === e.side
          )
      entries.push({
        key,
        kind: 'entry',
        portfolioId: e.portfolio_id,
        side: e.side,
        bar,
        price: e.price,
        event: e,
        holder,
        closed: !holder || !!holder.liquidated,
      })
    } else if (exits.length < MAX_ENTRY_MARKS) {
      exits.push({ key, bar, price: e.price, event: e })
    }
  }
  // A held position with no avatar on the chart (opened before monitoring
  // or before the first candle) is pinned at the left edge at its entry.
  for (const h of holders) {
    const shown = entries.some(
      (m) => m.portfolioId === h.portfolio_id && m.side === h.side
    )
    if (!shown) {
      entries.push({
        key: `pin-${h.portfolio_id}-${h.side}`,
        kind: 'pinned',
        portfolioId: h.portfolio_id,
        side: h.side,
        bar: 0,
        price: h.entry_price,
        holder: h,
        closed: !!h.liquidated,
      })
    }
  }
  return { entries, exits }
}

/**
 * Traders who closed this coin within windowMs and hold neither side of it
 * now, newest close first.
 */
export function recentlyClosed(
  events: FeedItem[],
  holders: Holder[],
  now: number,
  windowMs = 24 * 3600 * 1000
): FeedItem[] {
  const holding = new Set(holders.map((h) => h.portfolio_id))
  const seen = new Set<string>()
  const out: FeedItem[] = []
  for (const e of events) {
    if (e.action !== 'close' || now - e.time > windowMs) continue
    if (holding.has(e.portfolio_id) || seen.has(e.portfolio_id)) continue
    seen.add(e.portfolio_id)
    out.push(e)
  }
  return out
}

export interface PositionResult {
  state: 'held' | 'closed' | 'unknown'
  /** USDT; unrealized when held, realized when closed */
  pnl: number
  /** % of margin when leverage is known, else % price move */
  pct: number | null
  pctOfMargin: boolean
}

/**
 * Result of the position an entry belongs to: the live position when still
 * held, else the realized PnL of the reductions and close after it.
 */
export function positionResult(
  mark: EntryMark,
  events: FeedItem[],
  lastPrice: number
): PositionResult {
  const h = mark.holder
  if (h) {
    const dir = h.side === 'long' ? 1 : -1
    const pnl =
      h.unrealized_pnl ||
      (lastPrice ? dir * (lastPrice - h.entry_price) * h.qty : 0)
    if (h.margin)
      return {
        state: 'held',
        pnl,
        pct: (pnl / h.margin) * 100,
        pctOfMargin: true,
      }
    const pct =
      lastPrice && h.entry_price
        ? ((dir * (lastPrice - h.entry_price)) / h.entry_price) * 100
        : null
    return { state: 'held', pnl, pct, pctOfMargin: false }
  }
  const e = mark.event
  if (!e) return { state: 'unknown', pnl: 0, pct: null, pctOfMargin: false }
  // events are newest first: walk forward in time from the entry.
  let pnl = 0
  let closed = false
  for (let i = events.length - 1; i >= 0; i--) {
    const x = events[i]
    if (
      x.time <= e.time ||
      x.portfolio_id !== e.portfolio_id ||
      x.side !== e.side
    )
      continue
    if (x.action === 'reduce' || x.action === 'close') pnl += x.realized_pnl
    if (x.action === 'close') {
      closed = true
      break
    }
  }
  return {
    state: closed ? 'closed' : 'unknown',
    pnl,
    pct: null,
    pctOfMargin: false,
  }
}
