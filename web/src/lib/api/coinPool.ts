import { API_BASE, httpClient } from './helpers'

// Coin pool page: screeners over Binance USDT-M perpetuals and saved pools
// that strategies can trade (coin source "coin_pool").

export type HornStage = 'starting' | 'opening' | 'mature' | 'closing' | 'none'
export type PoolDirection = 'long' | 'short' | 'neutral'

export interface HornRow {
  symbol: string
  price: number
  change_pct: number
  quote_volume: number
  match: boolean
  direction: PoolDirection
  score: number
  stage: HornStage
  expansion: number
  squeeze_pctile: number
  percent_b: number
  bars_since: number
  bandwidth: number
  /** Consecutive closed candles the match has held (1 = joined with the latest candle). */
  matched_bars?: number
  /** When the first candle of the current match closed (ISO time). */
  matched_since?: string
  error?: string
}

/**
 * How long a horn match has been on the board, e.g. "新上榜 · 15 分钟前" or
 * "已 3 根 · 2 小时前". Empty for rows that do not match.
 */
export function hornOnBoard(
  row: Pick<HornRow, 'matched_bars' | 'matched_since'>,
  zh: boolean,
  now: number = Date.now()
): { label: string; isNew: boolean } {
  if (!row.matched_bars || !row.matched_since) return { label: '', isNew: false }
  const mins = Math.max(0, Math.round((now - Date.parse(row.matched_since)) / 60000))
  const ago =
    mins < 60
      ? zh ? `${mins} 分钟前` : `${mins}m ago`
      : mins < 48 * 60
        ? zh ? `${Math.round(mins / 60)} 小时前` : `${Math.round(mins / 60)}h ago`
        : zh ? `${Math.round(mins / 1440)} 天前` : `${Math.round(mins / 1440)}d ago`
  const isNew = row.matched_bars === 1
  const bars = isNew
    ? zh ? '新上榜' : 'New'
    : zh ? `已 ${row.matched_bars} 根` : `${row.matched_bars} bars`
  return { label: `${bars} · ${ago}`, isNew }
}

export interface HornBoard {
  interval: string
  rows: HornRow[]
  scanned: number
  matched: number
  fetched_at: string
}

export interface CoinPoolCoin {
  symbol: string
  direction?: PoolDirection
  score?: number
  note?: string
}

export interface CoinPool {
  id: string
  name: string
  screener: string
  interval: string
  coins: CoinPoolCoin[]
  created_at: string
  updated_at: string
}

export interface CoinPoolInput {
  name: string
  screener: string
  interval: string
  coins: CoinPoolCoin[]
}

function unwrap<T>(
  result: { success: boolean; data?: T; message?: string },
  fallback: string
): T {
  if (!result.success || result.data === undefined) {
    throw new Error(result.message || fallback)
  }
  return result.data
}

export const coinPoolApi = {
  async screenBollinger(interval: string, refresh = false): Promise<HornBoard> {
    const r = await httpClient.request<HornBoard>(
      `${API_BASE}/coin-pool/screen/bollinger`,
      {
        params: { interval, ...(refresh ? { refresh: '1' } : {}) },
        silent: true,
        timeout: 60000,
      }
    )
    return unwrap(r, 'Screening failed')
  },

  async getCoinPools(): Promise<CoinPool[]> {
    const r = await httpClient.request<{ pools: CoinPool[] }>(
      `${API_BASE}/coin-pools`,
      { silent: true }
    )
    return unwrap(r, 'Failed to load coin pools').pools || []
  },

  async createCoinPool(pool: CoinPoolInput): Promise<CoinPool> {
    const r = await httpClient.request<CoinPool>(`${API_BASE}/coin-pools`, {
      method: 'POST',
      data: pool,
    })
    return unwrap(r, 'Failed to save coin pool')
  },

  async updateCoinPool(id: string, pool: CoinPoolInput): Promise<CoinPool> {
    const r = await httpClient.request<CoinPool>(
      `${API_BASE}/coin-pools/${encodeURIComponent(id)}`,
      { method: 'PUT', data: pool }
    )
    return unwrap(r, 'Failed to update coin pool')
  },

  async deleteCoinPool(id: string): Promise<void> {
    const r = await httpClient.request<{ ok: boolean }>(
      `${API_BASE}/coin-pools/${encodeURIComponent(id)}`,
      { method: 'DELETE' }
    )
    unwrap(r, 'Failed to delete coin pool')
  },
}
