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
  /** percent gained since Beijing midnight */
  day_gain: number
  /** hours since Beijing midnight */
  elapsed_hours: number
  /** last 3h volume over the 3h before, as a fraction */
  volume_slope: number
  /** model estimate of the rest of the day's move, percent */
  predicted_remaining: number
  /** expected total gain by the day close, percent: the ambush ranking key */
  predicted_final: number
  /** already up 50%+ today */
  ran: boolean
  /** still early enough to enter */
  ambush: boolean
  /** first time this symbol appeared on the board (ms) */
  first_seen_ms: number
  /** minutes tracked since first seen */
  tracked_min: number
  /** appeared within the last ~15 minutes */
  is_new: boolean
  /** hours since today's low: the real length of the move */
  rise_h: number
  /** last hour's volume against the same hour on prior days */
  vol_spike_1h: number
  /** ignition score: volume spike x volatility */
  radar_score: number
  /** ignition watch flag */
  radar: boolean
  /** good | fair | thin */
  tradability: string
  oi_change_1h: number
  /** false when the open-interest request failed; show — rather than 0 */
  oi_known: boolean
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
  /** coins that already ran; shown apart from the ambush list */
  blasted?: ExplosiveRow[]
  /** ignition watch list, low measured hit rate */
  radar?: ExplosiveRow[]
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

export interface BandStat {
  band: string
  samples: number
  up_1h: number
  up_4h: number
  up_24h: number
  /** share that beat BTC over the same four hours */
  beat_4h: number
  avg_excess_4h: number
  avg_dd_4h: number
  /** rose over 4h without a 3% drawdown */
  sustained: number
  /** 4h return >= +20% */
  big_up: number
  /** 4h return <= -15% */
  big_down: number
}

export interface TagStat {
  tag: string
  samples: number
  up_4h: number
  sustained: number
}

export interface ExplosiveAccuracy {
  days: number
  total: number
  bands: BandStat[]
  top: BandStat[]
  tags: TagStat[]
  updated_at: string
}

export const explosiveApi = {
  async accuracy(days = 30): Promise<ExplosiveAccuracy> {
    const r = await httpClient.request<ExplosiveAccuracy>(
      `${API_BASE}/market/explosive/accuracy`,
      { params: { days }, silent: true, timeout: 20000 }
    )
    return unwrap(r, 'Failed to load accuracy')
  },

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
