import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SWRConfig } from 'swr'
import type {
  BaselineStat,
  OutcomeStat,
  OutcomesResp,
} from '../../lib/api/copyTrade'
import { BaselineComparePanel, aiBeatsBaselines } from './BaselineComparePanel'

const mocks = vi.hoisted(() => ({ outcomes: vi.fn() }))
vi.mock('../../lib/api', () => ({
  api: { getCoinScoreOutcomes: mocks.outcomes },
}))

const stat = (changes: Partial<OutcomeStat>): OutcomeStat => ({
  horizon: '4h',
  side: 'all',
  band: 'all',
  samples: 0,
  hit_rate: 0,
  beat_market: 0,
  avg_ret: 0,
  avg_excess: 0,
  dir_samples: 0,
  net_avg_ret: 0,
  net_win_rate: 0,
  net_excess: 0,
  net_excess_lb: 0,
  cost_pct: 0.2,
  ...changes,
})

const base = (changes: Partial<BaselineStat>): BaselineStat => ({
  name: 'short_all',
  label: '全部做空（不用AI）',
  samples: 0,
  hit_rate: 0,
  net_avg_ret: 0,
  net_win_rate: 0,
  ...changes,
})

async function mount(resp: Partial<OutcomesResp>) {
  mocks.outcomes.mockResolvedValue({
    rows: [],
    baselines: {},
    version: '',
    versions: [],
    window: {
      since: 0,
      oldest: 0,
      coverage_days: 3.2,
      days: 30,
      retention_days: 90,
      truncated: false,
      cost_pct: 0.2,
      neutral_pct: 0.5,
      exit_interval: '15m',
    },
    ...resp,
  })
  // A fresh SWR cache per test, or one test's response leaks into the next.
  render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <BaselineComparePanel language="zh" />
    </SWRConfig>
  )
  await act(async () => {})
}

beforeEach(() => mocks.outcomes.mockReset())
afterEach(cleanup)

describe('aiBeatsBaselines', () => {
  it('requires the AI to beat every measured rule', () => {
    const ai = stat({ samples: 50, net_avg_ret: 0.8 })
    expect(
      aiBeatsBaselines(ai, [base({ samples: 50, net_avg_ret: 0.5 })])
    ).toBe('yes')
    // One rule with no model does just as well: no edge shown.
    expect(
      aiBeatsBaselines(ai, [
        base({ samples: 50, net_avg_ret: 0.5 }),
        base({ name: 'momentum', samples: 50, net_avg_ret: 0.9 }),
      ])
    ).toBe('no')
  })

  it('ignores rules with no samples and reports unknown without data', () => {
    const ai = stat({ samples: 50, net_avg_ret: 0.8 })
    expect(
      aiBeatsBaselines(ai, [
        base({ samples: 50, net_avg_ret: 0.5 }),
        base({ name: 'momentum', samples: 0, net_avg_ret: 99 }),
      ])
    ).toBe('yes')
    expect(aiBeatsBaselines(ai, [base({ samples: 0 })])).toBe('unknown')
    expect(aiBeatsBaselines(stat({ samples: 0 }), [base({ samples: 9 })])).toBe(
      'unknown'
    )
    expect(aiBeatsBaselines(undefined, [base({ samples: 9 })])).toBe('unknown')
  })
})

describe('baseline compare panel', () => {
  it('warns when a good-looking AI number is just the falling market', async () => {
    await mount({
      rows: [
        stat({
          samples: 300,
          hit_rate: 65,
          net_win_rate: 61,
          net_avg_ret: 0.85,
        }),
      ],
      baselines: {
        '4h': [
          base({
            samples: 300,
            hit_rate: 63.6,
            net_avg_ret: 0.85,
            net_win_rate: 60,
          }),
          base({
            name: 'long_all',
            label: '全部做多（不用AI）',
            samples: 300,
            hit_rate: 36,
            net_avg_ret: -1.2,
          }),
        ],
      },
    })
    expect(await screen.findByText(/全部做空（不用AI）/)).toBeTruthy()
    expect(screen.getByRole('status').textContent).toMatch(/没有跑赢/)
  })

  it('confirms an edge only when the AI beats every rule', async () => {
    await mount({
      rows: [
        stat({
          samples: 300,
          hit_rate: 61,
          net_win_rate: 58,
          net_avg_ret: 1.4,
        }),
      ],
      baselines: {
        '4h': [
          base({ samples: 300, net_avg_ret: 0.3 }),
          base({
            name: 'momentum',
            label: '延续上一次方向（不用AI）',
            samples: 280,
            net_avg_ret: 0.1,
          }),
        ],
      },
    })
    expect(screen.getByRole('status').textContent).toMatch(/高于所有无模型规则/)
  })

  it('states the cost, neutral band, exit candles and data coverage', async () => {
    await mount({ rows: [stat({ samples: 10, net_avg_ret: 0.1 })] })
    const notes = screen.getByText(/统计最近 30 天/)
    expect(notes.textContent).toMatch(/现有数据约 3.2 天/)
    expect(notes.textContent).toMatch(/保留 90 天/)
    expect(notes.textContent).toMatch(/扣往返成本 0.2%/)
    expect(notes.textContent).toMatch(/不足 0.5%/)
    expect(notes.textContent).toMatch(/15m/)
  })

  it('says so instead of showing an empty table when nothing is mature', async () => {
    await mount({ rows: [stat({ samples: 0 })] })
    expect(screen.getByText(/还没有成熟样本/)).toBeTruthy()
    expect(screen.queryByRole('status')).toBeNull()
  })

  it('offers the scoring versions the server reported', async () => {
    await mount({
      rows: [stat({ samples: 5, net_avg_ret: 0.1 })],
      versions: ['copytrade-ai-v13', 'legacy'],
    })
    const select = screen.getByLabelText('评分版本') as HTMLSelectElement
    expect([...select.options].map((o) => o.value)).toEqual([
      '',
      'copytrade-ai-v13',
      'legacy',
    ])
  })
})
