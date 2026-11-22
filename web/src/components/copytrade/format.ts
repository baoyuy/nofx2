import { tr } from '../market/marketUi'
import type { TradeAction, TradeSide } from '../../lib/api/copyTrade'

/**
 * Money for tables: one rule for every amount, so a column never mixes an
 * exact figure with a compact one (33.70K next to 2,012 read as a bug).
 * 1.23B / 4.56M / 33.70K / 157.00
 */
export function usd(v: number): string {
  const a = Math.abs(Math.round(v * 100) / 100)
  const sign = v < 0 ? '-' : ''
  if (a >= 1e9) return `${sign}$${(a / 1e9).toFixed(2)}B`
  if (a >= 1e6) return `${sign}$${(a / 1e6).toFixed(2)}M`
  if (a >= 1e3) return `${sign}$${(a / 1e3).toFixed(2)}K`
  return `${sign}$${a.toFixed(2)}`
}

export function signedUsd(v: number): string {
  return (v > 0 ? '+' : '') + usd(v)
}

/**
 * Prices keep the precision entry prices actually carry: 73,692.9 / 356.353 /
 * 71.8. The column is right-aligned with tabular figures, so the differing
 * decimal counts do not read as a bug.
 */
export function price(v: number): string {
  if (!v) return '--'
  const d = v >= 1e3 ? 1 : v >= 1 ? 3 : v >= 0.01 ? 5 : 7
  return v.toLocaleString('en-US', { maximumFractionDigits: d })
}

export function pct(v: number): string {
  return `${v > 0 ? '+' : ''}${v.toFixed(Math.abs(v) >= 100 ? 0 : 1)}%`
}

export function base(symbol: string): string {
  return symbol.replace(/USDT$/, '')
}

export function ago(language: string, ms: number): string {
  if (!ms) return '--'
  const mins = Math.max(0, Math.floor((Date.now() - ms) / 60000))
  if (mins < 1) return tr(language, '刚刚', 'just now')
  if (mins < 60) return tr(language, `${mins} 分钟前`, `${mins} min ago`)
  const h = Math.floor(mins / 60)
  if (h < 24) return tr(language, `${h} 小时前`, `${h} h ago`)
  return tr(
    language,
    `${Math.floor(h / 24)} 天前`,
    `${Math.floor(h / 24)} d ago`
  )
}

