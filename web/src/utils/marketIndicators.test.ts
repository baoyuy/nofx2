import { describe, expect, it } from 'vitest'
import { boll, kdj, macd, priceDecimals, rsi, sma, type Candle } from './marketIndicators'

const rising: Candle[] = Array.from({ length: 120 }, (_, i) => ({
  time: i * 60,
  open: 100 + i - 0.5,
  high: 100 + i + 1,
  low: 100 + i - 1,
  close: 100 + i,
  volume: 10 + i,
}))

describe('marketIndicators', () => {
  it('sma skips warm-up bars and averages correctly', () => {
    const out = sma(rising, 7)
    expect(out).toHaveLength(rising.length - 6)
    expect(out[0]).toEqual({ time: rising[6].time, value: 103 })
  })

  it('volume sma uses the picked field', () => {
    expect(sma(rising, 5, (c) => c.volume)[0].value).toBe(12)
  })

  it('macd is positive on an uptrend and histogram is 2*(DIF-DEA)', () => {
    const m = macd(rising)
    const i = m.dif.length - 1
    expect(m.dif[i].value).toBeGreaterThan(0)
    expect(m.hist[i].value).toBeCloseTo(2 * (m.dif[i].value - m.dea[i].value))
  })

  it('rsi is 100 on a strictly rising series', () => {
    expect(rsi(rising, 14).at(-1)?.value).toBe(100)
  })

  it('kdj K stays high on an uptrend', () => {
    expect(kdj(rising).k.at(-1)!.value).toBeGreaterThan(80)
  })

  it('boll bands bracket the middle', () => {
    const b = boll(rising)
    expect(b.upper.at(-1)!.value).toBeGreaterThan(b.middle.at(-1)!.value)
    expect(b.lower.at(-1)!.value).toBeLessThan(b.middle.at(-1)!.value)
  })

  it('priceDecimals scales with price', () => {
    expect(priceDecimals(80000)).toBe(2)
    expect(priceDecimals(2.5)).toBe(4)
    expect(priceDecimals(0.00012)).toBe(7)
  })
})
