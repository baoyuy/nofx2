import { API_BASE, httpClient } from './helpers'

// Explosive board: Binance USDT-M futures top gainers scored for fresh
// breakouts. Read-only public market data.

export type ExplosiveState =
  | 'starting'
  | 'accelerating'
  | 'overheated'
  | 'fading'
  | 'falling'

export interface ExplosiveRow {
  symbol: string
  price: number
  change_24h: number
  change_5m: number
  change_15m: number
  volume_24h: number
  vol_ratio: number
  oi_change_1h: number
  funding: number
  atr_pct: number
  distance_high: number
  ema20: number
  ema60: number
  ema120: number
  /** momentum, 0-100, before risk discounts: the board is sorted by this */
  strength: number
  /** product of the risk discounts, 1 = none */
  risk: number
  /** strength × risk */
  score: number
  /** 0-1 parts of strength */
  speed: number
  volume: number
  open_interest: number
  structure: number
  rel_strength: number
  state: ExplosiveState
  tags: string[]
}

export interface ExplosiveBoard {
  rows: ExplosiveRow[]
  scanned: number
  candidates: number
  fetched_at: string
  /** age of the candle/open-interest data, seconds */
  age_seconds?: number
  /** the slow data is older than its refresh cycle */
  stale?: boolean
  /** a background refresh is running */
  refreshing?: boolean
  reason?: string
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

export const explosiveApi = {
  async board(refresh = false): Promise<ExplosiveBoard> {
    const r = await httpClient.request<ExplosiveBoard>(
      `${API_BASE}/market/explosive`,
      {
        params: refresh ? { refresh: '1' } : undefined,
        silent: true,
        timeout: 60000,
      }
    )
    return unwrap(r, 'Screening failed')
  },
}
