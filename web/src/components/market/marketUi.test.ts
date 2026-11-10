import { describe, expect, it } from 'vitest'
import { clearLegacyAnalyses, mergeAnalyses } from './marketUi'

type R = { analyzed_at: number; note?: string }

describe('AI analysis merge', () => {
  it('keeps the newer result per symbol', () => {
    const current: Record<string, R> = {
      BTCUSDT: { analyzed_at: 5, note: 'fresh' },
    }
    const saved: Record<string, R> = {
      BTCUSDT: { analyzed_at: 3, note: 'stale from server' },
      ETHUSDT: { analyzed_at: 4 },
    }
    const merged = mergeAnalyses(current, saved)
    expect(merged.BTCUSDT.note).toBe('fresh')
    expect(merged.ETHUSDT.analyzed_at).toBe(4)
  })

  it('a re-analysis replaces the old result', () => {
    const merged = mergeAnalyses(
      { BTCUSDT: { analyzed_at: 1, note: 'old' } },
      {
        BTCUSDT: { analyzed_at: 2, note: 'new' },
      }
    )
    expect(merged.BTCUSDT.note).toBe('new')
  })

  it('clears the old browser-only copy', () => {
    localStorage.setItem('nofx.market.analyses', '{}')
    clearLegacyAnalyses()
    expect(localStorage.getItem('nofx.market.analyses')).toBeNull()
  })
})
