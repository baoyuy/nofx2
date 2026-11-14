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
  touches?: number
  confirmedAt?: number
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
  trend: { label: '趋势线', color: '#7c3aed', desc: '已收盘K线中至少三次独立触碰，排除异常插针与已穿越的线；仅供分析' },
  support: { label: '支撑线', color: '#2e8b57', desc: '多次止跌的价格，跌到这里可能反弹' },
  resistance: { label: '阻力线', color: '#d6433a', desc: '多次冲高回落的价格，涨到这里可能受阻' },
  channel: { label: '通道包络', color: '#295be8', desc: '有效趋势线的平行价格包络，另一侧不保证多次触碰' },
  fib: { label: '斐波那契', color: '#d97706', desc: '一段行情的 23.6%~78.6% 回调位' },
  neckline: { label: '颈线', color: '#0d9488', desc: '双顶/双底的颈线，跌破/突破代表形态成立' },
  horizontal: { label: '整数关口', color: '#64748b', desc: '整数价格，常有挂单和支撑阻力' },
  gann: { label: '比例扇形（非标准江恩）', color: '#db2777', desc: '按当前窗口价格范围/根数生成的比例扇形，不是固定价格时间单位的标准江恩角度' },
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
  index: number
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
      if (kind === 'high' ? candles[j].high > candles[i].high || (j < i && candles[j].high === candles[i].high) : candles[j].low < candles[i].low || (j < i && candles[j].low === candles[i].low)) {
        ok = false
        break
      }
    }
    if (ok) {
      out.push({ time: candles[i].time, price: kind === 'high' ? candles[i].high : candles[i].low, kind, index: i })
    }
  }
  return out
}

