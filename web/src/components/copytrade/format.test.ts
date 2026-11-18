import { describe, expect, it } from 'vitest'
import {
  dayKey,
  dayLabel,
  hhmm,
  marketLabel,
  price,
  signedUsd,
  staleness,
  usd,
} from './format'

describe('money formatting', () => {
  it('uses one rule, so a column never mixes an exact figure with a compact one', () => {
    expect(usd(2012)).toBe('$2.01K')
    expect(usd(62000)).toBe('$62.00K')
    expect(usd(157)).toBe('$157.00')
    expect(usd(60.73)).toBe('$60.73')
    expect(usd(-496)).toBe('-$496.00')
    expect(usd(1.5e6)).toBe('$1.50M')
  })

  it('signs a gain and a loss the same way', () => {
    expect(signedUsd(60.73)).toBe('+$60.73')
    expect(signedUsd(-496)).toBe('-$496.00')
    expect(signedUsd(0)).toBe('$0.00')
  })

  it('keeps about four significant digits whatever the magnitude', () => {
    expect(price(71.8)).toBe('71.8')
    expect(price(356.353)).toBe('356.353')
    expect(price(73692.9)).toBe('73,692.9')
    expect(price(0)).toBe('--')
  })
})

describe('event dates', () => {
  const noon = new Date(2025, 10, 15, 12, 0, 0).getTime()

  it('labels today and yesterday instead of repeating the date', () => {
    expect(dayLabel('zh', noon, noon)).toBe('今天')
    expect(dayLabel('zh', noon - 24 * 3600 * 1000, noon)).toBe('昨天')
    expect(dayLabel('zh', new Date(2025, 10, 2, 9, 0, 0).getTime(), noon)).toBe(
      '11/02'
    )
    expect(dayLabel('en', noon, noon)).toBe('Today')
  })

  it('groups by local calendar day, not by 24-hour windows', () => {
    const beforeMidnight = new Date(2025, 10, 14, 23, 55, 0).getTime()
    const afterMidnight = new Date(2025, 10, 15, 0, 5, 0).getTime()
    expect(dayKey(beforeMidnight)).not.toBe(dayKey(afterMidnight))
  })

  it('prints the time alone, because the group carries the day', () => {
    expect(hhmm(new Date(2025, 10, 15, 9, 4, 0).getTime())).toBe('09:04')
    expect(hhmm(0)).toBe('--')
  })
})

describe('data freshness', () => {
  const now = 1_700_000_000_000

  it('treats a couple of minutes as normal, an hour as a gap', () => {
    expect(staleness(now - 2 * 60_000, now).level).toBe('ok')
    expect(staleness(now - 20 * 60_000, now).level).toBe('warn')
    expect(staleness(now - 61 * 60_000, now).level).toBe('bad')
    expect(staleness(0, now).level).toBe('bad')
  })
})

describe('market labels', () => {
  it('marks stock and commodity contracts only', () => {
    expect(marketLabel('zh', true)).toBe('股票/商品')
    expect(marketLabel('en', true)).toBe('Stock/commodity')
    expect(marketLabel('zh', false)).toBe('')
  })
})
