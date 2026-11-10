import { readFileSync } from 'node:fs'
import { expect, it } from 'vitest'
import { chartQuery } from './planningChart'

it.each([
  ['okx', 'BTC-USDT-SWAP'],
  ['bitget', 'BTCUSDT'],
  ['binance', 'BTCUSDT'],
  ['hyperliquid', 'xyz:GOOGL'],
])('routes %s %s chart history to its own venue', (venue, symbol) => {
  const query = new URLSearchParams(chartQuery(venue, symbol, '60'))
  expect(query.get('venue')).toBe(venue)
  expect(query.get('symbol')).toBe(symbol)
  expect(query.get('interval')).toBe('1h')
})

it('rejects unsupported venues, path-like symbols and timeframes before fetching', () => {
  expect(() => chartQuery('unsupported', 'BTCUSDT', '60')).toThrow(
    'Invalid symbol format'
  )
  expect(() => chartQuery('bitget', '../secret', '60')).toThrow(
    'Invalid symbol format'
  )
  expect(() => chartQuery('binance', 'BTCUSDT', '7')).toThrow(
    'Unsupported chart timeframe'
  )
})

it('uses the open-source chart library instead of the licensed TradingView widget', () => {
  const source = readFileSync('src/lib/planningChart.ts', 'utf8')
  expect(source).toContain("from 'lightweight-charts'")
  expect(source).not.toMatch(/charting_library|TradingView\.widget/)
})
