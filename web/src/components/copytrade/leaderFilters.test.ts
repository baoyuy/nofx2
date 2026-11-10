import { describe, expect, it } from 'vitest'
import type { Leader } from '../../lib/api/copyTrade'
import {
  activeFilters,
  defaultFilters,
  filterLeaders,
  parseFilters,
  sortLeaders,
} from './leaderFilters'

const DAY = 24 * 3600 * 1000
const now = 1_800_000_000_000

const leader = (p: Partial<Leader>): Leader => ({
  leadPortfolioId: 'x',
  nickname: 'X',
  avatarUrl: '',
  roi: 50,
  pnl: 5000,
  mdd: 15,
  winRate: 60,
  sharpRatio: 1.5,
  aum: 200000,
  copierPnl: 2000,
  currentCopyCount: 100,
  maxCopyCount: 200,
  apiKeyTag: '',
  tradFiTag: '',
  startTime: now - 120 * DAY,
  chartItems: null,
  ...p,
})

describe('leader filters', () => {
  const list = [
    leader({ leadPortfolioId: 'steady' }),
    leader({ leadPortfolioId: 'wild', roi: 900, mdd: 60, apiKeyTag: 'API' }),
    leader({
      leadPortfolioId: 'full',
      currentCopyCount: 200,
      tradFiTag: 'TRADFI',
    }),
    leader({ leadPortfolioId: 'new', startTime: now - 10 * DAY, roi: -5 }),
    leader({ leadPortfolioId: 'unknown-age', startTime: 0 }),
  ]
  const ids = (
    f: Partial<typeof defaultFilters>,
    followed = new Set<string>()
  ) =>
    filterLeaders(list, { ...defaultFilters, ...f }, followed, now).map(
      (r) => r.leadPortfolioId
    )

  it('passes everyone with no filters', () => {
    expect(ids({})).toHaveLength(5)
  })

  it('combines filters', () => {
    expect(ids({ maxMdd: 30, minRoi: 0 })).toEqual([
      'steady',
      'full',
      'unknown-age',
    ])
    expect(ids({ maxMdd: 30, openSlots: true, market: 'crypto' })).toEqual([
      'steady',
      'new',
      'unknown-age',
    ])
    expect(ids({ mode: 'api' })).toEqual(['wild'])
    expect(ids({ market: 'stocks' })).toEqual(['full'])
  })

  it('needs a known start time for a tenure minimum', () => {
    expect(ids({ minDays: 90 })).toEqual(['steady', 'wild', 'full'])
  })

  it('filters by follow state', () => {
    const followed = new Set(['steady', 'wild'])
    expect(ids({ followState: 'unfollowed' }, followed)).toEqual([
      'full',
      'new',
      'unknown-age',
    ])
    expect(ids({ followState: 'followed' }, followed)).toEqual([
      'steady',
      'wild',
    ])
  })

  it('treats a zero threshold as a real bound, not "any"', () => {
    expect(ids({ minRoi: 0 })).not.toContain('new')
  })

  it('sorts by a column and keeps Binance order otherwise', () => {
    expect(sortLeaders(list, 'binance', true, now)).toBe(list)
    expect(
      sortLeaders(list, 'mdd', false, now).map((r) => r.leadPortfolioId)[4]
    ).toBe('wild')
    expect(
      sortLeaders(list, 'days', true, now).map((r) => r.leadPortfolioId)
    ).toEqual(['steady', 'wild', 'full', 'new', 'unknown-age'])
  })

  it('counts active filters and drops bad saved values', () => {
    expect(activeFilters(defaultFilters)).toBe(0)
    expect(
      activeFilters({ ...defaultFilters, minWin: 50, openSlots: true })
    ).toBe(2)
    expect(
      parseFilters({ minWin: 60, maxMdd: 'x', market: 'moon', mode: 'api' })
    ).toEqual({ ...defaultFilters, minWin: 60, mode: 'api' })
    expect(parseFilters(null)).toEqual(defaultFilters)
  })
})
