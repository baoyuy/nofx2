import type { Candle } from './marketIndicators'

export interface Level {
  price: number
  touches: number
}

export interface LinePoint {
  time: number
  price: number
}

export interface TrendLine {
  a: LinePoint
  b: LinePoint
}

export interface Channel {
  upper: [LinePoint, LinePoint]
  lower: [LinePoint, LinePoint]
}

export interface Fib {
  high: number
  low: number
  levels: { ratio: number; price: number }[]
}

export interface ChartAnalysis {
  supports: Level[]
  resistances: Level[]
  trend?: TrendLine
  channel?: Channel
  fib?: Fib
}

interface Pivot {
  time: number
  price: number
}

const WINDOW = 180
const KERNEL = 3
const MAX_LEVELS = 3

function pivots(candles: Candle[], kind: 'high' | 'low'): Pivot[] {
  const out: Pivot[] = []
  for (let i = KERNEL; i < candles.length - KERNEL; i++) {
    let ok = true
    for (let j = i - KERNEL; j <= i + KERNEL; j++) {
      if (j === i) continue
      if (kind === 'high' ? candles[j].high > candles[i].high : candles[j].low < candles[i].low) {
        ok = false
        break
      }
    }
    if (ok) {
      out.push({ time: candles[i].time, price: kind === 'high' ? candles[i].high : candles[i].low })
    }
  }
  return out
}

/** Cluster pivots into price levels (support from lows, resistance from highs). */
function cluster(ps: Pivot[], tol: number): Level[] {
  const groups: { price: number; touches: number }[] = []
  for (const p of ps) {
    const g = groups.find((x) => Math.abs(x.price - p.price) / x.price <= tol)
    if (g) {
      g.price = (g.price * g.touches + p.price) / (g.touches + 1)
      g.touches++
    } else {
      groups.push({ price: p.price, touches: 1 })
    }
  }
  return groups
    .filter((g) => g.touches >= 2)
    .sort((a, b) => b.touches - a.touches)
    .slice(0, MAX_LEVELS)
}

function regressionSlope(candles: Candle[]): number {
  const n = candles.length
  if (n < 2) return 0
  let sx = 0
  let sy = 0
  let sxx = 0
  let sxy = 0
  for (let i = 0; i < n; i++) {
    const x = i
    const y = candles[i].close
    sx += x
    sy += y
    sxx += x * x
    sxy += x * y
  }
  const den = n * sxx - sx * sx
  return den === 0 ? 0 : (n * sxy - sx * sy) / den
}

/**
 * Draw lines from the candles the way a chartist would:
 * support/resistance from clustered swing pivots, a trend line through the
 * last two relevant pivots, a parallel channel through the extreme pivot, and
 * Fibonacci retracement of the window's swing.
 */
export function analyzeChart(candles: Candle[]): ChartAnalysis {
  const cs = candles.slice(-WINDOW)
  const empty: ChartAnalysis = { supports: [], resistances: [] }
  if (cs.length < 30) return empty

  let lo = Infinity
  let hi = -Infinity
  let range = 0
  for (const c of cs) {
    lo = Math.min(lo, c.low)
    hi = Math.max(hi, c.high)
    range += c.high - c.low
  }
  const avgRange = range / cs.length
  const tol = Math.max(avgRange / (lo || 1), 0.004) // 0.4% min

  const lows = pivots(cs, 'low')
  const highs = pivots(cs, 'high')
  const supports = cluster(lows, tol)
  const resistances = cluster(highs, tol)

  const result: ChartAnalysis = { supports, resistances }

  const slope = regressionSlope(cs)
  const upTrend = slope >= 0
  const base = upTrend ? lows : highs
  if (base.length >= 2) {
    const a = base[base.length - 2]
    const b = base[base.length - 1]
    if (b.time !== a.time) {
      result.trend = { a: { time: a.time, price: a.price }, b: { time: b.time, price: b.price } }
      // Parallel channel through the opposite extreme pivot inside the span.
      const opposite = upTrend ? highs : lows
      const between = opposite.filter((p) => p.time >= a.time && p.time <= b.time)
      if (between.length) {
        const extreme = upTrend
          ? between.reduce((m, p) => (p.price > m.price ? p : m))
          : between.reduce((m, p) => (p.price < m.price ? p : m))
        // Offset the trend line to pass through the extreme pivot.
        const slopePrice = (b.price - a.price) / (b.time - a.time)
        const shift = extreme.price - (a.price + slopePrice * (extreme.time - a.time))
        result.channel = {
          lower: upTrend
            ? [
                { time: a.time, price: a.price },
                { time: b.time, price: b.price },
              ]
            : [
                { time: a.time, price: a.price + shift },
                { time: b.time, price: b.price + shift },
              ],
          upper: upTrend
            ? [
                { time: a.time, price: a.price + shift },
                { time: b.time, price: b.price + shift },
              ]
            : [
                { time: a.time, price: a.price },
                { time: b.time, price: b.price },
              ],
        }
      }
    }
  }

  if (hi > lo) {
    const ratios = [0.236, 0.382, 0.5, 0.618, 0.786]
    result.fib = {
      high: hi,
      low: lo,
      levels: ratios.map((r) => ({ ratio: r, price: hi - (hi - lo) * r })),
    }
  }

  return result
}
