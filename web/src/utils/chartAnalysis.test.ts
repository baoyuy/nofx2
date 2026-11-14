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
