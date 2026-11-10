import { API_BASE, httpClient } from './helpers'

// Copy trading: another Binance account (real or paper) of the user copies
// an AI trader's positions. Backend: mirror package.

export type MirrorSizeMode = 'fixed' | 'ratio'
export type MirrorLeverageMode = 'follow' | 'fixed'

export interface MirrorSettings {
  size_mode: MirrorSizeMode
  fixed_margin: number
  ratio: number
  leverage_mode: MirrorLeverageMode
  leverage: number
  max_leverage: number
  follow_protect: boolean
  max_margin: number
  max_loss: number
}

export interface MirrorAccount {
  id: string
  account_name: string
  paper: boolean
  /** AI traders configured on this account. */
  used_by: string[]
}

export interface MirrorPosition {
  symbol: string
  side: 'long' | 'short'
  leader_qty: number
  follower_qty: number
  skip?: 'before_start' | 'open_failed' | 'follower_closed'
  stop_loss?: number
  take_profit?: number
  opened_at: number
}

export interface MirrorHolding {
  symbol: string
  side: 'long' | 'short'
  qty: number
  entry: number
  mark: number
  leverage: number
  pnl: number
}

export interface MirrorStatus {
  equity: number
  available: number
  pnl: number
  error?: string
  updated_at: string
  positions: MirrorHolding[]
}

export interface MirrorLink extends MirrorSettings {
  id: string
  leader_id: string
  exchange_id: string
  running: boolean
  start_equity: number
  stop_reason: string
  created_at: string
  account_name: string
  paper: boolean
  /** The engine runs it now. */
  active: boolean
  status?: MirrorStatus
  positions: MirrorPosition[]
}

export interface MirrorEvent {
  id: number
  time: number
  action: string
  symbol?: string
  side?: string
  qty?: number
  price?: number
  detail?: string
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

const send = async <T>(
  method: 'GET' | 'POST' | 'PUT' | 'DELETE',
  path: string,
  body?: unknown,
  params?: Record<string, unknown>
) =>
  unwrap(
    await httpClient.request<T>(`${API_BASE}${path}`, {
      method,
      data: body,
      params,
      silent: true,
      timeout: 60000,
    }),
    'Request failed'
  )

const linkPath = (id: string) => `/mirror/links/${encodeURIComponent(id)}`

export const mirrorApi = {
  getMirrorAccounts: async () =>
    (await send<{ accounts: MirrorAccount[] }>('GET', '/mirror/accounts'))
      .accounts,

  getMirrorLinks: async (leaderId: string) =>
    (
      await send<{ links: MirrorLink[] }>('GET', '/mirror/links', undefined, {
        leader_id: leaderId,
      })
    ).links,

  createMirrorLink: async (
    req: MirrorSettings & {
      leader_id: string
      exchange_id: string
      confirm_real: boolean
    }
  ) => (await send<{ link: MirrorLink }>('POST', '/mirror/links', req)).link,

  updateMirrorLink: async (id: string, s: MirrorSettings) =>
    (await send<{ link: MirrorLink }>('PUT', linkPath(id), s)).link,

  startMirrorLink: async (id: string, confirmReal: boolean) =>
    (
      await send<{ link: MirrorLink }>('POST', `${linkPath(id)}/start`, {
        confirm_real: confirmReal,
      })
    ).link,

  stopMirrorLink: (id: string, closePositions: boolean) =>
    send<{ link: MirrorLink; close_failed?: string[] }>(
      'POST',
      `${linkPath(id)}/stop`,
      { close_positions: closePositions }
    ),

  deleteMirrorLink: (id: string) =>
    send<{ ok: boolean }>('DELETE', linkPath(id)),

  getMirrorEvents: async (id: string) =>
    (await send<{ events: MirrorEvent[] }>('GET', `${linkPath(id)}/events`))
      .events,
}
