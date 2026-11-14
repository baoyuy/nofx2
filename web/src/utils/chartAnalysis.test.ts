import { describe, expect, it } from 'vitest'
import { analyzeChart, analyzeVisibleChart } from './chartAnalysis'
import type { Candle } from './marketIndicators'

function makeCandles(): Candle[] {
  const out: Candle[] = []
  for (let i = 0; i < 80; i++) {
    const base = 100 + Math.sin(i / 5) * 6
    const low = i % 20 === 10 ? 94 : Math.min(base, 98)
    const high = i % 20 === 0 ? 112 : Math.max(base, 102)
    out.push({ time: 1_700_000_000 + i * 3600, open: base, high, low, close: base, volume: 1000 })
  }
  return out
}

describe('analyzeChart', () => {
  it('finds clustered support/resistance and a trend line', () => {
    const a = analyzeChart(makeCandles())
    expect(a.supports.length).toBeGreaterThan(0)
    expect(a.resistances.length).toBeGreaterThan(0)
    expect(a.supports[0].price).toBeLessThan(a.resistances[0].price)
    expect(a.trend).toBeTruthy()
    // Fib levels sit between the window low and high.
    expect(a.fib?.levels.length).toBe(5)
    for (const l of a.fib!.levels) {
      expect(l.price).toBeGreaterThanOrEqual(a.fib!.low)
      expect(l.price).toBeLessThanOrEqual(a.fib!.high)
    }
  })

  it('returns nothing on too little data', () => {
    const a = analyzeChart(makeCandles().slice(0, 10))
    expect(a.supports).toHaveLength(0)
    expect(a.resistances).toHaveLength(0)
  })
})

function trendingCandles(direction: 1 | -1): Candle[] {
  const out: Candle[] = []
  let price = 100
  for (let i = 0; i < 80; i++) {
    const wave = Math.sin(i / 4) * 3
    const close = price + direction * i * 0.5 + wave
    out.push({
      time: 1_700_000_000 + i * 900,
      open: close - wave / 2,
      high: close + 2,
      low: close - 2,
      close,
      volume: 1000,
    })
  }
  return out
}

describe('analyzeChart geometry', () => {
  it('keeps Gann rays on the correct side and ordered by slope', () => {
    const a = analyzeChart(trendingCandles(1))
    expect(a.gann?.length).toBe(3)
    const ends = a.gann!.map((g) => g.b.price)
    expect(ends[0]).toBeLessThan(ends[1])
    expect(ends[1]).toBeLessThan(ends[2])
  })

  it('draws three parallel pitchfork lines', () => {
    const a = analyzeChart(trendingCandles(1))
    expect(a.pitchfork?.length).toBe(3)
    const slopes = a.pitchfork!.map((g) => (g.b.price - g.a.price) / (g.b.time - g.a.time))
    expect(Math.abs(slopes[0] - slopes[1])).toBeLessThan(1e-12)
    expect(Math.abs(slopes[1] - slopes[2])).toBeLessThan(1e-12)
  })

  it('keeps round-number levels positive and near the price', () => {
    const cs = trendingCandles(1)
    const a = analyzeChart(cs)
    const last = cs[cs.length - 1].close
    expect(a.horizontals.length).toBeGreaterThan(0)
    for (const v of a.horizontals) {
      expect(v).toBeGreaterThan(0)
      expect(Math.abs(v - last) / last).toBeLessThan(0.3)
    }
  })

  it('never labels a level on the wrong side of the current price', () => {
    const a = analyzeChart(makeCandles())
    const last = makeCandles().slice(-1)[0].close
    for (const s of a.supports) expect(s.price).toBeLessThan(last)
    for (const r of a.resistances) expect(r.price).toBeGreaterThan(last)
  })
})

