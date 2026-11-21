import { useMemo, useRef, useState } from 'react'
import useSWR from 'swr'
import {
  AlertTriangle,
  ExternalLink,
  Loader2,
  MoreHorizontal,
  Search,
  Trash2,
} from 'lucide-react'
import { api } from '../../lib/api'
import type {
  TradeEvent,
  TraderPosition,
  TraderSnapshot,
} from '../../lib/api/copyTrade'
import { confirmToast, notify } from '../../lib/notify'
import { tr, ui } from '../market/marketUi'
import { NoticeText } from '../NoticeText'
import { HealthTag, ScoreBadge } from './ConfidenceUi'
import { BoardSyncPanel } from './BoardSyncPanel'
import {
  actionClass,
  actionLabel,
  ago,
  base,
  currentTime,
  dayKey,
  dayLabel,
  hhmm,
  marketLabel,
  pct,
  price,
  rulesLabel,
  sideClass,
  sideLabel,
  signedUsd,
  staleness,
  styleHint,
  styleLabel,
  usd,
} from './format'

const traderPage = (id: string) =>
  `https://www.binance.com/zh-CN/copy-trading/lead-details/${id}`

type Tone = 'danger' | 'gold' | 'muted' | 'success'

const toneClass: Record<Tone, string> = {
  danger: 'bg-nofx-danger/10 text-nofx-danger',
  gold: 'bg-nofx-gold/10 text-nofx-gold',
  muted: 'bg-nofx-bg-deeper text-nofx-text-muted',
  success: 'bg-nofx-success/10 text-nofx-success',
}

/** A small label; focusable when it carries a tooltip, so touch and keyboard
 *  users get the explanation that hover shows. */
function Tag({
  tone,
  text,
  hint,
}: {
  tone: Tone
  text: string
  hint?: string
}) {
  return (
    <span
      tabIndex={hint ? 0 : undefined}
      title={hint}
      aria-label={hint ? `${text}: ${hint}` : undefined}
      className={`whitespace-nowrap rounded px-1.5 text-[11px] ${toneClass[tone]}`}
    >
      {text}
    </span>
  )
}

/** One number with its label above it, so cards can be read down the page. */
function Stat({
  label,
  value,
  valueClass,
  hint,
}: {
  label: string
  value: string
  valueClass?: string
  hint?: string
}) {
  return (
    <span className="inline-flex flex-col leading-tight" title={hint}>
      <span className="text-[11px] text-nofx-text-muted">{label}</span>
      <span
        className={`text-[13px] font-semibold tabular-nums ${valueClass || 'text-nofx-text'}`}
      >
        {value}
      </span>
    </span>
  )
}

/** Freshness of a card's own data, in the terms the page reacts to. */
function freshness(snap: TraderSnapshot, now: number) {
  const t = snap.trader
  // These never update: a private or closed portfolio has nothing to refresh,
  // and a dormant trader (holding nothing, idle for days) is polled slowly on
  // purpose. Being on schedule is not being stale.
  const dormant =
    (snap.positions?.length || 0) === 0 &&
    t.last_trade_time > 0 &&
    now - t.last_trade_time > 3 * 24 * 3600 * 1000
  if (
    t.private ||
    (t.status && t.status.toUpperCase() !== 'ACTIVE') ||
    dormant
  ) {
    return { level: 'ok' as const, ms: 0 }
  }
  const at = Date.parse(snap.updated_at) || 0
  return staleness(at, now)
}

const freshnessClass = (level: 'ok' | 'warn' | 'bad') =>
  level === 'bad'
    ? 'text-nofx-danger'
    : level === 'warn'
      ? 'text-nofx-gold'
      : 'text-nofx-text-muted'

/** Positions and actions grouped by calendar day. */
function groupByDay(events: TradeEvent[], language: string, now: number) {
  const groups: { label: string; items: TradeEvent[] }[] = []
  for (const e of events) {
    const label = dayLabel(language, e.time, now)
    const last = groups[groups.length - 1]
    if (last && last.label === label) last.items.push(e)
    else groups.push({ label, items: [e] })
  }
  return groups
}

