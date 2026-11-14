import type { Candle } from './marketIndicators'

export const AI_DRAWING_META = {
  trend: '趋势线', support: '支撑线', resistance: '阻力线', zone: '价格区域',
  channel: '通道', fib: '斐波那契', neckline: '形态颈线', horizontal: '水平价格线',
  gann: '比例扇形（非标准江恩）', pitchfork: '安德鲁音叉',
} as const
export type AIDrawingKind = keyof typeof AI_DRAWING_META
export interface DrawingAnchor { index: number; field: 'high' | 'low' | 'close' }
export interface AIChartDrawing {
  kind: AIDrawingKind
  anchors: DrawingAnchor[]
  reason: string
  invalidation: string
}
export interface AIChartDrawingResult {
  symbol: string; interval: string; from: number; to: number; analyzed_at: number
  summary: string; drawings: AIChartDrawing[]; warnings: string[]
}
export interface DrawingPoint { time: number; price: number }
export interface DrawingPrimitive {
  id: string; kind: AIDrawingKind; a: DrawingPoint; b: DrawingPoint
  label: string; reason: string; invalidation: string; region?: boolean
}
export interface ChartViewport { candles: Candle[]; from: number; to: number }
export const viewportKey = (symbol: string, interval: string, viewport: ChartViewport | null) =>
  viewport ? `${symbol}|${interval}|${viewport.from}|${viewport.to}|${viewport.candles.length}` : ''

/** Resolve model-selected indices against an immutable request snapshot. Reject
 * unknown types, offscreen anchors, impossible prices and degenerate geometry.
 * No model-authored free coordinates or executable content is accepted. */
export function buildAIDrawingPrimitives(drawings: AIChartDrawing[], candles: Candle[]): DrawingPrimitive[] {
  if (!Array.isArray(drawings) || drawings.length > 20 || !candles.length) throw new Error('Invalid drawing response')
  const counts: Record<AIDrawingKind, number> = { trend: 2, support: 1, resistance: 1, zone: 2, channel: 3, fib: 2, neckline: 2, horizontal: 1, gann: 2, pitchfork: 3 }
  const end = candles[candles.length - 1].time
  const out: DrawingPrimitive[] = []
  drawings.forEach((d, i) => {
    if (!d || !Object.prototype.hasOwnProperty.call(counts, d.kind) || !Array.isArray(d.anchors) || d.anchors.length !== counts[d.kind] ||
        typeof d.reason !== 'string' || !d.reason.trim() || d.reason.length > 1000 ||
        typeof d.invalidation !== 'string' || !d.invalidation.trim() || d.invalidation.length > 1000) throw new Error('Invalid drawing evidence')
    const points = d.anchors.map(anchor => {
      if (!anchor || !Number.isInteger(anchor.index) || anchor.index < 0 || anchor.index >= candles.length ||
          !['high', 'low', 'close'].includes(anchor.field)) throw new Error('Invalid drawing anchor')
      const bar = candles[anchor.index]
      const price = bar[anchor.field]
      if (!Number.isFinite(price) || price <= 0 || !Number.isFinite(bar.time)) throw new Error('Invalid drawing price')
      return { time: bar.time, price }
    })
    if (new Set(d.anchors.map(a => a.index)).size !== points.length) throw new Error('Duplicate drawing anchors')
    const [a, b, c] = points
    if (b && a.time >= b.time) throw new Error('Anchors must be chronological')
    if (d.kind === 'pitchfork' && (d.anchors.some(p => p.field === 'close') || d.anchors[0].field === d.anchors[1].field || d.anchors[1].field === d.anchors[2].field)) throw new Error('Pitchfork requires alternating highs and lows')
    if (b && ['trend', 'neckline', 'fib', 'gann'].includes(d.kind) && a.price === b.price && d.kind !== 'neckline') throw new Error('Degenerate baseline')
    if (d.kind === 'pitchfork' && b.time >= c.time) throw new Error('Pitchfork anchors must alternate chronologically')
    const add = (p: DrawingPoint, q: DrawingPoint, suffix = '', region = false) => {
      if (![p.price, q.price, p.time, q.time].every(Number.isFinite) || p.price <= 0 || q.price <= 0) throw new Error('Invalid projected geometry')
      out.push({ id: `ai-${i}-${out.length}`, kind: d.kind, a: p, b: q, label: `${AI_DRAWING_META[d.kind]}${suffix}`, reason: d.reason, invalidation: d.invalidation, region })
    }
    const project = (p: DrawingPoint, slope: number) => ({ time: end, price: p.price + slope * (end - p.time) })
    switch (d.kind) {
      case 'support': case 'resistance': case 'horizontal':
        add(a, { time: end, price: a.price }); break
      case 'zone':
        if (a.price === b.price) throw new Error('Empty price zone')
        add(a, { time: end, price: b.price }, '', true); break
      case 'trend': case 'neckline':
        add(a, project(a, (b.price - a.price) / (b.time - a.time))); break
      case 'channel': {
        const slope = (b.price - a.price) / (b.time - a.time)
        const shift = c.price - (a.price + slope * (c.time - a.time))
        if (Math.abs(shift) < a.price * 1e-10) throw new Error('Empty channel')
        add(a, project(a, slope)); add(c, project(c, slope)); break
      }
      case 'fib':
        if (a.price === b.price) throw new Error('Empty Fibonacci swing')
        for (const ratio of [0.236, 0.382, 0.5, 0.618, 0.786]) {
          const price = b.price + (a.price - b.price) * ratio
          add({ time: b.time, price }, { time: end, price }, ` ${(ratio * 100).toFixed(1)}%`)
        }
        break
      case 'gann': {
        if (a.price === b.price) throw new Error('Empty fan unit')
        // Unit explicitly defined by the two selected time/price anchors;
        // never claim screen angle equals a standard Gann angle.
        const unit = (b.price - a.price) / (b.time - a.time)
        for (const ratio of [0.5, 1, 2]) add(a, project(a, unit * ratio), ` ${ratio}:1`)
        break
      }
      case 'pitchfork': {
        const slope = ((b.price + c.price) / 2 - a.price) / ((b.time + c.time) / 2 - a.time)
        for (const p of [b, a, c]) add(p, project(p, slope))
        break
      }
    }
  })
  return out
}
