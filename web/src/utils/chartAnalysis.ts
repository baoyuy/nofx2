import type { Candle } from './marketIndicators'

export interface Level {
  price: number
  touches: number
}

export interface LinePoint {
  time: number
  price: number
}

export interface Segment {
  a: LinePoint
  b: LinePoint
}

export type LineKind =
  | 'trend'
  | 'support'
  | 'resistance'
  | 'channel'
  | 'fib'
  | 'neckline'
  | 'horizontal'
  | 'gann'
  | 'pitchfork'

export const LINE_META: Record<LineKind, { label: string; color: string; desc: string }> = {
  trend: { label: '趋势线', color: '#7c3aed', desc: '连接最近两个同向摆动高低点，判断方向' },
  support: { label: '支撑线', color: '#2e8b57', desc: '多次止跌的价格，跌到这里可能反弹' },
  resistance: { label: '阻力线', color: '#d6433a', desc: '多次冲高回落的价格，涨到这里可能受阻' },
  channel: { label: '通道线', color: '#295be8', desc: '趋势线的平行线，把价格框在通道里' },
  fib: { label: '斐波那契', color: '#d97706', desc: '一段行情的 23.6%~78.6% 回调位' },
  neckline: { label: '颈线', color: '#0d9488', desc: '双顶/双底的颈线，跌破/突破代表形态成立' },
  horizontal: { label: '整数关口', color: '#64748b', desc: '整数价格，常有挂单和支撑阻力' },
  gann: { label: '江恩扇形', color: '#db2777', desc: '从关键点按不同角度发出的扇形线' },
  pitchfork: { label: '音叉线', color: '#0891b2', desc: '三个关键点构成的安德鲁音叉通道' },
}

/** Which line kinds to draw. Defaults keep the chart readable. */
export type LineKinds = Record<LineKind, boolean>

export const DEFAULT_LINE_KINDS: LineKinds = {
  trend: true,
  support: true,
  resistance: true,
  channel: false,
  fib: false,
  neckline: false,
  horizontal: false,
  gann: false,
  pitchfork: false,
}

export interface Neckline {
  price: number
  kind: 'double_top' | 'double_bottom'
  from: number
  to: number
}

export interface Fib {
  high: number
  low: number
  levels: { ratio: number; price: number }[]
}

export interface ChartAnalysis {
  supports: Level[]
  resistances: Level[]
  trend?: Segment
  channel?: { upper: Segment; lower: Segment }
  fib?: Fib
  neckline?: Neckline
  horizontals: number[]
  gann?: Segment[]
  pitchfork?: Segment[]
}

interface Pivot {
  time: number
  price: number
  kind: 'high' | 'low'
}

const WINDOW = 180
const KERNEL = 3
const MAX_LEVELS = 2

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
      out.push({ time: candles[i].time, price: kind === 'high' ? candles[i].high : candles[i].low, kind })
    }
  }
  return out
}

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

/** Double top/bottom neckline: two similar pivots with a counter-swing between. */
function neckline(highs: Pivot[], lows: Pivot[], tol: number): Neckline | undefined {
  const find = (ps: Pivot[], other: Pivot[], kind: 'double_top' | 'double_bottom'): Neckline | undefined => {
    for (let i = ps.length - 1; i >= 1; i--) {
      for (let j = i - 1; j >= 0; j--) {
        const a = ps[j]
        const b = ps[i]
        if (Math.abs(a.price - b.price) / b.price > tol) continue
        if (b.time - a.time < 3 * 3600 * 1000) continue
        const between = other.filter((p) => p.time > a.time && p.time < b.time)
        if (!between.length) continue
        const ext = kind === 'double_top'
          ? between.reduce((m, p) => (p.price < m.price ? p : m))
          : between.reduce((m, p) => (p.price > m.price ? p : m))
        return { price: ext.price, kind, from: a.time, to: b.time }
      }
    }
    return undefined
  }
  return find(highs, lows, 'double_top') || find(lows, highs, 'double_bottom')
}

