import type {
  MirrorEvent,
  MirrorLink,
  MirrorPosition,
  MirrorSettings,
} from '../../lib/api/mirror'
import { tr } from '../market/marketUi'

export const DEFAULT_MIRROR: MirrorSettings = {
  size_mode: 'fixed',
  fixed_margin: 20,
  ratio: 1,
  leverage_mode: 'follow',
  leverage: 5,
  max_leverage: 10,
  follow_protect: true,
  max_margin: 0,
  max_loss: 0,
}

/** The form keeps numbers as typed. */
export type MirrorForm = Record<
  | 'fixed_margin'
  | 'ratio'
  | 'leverage'
  | 'max_leverage'
  | 'max_margin'
  | 'max_loss',
  string
> &
  Pick<MirrorSettings, 'size_mode' | 'leverage_mode' | 'follow_protect'>

export function toForm(s: MirrorSettings): MirrorForm {
  const n = (v: number) => (v ? String(v) : '')
  return {
    size_mode: s.size_mode,
    leverage_mode: s.leverage_mode,
    follow_protect: s.follow_protect,
    fixed_margin: n(s.fixed_margin),
    ratio: n(s.ratio),
    leverage: n(s.leverage),
    max_leverage: n(s.max_leverage),
    max_margin: n(s.max_margin),
    max_loss: n(s.max_loss),
  }
}

/** Parses the form as the backend checks it; returns an error message. */
export function fromForm(
  language: string,
  f: MirrorForm
): MirrorSettings | string {
  const num = (v: string) => (v.trim() === '' ? 0 : Number(v))
  const s: MirrorSettings = {
    size_mode: f.size_mode,
    leverage_mode: f.leverage_mode,
    follow_protect: f.follow_protect,
    fixed_margin: num(f.fixed_margin),
    ratio: num(f.ratio),
    leverage: Math.round(num(f.leverage)),
    max_leverage: Math.round(num(f.max_leverage)),
    max_margin: num(f.max_margin),
    max_loss: num(f.max_loss),
  }
  // Each mode has one leverage field; the other is not used.
  if (s.leverage_mode === 'fixed') s.max_leverage = 0
  if (Object.values(s).some((v) => typeof v === 'number' && !isFinite(v)))
    return tr(language, '请输入数字', 'Enter numbers')
  if (s.size_mode === 'fixed' && (s.fixed_margin < 1 || s.fixed_margin > 1e6))
    return tr(
      language,
      '每单保证金要在 1 到 1000000 U 之间',
      'Margin per open must be 1 to 1,000,000 USDT'
    )
  if (s.size_mode === 'ratio' && (s.ratio <= 0 || s.ratio > 10))
    return tr(
      language,
      '比例倍数要大于 0、不超过 10',
      'Ratio must be above 0 and at most 10'
    )
  if (s.leverage_mode === 'fixed' && (s.leverage < 1 || s.leverage > 125))
    return tr(language, '杠杆要在 1 到 125 之间', 'Leverage must be 1 to 125')
  if (
    s.leverage_mode === 'follow' &&
    (s.max_leverage < 0 || s.max_leverage > 125)
  )
    return tr(
      language,
      '杠杆上限要在 0（不限）到 125 之间',
      'Max leverage must be 0 (no cap) to 125'
    )
  if (s.max_margin < 0 || s.max_loss < 0)
    return tr(language, '上限不能是负数', 'Caps must not be negative')
  return s
}

/** One line of a link's settings. */
export function describeLink(language: string, l: MirrorSettings): string {
  const size =
    l.size_mode === 'fixed'
      ? tr(
          language,
          `每单 ${l.fixed_margin} U 保证金`,
          `${l.fixed_margin} USDT margin per open`
        )
      : tr(
          language,
          `按 AI 仓位比例 ×${l.ratio}`,
          `AI's share of equity ×${l.ratio}`
        )
  const lev =
    l.leverage_mode === 'fixed'
      ? tr(language, `固定 ${l.leverage}x`, `fixed ${l.leverage}x`)
      : l.max_leverage
        ? tr(
            language,
            `跟随 AI 杠杆，最多 ${l.max_leverage}x`,
            `AI's leverage, at most ${l.max_leverage}x`
          )
        : tr(language, '跟随 AI 杠杆', "AI's leverage")
  const parts = [
    size,
    lev,
    l.follow_protect
      ? tr(language, '跟随止盈止损', 'copies stops')
      : tr(language, '不挂止盈止损', 'no stops'),
  ]
  if (l.max_margin)
    parts.push(
      tr(
        language,
        `单笔最多 ${l.max_margin} U`,
        `at most ${l.max_margin} USDT a position`
      )
    )
  if (l.max_loss)
    parts.push(
      tr(
        language,
        `亏 ${l.max_loss} U 停止`,
        `stops after losing ${l.max_loss} USDT`
      )
    )
  return parts.join(tr(language, '，', ', '))
}

export function skipLabel(
  language: string,
  skip: MirrorPosition['skip']
): string {
  switch (skip) {
    case 'before_start':
      return tr(
        language,
        '跟单前已有，不跟',
        'Open before the link, not copied'
      )
    case 'open_failed':
      return tr(language, '开仓没成功，不跟', 'Open failed, not copied')
    case 'follower_closed':
      return tr(
        language,
        '跟单仓位已在外部平掉，不再跟',
        'Copy closed outside, not reopened'
      )
    default:
      return ''
  }
}

export function stopReasonLabel(language: string, reason: string): string {
  switch (reason) {
    case 'manual':
      return tr(language, '手动停止', 'Stopped by you')
    case 'max_loss':
      return tr(
        language,
        '亏损达到上限，已平仓并停止',
        'Loss cap reached: closed and stopped'
      )
    case 'busy':
      return tr(
        language,
        '账户被其他交易员占用，已停止',
        'Account taken by another trader: stopped'
      )
    default:
      return reason
  }
}

const ACTIONS: Record<string, [string, string]> = {
  open: ['开仓', 'Open'],
  add: ['加仓', 'Add'],
  reduce: ['减仓', 'Reduce'],
  close: ['平仓', 'Close'],
  protect: ['止盈止损', 'Stops'],
  skip: ['跳过', 'Skipped'],
  error: ['错误', 'Error'],
  start: ['启动', 'Started'],
  stop: ['停止', 'Stopped'],
  follower_closed: ['外部平仓', 'Closed outside'],
}

export function actionLabel(language: string, action: string): string {
  const a = ACTIONS[action]
  return a ? tr(language, a[0], a[1]) : action
}

export const eventTone = (e: MirrorEvent) =>
  e.action === 'error'
    ? 'text-nofx-danger'
    : e.action === 'skip' || e.action === 'follower_closed'
      ? 'text-nofx-text-muted'
      : e.action === 'open' || e.action === 'add'
        ? 'text-nofx-success'
        : 'text-nofx-text'

/** Settings of a link, without its runtime fields. */
export function settingsOf(l: MirrorLink): MirrorSettings {
  return {
    size_mode: l.size_mode,
    fixed_margin: l.fixed_margin,
    ratio: l.ratio,
    leverage_mode: l.leverage_mode,
    leverage: l.leverage,
    max_leverage: l.max_leverage,
    follow_protect: l.follow_protect,
    max_margin: l.max_margin,
    max_loss: l.max_loss,
  }
}