function TraderCard({
  snap,
  language,
  onChanged,
  onSymbol,
  selected,
  onSelect,
}: {
  snap: TraderSnapshot
  language: string
  onChanged: () => void
  onSymbol: (symbol: string) => void
  selected: boolean
  onSelect: (next: boolean) => void
}) {
  const t = snap.trader
  const name = t.nickname || t.portfolio_id
  const positions = snap.positions || []
  const [menu, setMenu] = useState(false)
  const [expanded, setExpanded] = useState(false)
  const now = currentTime()
  const loaded = !snap.updated_at.startsWith('0001')
  const fresh = freshness(snap, now)
  // The list endpoint sends the newest actions only; opening one card asks for
  // the full set, so collapsed cards stay cheap.
  const { data: detail, isLoading: loadingMore } = useSWR(
    expanded ? ['copytrade/follow', t.portfolio_id] : null,
    () => api.getFollow(t.portfolio_id),
    { revalidateOnFocus: false, refreshInterval: expanded ? 15000 : 0 }
  )
  const events =
    (expanded && detail?.events?.length ? detail.events : snap.events) || []
  const shown = events.slice(0, expanded ? 20 : 3)
  const todayRealized = expanded
    ? events
        .filter((e) => dayKey(e.time) === dayKey(now))
        .reduce((sum, e) => sum + (e.realized_pnl || 0), 0)
    : 0

  const unfollow = async () => {
    setMenu(false)
    const ok = await confirmToast(
      tr(
        language,
        `取消关注「${name}」？已保存的成交记录会一并删除，且此人会被永久排除、不会因榜单同步再加回。`,
        `Unfollow "${name}"? Stored trades are deleted too, and this trader is excluded for good, so the board sync will not add them back.`
      ),
      {
        title: tr(language, '取消关注', 'Unfollow'),
        okText: tr(language, '取消关注', 'Unfollow'),
        cancelText: tr(language, '保留', 'Keep'),
      }
    )
    if (!ok) return
    try {
      await api.unfollow(t.portfolio_id)
      onChanged()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : String(e))
    }
  }
  // Follows the public twin, then drops the private portfolio.
  const followPublic = async (id: string) => {
    try {
      await api.follow(id).catch((e: unknown) => {
        const msg = e instanceof Error ? e.message : String(e)
        if (!msg.includes('already following')) throw e
      })
      await api.unfollow(t.portfolio_id)
      onChanged()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : String(e))
    }
  }
  const copyLink = async () => {
    try {
      await navigator.clipboard.writeText(traderPage(t.portfolio_id))
      notify.success(tr(language, '主页链接已复制', 'Profile link copied'))
    } catch {
      notify.error(tr(language, '复制失败', 'Copy failed'))
    }
    setMenu(false)
  }
  const emptyPositions = () => {
    if (snap.positions_source === 'public')
      return tr(language, '没有持仓', 'No positions')
    if (!snap.history_complete)
      return tr(
        language,
        '成交还在回填，暂时看不到推算的持仓',
        'Trades are still backfilling, so inferred positions are not complete yet'
      )
    return tr(
      language,
      '监控以来没有看到未平的仓位',
      'No open positions seen since monitoring started'
    )
  }

  return (
    <section
      className={`${ui.panel} relative`}
      aria-labelledby={`trader-${t.portfolio_id}`}
    >
      <header className="flex items-start gap-2">
        <input
          type="checkbox"
          checked={selected}
          onChange={(e) => onSelect(e.target.checked)}
          aria-label={tr(
            language,
            `选择「${name}」用于批量操作`,
            `Select "${name}" for bulk actions`
          )}
          className="mt-1 h-3.5 w-3.5 shrink-0 accent-nofx-gold"
        />
        {t.avatar_url ? (
          <img
            src={t.avatar_url}
            alt=""
            className="h-7 w-7 rounded-full"
            referrerPolicy="no-referrer"
          />
        ) : null}
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <h3
              id={`trader-${t.portfolio_id}`}
              className="min-w-0 text-sm font-semibold"
            >
              <a
                href={traderPage(t.portfolio_id)}
                target="_blank"
                rel="noreferrer"
                className="inline-flex min-w-0 max-w-full items-center gap-1 text-nofx-text hover:underline"
              >
                <span className="truncate">{name}</span>
                <ExternalLink className="h-3 w-3 shrink-0 opacity-50" />
              </a>
            </h3>
            <ScoreBadge score={snap.score} language={language} />
            {snap.review?.length ? (
              <Tag
                tone="danger"
                text={tr(
                  language,
                  `${
                    t.source === 'manual'
                      ? tr(language, '建议移除', 'Consider dropping')
                      : tr(language, '不达标', 'Below the bar')
                  }：${rulesLabel(language, snap.review.join(','))}`,
                  `${
                    t.source === 'manual'
                      ? 'Consider dropping'
                      : 'Below the bar'
                  }: ${rulesLabel(language, snap.review.join(','))}`
                )}
              />
            ) : null}
            {snap.probation ? (
              <Tag
                tone="gold"
                text={tr(language, '观察期', 'Probation')}
                hint={tr(
                  language,
                  '自动关注新选的人，观察 7 天，期间投票只算一半',
                  'New auto pick, watched for 7 days; its votes count half'
                )}
              />
            ) : null}
            {t.private ? (
              <Tag
                tone="gold"
                text={tr(language, '私域', 'Private')}
                hint={tr(
                  language,
                  '币安不公开私域账户的成交和持仓，这里不会有数据',
                  'Binance hides private portfolios: no trades or positions ever show here'
                )}
              />
            ) : null}
            {t.status && t.status !== 'ACTIVE' ? (
              <Tag
                tone="danger"
                text={
                  t.status === 'CLOSED'
                    ? tr(language, '已结束带单', 'Closed')
                    : tr(language, '带单状态异常', t.status)
                }
              />
            ) : null}
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1">
            <Stat
              label={tr(language, '30 天', '30d')}
              value={pct(t.roi)}
              valueClass={t.roi >= 0 ? 'text-nofx-success' : 'text-nofx-danger'}
            />
            <Stat
              label={tr(language, '最大回撤', 'Max DD')}
              value={`${t.mdd.toFixed(1)}%`}
            />
            <Stat
              label={tr(language, '胜率', 'Win rate')}
              value={`${t.win_rate.toFixed(0)}%`}
              hint={tr(
                language,
                '币安口径：30 天里盈利的平仓笔数占比。样本少时容易是 100%。',
                "Binance's figure: share of closed trades in profit over 30 days. Small samples read as 100%."
              )}
            />
            {snap.accuracy && snap.accuracy.calls >= 10 ? (
              <span className="inline-flex flex-col leading-tight">
                <span className="text-[11px] text-nofx-text-muted">
                  {tr(language, '自身命中', 'Own calls')}
                </span>
                <span
                  tabIndex={0}
                  className={`text-[13px] font-semibold tabular-nums ${
                    snap.accuracy.rate >= 0.55
                      ? 'text-nofx-success'
                      : snap.accuracy.rate <= 0.45
                        ? 'text-nofx-danger'
                        : 'text-nofx-text'
                  }`}
                  title={tr(
                    language,
                    `30 天里他自己的 ${snap.accuracy.calls} 次开仓/加仓（24 小时内重复的算一次），24 小时后方向对了 ${snap.accuracy.hits} 次，平均顺向 ${(snap.accuracy.avg_return * 100).toFixed(2)}%。准的人投票更重，满 30 次且明显低于 50% 会被自动关注淘汰。`,
                    `Of their own ${snap.accuracy.calls} opens/adds in 30 days (repeats within 24h count once), ${snap.accuracy.hits} moved their way 24h later, average ${(snap.accuracy.avg_return * 100).toFixed(2)}%. Accurate traders vote more; 30+ calls clearly under 50% get dropped by auto-follow.`
                  )}
                >
                  {Math.round(snap.accuracy.rate * 100)}%
                  <span className="ml-1 text-[11px] font-normal text-nofx-text-muted">
                    {tr(
                      language,
                      `${snap.accuracy.calls} 次`,
                      `${snap.accuracy.calls} calls`
                    )}
                  </span>
                </span>
              </span>
            ) : null}
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-nofx-text-muted">
            {styleLabel(language, snap.style?.kind) ? (
              <span title={styleHint(language, snap.style?.kind)}>
                {styleLabel(language, snap.style?.kind)}
              </span>
            ) : null}
            {snap.high_freq ? (
              <span
                tabIndex={0}
                title={tr(
                  language,
                  '近 24 小时动作很多，多半是程序或网格交易：仓位来回开平，置信度里权重只算 0.4，实时动态默认隐藏。',
                  'Many actions in 24h, most likely a bot or grid: positions churn, so its confidence weight is 0.4 and the feed hides it by default.'
                )}
              >
                {tr(
                  language,
                  `高频 · 每天 ${snap.orders_per_day} 单`,
                  `High-freq · ${snap.orders_per_day}/day`
                )}
              </span>
            ) : null}
            <span
              tabIndex={0}
              title={
                t.source === 'manual'
                  ? tr(
                      language,
                      '你手动关注的，榜单同步不会取关他',
                      'You followed this one; the board sync never drops it'
                    )
                  : t.source === 'board'
                    ? tr(
                        language,
                        '榜单同步关注的，掉出榜单或连续缺席会取关',
                        'Followed by the board sync; dropped on falling out or missing days'
                      )
                    : tr(
                        language,
                        '自动关注管理的，不达标会被淘汰',
                        'Managed by auto-follow; dropped when it falls below the bar'
                      )
              }
            >
              {t.source === 'manual'
                ? tr(language, '手动关注', 'Manual')
                : t.source === 'board'
                  ? tr(language, '榜单关注', 'Board')
                  : tr(language, '自动关注', 'Auto')}
            </span>
          </div>
        </div>
        <div className="relative flex shrink-0 items-center gap-1">
          <span
            className={`whitespace-nowrap text-[11px] ${freshnessClass(fresh.level)}`}
          >
            {loaded ? `${ago(language, Date.parse(snap.updated_at))}` : ''}
          </span>
          <button
            type="button"
            className={`${ui.btnGhost} relative inline-flex items-center px-1.5`}
            aria-label={tr(language, '更多操作', 'More actions')}
            aria-expanded={menu}
            onClick={() => setMenu(!menu)}
          >
            <MoreHorizontal className="h-4 w-4" />
          </button>
          {menu ? (
            <>
              <button
                type="button"
                aria-hidden="true"
                tabIndex={-1}
                className="fixed inset-0 z-10 cursor-default"
                onClick={() => setMenu(false)}
              />
              <div className="absolute right-0 top-full z-20 mt-1 w-44 rounded-lg border border-[rgba(25,36,52,0.14)] bg-nofx-bg-lighter p-1 text-xs shadow-lg">
                <a
                  href={traderPage(t.portfolio_id)}
                  target="_blank"
                  rel="noreferrer"
                  className="block rounded px-2 py-1.5 text-nofx-text hover:bg-nofx-bg-deeper"
                >
                  {tr(language, '在币安查看主页', 'Open on Binance')}
                </a>
                <button
                  type="button"
                  onClick={() => void copyLink()}
                  className="block w-full rounded px-2 py-1.5 text-left text-nofx-text hover:bg-nofx-bg-deeper"
                >
                  {tr(language, '复制主页链接', 'Copy profile link')}
                </button>
                <button
                  type="button"
                  onClick={() => void unfollow()}
                  className="block w-full rounded px-2 py-1.5 text-left text-nofx-danger hover:bg-nofx-danger/10"
                >
                  {tr(language, '取消关注', 'Unfollow')}
                </button>
              </div>
            </>
          ) : null}
        </div>
      </header>
      {snap.error ? (
        <p role="alert" className="mb-2 mt-2 text-xs text-nofx-danger">
          <NoticeText text={snap.error} />
        </p>
      ) : null}
      {t.private ? (
        <div className="mt-2 rounded bg-nofx-gold/10 px-2 py-2 text-xs text-nofx-text">
          {tr(
            language,
            '这是私域带单账户：币安不公开它的成交和持仓，这里永远不会有数据，也不计入币种统计。',
            'This is a private copy-trading portfolio: Binance hides its trades and positions, so nothing will ever show here and it is left out of coin stats.'
          )}{' '}
          {t.public_id ? (
            <button
              type="button"
              className="font-medium text-nofx-gold hover:underline"
              onClick={() => void followPublic(t.public_id || '')}
            >
              {tr(
                language,
                '改为关注他的公域账户',
                'Follow the public portfolio instead'
              )}
            </button>
          ) : (
            tr(
              language,
              '他没有公域账户，建议取消关注。',
              'The trader has no public portfolio; consider unfollowing.'
            )
          )}
        </div>
      ) : !loaded && !snap.error ? (
        <p className={`${ui.muted} mt-2 flex items-center gap-1 py-2`}>
          <Loader2 className="h-3 w-3 animate-spin" />
          {tr(
            language,
            '正在拉取最近 30 天的成交…',
            'Fetching the last 30 days of trades…'
          )}
        </p>
      ) : (
        <div className="mt-2">
          <div className="mb-1 flex flex-wrap items-center gap-2 text-xs">
            <span className="font-medium text-nofx-text">
              {tr(language, '当前持仓', 'Positions')}
              {positions.length ? (
                <span className="ml-1 tabular-nums text-nofx-text-muted">
                  {positions.length}
                </span>
              ) : null}
            </span>
            <Tag
              tone={snap.positions_source === 'public' ? 'success' : 'gold'}
              text={
                snap.positions_source === 'public'
                  ? tr(language, '公开持仓', 'Public positions')
                  : tr(language, '推算持仓', 'Inferred positions')
              }
              hint={
                snap.positions_source === 'public'
                  ? tr(
                      language,
                      '币安公开了这个账户的持仓：数量和均价都以币安为准',
                      'Binance publishes this book: sizes and averages come from it'
                    )
                  : tr(
                      language,
                      '币安不公开持仓，这里由监控以来的成交推算，可能有出入',
                      'Positions are hidden, so these are inferred from trades seen since monitoring started'
                    )
              }
            />
            <span className={`tabular-nums ${freshnessClass(fresh.level)}`}>
              {tr(
                language,
                `更新 ${ago(language, Date.parse(snap.updated_at))}`,
                `updated ${ago(language, Date.parse(snap.updated_at))}`
              )}
            </span>
            {!snap.history_complete ? (
              <span
                tabIndex={0}
                className="inline-flex items-center gap-1 text-nofx-gold"
                title={tr(
                  language,
                  '30 天成交还没读完，这期间的平仓可能没记上，推算的持仓会偏多或偏少。',
                  'The 30-day backfill is incomplete: closes in that window may be missing, so inferred positions can be off.'
                )}
              >
                <AlertTriangle className="h-3 w-3" />
                {tr(language, '成交未回填完', 'Backfill incomplete')}
              </span>
            ) : null}
          </div>
          {positions.length ? (
            <div className="overflow-x-auto">
              <table className="w-full text-xs">
                <thead>
                  <tr className="text-left text-[11px] text-nofx-text-muted">
                    <th className="py-0.5 pr-2 font-normal">
                      {tr(language, '币种', 'Coin')}
                    </th>
                    <th className="pr-2 font-normal">
                      {tr(language, '方向', 'Side')}
                    </th>
                    <th className="pr-2 text-right font-normal">
                      {tr(language, '仓位', 'Size')}
                    </th>
                    <th className="pr-2 text-right font-normal">
                      {tr(language, '浮动盈亏', 'Open PnL')}
                    </th>
                    <th className="font-normal">
                      {tr(language, '状态', 'State')}
                    </th>
                  </tr>
                </thead>
                <tbody className="text-nofx-text">
                  {(expanded ? positions : positions.slice(0, 6)).map(
                    (p: TraderPosition) => (
                      <tr
                        key={p.symbol + p.side}
                        className={p.liquidated ? 'text-nofx-text-muted' : ''}
                      >
                        <td className="whitespace-nowrap py-0.5 pr-2">
                          <button
                            type="button"
                            className="font-medium hover:underline"
                            onClick={() => onSymbol(p.symbol)}
                          >
                            {base(p.symbol)}
                          </button>
                          {p.tradfi ? (
                            <span
                              tabIndex={0}
                              title={tr(
                                language,
                                '股票或商品合约：策略若设了只做加密货币，不会开这类币',
                                'A stock or commodity contract: a crypto-only strategy skips it'
                              )}
                              className="ml-1 rounded bg-nofx-bg-deeper px-1 text-[11px] text-nofx-text-muted"
                            >
                              {tr(language, '股', 'Stk')}
                            </span>
                          ) : null}
                        </td>
                        <td
                          className={`whitespace-nowrap pr-2 ${sideClass(p.side)}`}
                          title={tr(
                            language,
                            p.leverage
                              ? `${p.leverage} 倍杠杆`
                              : '杠杆未知（推算持仓）',
                            p.leverage
                              ? `${p.leverage}x leverage`
                              : 'Leverage unknown (inferred)'
                          )}
                        >
                          {sideLabel(language, p.side)}
                          {p.leverage ? (
                            <span className="ml-0.5 text-[11px] text-nofx-text-muted">
                              {p.leverage}x
                            </span>
                          ) : null}
                        </td>
                        <td className="whitespace-nowrap pr-2 text-right tabular-nums">
                          {usd(p.entry_value)}
                          <span className="ml-1 hidden text-[11px] text-nofx-text-muted sm:inline">
                            @ {price(p.entry_price)}
                          </span>
                        </td>
                        <td
                          className={`whitespace-nowrap pr-2 text-right tabular-nums ${
                            p.unrealized_pnl
                              ? p.unrealized_pnl >= 0
                                ? 'text-nofx-success'
                                : 'text-nofx-danger'
                              : ''
                          }`}
                        >
                          {p.priced && p.unrealized_pnl
                            ? signedUsd(p.unrealized_pnl)
                            : '--'}
                          {p.priced ? (
                            <span
                              tabIndex={0}
                              title={
                                p.pct_of_margin
                                  ? tr(
                                      language,
                                      '按保证金计的浮动收益率',
                                      'Open return on margin'
                                    )
                                  : tr(
                                      language,
                                      '按开仓价计的浮动收益率',
                                      'Open return on entry price'
                                    )
                              }
                              className="ml-1 text-[11px] opacity-80"
                            >
                              {pct(p.return_pct)}
                            </span>
                          ) : null}
                        </td>
                        <td className="align-top">
                          <HealthTag h={p} language={language} />
                        </td>
                      </tr>
                    )
                  )}
                </tbody>
              </table>
              {!expanded && positions.length > 6 ? (
                <button
                  type="button"
                  className={`${ui.muted} mt-0.5 text-left hover:text-nofx-gold hover:underline`}
                  onClick={() => setExpanded(true)}
                >
                  {tr(
                    language,
                    `还有 ${positions.length - 6} 个持仓，展开查看`,
                    `${positions.length - 6} more positions, show all`
                  )}
                </button>
              ) : null}
            </div>
          ) : (
            <p className={ui.muted}>{emptyPositions()}</p>
          )}
          {events.length ? (
            <div className="mt-2 border-t border-[rgba(25,36,52,0.08)] pt-1.5 text-xs">
              <div className="mb-1 flex flex-wrap items-center gap-2">
                <span className="font-medium text-nofx-text">
                  {tr(language, '最近动作', 'Recent actions')}
                </span>
                {expanded && todayRealized ? (
                  <span
                    tabIndex={0}
                    className={`tabular-nums ${
                      todayRealized >= 0
                        ? 'text-nofx-success'
                        : 'text-nofx-danger'
                    }`}
                    title={tr(
                      language,
                      '只统计已经拉到的成交，可能不是今天的全部',
                      'Only trades already loaded; may not be the whole day'
                    )}
                  >
                    {tr(language, '今日已实现 ', 'Today realised ')}
                    {signedUsd(todayRealized)}
                  </span>
                ) : null}
              </div>
              <div className="space-y-1">
                {groupByDay(shown, language, now).map((group) => (
                  <div key={group.label}>
                    <div className="text-[11px] text-nofx-text-muted">
                      {group.label}
                    </div>
                    {group.items.map((e, i) => (
                      <div
                        key={`${e.time}-${e.symbol}-${i}`}
                        className="flex flex-wrap items-baseline gap-x-2 text-nofx-text"
                      >
                        <span className="tabular-nums text-nofx-text-muted">
                          {hhmm(e.time)}
                        </span>
                        <span
                          className={`font-medium ${actionClass(e.action, e.side)}`}
                        >
                          {actionLabel(language, e.action, e.side)}
                        </span>
                        <button
                          type="button"
                          className="hover:underline"
                          onClick={() => onSymbol(e.symbol)}
                        >
                          {base(e.symbol)}
                        </button>
                        {e.tradfi ? (
                          <span
                            className="rounded bg-nofx-bg-deeper px-1 text-[11px] text-nofx-text-muted"
                            title={tr(
                              language,
                              marketLabel(language, true),
                              marketLabel(language, true)
                            )}
                          >
                            {tr(language, '股', 'Stk')}
                          </span>
                        ) : null}
                        <span className="tabular-nums text-nofx-text-muted">
                          {usd(e.value)} @ {price(e.price)}
                        </span>
                        {e.realized_pnl ? (
                          <span
                            className={`tabular-nums ${
                              e.realized_pnl >= 0
                                ? 'text-nofx-success'
                                : 'text-nofx-danger'
                            }`}
                          >
                            {signedUsd(e.realized_pnl)}
                          </span>
                        ) : null}
                      </div>
                    ))}
                  </div>
                ))}
              </div>
              {events.length > 3 || expanded ? (
                <button
                  type="button"
                  className="mt-1 inline-flex items-center gap-1 text-nofx-gold hover:underline"
                  onClick={() => setExpanded(!expanded)}
                >
                  {loadingMore ? (
                    <Loader2 className="h-3 w-3 animate-spin" />
                  ) : null}
                  {expanded
                    ? tr(language, '收起动作', 'Show less')
                    : tr(
                        language,
                        `展开更多动作${detail?.events?.length ? `（${detail.events.length}）` : ''}`,
                        'Show more actions'
                      )}
                </button>
              ) : null}
            </div>
          ) : null}
        </div>
      )}
    </section>
  )
}

