import { describe, expect, it } from 'vitest'
import { ruleLabel, rulesLabel } from './format'

describe('auto-follow rule labels', () => {
  it('labels every drop rule the backend sends', () => {
    for (const code of [
      'closed',
      'private',
      'martingale',
      'roi_negative',
      'mdd_high',
      'balance_low',
      'stale_trapped',
      'inactive',
      'over_target',
      'manual',
    ]) {
      expect(ruleLabel('zh', code)).not.toBe(code)
      expect(ruleLabel('en', code)).not.toBe(code)
    }
  })

  it('joins several rules and reads the pick reason', () => {
    expect(rulesLabel('zh', 'martingale,roi_negative')).toBe(
      '摊平型、30 天收益为负'
    )
    expect(rulesLabel('en', 'roi90=120,mdd90=15')).toBe(
      '90-day ROI 120%, drawdown 15%'
    )
  })

  it('reads why a candidate was not picked', () => {
    expect(rulesLabel('zh', 'pick_balance=3000')).toBe(
      '余额 3000 U，不到 5000 U'
    )
    expect(rulesLabel('zh', 'pick_roi30=-5,mdd30=12')).toBe(
      '近 30 天收益 -5%，回撤 12%'
    )
    expect(rulesLabel('en', 'private')).toBe('Private, trades hidden')
  })
})
