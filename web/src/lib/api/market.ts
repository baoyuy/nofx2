import { API_BASE, httpClient } from './helpers'

// Binance USDT-M market page: public data, AI analysis, manual orders.

export interface MarketKline {
  time: number // open time, unix seconds
  open: number
  high: number
  low: number
  close: number
  volume: number
}

export interface MarketTicker {
  symbol: string
  last_price: number
  mark_price: number
  price_change: number
  price_change_pct: number
  high_24h: number
  low_24h: number
  volume_24h: number
  quote_volume_24h: number
  funding_rate: number
  next_funding_time: number
}

export interface MarketTickerLite {
  symbol: string
  last_price: number
  price_change_pct: number
  quote_volume_24h: number
}

export interface MarketAnalysis {
  signal: 'long' | 'short' | 'wait'
  confidence: number
  entry: number
  stop_loss: number
  take_profit: number
  leverage: number
  summary: string
  reasons: string[]
  risks: string[]
}

export interface MarketAnalyzeResult {
  symbol: string
  interval: string
  price: number
  analyzed_at: number
  raw: string
  analysis?: MarketAnalysis
  parse_error?: string
  save_error?: string
}

export interface MarketPosition {
  symbol: string
  side: 'long' | 'short'
  positionAmt: number
  entryPrice: number
  markPrice: number
  unRealizedProfit: number
  leverage: number
  liquidationPrice: number
}

export interface MarketAccount {
  exchange_type: string
  paper: boolean
  balance: Record<string, number | boolean>
  positions: MarketPosition[]
}

export interface MarketOrderRequest {
  exchange_id: string
  symbol: string
  side: 'long' | 'short'
  margin_usdt: number
  leverage: number
  stop_loss?: number
  take_profit?: number
  confirm_live?: boolean
}

export interface MarketOrderResult {
  symbol: string
  quantity: number
  price: number
  paper: boolean
  warnings?: string[]
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

export const marketApi = {
  async getMarketKlines(
    symbol: string,
    interval: string,
    limit = 500
  ): Promise<MarketKline[]> {
    const r = await httpClient.request<MarketKline[]>(
      `${API_BASE}/market/klines`,
      {
        params: { symbol, interval, limit },
        silent: true,
      }
    )
    return unwrap(r, 'Failed to load candles')
  },

  async getMarketTicker(symbol: string): Promise<MarketTicker> {
    const r = await httpClient.request<MarketTicker>(
      `${API_BASE}/market/ticker`,
      {
        params: { symbol },
        silent: true,
      }
    )
    return unwrap(r, 'Failed to load ticker')
  },

  async getMarketSymbols(): Promise<string[]> {
    const r = await httpClient.get<{ symbols: string[] }>(
      `${API_BASE}/market/symbols`
    )
    return unwrap(r, 'Failed to load symbols').symbols || []
  },

  async getMarketTickers(): Promise<MarketTickerLite[]> {
    const r = await httpClient.request<{ tickers: MarketTickerLite[] }>(
      `${API_BASE}/market/tickers`,
      { silent: true }
    )
    return unwrap(r, 'Failed to load tickers').tickers || []
  },

  // Latest saved analysis per symbol for the logged-in user.
  async getMarketAnalyses(): Promise<Record<string, MarketAnalyzeResult>> {
    const r = await httpClient.request<{
      analyses: Record<string, MarketAnalyzeResult>
    }>(`${API_BASE}/market/analyses`, { silent: true })
    return unwrap(r, 'Failed to load saved analyses').analyses || {}
  },

  async analyzeMarket(body: {
    symbol: string
    interval: string
    model_id: string
    lang: string
  }): Promise<MarketAnalyzeResult> {
    // Reasoning models can take a minute or more.
    const r = await httpClient.request<MarketAnalyzeResult>(
      `${API_BASE}/market/analyze`,
      {
        method: 'POST',
        data: body,
        timeout: 180000,
      }
    )
    return unwrap(r, 'AI analysis failed')
  },

  async getMarketAccount(exchangeId: string): Promise<MarketAccount> {
    const r = await httpClient.request<MarketAccount>(
      `${API_BASE}/market/account`,
      {
        params: { exchange_id: exchangeId },
        silent: true,
      }
    )
    return unwrap(r, 'Failed to load account')
  },

  async placeMarketOrder(body: MarketOrderRequest): Promise<MarketOrderResult> {
    const r = await httpClient.request<MarketOrderResult>(
      `${API_BASE}/market/order`,
      {
        method: 'POST',
        data: body,
        timeout: 60000,
      }
    )
    return unwrap(r, 'Order failed')
  },

  async closeMarketPosition(body: {
    exchange_id: string
    symbol: string
    side: 'long' | 'short'
    confirm_live?: boolean
  }): Promise<void> {
    const r = await httpClient.request(`${API_BASE}/market/close`, {
      method: 'POST',
      data: body,
      timeout: 60000,
    })
    unwrap(r, 'Close failed')
  },
}