describe('market auto-line safety', () => {
  it('ignores changes to the forming candle', () => {
    const cs = trendingCandles(1)
    const expected = analyzeChart(cs)
    const changed = cs.map((c, i) => i === cs.length - 1 ? { ...c, high: 10000, low: 1, close: 500, open: 200 } : c)
    expect(analyzeChart(changed)).toEqual(expected)
  })

  it('does not turn a single spike and a second low into a trend', () => {
    const cs: Candle[] = Array.from({ length: 90 }, (_, i) => {
      const close = 100 + i * 0.05
      return { time: 1700000000 + i * 900, open: close, close, low: close - 1, high: close + 1, volume: 1 }
    })
    cs[65].low = 60
    cs[76].low = 98
    expect(analyzeChart(cs).trend).toBeUndefined()
  })

  it('requires at least three touches and validates the projected support', () => {
    const cs = trendingCandles(1)
    const trend = analyzeChart(cs).trend
    expect(trend).toBeDefined()
    expect(trend!.touches).toBeGreaterThanOrEqual(3)
    const slope = (trend!.b.price - trend!.a.price) / (trend!.b.time - trend!.a.time)
    for (const c of cs.slice(0, -1).filter(c => c.time >= trend!.a.time)) {
      expect(c.low).toBeGreaterThanOrEqual(trend!.a.price + slope * (c.time - trend!.a.time) - 2)
    }
  })

  it('places the pitchfork median through the P1/P2 midpoint', () => {
    const fork = analyzeChart(trendingCandles(1)).pitchfork!
    const [upperOrLower, median, other] = fork
    const midTime = (upperOrLower.a.time + other.a.time) / 2
    const midPrice = (upperOrLower.a.price + other.a.price) / 2
    const slope = (median.b.price - median.a.price) / (median.b.time - median.a.time)
    expect(median.a.price + slope * (midTime - median.a.time)).toBeCloseTo(midPrice, 10)
  })

  it('does not count flat candles as repeated support or resistance', () => {
    const cs = trendingCandles(1).map(c => ({ ...c, open: 100, close: 100, high: 101, low: 99 }))
    const a = analyzeChart(cs)
    expect(a.supports).toEqual([])
    expect(a.resistances).toEqual([])
    expect(a.trend).toBeUndefined()
  })
})

it('filters historical levels far from the current trading range', () => {
  const cs: Candle[] = Array.from({ length: 160 }, (_, i) => {
    const close = (i < 70 ? 50 : 100) + Math.sin(i / 3)
    return { time: 1700000000 + i * 900, open: close, close, high: close + 0.3, low: close - 0.3, volume: 1 }
  })
  const a = analyzeChart(cs)
  expect(a.supports.length).toBeGreaterThan(0)
  expect(a.supports.every(s => s.price > 85)).toBe(true)
})

it('validates falling resistance trends as well as rising support', () => {
  const trend = analyzeChart(trendingCandles(-1)).trend
  expect(trend).toBeDefined()
  expect(trend!.b.price).toBeLessThan(trend!.a.price)
  expect(trend!.touches).toBeGreaterThanOrEqual(3)
})

describe('visible viewport analysis', () => {
  it('uses only the visible candles, including the historical window endpoint', () => {
    const cs = trendingCandles(1)
    const range = { from: 10.2, to: 60.8 }
    const result = analyzeVisibleChart(cs, range)!
    expect(result.from).toBe(cs[11].time)
    expect(result.to).toBe(cs[60].time)
    expect(result.analysis).toEqual(analyzeChart(cs.slice(11, 61), 2, { includeLast: true, windowSize: 50 }))
    const changed = cs.map((c, i) => i >= 11 && i <= 60 ? c : { ...c, high: 10000, low: 1 })
    expect(analyzeVisibleChart(changed, range)).toEqual(result)
  })

  it('recalculates for panning and excludes only the global forming bar', () => {
    const cs = trendingCandles(1)
    const left = analyzeVisibleChart(cs, { from: 0, to: 40 })!
    const right = analyzeVisibleChart(cs, { from: 35, to: 100 })!
    expect(left.analysis).not.toEqual(right.analysis)
    expect(right.to).toBe(cs[78].time)
    expect(right.analysis).toEqual(analyzeChart(cs.slice(35, 79), 2, { includeLast: true, windowSize: 44 }))
  })

  it('does not borrow offscreen pivots when zoomed into fewer than 30 bars', () => {
    const cs = trendingCandles(1)
    expect(analyzeVisibleChart(cs, { from: 50, to: 60 })!.analysis).toEqual({ supports: [], resistances: [], horizontals: [] })
    expect(analyzeVisibleChart(cs, null)).toBeNull()
    expect(analyzeVisibleChart(cs, { from: 100, to: 120 })).toBeNull()
  })
})