type SortKey = 'score' | 'roi' | 'followed' | 'recent'
type FilterKey =
  | 'positions'
  | 'active'
  | 'probation'
  | 'flagged'
  | 'nodata'
  | 'manual'

const FILTERS: { key: FilterKey; zh: string; en: string }[] = [
  { key: 'positions', zh: '有持仓', en: 'Has positions' },
  { key: 'active', zh: '24h 有动作', en: 'Active 24h' },
  { key: 'flagged', zh: '不达标', en: 'Below the bar' },
  { key: 'probation', zh: '观察期', en: 'Probation' },
  { key: 'nodata', zh: '数据不全', en: 'Data gaps' },
  { key: 'manual', zh: '手动关注', en: 'Manual' },
]

/** The value a card is ordered by, frozen so a refresh cannot move it out from
 *  under the pointer. */
function sortValue(snap: TraderSnapshot, sort: SortKey): number {
  switch (sort) {
    case 'roi':
      return snap.trader.roi
    case 'followed':
      return Date.parse(snap.trader.followed_at) || 0
    case 'recent':
      return snap.events?.[0]?.time || 0
    default:
      return snap.score?.score || 0
  }
}

const matchesFilter = (snap: TraderSnapshot, key: FilterKey, now: number) => {
  switch (key) {
    case 'positions':
      return (snap.positions?.length || 0) > 0
    case 'active':
      return (snap.events?.[0]?.time || 0) > now - 24 * 3600 * 1000
    case 'probation':
      return Boolean(snap.probation)
    case 'flagged':
      return Boolean(snap.review?.length)
    case 'manual':
      return snap.trader.source === 'manual'
    default:
      return (
        Boolean(snap.trader.private) ||
        !snap.history_complete ||
        Boolean(snap.error)
      )
  }
}

