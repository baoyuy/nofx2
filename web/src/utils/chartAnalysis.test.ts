import { describe, expect, it } from 'vitest'
import { analyzeChart } from './chartAnalysis'
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
