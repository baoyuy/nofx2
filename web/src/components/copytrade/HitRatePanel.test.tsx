import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { HitRate } from '../../lib/api/copyTrade'
import { HitRatePanel } from './ConfidenceUi'

const mocks = vi.hoisted(() => ({ hitRate: vi.fn() }))
vi.mock('../../lib/api', () => ({ api: { getHitRate: mocks.hitRate } }))

const row = (changes: Partial<HitRate>): HitRate => ({
  group: 'confidence',
  band: 'all',
  horizon: '24h',
  direction: 'all',
  samples: 0,
  hits: 0,
  rate: 0,
  avg_return: 0,
  ...changes,
})

async function mount(rows: HitRate[]) {
  mocks.hitRate.mockResolvedValue({ days: 30, rows })
  render(<HitRatePanel language="zh" />)
  await act(async () => {})
}

beforeEach(() => mocks.hitRate.mockReset())
afterEach(cleanup)

describe('hit-rate panel', () => {
  it('tells a falling market from a signal that works', async () => {
    await mount([
      // Shorts "hit" 70% of the time, but only 40% beat BTC.
      row({
        samples: 100,
        hits: 70,
        rate: 0.7,
        avg_return: 0.02,
        excess_samples: 100,
        excess_rate: 0.4,
        avg_excess: -0.005,
      }),
      row({ horizon: '4h', samples: 100, hits: 50, rate: 0.5 }),
      row({ horizon: '72h', samples: 80, hits: 40, rate: 0.5 }),
      row({ group: 'openers', band: '2', samples: 40, hits: 26, rate: 0.65 }),
    ])
    expect(screen.getByText('70%')).toBeInTheDocument()
    expect(screen.getByText('72 小时后')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('口径'), {
      target: { value: 'excess' },
    })
    expect(screen.queryByText('70%')).not.toBeInTheDocument()
    expect(screen.getByText('40%')).toHaveAttribute(
      'title',
      expect.stringContaining('-0.50%')
    )
    // Split by fresh openers: its own bands.
    fireEvent.change(screen.getByLabelText('口径'), {
      target: { value: 'raw' },
    })
    fireEvent.change(screen.getByLabelText('统计对象'), {
      target: { value: 'openers' },
    })
    expect(screen.getByText('2 人新开仓')).toBeInTheDocument()
    expect(screen.getByText('65%')).toBeInTheDocument()
  })

  it('still reads an older server that sends no groups or 72h', async () => {
    const old = { band: 'all', direction: 'all', avg_return: 0.01 } as const
    await mount([
      { ...old, horizon: '4h', samples: 50, hits: 30, rate: 0.6 },
      { ...old, horizon: '24h', samples: 50, hits: 25, rate: 0.5 },
    ] as HitRate[])
    expect(screen.getByText('60%')).toBeInTheDocument()
    expect(screen.queryByText('72 小时后')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('统计对象')).not.toBeInTheDocument()
  })
})
