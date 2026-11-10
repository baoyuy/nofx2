// Indicator math for the market page, using Binance app default periods.
// Every function returns points aligned to candle time; warm-up bars are
// omitted so the chart does not draw misleading early values.

export interface Candle {
  time: number
  open: number
  high: number
  low: number
  close: number
  volume: number
}

export interface Point {
  time: number
  value: number
}

export function sma(
  data: Candle[],
  period: number,
  pick = (c: Candle) => c.close
): Point[] {
  const out: Point[] = []
  let sum = 0
  for (let i = 0; i < data.length; i++) {
    sum += pick(data[i])
    if (i >= period) sum -= pick(data[i - period])
    if (i >= period - 1) out.push({ time: data[i].time, value: sum / period })
  }
  return out
}

function emaValues(values: number[], period: number): number[] {
  const k = 2 / (period + 1)
  const out: number[] = []
  values.forEach((v, i) => out.push(i === 0 ? v : v * k + out[i - 1] * (1 - k)))
  return out
}

export function ema(data: Candle[], period: number): Point[] {
  const values = emaValues(
    data.map((c) => c.close),
    period
  )
  return data
    .slice(period - 1)
    .map((c, i) => ({ time: c.time, value: values[i + period - 1] }))
}

export interface Boll {
  upper: Point[]
  middle: Point[]
  lower: Point[]
}

export function boll(data: Candle[], period = 20, mult = 2): Boll {
  const result: Boll = { upper: [], middle: [], lower: [] }
  for (let i = period - 1; i < data.length; i++) {
    let sum = 0
    for (let j = i - period + 1; j <= i; j++) sum += data[j].close
    const mid = sum / period
    let variance = 0
    for (let j = i - period + 1; j <= i; j++)
      variance += (data[j].close - mid) ** 2
    const sd = Math.sqrt(variance / period)
    const time = data[i].time
    result.middle.push({ time, value: mid })
    result.upper.push({ time, value: mid + mult * sd })
    result.lower.push({ time, value: mid - mult * sd })
  }
  return result
}

export interface Macd {
  dif: Point[]
  dea: Point[]
  hist: Point[]
}

// MACD(12,26,9); histogram is 2*(DIF-DEA) like the Binance app.
export function macd(data: Candle[], fast = 12, slow = 26, signal = 9): Macd {
  const closes = data.map((c) => c.close)
  const f = emaValues(closes, fast)
  const s = emaValues(closes, slow)
  const dif = closes.map((_, i) => f[i] - s[i])
  const dea = emaValues(dif, signal)
  const result: Macd = { dif: [], dea: [], hist: [] }
  for (let i = slow - 1; i < data.length; i++) {
    const time = data[i].time
    result.dif.push({ time, value: dif[i] })
    result.dea.push({ time, value: dea[i] })
    result.hist.push({ time, value: 2 * (dif[i] - dea[i]) })
  }
  return result
}

// Wilder RSI.
export function rsi(data: Candle[], period: number): Point[] {
  const out: Point[] = []
  if (data.length <= period) return out
  let gain = 0
  let loss = 0
  for (let i = 1; i <= period; i++) {
    const d = data[i].close - data[i - 1].close
    if (d > 0) gain += d
    else loss -= d
  }
  gain /= period
  loss /= period
  const push = (i: number) =>
    out.push({
      time: data[i].time,
      value: loss === 0 ? 100 : 100 - 100 / (1 + gain / loss),
    })
  push(period)
  for (let i = period + 1; i < data.length; i++) {
    const d = data[i].close - data[i - 1].close
    gain = (gain * (period - 1) + Math.max(d, 0)) / period
    loss = (loss * (period - 1) + Math.max(-d, 0)) / period
    push(i)
  }
  return out
}

export interface Kdj {
  k: Point[]
  d: Point[]
  j: Point[]
}

// KDJ(9,3,3).
export function kdj(data: Candle[], period = 9): Kdj {
  const result: Kdj = { k: [], d: [], j: [] }
  let k = 50
  let d = 50
  for (let i = period - 1; i < data.length; i++) {
    let hi = -Infinity
    let lo = Infinity
    for (let j = i - period + 1; j <= i; j++) {
      hi = Math.max(hi, data[j].high)
      lo = Math.min(lo, data[j].low)
    }
    const rsv = hi > lo ? ((data[i].close - lo) / (hi - lo)) * 100 : 50
    k = (2 * k + rsv) / 3
    d = (2 * d + k) / 3
    const time = data[i].time
    result.k.push({ time, value: k })
    result.d.push({ time, value: d })
    result.j.push({ time, value: 3 * k - 2 * d })
  }
  return result
}

// Price precision for display, from the magnitude of the price.
export function priceDecimals(price: number): number {
  if (price >= 1000) return 2
  if (price >= 10) return 3
  if (price >= 1) return 4
  if (price >= 0.01) return 5
  return 7
}

export function formatCompact(v: number): string {
  const abs = Math.abs(v)
  if (abs >= 1e9) return (v / 1e9).toFixed(2) + 'B'
  if (abs >= 1e6) return (v / 1e6).toFixed(2) + 'M'
  if (abs >= 1e3) return (v / 1e3).toFixed(2) + 'K'
  return v.toFixed(2)
}
