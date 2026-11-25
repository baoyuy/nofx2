import { describe, expect, it } from 'vitest'
import type { SymbolSummary } from '../../lib/api/copyTrade'
import {
  directionFor,
  evidenceLabel,
  ineligibleText,
  sortCoins,
} from './CoinsTab'

type Stable = NonNullable<SymbolSummary['stable_direction']>

function coin(
  symbol: string,
  longTraders: number,
  shortTraders: number,
  stable?: Partial<Stable>
): SymbolSummary {
  return {
    symbol,
    long: { traders: longTraders, value: 0 },
    short: { traders: shortTraders, value: 0 },
    stable_direction: stable
      ? ({
          direction: 'neutral',
          score: 0,
          accuracy: 0,
          ai_accuracy: 0,
          trader_accuracy: 0,
          ai_samples: 0,
          trader_samples: 0,
          traders: 0,
          risk: 0,
          fresh: true,
          eligible: true,
          ...stable,
        } as Stable)
      : undefined,
  } as unknown as SymbolSummary
}

describe('direction filter follows the active ranking', () => {
  // More traders are long, but the AI calls it short.
  const crowdLongAiShort = coin('AUSDT', 5, 1, { direction: 'short' })
  const aiBias = (sym: string) => (sym === 'AUSDT' ? 'short' : undefined)

  it('under the AI sort, "short" means the AI says short', () => {
    expect(directionFor(crowdLongAiShort, 'ai', aiBias)).toBe('short')
    const out = sortCoins([crowdLongAiShort], 'ai', 'short', () => 1, aiBias)
    expect(out.map((c) => c.symbol)).toEqual(['AUSDT'])
    // It must not appear when filtering for long under the AI sort.
    expect(
      sortCoins([crowdLongAiShort], 'ai', 'long', () => 1, aiBias)
    ).toEqual([])
  })

  it('under the stable sort, it is the stable direction shown', () => {
    expect(directionFor(crowdLongAiShort, 'stable')).toBe('short')
  })

  it('other sorts keep the trader head count', () => {
    expect(directionFor(crowdLongAiShort, 'traders')).toBe('long')
    expect(
      sortCoins([crowdLongAiShort], 'traders', 'long').map((c) => c.symbol)
    ).toEqual(['AUSDT'])
  })
})

describe('evidence label', () => {
  it('states the sample size on the shown side, not a grade', () => {
    const ev = evidenceLabel(
      coin('A', 1, 0, {
        direction: 'short',
        ai_samples: 25,
        ai_accuracy: 0.62,
      }),
      'zh'
    )
    expect(ev?.text).toBe('证据充足 25')
    expect(ev?.title).toMatch(/做空/)
    expect(ev?.title).toMatch(/不是下一次判断的胜率/)
    expect(ev?.text).not.toMatch(/可靠/)
  })

  it('reports thin history honestly', () => {
    expect(
      evidenceLabel(coin('A', 1, 0, { direction: 'long', ai_samples: 3 }), 'zh')
        ?.text
    ).toBe('证据少 3')
    expect(
      evidenceLabel(
        coin('A', 1, 0, { direction: 'long', ai_samples: 10 }),
        'zh'
      )?.text
    ).toBe('证据一般 10')
  })

  it('has nothing to say without a direction', () => {
    expect(
      evidenceLabel(
        coin('A', 1, 0, { direction: 'neutral', ai_samples: 50 }),
        'zh'
      )
    ).toBeNull()
    expect(evidenceLabel(coin('A', 1, 0), 'zh')).toBeNull()
  })
})

describe('ineligibility reason', () => {
  it('tells a stale reading from thin history', () => {
    expect(ineligibleText('zh', 'stale')).toMatch(/过期/)
    expect(ineligibleText('zh', 'stale')).not.toMatch(/样本不足/)
    expect(ineligibleText('zh', 'few_ai')).toMatch(/AI 独立样本不足/)
    expect(ineligibleText('zh', 'few_trader')).toMatch(/带单独立样本不足/)
    expect(ineligibleText('zh', 'no_direction')).toMatch(/没有明确方向/)
  })
})