function cluster(ps: Pivot[], tol: number): Level[] {
  const groups: { price: number; points: Pivot[] }[] = []
  for (const p of [...ps].sort((a, b) => a.price - b.price)) {
    const g = groups.find(x => Math.abs(x.price - p.price) / x.price <= tol)
    if (g) {
      // One candle (or nearby platform) is not several independent tests.
      if (g.points.some(q => Math.abs(q.index - p.index) <= KERNEL * 2)) continue
      g.points.push(p)
      g.price = g.points.reduce((sum, q) => sum + q.price, 0) / g.points.length
    } else groups.push({ price: p.price, points: [p] })
  }
  return groups.filter(g => g.points.length >= 2).map(g => ({ price: g.price, touches: g.points.length })).sort((a, b) => b.touches - a.touches)
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
function neckline(highs: Pivot[], lows: Pivot[], tol: number, barMs: number): Neckline | undefined {
  const find = (ps: Pivot[], other: Pivot[], kind: 'double_top' | 'double_bottom'): Neckline | undefined => {
    for (let i = ps.length - 1; i >= 1; i--) {
      for (let j = i - 1; j >= 0; j--) {
        const a = ps[j]
        const b = ps[i]
        if (Math.abs(a.price - b.price) / b.price > tol) continue
        if (b.time - a.time < 3 * barMs) continue
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
  // Aim for a step around 1% of price, snapped to a "nice" 1/2/5 × 10^n.
  const target = last / 100
  const mag = 10 ** Math.floor(Math.log10(target))
  let step = mag * 10
  for (const m of [1, 2, 5, 10]) {
    if (mag * m >= target) {
      step = mag * m
      break
    }
  }
  const out: number[] = []
  const start = Math.ceil((last - 3 * step) / step) * step
  for (let v = start; v <= last + 3 * step && out.length < 7; v += step) {
    if (v > 0 && Math.abs(v - last) / last <= 0.06) out.push(Number(v.toPrecision(10)))
  }
  return out
}

export function analyzeChart(candles: Candle[], maxLevels = MAX_LEVELS, options: { includeLast?: boolean; windowSize?: number } = {}): ChartAnalysis {
  // The final bar may still be forming. Never use it for recognition.
  const cs = [...new Map(candles.filter(c =>
    [c.time, c.open, c.high, c.low, c.close].every(Number.isFinite) && c.low > 0 &&
    c.low <= Math.min(c.open, c.close) && c.high >= Math.max(c.open, c.close),
  ).map(c => [c.time, c])).values()].sort((a, b) => a.time - b.time).slice(0, options.includeLast ? undefined : -1).slice(-(options.windowSize ?? WINDOW))
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
  const ranges = cs.map(c => c.high - c.low).sort((a, b) => a - b)
  const typicalRange = ranges[Math.floor(ranges.length / 2)]
  const avgRange = Math.max(typicalRange, range / cs.length * 0.25)
  const tol = Math.min(0.02, Math.max(avgRange / cs[cs.length - 1].close * 0.5, 0.004))

  const lows = pivots(cs, 'low')
  const highs = pivots(cs, 'high')
  // A level is support or resistance by where price is NOW, not by whether a
  // swing high or low formed it: an old resistance that price broke above is
  // support today, and vice versa.
  const current = cs[cs.length - 1].close
  const levels = cluster([...highs, ...lows], tol)
  const supports = levels
    .filter((l) => l.price < current && current - l.price <= Math.max(current * 0.12, avgRange * 6))
    .sort((a, b) => b.price - a.price) // nearest support first
    .slice(0, maxLevels)
  const resistances = levels
    .filter((l) => l.price > current && l.price - current <= Math.max(current * 0.12, avgRange * 6))
    .sort((a, b) => a.price - b.price) // nearest resistance first
    .slice(0, maxLevels)
  const result: ChartAnalysis = { supports, resistances, horizontals: [] }

  const barMs = cs.length > 1 ? Math.max(cs[1].time - cs[0].time, 1) : 3600000
  const slope = regressionSlope(cs)
  const upTrend = slope >= 0
  const base = (upTrend ? lows : highs).filter(p => {
    const c = cs[p.index]
    const wick = upTrend ? Math.min(c.open, c.close) - c.low : c.high - Math.max(c.open, c.close)
    return wick <= Math.max(typicalRange * 3, Math.abs(c.close - c.open) * 3)
  }).slice(-16)
  const tolerance = Math.max(avgRange * 0.35, current * 0.001)
  const candidates: { a: Pivot; b: Pivot; slope: number; touches: number }[] = []
  for (let i = 0; i < base.length; i++) for (let j = i + 1; j < base.length; j++) {
    const a = base[i], b = base[j]
    if (b.index - a.index < KERNEL * 2) continue
    const slope = (b.price - a.price) / (b.time - a.time)
    if (upTrend ? slope <= 0 : slope >= 0) continue
    const at = (time: number) => a.price + slope * (time - a.time)
    const touches = base.filter(p => p.time >= a.time && Math.abs(p.price - at(p.time)) <= tolerance)
      .reduce<Pivot[]>((accepted, p) => {
        if (!accepted.length || p.index - accepted[accepted.length - 1].index >= KERNEL * 2) accepted.push(p)
        return accepted
      }, [])
    if (touches.length < 3) continue
    if (cs.slice(a.index).some(c => upTrend ? c.low < at(c.time) - tolerance : c.high > at(c.time) + tolerance)) continue
    candidates.push({ a, b, slope, touches: touches.length })
  }
  const best = candidates.sort((a, b) => b.touches - a.touches || b.b.time - a.b.time)[0]
  if (best) {
    const end = cs[cs.length - 1]
    const a = { time: best.a.time, price: best.a.price }
    const b = { time: end.time, price: best.a.price + best.slope * (end.time - best.a.time) }
    result.trend = { a, b, touches: best.touches, confirmedAt: end.time + barMs }
    const offsets = cs.slice(best.a.index).map(c => (upTrend ? c.high : c.low) - (a.price + best.slope * (c.time - a.time)))
    const shift = upTrend ? Math.max(...offsets) : Math.min(...offsets)
    const shifted = { a: { time: a.time, price: a.price + shift }, b: { time: b.time, price: b.price + shift } }
    result.channel = { lower: upTrend ? result.trend : shifted, upper: upTrend ? shifted : result.trend }
  }

  if (hi > lo) {
    const ratios = [0.236, 0.382, 0.5, 0.618, 0.786]
    result.fib = { high: hi, low: lo, levels: ratios.map((r) => ({ ratio: r, price: hi - (hi - lo) * r })) }
  }

  result.neckline = neckline(highs, lows, tol, barMs)
  result.horizontals = roundLevels(cs)

  // Gann fan from the last pivot in the trend direction.
  const anchor = upTrend ? lows[lows.length - 1] : highs[highs.length - 1]
  if (anchor) {
    const endTime = cs[cs.length - 1].time
    const span = endTime - anchor.time
    if (span > 0) {
      const dir = upTrend ? 1 : -1
      const unitPerBar = ((hi - lo) / cs.length) * dir
      const bars = span / barMs
      result.gann = [0.5, 1, 2].map((k) => ({
        a: { time: anchor.time, price: anchor.price },
        b: { time: endTime, price: anchor.price + unitPerBar * k * bars },
      }))
    }
  }

  // Standard Andrews pitchfork: median from P0 through midpoint(P1, P2),
  // with parallels through P1/P2. Require alternating, distinct pivots.
  const all: Pivot[] = []
  for (const p of [...highs, ...lows].sort((x, y) => x.time - y.time)) {
    const prev = all[all.length - 1]
    if (prev?.time === p.time) continue
    if (!prev || prev.kind !== p.kind) all.push(p)
    else if (p.kind === 'high' ? p.price > prev.price : p.price < prev.price) all[all.length - 1] = p
  }
  if (all.length >= 3) {
    const [p0, p1, p2] = all.slice(-3)
    const mid = { time: (p1.time + p2.time) / 2, price: (p1.price + p2.price) / 2 }
    const slopePrice = (mid.price - p0.price) / (mid.time - p0.time)
    const endTime = cs[cs.length - 1].time
    const line = (start: LinePoint): Segment => ({
      a: { time: start.time, price: start.price },
      b: { time: endTime, price: start.price + slopePrice * (endTime - start.time) },
    })
    result.pitchfork = [line(p1), line(p0), line(p2)]
  }

  return result
}

/** Logical indices match the candle series. Exclude the global forming bar,
 * not the last bar of a historical viewport. Never borrow offscreen pivots. */
export function analyzeVisibleChart(candles: Candle[], range: { from: number; to: number } | null, maxLevels = MAX_LEVELS) {
  if (!range || !Number.isFinite(range.from) || !Number.isFinite(range.to)) return null
  const first = Math.max(0, Math.ceil(range.from))
  const end = Math.min(candles.length - 1, Math.floor(range.to) + 1)
  const visible = end > first ? candles.slice(first, end) : []
  if (!visible.length) return null
  return {
    from: visible[0].time,
    to: visible[visible.length - 1].time,
    analysis: analyzeChart(visible, maxLevels, { includeLast: true, windowSize: visible.length }),
  }
}