export function FollowingTab({
  language,
  snaps,
  onChanged,
  onSymbol,
  onDiscover,
  loading,
  error,
}: {
  language: string
  snaps: TraderSnapshot[]
  onChanged: () => void
  onSymbol: (symbol: string) => void
  onDiscover: () => void
  loading?: boolean
  error?: boolean
}) {
  const [sort, setSort] = useState<SortKey>('score')
  // Filter by trading style: scalp | swing | long | martingale | few_samples.
  const [style, setStyle] = useState('')
  const [query, setQuery] = useState('')
  const [filters, setFilters] = useState<FilterKey[]>([])
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [bulkBusy, setBulkBusy] = useState(false)
  const [orderVersion, setOrderVersion] = useState(0)
  // Order keys are frozen per trader: the snapshot refreshes every few seconds
  // and re-sorting mid-click has already put the trash button on someone else.
  const order = useRef<{ key: string; map: Map<string, number> }>({
    key: '',
    map: new Map(),
  })
  const now = currentTime()

  const { list, shifted } = useMemo(() => {
    const key = `${sort}|${style}|${orderVersion}`
    if (order.current.key !== key) order.current = { key, map: new Map() }
    const frozen = order.current.map
    let moved = 0
    for (const s of snaps) {
      const id = s.trader.portfolio_id
      const v = sortValue(s, sort)
      const prev = frozen.get(id)
      // Only the score order is frozen: it is the default, it moves on its own
      // every few seconds, and a card sliding under the pointer is a misclick.
      // The other orders follow their own value as it changes.
      if (prev === undefined) frozen.set(id, v)
      else if (sort === 'score') {
        if (Math.abs(prev - v) >= 2) moved++
      } else frozen.set(id, v)
    }
    const q = query.trim().toLowerCase()
    const next = snaps.filter((s) => {
      if (style && s.style?.kind !== style) return false
      if (filters.some((f) => !matchesFilter(s, f, now))) return false
      if (!q) return true
      const name = (s.trader.nickname || '').toLowerCase()
      if (name.includes(q)) return true
      const symbols = [
        ...(s.positions || []).map((p) => p.symbol),
        ...(s.events || []).map((e) => e.symbol),
      ]
      return symbols.some((sym) => base(sym).toLowerCase().includes(q))
    })
    next.sort(
      (a, b) =>
        (frozen.get(b.trader.portfolio_id) || 0) -
        (frozen.get(a.trader.portfolio_id) || 0)
    )
    return { list: next, shifted: moved }
  }, [snaps, sort, style, query, filters, orderVersion, now])

  const toggleFilter = (key: FilterKey) =>
    setFilters((prev) =>
      prev.includes(key) ? prev.filter((k) => k !== key) : [...prev, key]
    )
  const toggleSelect = (id: string, next: boolean) =>
    setSelected((prev) => {
      const copy = new Set(prev)
      if (next) copy.add(id)
      else copy.delete(id)
      return copy
    })
  const clearFilters = () => {
    setQuery('')
    setFilters([])
    setStyle('')
  }
  const bulkUnfollow = async () => {
    // Only the traders currently shown: a hidden card must never be unfollowed
    // by a stale selection after the filter changed.
    const visible = new Set(list.map((s) => s.trader.portfolio_id))
    const ids = [...selected].filter((id) => visible.has(id))
    if (!ids.length) return
    const names = list
      .filter((s) => ids.includes(s.trader.portfolio_id))
      .map((s) => s.trader.nickname || s.trader.portfolio_id)
    const shown = names.slice(0, 10).join('、')
    const more = names.length > 10 ? ` 等 ${names.length} 人` : ''
    const ok = await confirmToast(
      tr(
        language,
        `取消关注这 ${ids.length} 人：${shown}${more}？已保存的成交记录会一并删除，且这些人会被永久排除、不会因榜单同步再加回。`,
        `Unfollow these ${ids.length}: ${shown}${more}? Stored trades are deleted too, and they are excluded for good, so the board sync will not add them back.`
      ),
      {
        title: tr(language, '批量取消关注', 'Unfollow selected'),
        okText: tr(language, '取消关注', 'Unfollow'),
        cancelText: tr(language, '取消', 'Cancel'),
      }
    )
    if (!ok) return
    setBulkBusy(true)
    try {
      let failed = 0
      for (const id of ids) {
        try {
          await api.unfollow(id)
        } catch {
          failed++
        }
      }
      setSelected(new Set())
      onChanged()
      if (failed)
        notify.error(
          tr(
            language,
            `有 ${failed} 人取消失败，可能是已经不在关注列表里`,
            `${failed} could not be unfollowed; they may already be gone`
          )
        )
    } finally {
      setBulkBusy(false)
    }
  }

  if (!snaps.length) {
    return (
      <div>
        <BoardSyncPanel language={language} onChanged={onChanged} />
        <div className={`${ui.panel} py-10 text-center ${ui.muted}`}>
          {loading ? (
            <p>{tr(language, '正在加载关注列表…', 'Loading follows…')}</p>
          ) : error ? (
            <p>
              {tr(
                language,
                '关注列表加载失败，请稍后重试；这不代表你没有关注任何人。',
                'Could not load the follow list; this does not mean you follow no one.'
              )}
            </p>
          ) : (
            <>
              <p>
                {tr(
                  language,
                  '还没有关注任何带单员。榜单同步会自动关注，或到「发现交易员」里挑选，或直接粘贴带单员主页链接。',
                  'Not following anyone yet. The board sync follows automatically, or pick traders under "Discover", or paste a lead trader page link.'
                )}
              </p>
              <div className="mt-3 flex justify-center">
                <button
                  type="button"
                  className={ui.btnGhost}
                  onClick={onDiscover}
                >
                  {tr(language, '去发现交易员', 'Discover traders')}
                </button>
              </div>
            </>
          )}
        </div>
      </div>
    )
  }
  const filtered = Boolean(query || filters.length || style)
  return (
    <div>
      <BoardSyncPanel language={language} onChanged={onChanged} />
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <label className="relative flex items-center">
          <Search className="pointer-events-none absolute left-2 h-3.5 w-3.5 text-nofx-text-muted" />
          <input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={tr(language, '搜昵称或币种', 'Name or coin')}
            aria-label={tr(language, '搜索带单员', 'Search traders')}
            className={`${ui.inputInline} w-44`}
            style={{ paddingLeft: '1.75rem' }}
          />
        </label>
        <select
          className={`${ui.inputInline}`}
          value={sort}
          onChange={(e) => setSort(e.target.value as SortKey)}
          aria-label={tr(language, '排序', 'Sort')}
        >
          <option value="score">
            {tr(language, '可信度最高', 'Highest score')}
          </option>
          <option value="roi">
            {tr(language, '30 天收益最高', 'Best 30-day ROI')}
          </option>
          <option value="recent">
            {tr(language, '最近有动作', 'Latest action')}
          </option>
          <option value="followed">
            {tr(language, '关注时间', 'Follow order')}
          </option>
        </select>
        <select
          className={`${ui.inputInline}`}
          value={style}
          onChange={(e) => setStyle(e.target.value)}
          aria-label={tr(language, '风格', 'Style')}
        >
          <option value="">{tr(language, '全部风格', 'All styles')}</option>
          <option value="scalp">{tr(language, '短线', 'Scalp')}</option>
          <option value="swing">{tr(language, '波段', 'Swing')}</option>
          <option value="long">{tr(language, '长线', 'Long')}</option>
          <option value="martingale">
            {tr(language, '摊平型', 'Averages down')}
          </option>
          <option value="few_samples">
            {tr(language, '样本不足', 'Few samples')}
          </option>
        </select>
        {shifted ? (
          <button
            type="button"
            className="inline-flex items-center gap-1 rounded-lg bg-nofx-gold/10 px-2 py-1 text-xs text-nofx-gold hover:underline"
            onClick={() => setOrderVersion((v) => v + 1)}
            title={tr(
              language,
              '排序按打开页面时的分数固定，避免刷新时卡片在鼠标下换位。点了才按最新分数重排。',
              'Order is frozen at the scores this page opened with, so a refresh cannot move a card out from under the pointer. Click to re-sort.'
            )}
          >
            {tr(
              language,
              `${shifted} 人分数有变化 · 重新排序`,
              `${shifted} scores changed · re-sort`
            )}
          </button>
        ) : null}
      </div>
      <div className="mb-2 flex flex-wrap items-center gap-1.5">
        {FILTERS.map((f) => {
          const on = filters.includes(f.key)
          return (
            <button
              key={f.key}
              type="button"
              aria-pressed={on}
              onClick={() => toggleFilter(f.key)}
              className={`rounded-lg border px-2 py-1 text-xs ${
                on
                  ? 'border-nofx-gold bg-nofx-gold/10 text-nofx-text'
                  : 'border-[rgba(25,36,52,0.14)] bg-nofx-bg-deeper text-nofx-text-muted hover:border-nofx-gold'
              }`}
            >
              {tr(language, f.zh, f.en)}
            </button>
          )
        })}
        {filtered ? (
          <button
            type="button"
            className="text-xs text-nofx-gold hover:underline"
            onClick={clearFilters}
          >
            {tr(language, '清除筛选', 'Clear')}
          </button>
        ) : null}
        <span className={`${ui.muted} ml-auto`}>
          {tr(
            language,
            `显示 ${list.length} / ${snaps.length} 人`,
            `${list.length} of ${snaps.length} shown`
          )}
        </span>
      </div>
      {selected.size ? (
        <div className="mb-2 flex flex-wrap items-center gap-2 rounded-lg bg-nofx-bg-deeper px-2 py-1.5 text-xs">
          <span className="text-nofx-text">
            {tr(
              language,
              `已选 ${selected.size} 人`,
              `${selected.size} selected`
            )}
          </span>
          <button
            type="button"
            className="text-nofx-gold hover:underline"
            onClick={() =>
              setSelected(new Set(list.map((s) => s.trader.portfolio_id)))
            }
          >
            {tr(language, '选中当前结果', 'Select shown')}
          </button>
          <button
            type="button"
            className="text-nofx-gold hover:underline"
            onClick={() => setSelected(new Set())}
          >
            {tr(language, '清空', 'Clear')}
          </button>
          <button
            type="button"
            disabled={bulkBusy}
            className="ml-auto inline-flex items-center gap-1 rounded-lg border border-nofx-danger/40 px-2 py-1 text-nofx-danger hover:bg-nofx-danger/10 disabled:opacity-50"
            onClick={() => void bulkUnfollow()}
          >
            {bulkBusy ? (
              <Loader2 className="h-3 w-3 animate-spin" />
            ) : (
              <Trash2 className="h-3 w-3" />
            )}
            {tr(language, '取消关注', 'Unfollow')}
          </button>
        </div>
      ) : null}
      <p className={`${ui.muted} mb-2`}>
        {tr(
          language,
          '可信度按 30 天收益与回撤之比、回撤、胜率、带单时长、资金规模、是否公开持仓打分（0–100）；点或聚焦分数看各项得分。',
          'Score (0–100) weighs 30-day ROI vs drawdown, drawdown, win rate, tenure, scale and whether positions are public; focus a score for its parts.'
        )}
      </p>
      {list.length ? (
        <div className="grid items-start gap-3 lg:grid-cols-2">
          {list.map((s) => (
            <TraderCard
              key={s.trader.portfolio_id}
              snap={s}
              language={language}
              onChanged={onChanged}
              onSymbol={onSymbol}
              selected={selected.has(s.trader.portfolio_id)}
              onSelect={(next) => toggleSelect(s.trader.portfolio_id, next)}
            />
          ))}
        </div>
      ) : (
        <p className={`${ui.panel} py-8 text-center ${ui.muted}`}>
          {tr(
            language,
            '没有符合当前筛选的人。',
            'No trader matches these filters.'
          )}{' '}
          <button
            type="button"
            className="text-nofx-gold hover:underline"
            onClick={clearFilters}
          >
            {tr(language, '清除筛选', 'Clear filters')}
          </button>
        </p>
      )}
    </div>
  )
}
