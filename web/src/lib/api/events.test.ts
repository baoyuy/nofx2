import { afterEach, describe, expect, it, vi } from 'vitest'
import { getEvents, labelEvent, type EventsResponse } from './events'

vi.mock('./helpers', () => ({
  API_BASE: '/api',
  getAuthHeaders: () => ({ Authorization: 'Bearer session' }),
  handleJSONResponse: async (response: Response) => {
    if (!response.ok) throw new Error('Request failed')
    return response.json()
  },
}))

afterEach(() => vi.unstubAllGlobals())

describe('getEvents', () => {
  it('fetches the authenticated board without caching and preserves prediction prices and deadlines', async () => {
    const board: EventsResponse = {
      generated_at: '2026-06-01T10:00:00Z',
      running_traders: 1,
      watches: [],
      warnings: [],
      events: [
        {
          id: '1',
          title: 'Bitcoin above $100k?',
          source: 'Kalshi',
          url: 'https://kalshi.com/markets/btc',
          published_at: '2026-05-01T10:00:00Z',
          received_at: '2026-06-01T10:00:00Z',
          end_at: '2026-12-31T23:59:00Z',
          coins: ['BTC'],
          traders: [],
          markets: [
            {
              id: 'm1',
              question: 'Above $100k?',
              outcomes: [{ name: 'Yes', price: 0.62 }],
              price_basis: 'Last traded price',
              change_24h: -0.03,
            },
          ],
        },
      ],
    }
    const fetchMock = vi
      .fn()
      .mockResolvedValue(new Response(JSON.stringify(board)))
    vi.stubGlobal('fetch', fetchMock)
    expect(await getEvents()).toEqual(board)
    expect(fetchMock).toHaveBeenCalledWith('/api/events', {
      headers: { Authorization: 'Bearer session' },
      cache: 'no-store',
    })
  })

  it('encodes explicit history settings in the request', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}'))
    vi.stubGlobal('fetch', fetchMock)
    await getEvents({ windowMinutes: 15, thresholdPP: 10, endingHours: 6 })
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/events?window_minutes=15&threshold_pp=10&ending_hours=6',
      {
        headers: { Authorization: 'Bearer session' },
        cache: 'no-store',
      }
    )
  })

  it('reads labels without invoking analysis and posts only identifiers on explicit labeling', async () => {
    const fetchMock = vi.fn().mockImplementation(async () => new Response('{}'))
    vi.stubGlobal('fetch', fetchMock)
    await getEvents({ labelModelId: 'model/a' })
    expect(fetchMock).toHaveBeenLastCalledWith(
      '/api/events?label_model_id=model%2Fa',
      { headers: { Authorization: 'Bearer session' }, cache: 'no-store' }
    )
    await labelEvent('polymarket:123', 'model/a')
    expect(fetchMock).toHaveBeenLastCalledWith('/api/events/labels', {
      method: 'POST',
      headers: { Authorization: 'Bearer session' },
      cache: 'no-store',
      body: JSON.stringify({ event_id: 'polymarket:123', model_id: 'model/a' }),
    })
  })

  it('propagates request errors', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response('', { status: 503 }))
    )
    await expect(getEvents()).rejects.toThrow('Request failed')
  })
})
