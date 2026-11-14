import { describe, expect, it } from 'vitest'
import { AI_DRAWING_META, buildAIDrawingPrimitives, type AIChartDrawing, type AIDrawingKind } from './aiChartDrawings'
import type { Candle } from './marketIndicators'
const bars: Candle[] = Array.from({ length: 40 }, (_, i) => ({ time: 1700000000 + i * 900, open: 100 + i, close: 101 + i, high: 105 + i, low: 95 + i, volume: 100 }))
function drawing(kind: AIDrawingKind): AIChartDrawing {
  const counts = { trend: 2, support: 1, resistance: 1, zone: 2, channel: 3, fib: 2, neckline: 2, horizontal: 1, gann: 2, pitchfork: 3 }
  return { kind, anchors: [{ index: 4, field: 'low' }, { index: 12, field: 'high' }, { index: 20, field: 'low' }].slice(0, counts[kind]) as AIChartDrawing['anchors'], reason: 'Observed swing', invalidation: 'Close below support' }
}
describe('AI selected chart geometry', () => {
  it.each(Object.keys(AI_DRAWING_META) as AIDrawingKind[])('builds %s from snapshot OHLC only', kind => {
    const result = buildAIDrawingPrimitives([drawing(kind)], bars)
    expect(result.length).toBeGreaterThan(0)
    expect(result.every(s => s.a.time >= bars[0].time && s.b.time <= bars[39].time && Number.isFinite(s.b.price))).toBe(true)
  })
  it('computes retracement prices rather than trusting model coordinates', () => {
    const result = buildAIDrawingPrimitives([drawing('fib')], bars)
    expect(result).toHaveLength(5)
    expect(result[0].a.price).toBeCloseTo(bars[12].high + (bars[4].low - bars[12].high) * 0.236)
  })
  it('keeps the Andrews median through the two later anchors midpoint', () => {
    const result = buildAIDrawingPrimitives([drawing('pitchfork')], bars)
    const slopes = result.map(s => (s.b.price - s.a.price) / (s.b.time - s.a.time))
    expect(slopes[0]).toBeCloseTo(slopes[1], 10)
    expect(slopes[1]).toBeCloseTo(slopes[2], 10)
    const median = result[1]
    const midpointTime = (bars[12].time + bars[20].time) / 2
    expect(median.a.price + slopes[1] * (midpointTime - median.a.time)).toBeCloseTo((bars[12].high + bars[20].low) / 2)
  })
  it('rejects invalid indices, fields, unsupported types and duplicate anchors', () => {
    const base = drawing('trend')
    expect(() => buildAIDrawingPrimitives([{ ...base, anchors: [{ index: 99, field: 'low' }, base.anchors[1]] }], bars)).toThrow()
    expect(() => buildAIDrawingPrimitives([{ ...base, kind: 'unknown' as AIDrawingKind }], bars)).toThrow()
    expect(() => buildAIDrawingPrimitives([{ ...base, anchors: [base.anchors[0], base.anchors[0]] }], bars)).toThrow()
    expect(() => buildAIDrawingPrimitives([{ ...base, reason: '' }], bars)).toThrow()
  })
  it('accepts a valid no-structure response', () => {
    expect(buildAIDrawingPrimitives([], bars)).toEqual([])
  })
})
