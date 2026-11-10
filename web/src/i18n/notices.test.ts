import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { localizeNotice } from './notices'
import { getDisplayLanguage, setDisplayLanguage } from './displayLanguage'

beforeEach(() => setDisplayLanguage('zh'))
afterEach(() => {
  vi.restoreAllMocks()
  setDisplayLanguage('zh')
})

describe('Chinese user-facing notices', () => {
  it('translates the exact copy-trade rate-limit response from the screenshot', () => {
    expect(
      localizeNotice(
        'Binance is rate limiting copy-trade requests; paused, retry later'
      )
    ).toBe('币安带单数据请求触发限流保护，已暂停获取，稍后将自动重试。')
  })

  it.each([
    'Network error',
    'Request timed out',
    'Permission Denied',
    'API Not Found',
    'Server Error',
    'Session expired',
    'Copy failed',
    'Operation failed',
    'Request failed',
    'Save failed',
    'The upstream service took too long to respond',
    'Unable to reach the server',
    'Binance rate limited the request: connection dropped',
    'Binance changed the copy-trade data format: missing price',
    'Could not load Binance candles. Check the network or proxy.',
    'API scheduler queue is full',
    'unknown upstream failure with secret=not-for-display',
  ])('does not leak English error prose: %s', (raw) => {
    const translated = localizeNotice(raw, 'zh')
    expect(translated).toMatch(/\p{Script=Han}/u)
    expect(translated).not.toMatch(/[a-z]/i)
    expect(translated).not.toContain('not-for-display')
  })

  it('keeps errors distinguishable and preserves numerical error codes', () => {
    expect(
      localizeNotice('Binance rate limited the request: HTTP 429')
    ).toContain('429')
    expect(localizeNotice('you can follow at most 100 traders')).toContain(
      '100'
    )
    expect(localizeNotice('invalid proxy port')).toContain('端口')
    expect(localizeNotice('insufficient margin')).toContain('保证金不足')
    expect(localizeNotice('stop loss placement failed')).toContain('立即检查')
  })

  it('explains rejected configuration updates without implying an automatic stop', () => {
    expect(localizeNotice('Stop the trader before detaching its strategy')).toContain('先明确停止')
    const grid = 'trader custom_prompt/override_base_prompt are not supported for grid strategies; clear both settings or use an AI strategy'
    expect(localizeNotice(grid)).toContain('网格策略暂不支持')
    expect(localizeNotice(grid)).toContain('未保存')
    expect(localizeNotice(grid, 'en')).toBe(grid)
    expect(localizeNotice('stop trader fixture before modifying its grid strategy or switching AI/grid strategy type; no changes were saved')).toContain('未自动停止或撤单')
  })

  it('preserves authored Chinese and user resource names', () => {
    const text = '策略「BTC swing」已保存，请检查 USDT 余额。'
    expect(localizeNotice(text)).toBe(text)
  })

  it('leaves English and Indonesian mode responses unchanged', () => {
    const raw =
      'Binance is rate limiting copy-trade requests; paused, retry later'
    expect(localizeNotice(raw, 'en')).toBe(raw)
    expect(localizeNotice(raw, 'id')).toBe(raw)
  })

  it('reads the selected language at display time', () => {
    expect(localizeNotice('Network error')).toContain('网络')
    setDisplayLanguage('en')
    expect(localizeNotice('Network error')).toBe('Network error')
    setDisplayLanguage('zh')
    expect(localizeNotice('Network error')).toContain('网络')
  })

  it('continues switching language if browser storage is unavailable', () => {
    vi.spyOn(localStorage, 'getItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    vi.spyOn(localStorage, 'setItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    setDisplayLanguage('en')
    expect(getDisplayLanguage()).toBe('en')
    setDisplayLanguage('zh')
    expect(getDisplayLanguage()).toBe('zh')
  })
})

it('translates English details after a Chinese error prefix', () => {
  const message = localizeNotice(
    '连接失败：Binance is rate limiting copy-trade requests; paused, retry later',
    'zh'
  )
  expect(message).toContain('连接失败：币安带单')
  expect(message).not.toMatch(/[a-z]/i)
})

it('never hides uncertain payment or trade outcomes behind a retry suggestion', () => {
  expect(
    localizeNotice(
      'paid request outcome unknown; context deadline exceeded',
      'zh'
    )
  ).toContain('勿重复付款')
  expect(localizeNotice('order submission outcome unknown', 'zh')).toContain(
    '勿重复下单'
  )
})
