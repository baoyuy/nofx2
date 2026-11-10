import type { Leader } from '../../lib/api/copyTrade'

// Filters and sorting for the leaderboard, applied to every page scanned so
// far. null means "any".

export interface LeaderFilters {
  minRoi: number | null
  minPnl: number | null
  maxMdd: number | null
  minWin: number | null
  minSharpe: number | null
  minCopierPnl: number | null
  minAum: number | null
  minDays: number | null
  minCopiers: number | null
  openSlots: boolean
  market: 'all' | 'crypto' | 'stocks'
  mode: 'all' | 'api' | 'manual'
  followState: 'all' | 'unfollowed' | 'followed'
}

export const defaultFilters: LeaderFilters = {
  minRoi: null,
  minPnl: null,
  maxMdd: null,
  minWin: null,
  minSharpe: null,
  minCopierPnl: null,
  minAum: null,
  minDays: null,
  minCopiers: null,
  openSlots: false,
  market: 'all',
  mode: 'all',
  followState: 'all',
}

export type LeaderSortKey =
  | 'binance'
  | 'roi'
  | 'pnl'
  | 'mdd'
  | 'winRate'
  | 'sharpRatio'
  | 'copierPnl'
  | 'aum'
  | 'copiers'
  | 'days'

const DAY = 24 * 3600 * 1000

/** Days leading, or null when Binance gives no start time. */
export function leaderDays(r: Leader, now: number): number | null {
  return r.startTime > 0 ? Math.max(0, (now - r.startTime) / DAY) : null
}

const atLeast = (v: number, min: number | null) => min === null || v >= min

export function filterLeaders(
  list: Leader[],
  f: LeaderFilters,
  followed: Set<string>,
  now: number
): Leader[] {
  return list.filter((r) => {
    const days = leaderDays(r, now)
    const isFollowed = followed.has(r.leadPortfolioId)
    return (
      atLeast(r.roi, f.minRoi) &&
      atLeast(r.pnl, f.minPnl) &&
      (f.maxMdd === null || r.mdd <= f.maxMdd) &&
      atLeast(r.winRate, f.minWin) &&
      atLeast(r.sharpRatio, f.minSharpe) &&
      atLeast(r.copierPnl, f.minCopierPnl) &&
      atLeast(r.aum, f.minAum) &&
      // Unknown tenure passes only when no minimum is set.
      (f.minDays === null || (days !== null && days >= f.minDays)) &&
      atLeast(r.currentCopyCount, f.minCopiers) &&
      (!f.openSlots || r.currentCopyCount < r.maxCopyCount) &&
      (f.market === 'all' ||
        (f.market === 'crypto' ? !r.tradFiTag : Boolean(r.tradFiTag))) &&
      (f.mode === 'all' ||
        (f.mode === 'api' ? Boolean(r.apiKeyTag) : !r.apiKeyTag)) &&
      (f.followState === 'all' ||
        (f.followState === 'followed' ? isFollowed : !isFollowed))
    )
  })
}

/** Sorts a copy; 'binance' keeps Binance's order. */
export function sortLeaders(
  list: Leader[],
  key: LeaderSortKey,
  desc: boolean,
  now: number
): Leader[] {
  if (key === 'binance') return list
  const value = (r: Leader): number => {
    switch (key) {
      case 'copiers':
        return r.currentCopyCount
      case 'days':
        return leaderDays(r, now) ?? -1
      default:
        return Number(r[key]) || 0
    }
  }
  return [...list].sort((a, b) =>
    desc ? value(b) - value(a) : value(a) - value(b)
  )
}

/** How many filters differ from the defaults. */
export function activeFilters(f: LeaderFilters): number {
  return (Object.keys(defaultFilters) as (keyof LeaderFilters)[]).filter(
    (k) => f[k] !== defaultFilters[k]
  ).length
}

/** Reads saved filters, keeping only known keys of the right type. */
export function parseFilters(raw: unknown): LeaderFilters {
  const out = { ...defaultFilters }
  if (!raw || typeof raw !== 'object') return out
  const src = raw as Record<string, unknown>
  const choices: Partial<Record<keyof LeaderFilters, string[]>> = {
    market: ['all', 'crypto', 'stocks'],
    mode: ['all', 'api', 'manual'],
    followState: ['all', 'unfollowed', 'followed'],
  }
  for (const k of Object.keys(defaultFilters) as (keyof LeaderFilters)[]) {
    const v = src[k]
    const d = defaultFilters[k]
    const ok =
      d === null
        ? v === null || (typeof v === 'number' && Number.isFinite(v))
        : choices[k]
          ? choices[k].includes(v as string)
          : typeof v === typeof d
    if (ok) (out as Record<string, unknown>)[k] = v
  }
  return out
}
