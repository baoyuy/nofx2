import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { TraderPosition } from '../../lib/api/copyTrade'
import { HealthTag } from './ConfidenceUi'

const position = (changes: Partial<TraderPosition> = {}): TraderPosition => ({
  symbol: 'ETHUSDT',
  side: 'short',
  qty: 1,
  entry_price: 100,
  entry_value: 100,
  opened_at: 0,
  last_add_at: 0,
  source: 'public',
  return_pct: -9.4,
  pct_of_margin: true,
  priced: true,
  trapped: false,
  liquidated: false,
  fresh: false,
  ...changes,
})

describe('position health labels', () => {
  it('shows historical deviation without calling a 9.4% current loss trapped', () => {
    render(
      <HealthTag
        language="zh"
        h={position({
          historical_entry_warning: true,
          first_entry_return_pct: -115.2,
          first_entry_pct_of_margin: true,
          first_entry_threshold_pct: 50,
        })}
      />
    )
    expect(screen.queryByText('被套')).not.toBeInTheDocument()
    const history = screen.getByText('历史入场偏离')
    expect(history).toHaveAttribute('title', expect.stringContaining('-115.2%'))
    expect(history).toHaveAttribute(
      'title',
      expect.stringContaining('按当前杠杆折算')
    )
    expect(history).toHaveAttribute(
      'title',
      expect.stringContaining('不是当前实际浮亏')
    )
    expect(history).toHaveAttribute(
      'title',
      expect.stringContaining('不计入被套人数')
    )
    expect(history).toHaveAttribute('tabindex', '0')
  })

  it('does not label a 30% margin loss as trapped', () => {
    render(<HealthTag language="zh" h={position({ return_pct: -30 })} />)
    expect(screen.queryByText('被套')).not.toBeInTheDocument()
    expect(screen.queryByText('历史入场偏离')).not.toBeInTheDocument()
  })

  it('explains the current margin threshold on a trapped position', () => {
    render(
      <HealthTag
        language="zh"
        h={position({
          return_pct: -50,
          trapped: true,
          trapped_threshold_pct: 50,
        })}
      />
    )
    expect(screen.getByText('被套')).toHaveAttribute(
      'title',
      expect.stringContaining('当前浮亏 -50.0%（保证金）')
    )
    expect(screen.getByText('被套')).toHaveAttribute(
      'title',
      expect.stringContaining('50%')
    )
    expect(screen.getByText('被套')).toHaveAttribute(
      'title',
      expect.stringContaining('不使用历史首笔价格')
    )
  })

  it('distinguishes price movement when margin is unavailable', () => {
    render(
      <HealthTag
        language="zh"
        h={position({
          return_pct: -5,
          pct_of_margin: false,
          trapped: true,
          trapped_threshold_pct: 5,
          historical_entry_warning: true,
          first_entry_return_pct: -8,
          first_entry_pct_of_margin: false,
          first_entry_threshold_pct: 5,
        })}
      />
    )
    expect(screen.getByText('被套')).toHaveAttribute(
      'title',
      expect.stringContaining('-5.0%（价格）')
    )
    expect(screen.getByText('历史入场偏离')).toHaveAttribute(
      'title',
      expect.stringContaining('-8.0%（价格）')
    )
  })

  it('keeps English explanations in English mode', () => {
    render(
      <HealthTag
        language="en"
        h={position({
          return_pct: -55,
          trapped: true,
          historical_entry_warning: true,
          first_entry_return_pct: -90,
          first_entry_pct_of_margin: true,
        })}
      />
    )
    expect(screen.getByText('Trapped')).toHaveAttribute(
      'title',
      expect.stringContaining('Current return -55.0% (margin)')
    )
    expect(screen.getByText('Historical entry deviation')).toHaveAttribute(
      'title',
      expect.stringContaining('not current PnL')
    )
    expect(screen.queryByText('被套')).not.toBeInTheDocument()
  })

  it('does not show historical risk alongside a likely-liquidated label', () => {
    render(
      <HealthTag
        language="zh"
        h={position({
          liquidated: true,
          historical_entry_warning: true,
        })}
      />
    )
    expect(screen.getByText('可能已强平')).toBeInTheDocument()
    expect(screen.queryByText('历史入场偏离')).not.toBeInTheDocument()
  })
})
