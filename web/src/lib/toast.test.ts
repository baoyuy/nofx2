import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { toast } from './toast'
import { notify } from './notify'
import { setDisplayLanguage } from '../i18n/displayLanguage'

const mocks = vi.hoisted(() => ({
  error: vi.fn(),
  success: vi.fn(),
  dismiss: vi.fn(),
  promise: vi.fn(),
}))
vi.mock('sonner', () => ({ toast: Object.assign(vi.fn(), mocks) }))
beforeEach(() => {
  vi.clearAllMocks()
  setDisplayLanguage('zh')
})
afterEach(() => setDisplayLanguage('zh'))

it('translates notification title, description and action without altering IDs or callbacks', () => {
  const action = vi.fn()
  mocks.error.mockReturnValue('toast-1')
  const result = toast.error('Network error', {
    id: 'original-id',
    description: 'Unable to reach the server',
    action: { label: 'Retry', onClick: action },
  })
  expect(result).toBe('toast-1')
  expect(mocks.error).toHaveBeenCalledWith(
    '网络连接异常，请检查网络或代理',
    expect.objectContaining({
      id: 'original-id',
      description: '无法连接服务器，请检查网络或代理',
      action: { label: '重试', onClick: action },
    })
  )
})

it('covers notify.error and preserves the English text after switching language', () => {
  const raw =
    'Binance is rate limiting copy-trade requests; paused, retry later'
  notify.error(raw)
  expect(mocks.error.mock.calls[0][0]).toContain('币安带单')
  setDisplayLanguage('en')
  notify.error(raw)
  expect(mocks.error.mock.calls[1][0]).toBe(raw)
})

it('does not translate dismissal IDs or suppress a callback', () => {
  toast.dismiss('Network error')
  expect(mocks.dismiss).toHaveBeenCalledWith('Network error')
})

it('uses the active language for asynchronous descriptions', () => {
  toast.error('Network error', {
    description: () => 'Unable to reach the server',
  })
  const options = mocks.error.mock.calls[0][1]
  expect(options.description()).toContain('无法连接')
  setDisplayLanguage('en')
  expect(options.description()).toBe('Unable to reach the server')
})
