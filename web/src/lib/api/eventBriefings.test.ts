import { afterEach, expect, it, vi } from 'vitest'
import { generateEventBriefing, getEventBriefing } from './eventBriefings'
vi.mock('./helpers', () => ({
  API_BASE: '/api',
  getAuthHeaders: () => ({ Authorization: 'Bearer test' }),
  handleJSONResponse: async (res: Response) => {
    if (!res.ok) throw new Error('failed')
    return res.json()
  },
}))
afterEach(() => vi.unstubAllGlobals())
it('only reads cached briefings on GET and submits model ID on explicit POST', async () => {
  const fetchMock = vi.fn().mockImplementation(async () => new Response('{}'))
  vi.stubGlobal('fetch', fetchMock)
  await getEventBriefing('model/a')
  expect(fetchMock).toHaveBeenLastCalledWith(
    '/api/events/briefing?model_id=model%2Fa',
    { headers: { Authorization: 'Bearer test' }, cache: 'no-store' }
  )
  await generateEventBriefing('model/a')
  expect(fetchMock).toHaveBeenLastCalledWith('/api/events/briefing', {
    method: 'POST',
    headers: { Authorization: 'Bearer test' },
    cache: 'no-store',
    body: JSON.stringify({ model_id: 'model/a' }),
  })
})
it('does not retry errors', async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response('', { status: 429 }))
  vi.stubGlobal('fetch', fetchMock)
  await expect(generateEventBriefing('m')).rejects.toThrow('failed')
  expect(fetchMock).toHaveBeenCalledTimes(1)
})