/** Round-number price levels near the current price. */
function roundLevels(cs: Candle[]): number[] {
  const last = cs[cs.length - 1].close
  if (last <= 0) return []
  const mag = 10 ** Math.floor(Math.log10(last))
  let step = mag
  for (const m of [1, 2, 5, 10]) {
    if (last / (mag * m) <= 8) {
      step = mag * m
      break
    }
  }
  const out: number[] = []
  const start = Math.ceil((last - 4 * step) / step) * step
  for (let v = start; v <= last + 4 * step; v += step) {
    if (v > 0) out.push(Number(v.toPrecision(10)))
    if (out.length >= 5) break
  }
  return out
}

export function analyzeChart(candles: Candle[]): ChartAnalysis {
  const cs = candles.slice(-WINDOW)
  const empty: ChartAnalysis = { supports: [], resistances: [], horizontals: [] }
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
  const tol = Math.max(avgRange / (lo || 1), 0.004)

  const lows = pivots(cs, 'low')
  const highs = pivots(cs, 'high')
  const supports = cluster(lows, tol)
  const resistances = cluster(highs, tol)
  const result: ChartAnalysis = { supports, resistances, horizontals: [] }

  const slope = regressionSlope(cs)
  const upTrend = slope >= 0
  const base = upTrend ? lows : highs
  if (base.length >= 2) {
    const a = base[base.length - 2]
    const b = base[base.length - 1]
    if (b.time !== a.time) {
      result.trend = { a: { time: a.time, price: a.price }, b: { time: b.time, price: b.price } }
      const opposite = upTrend ? highs : lows
      const between = opposite.filter((p) => p.time >= a.time && p.time <= b.time)
      if (between.length) {
        const ext = upTrend
          ? between.reduce((m, p) => (p.price > m.price ? p : m))
          : between.reduce((m, p) => (p.price < m.price ? p : m))
        const slopePrice = (b.price - a.price) / (b.time - a.time)
        const shift = ext.price - (a.price + slopePrice * (ext.time - a.time))
        const segA = { time: a.time, price: a.price }
        const segB = { time: b.time, price: b.price }
        const offA = { time: a.time, price: a.price + shift }
        const offB = { time: b.time, price: b.price + shift }
        result.channel = {
          lower: { a: upTrend ? segA : offA, b: upTrend ? segB : offB },
          upper: { a: upTrend ? offA : segA, b: upTrend ? offB : segB },
        }
      }
    }
  }

  if (hi > lo) {
    const ratios = [0.236, 0.382, 0.5, 0.618, 0.786]
    result.fib = { high: hi, low: lo, levels: ratios.map((r) => ({ ratio: r, price: hi - (hi - lo) * r })) }
  }

  result.neckline = neckline(highs, lows, tol)
  result.horizontals = roundLevels(cs)

  // Gann fan from the last pivot in the trend direction.
  const anchor = upTrend ? lows[lows.length - 1] : highs[highs.length - 1]
  if (anchor) {
    const endTime = cs[cs.length - 1].time
    const span = endTime - anchor.time
    if (span > 0) {
      const dir = upTrend ? 1 : -1
      const unit = ((hi - lo) / cs.length) * dir
      result.gann = [0.5, 1, 2].map((k) => ({
        a: { time: anchor.time, price: anchor.price },
        b: { time: endTime, price: anchor.price + unit * k * (span / (3600 * 1000)) },
      }))
    }
  }

  // Andrew pitchfork: P0, P1, P2 alternating pivots; median through P1 and the
  // P0-P2 midpoint, parallels through P0 and P2.
  const all = [...highs, ...lows].sort((x, y) => x.time - y.time)
  if (all.length >= 3) {
    const [p0, p2, p1] = all.slice(-3)
    if (p0.time !== p1.time && p2.time !== p1.time) {
      const mid = { time: (p0.time + p2.time) / 2, price: (p0.price + p2.price) / 2 }
      const slopePrice = (mid.price - p1.price) / (mid.time - p1.time)
      const endTime = cs[cs.length - 1].time
      const line = (start: LinePoint, offset: number): Segment => ({
        a: { time: start.time, price: start.price + offset },
        b: { time: endTime, price: start.price + offset + slopePrice * (endTime - start.time) },
      })
      result.pitchfork = [line(p0, 0), line({ time: p1.time, price: p1.price }, 0), line(p2, 0)]
    }
  }

  return result
}
