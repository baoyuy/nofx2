import { API_BASE, getAuthHeaders, handleJSONResponse } from './helpers'

export interface EventTraderRef {
  trader_id: string
  trader_name: string
  strategy_id: string
  strategy_name: string
}

export interface EventHistoryOptions {
  windowMinutes?: 15 | 60 | 360
  thresholdPP?: 1 | 5 | 10
  endingHours?: 6 | 24 | 72
  labelModelId?: string
}

export interface EventPriceChange {
  outcome: string
  previous_price: number
  current_price: number
  delta_pp: number
  from_at: string
  to_at: string
  significant: boolean
}

export interface EventMarket {
  id: string
  question: string
  outcomes: { name: string; price: number }[]
  price_basis: string
  change_24h?: number
  changes?: EventPriceChange[]
  history_status?: 'available' | 'insufficient' | 'stale' | 'unavailable'
}

export interface EventLabel {
  model_id: string
  model_name: string
  input_hash: string
  analyzed_at: string
  stale: boolean
  cached: boolean
  analysis: {
    event_type:
      | 'price_target'
      | 'regulation'
      | 'etf'
      | 'protocol'
      | 'security'
      | 'adoption'
      | 'other'
    summary: string
    associations: {
      coin: string
      relation: 'direct' | 'indirect' | 'insufficient'
      reason: string
    }[]
    attention_reason: string
    impact_conditions: string[]
    limitations: string[]
    evidence_ids: string[]
  }
}

export interface EventsResponse {
  generated_at: string
  running_traders: number
  watches: {
    symbol: string
    coin: string
    traders: EventTraderRef[]
  }[]
  events: {
    id: string
    title: string
    url: string
    source: 'Polymarket' | 'Kalshi' | 'Free Crypto News' | 'Hacker News'
    kind?: 'prediction' | 'news' | 'community'
    publisher?: string
    summary?: string
    discussion_url?: string
    score?: number
    comments?: number
    article_type?: 'report' | 'opinion' | 'discussion'
    published_at: string
    received_at: string
    end_at?: string
    first_seen_at?: string
    is_new?: boolean
    ending_soon?: boolean
    significant_change?: boolean
    markets: EventMarket[]
    coins: string[]
    traders: EventTraderRef[]
    label?: EventLabel
  }[]
  warnings: string[]
}

export async function getEvents(
  options?: EventHistoryOptions
): Promise<EventsResponse> {
  const params = new URLSearchParams()
  if (options?.windowMinutes !== undefined)
    params.set('window_minutes', String(options.windowMinutes))
  if (options?.thresholdPP !== undefined)
    params.set('threshold_pp', String(options.thresholdPP))
  if (options?.endingHours !== undefined)
    params.set('ending_hours', String(options.endingHours))
  if (options?.labelModelId) params.set('label_model_id', options.labelModelId)
  const query = params.size ? `?${params}` : ''
  const response = await fetch(`${API_BASE}/events${query}`, {
    headers: getAuthHeaders(),
    cache: 'no-store',
  })
  return handleJSONResponse<EventsResponse>(response)
}

export async function labelEvent(
  eventId: string,
  modelId: string,
  options?: EventHistoryOptions
): Promise<EventLabel> {
  const params = new URLSearchParams()
  if (options?.windowMinutes !== undefined)
    params.set('window_minutes', String(options.windowMinutes))
  if (options?.thresholdPP !== undefined)
    params.set('threshold_pp', String(options.thresholdPP))
  if (options?.endingHours !== undefined)
    params.set('ending_hours', String(options.endingHours))
  const query = params.size ? `?${params}` : ''
  const response = await fetch(`${API_BASE}/events/labels${query}`, {
    method: 'POST',
    headers: getAuthHeaders(),
    cache: 'no-store',
    body: JSON.stringify({ event_id: eventId, model_id: modelId }),
  })
  return handleJSONResponse<EventLabel>(response)
}

// Two-model trade stance per coin, produced by the trader cycle and shown here
// so tagging is visible without opening the trading dashboard.
export interface EventTradeTag {
  user_id: string
  trader_id: string
  coin: string
  bias: 'bullish' | 'bearish' | 'neutral' | string
  confidence: number
  reason: string
  tagged_at: string
}

export async function getEventTradeTags(): Promise<{
  tags: EventTradeTag[]
}> {
  const response = await fetch(`${API_BASE}/events/trade-tags`, {
    headers: getAuthHeaders(),
    cache: 'no-store',
  })
  return handleJSONResponse<{ tags: EventTradeTag[] }>(response)
}

export interface EventCard {
  user_id: string
  trader_id: string
  coin: string
  card_id: string
  type: string
  title: string
  summary: string
  direction: 'bullish' | 'bearish' | 'neutral' | string
  surprise: 'above' | 'below' | 'inline' | 'unknown' | string
  importance: number
  is_node: boolean
  node_at?: string
  valid_until?: string
  source: string
  reason: string
  model_id: string
  tagged_at: string
}

export async function getEventCards(
  traderId?: string
): Promise<{ cards: EventCard[] }> {
  const q = traderId ? `?trader_id=${encodeURIComponent(traderId)}` : ''
  const response = await fetch(`${API_BASE}/events/cards${q}`, {
    headers: getAuthHeaders(),
    cache: 'no-store',
  })
  return handleJSONResponse<{ cards: EventCard[] }>(response)
}

export interface EventCardStats {
  total: number
  pending: number
  hit: number
  miss: number
  neutral: number
  skipped: number
}

export async function getEventCardStats(): Promise<EventCardStats> {
  const response = await fetch(`${API_BASE}/events/card-stats`, {
    headers: getAuthHeaders(),
    cache: 'no-store',
  })
  return handleJSONResponse<EventCardStats>(response)
}
