import { expect, test } from 'vitest'
import { hornOnBoard } from './coinPool'

const now = Date.parse('2026-11-01T08:30:00Z')

test('marks a match that joined with the latest candle as new', () => {
  expect(hornOnBoard({ matched_bars: 1, matched_since: '2026-11-01T08:15:00Z' }, true, now)).toEqual({
    label: '新上榜 · 15 分钟前',
    isNew: true,
  })
})

test('shows how long an older match has held', () => {
  expect(hornOnBoard({ matched_bars: 7, matched_since: '2026-11-01T02:00:00Z' }, true, now).label).toBe(
    '已 7 根 · 7 小时前'
  )
  expect(hornOnBoard({ matched_bars: 7, matched_since: '2026-10-30T08:30:00Z' }, false, now).label).toBe(
    '7 bars · 2d ago'
  )
})

test('non-matching rows have no board time', () => {
  expect(hornOnBoard({}, true, now)).toEqual({ label: '', isNew: false })
})
