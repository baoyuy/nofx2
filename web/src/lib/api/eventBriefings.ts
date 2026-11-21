import { API_BASE, getAuthHeaders, handleJSONResponse } from './helpers'

export interface EventLabelSettings {
  model_id: string
  auto_label: boolean
}
export async function getEventLabelSettings(): Promise<EventLabelSettings> {
  const response = await fetch(`${API_BASE}/events/settings`, {
    headers: getAuthHeaders(),
    cache: 'no-store',
  })
  return handleJSONResponse<EventLabelSettings>(response)
}
export async function saveEventLabelSettings(
  settings: EventLabelSettings
): Promise<EventLabelSettings> {
  const response = await fetch(`${API_BASE}/events/settings`, {
    method: 'PUT',
    headers: getAuthHeaders(),
    cache: 'no-store',
    body: JSON.stringify(settings),
  })
  return handleJSONResponse<EventLabelSettings>(response)
}
export type BriefingStance =
  | 'favorable'
  | 'unfavorable'
  | 'mixed'
  | 'unconfirmed'
  | 'low_relevance'
export interface EventBriefing {
  model_id: string
  model_name: string
  input_hash: string
  analyzed_at: string
  stale: boolean
  cached: boolean
  coverage: {
    total_events: number
    included_events: number
    omitted_events: number
    coins: string[]
  }
  warnings?: string[]
  briefings: {
    coin: string
    summary: string
    groups: {
      stance: BriefingStance
      title: string
      reason: string
      conditions: string[]
      limitations: string[]
      evidence_ids: string[]
    }[]
    watch_points: string[]
  }[]
}
export type BriefingResponse = EventBriefing | { status: 'empty' }
export async function getEventBriefing(
  modelId: string
): Promise<BriefingResponse> {
  const response = await fetch(
    `${API_BASE}/events/briefing?model_id=${encodeURIComponent(modelId)}`,
    { headers: getAuthHeaders(), cache: 'no-store' }
  )
  return handleJSONResponse<BriefingResponse>(response)
}
export async function generateEventBriefing(
  modelId: string
): Promise<BriefingResponse> {
  const response = await fetch(`${API_BASE}/events/briefing`, {
    method: 'POST',
    headers: getAuthHeaders(),
    cache: 'no-store',
    body: JSON.stringify({ model_id: modelId }),
  })
  return handleJSONResponse<EventBriefing>(response)
}