export function clock(ms: number): string {
  if (!ms) return '--'
  return new Date(ms).toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

export function sideLabel(language: string, side: TradeSide): string {
  return side === 'long'
    ? tr(language, '多', 'Long')
    : tr(language, '空', 'Short')
}

export function sideClass(side: TradeSide): string {
  return side === 'long' ? 'text-nofx-success' : 'text-nofx-danger'
}

export function actionLabel(
  language: string,
  action: TradeAction,
  side: TradeSide,
  signalType?: string
): string {
  const s = sideLabel(language, side)
  if (signalType === 'adverse_add') return tr(language, `逆势加${s}`, `Adverse add ${s}`)
  if (signalType === 'trend_add') return tr(language, `顺势加${s}`, `Trend add ${s}`)
  switch (action) {
    case 'open':
      return tr(language, `开${s}`, `Open ${s}`)
    case 'add':
      return tr(language, `加${s}`, `Add ${s}`)
    case 'reduce':
      return tr(language, `减${s}`, `Reduce ${s}`)
    default:
      return tr(language, `平${s}`, `Close ${s}`)
  }
}

/** Opening actions are colored by side; closing actions are muted. */
export function actionClass(action: TradeAction, side: TradeSide): string {
  if (action === 'open' || action === 'add') return sideClass(side)
  return 'text-nofx-text-muted'
}

/**
 * How a lead trader trades, from 30 days of fills. An empty string when there
 * is nothing worth showing (too few trades, or the portfolio hides its fills).
 */
export function styleLabel(language: string, kind?: string): string {
  switch (kind) {
    case 'scalp':
      return tr(language, '短线', 'Scalp')
    case 'swing':
      return tr(language, '波段', 'Swing')
    case 'long':
      return tr(language, '长线', 'Long')
    case 'martingale':
      return tr(language, '摊平型', 'Averages down')
    default:
      return ''
  }
}

/** The style tooltip: what the classification is based on. */
export function styleHint(language: string, kind?: string): string {
  switch (kind) {
    case 'scalp':
      return tr(
        language,
        '30 天里多数交易在 4 小时内平掉：仓位是抓拍，不是观点。',
        'Most trades close within 4 hours over 30 days: a snapshot, not a view.'
      )
    case 'swing':
      return tr(
        language,
        '30 天里多数交易持有 4 小时到 3 天。',
        'Most trades are held 4 hours to 3 days over 30 days.'
      )
    case 'long':
      return tr(
        language,
        '30 天里多数交易持有超过 3 天：方向有意义，但入场价已经旧了。',
        'Most trades are held over 3 days: the direction matters, the entry is old.'
      )
    case 'martingale':
      return tr(
        language,
        '亏损时反复加仓、等回本再走：仓位越重越可能是在摊平，票权只算 0.3。',
        'Keeps adding to losing positions and waits to break even: a bigger position means averaging down, so its vote counts 0.3.'
      )
    case 'few_samples':
      return tr(
        language,
        '30 天里完整交易不足 5 笔，看不出风格。',
        'Fewer than 5 completed trades in 30 days: no style to tell.'
      )
    default:
      return ''
  }
}

/** An auto-follow drop rule, or the reason of a log entry, for people. */
export function ruleLabel(language: string, code: string): string {
  switch (code) {
    case 'closed':
      return tr(language, '已停止带单', 'Stopped leading')
    case 'private':
      return tr(language, '私域账户，看不到成交', 'Private, trades hidden')
    case 'martingale':
      return tr(language, '摊平型', 'Averages down')
    case 'roi_negative':
      return tr(
        language,
        '30 天收益为负，且明显差于其他带单员',
        'Negative 30-day ROI, well below the others'
      )
    case 'high_freq':
      return tr(
        language,
        '高频账户（每天几十单以上）',
        'High-frequency account (dozens of orders a day)'
      )
    case 'unsynced':
      return tr(
        language,
        '成交记录两天都没能读完',
        'Trade history could not be read for two days'
      )
    case 'mdd_high':
      return tr(language, '30 天回撤超过 40%', '30-day drawdown over 40%')
    case 'balance_low':
      return tr(language, '余额低于 1000 U', 'Balance under 1,000 USDT')
    case 'stale_trapped':
      return tr(
        language,
        '过半持仓是一周以上的被套单',
        'Over half the book is week-old trapped positions'
      )
    case 'inactive':
      return tr(language, '两周没有交易', 'No trade for two weeks')
    case 'inaccurate':
      return tr(
        language,
        '自己的开仓 24 小时后对的不到一半',
        'Own calls right under half the time'
      )
    case 'over_target':
      return tr(language, '超出目标人数', 'Over the target count')
    case 'manual':
      return tr(language, '你手动取消关注', 'You unfollowed')
    default:
      if (code.startsWith('pick_balance=')) {
        const v = code.slice('pick_balance='.length)
        return tr(
          language,
          `余额 ${v} U，不到 5000 U`,
          `Balance ${v} USDT, under 5,000`
        )
      }
      if (code.startsWith('pick_roi30=')) {
        const m = /pick_roi30=(-?\d+),mdd30=(-?\d+)/.exec(code)
        if (m)
          return tr(
            language,
            `近 30 天收益 ${m[1]}%，回撤 ${m[2]}%`,
            `30-day ROI ${m[1]}%, drawdown ${m[2]}%`
          )
      }
      if (code.startsWith('roi90=')) {
        const m = /roi90=(-?\d+),mdd90=(-?\d+)/.exec(code)
        if (m)
          return tr(
            language,
            `90 天收益 ${m[1]}%，回撤 ${m[2]}%`,
            `90-day ROI ${m[1]}%, drawdown ${m[2]}%`
          )
      }
      return code
  }
}

/** Comma-separated rule codes, labelled. */
export function rulesLabel(language: string, codes: string): string {
  // A pick reason is one item even though it has a comma.
  if (codes.startsWith('roi90=') || codes.startsWith('pick_roi30='))
    return ruleLabel(language, codes)
  return codes
    .split(',')
    .filter(Boolean)
    .map((c) => ruleLabel(language, c))
    .join(tr(language, '、', ', '))
}
/** Clock time only: the day is carried by the group heading. */
export function hhmm(ms: number): string {
  if (!ms) return '--'
  return new Date(ms).toLocaleTimeString('zh-CN', {
    hour: '2-digit',
    minute: '2-digit',
  })
}

/** A local calendar day key, for grouping events. */
export function dayKey(ms: number): string {
  const d = new Date(ms)
  return `${d.getFullYear()}-${d.getMonth() + 1}-${d.getDate()}`
}

/** 今天 / 昨天 / 11/15: keeps a repeated date out of every row. */
export function dayLabel(
  language: string,
  ms: number,
  now = Date.now()
): string {
  if (!ms) return '--'
  if (dayKey(ms) === dayKey(now)) return tr(language, '今天', 'Today')
  if (dayKey(ms) === dayKey(now - 24 * 3600 * 1000))
    return tr(language, '昨天', 'Yesterday')
  const d = new Date(ms)
  return `${String(d.getMonth() + 1).padStart(2, '0')}/${String(d.getDate()).padStart(2, '0')}`
}

export type Staleness = { level: 'ok' | 'warn' | 'bad'; ms: number }

/**
 * How old a timestamp is, in the terms this page reacts to: the monitor polls
 * every couple of minutes, so 15 minutes is already late and an hour is a gap
 * worth showing in red.
 */
export function staleness(ms: number, now = Date.now()): Staleness {
  if (!ms) return { level: 'bad', ms: 0 }
  const age = Math.max(0, now - ms)
  if (age >= 60 * 60 * 1000) return { level: 'bad', ms: age }
  if (age >= 15 * 60 * 1000) return { level: 'warn', ms: age }
  return { level: 'ok', ms: age }
}

/** Stock and commodity contracts, which a crypto-only strategy skips. */
export function marketLabel(language: string, tradfi?: boolean): string {
  if (!tradfi) return ''
  return tr(language, '股票/商品', 'Stock/commodity')
}

/**
 * The current time. Kept here so components read the clock through a function
 * instead of calling Date.now during render (React's purity rule), the same way
 * ago() already does.
 */
export function currentTime(): number {
  return Date.now()
}
