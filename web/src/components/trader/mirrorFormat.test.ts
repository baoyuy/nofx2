import { describe, expect, it } from 'vitest'
import { DEFAULT_MIRROR, describeLink, fromForm, toForm } from './mirrorFormat'

describe('copy link settings', () => {
  it('round-trips the defaults through the form', () => {
    expect(fromForm('en', toForm(DEFAULT_MIRROR))).toEqual(DEFAULT_MIRROR)
  })

  it('reads empty caps as no cap and rejects what the backend rejects', () => {
    const f = { ...toForm(DEFAULT_MIRROR), max_margin: '', max_loss: '' }
    expect(fromForm('en', f)).toMatchObject({ max_margin: 0, max_loss: 0 })
    expect(fromForm('en', { ...f, fixed_margin: '0.5' })).toBeTypeOf('string')
    expect(
      fromForm('en', { ...f, size_mode: 'ratio', ratio: '11' })
    ).toBeTypeOf('string')
    expect(
      fromForm('en', { ...f, leverage_mode: 'fixed', leverage: '200' })
    ).toBeTypeOf('string')
    expect(fromForm('en', { ...f, max_loss: '-1' })).toBeTypeOf('string')
    // A fixed leverage is not capped by the follow-mode cap.
    expect(
      fromForm('en', { ...f, leverage_mode: 'fixed', leverage: '20' })
    ).toMatchObject({ leverage: 20, max_leverage: 0 })
  })

  it('describes a link in one line', () => {
    expect(
      describeLink('zh', {
        ...DEFAULT_MIRROR,
        size_mode: 'ratio',
        ratio: 0.5,
        max_loss: 100,
      })
    ).toBe(
      '按 AI 仓位比例 ×0.5，跟随 AI 杠杆，最多 10x，跟随止盈止损，亏 100 U 停止'
    )
  })
})
