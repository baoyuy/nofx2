import { useMemo, useState } from 'react'
import { Loader2, Trash2 } from 'lucide-react'
import { api } from '../../lib/api'
import type { TraderSnapshot } from '../../lib/api/copyTrade'
import { confirmToast, notify } from '../../lib/notify'
import { tr, ui } from '../market/marketUi'
import { NoticeText } from '../NoticeText'
import { HealthTag, ScoreBadge } from './ConfidenceUi'
import { AutoFollowPanel } from './AutoFollowPanel'
import {
  actionClass,
  actionLabel,
  ago,
  base,
  clock,
  pct,
  price,
  rulesLabel,
  sideClass,
  sideLabel,
  signedUsd,
  styleHint,
  styleLabel,
  usd,
} from './format'

function TraderCard({
  snap,
  language,
  onChanged,
  onSymbol,
}: {
  snap: TraderSnapshot
  language: string
  onChanged: () => void
  onSymbol: (symbol: string) => void
}) {
  const t = snap.trader
  const positions = snap.positions || []
  const events = snap.events || []
  const loaded = !snap.updated_at.startsWith('0001')
  const unfollow = async () => {
    const ok = await confirmToast(
      tr(
        language,
        `取消关注「${t.nickname}」？已保存的成交记录会一并删除。`,
        `Unfollow "${t.nickname}"? Stored trades are deleted too.`
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
  return (
    <section className={ui.panel}>
      <header className="mb-2 flex flex-wrap items-center gap-2">
        {t.avatar_url ? (
          <img
            src={t.avatar_url}
            alt=""
            className="h-7 w-7 rounded-full"
            referrerPolicy="no-referrer"
          />
        ) : null}
        <a
          href={`https://www.binance.com/zh-CN/copy-trading/lead-details/${t.portfolio_id}`}
          target="_blank"
          rel="noreferrer"
          className="font-semibold text-nofx-text hover:underline"
        >
          {t.nickname || t.portfolio_id}
        </a>
        <span className={ui.muted}>
          {tr(language, '30天', '30d')}{' '}
          <span
            className={t.roi >= 0 ? 'text-nofx-success' : 'text-nofx-danger'}
          >
            {pct(t.roi)}
          </span>
          {' · '}
          {tr(language, '回撤', 'MDD')} {t.mdd.toFixed(1)}% ·{' '}
          {tr(language, '胜率', 'Win')} {t.win_rate.toFixed(0)}%
        </span>
        <ScoreBadge score={snap.score} language={language} />
        {snap.accuracy && snap.accuracy.calls >= 10 ? (
          <span
            className={`rounded bg-nofx-bg-deeper px-1.5 text-[11px] ${
              snap.accuracy.rate >= 0.55
                ? 'text-nofx-success'
                : snap.accuracy.rate <= 0.45
                  ? 'text-nofx-danger'
                  : 'text-nofx-text-muted'
            }`}
            title={tr(
              language,
              `30 天里他自己的 ${snap.accuracy.calls} 次开仓/加仓（24 小时内重复的算一次），24 小时后方向对了 ${snap.accuracy.hits} 次，平均顺向 ${(snap.accuracy.avg_return * 100).toFixed(2)}%。准的人投票更重，满 30 次且明显低于 50% 会被自动关注淘汰。`,
              `Of their own ${snap.accuracy.calls} opens/adds in 30 days (repeats within 24h count once), ${snap.accuracy.hits} moved their way 24h later, average ${(snap.accuracy.avg_return * 100).toFixed(2)}%. Accurate traders vote more; 30+ calls clearly under 50% get dropped by auto-follow.`
            )}
          >
            {tr(
              language,
              `准确率 ${Math.round(snap.accuracy.rate * 100)}% / ${snap.accuracy.calls} 次`,
              `Right ${Math.round(snap.accuracy.rate * 100)}% of ${snap.accuracy.calls}`
            )}
          </span>
        ) : null}
        {styleLabel(language, snap.style?.kind) ? (
          <span
            className={`rounded bg-nofx-bg-deeper px-1.5 text-[11px] ${
              snap.style?.kind === 'martingale'
                ? 'text-nofx-danger'
                : 'text-nofx-text-muted'
            }`}
            title={styleHint(language, snap.style?.kind)}
          >
            {styleLabel(language, snap.style?.kind)}
          </span>
        ) : null}
        {snap.high_freq ? (
          <span
            className="rounded bg-nofx-bg-deeper px-1.5 text-[11px] text-nofx-text-muted"
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
        {t.private ? (
          <span className="rounded bg-nofx-gold/10 px-1.5 text-[11px] text-nofx-gold">
            {tr(language, '私域', 'Private')}
          </span>
        ) : null}
        {t.status && t.status !== 'ACTIVE' ? (
          <span className="rounded bg-nofx-danger/10 px-1.5 text-[11px] text-nofx-danger">
            {t.status === 'CLOSED'
              ? tr(language, '已结束带单', 'Closed')
              : tr(language, '带单状态异常', t.status)}
          </span>
        ) : null}
        <span
          className="rounded bg-nofx-bg-deeper px-1.5 text-[11px] text-nofx-text-muted"
          title={
            t.source === 'manual'
              ? tr(
                  language,
                  '你手动关注的，自动关注不会取关他',
                  'You followed this one; auto-follow never drops it'
                )
              : tr(
                  language,
                  '自动关注管理的，不达标会被淘汰',
                  'Managed by auto-follow; dropped when it falls below the bar'
                )
          }
        >
          {t.source === 'manual'
            ? tr(language, '手动', 'Manual')
            : tr(language, '自动', 'Auto')}
        </span>
        {snap.probation ? (
          <span
            className="rounded bg-nofx-gold/10 px-1.5 text-[11px] text-nofx-gold"
            title={tr(
              language,
              '自动关注新选的人，观察 7 天，期间投票只算一半',
              'New auto pick, watched for 7 days; its votes count half'
            )}
          >
            {tr(language, '观察期', 'Probation')}
          </span>
        ) : null}
        {snap.review?.length ? (
          <span
            className="rounded bg-nofx-danger/10 px-1.5 text-[11px] text-nofx-danger"
            title={rulesLabel(language, snap.review.join(','))}
          >
            {t.source === 'manual'
              ? tr(language, '建议移除', 'Consider dropping')
              : tr(language, '不达标', 'Below the bar')}
            {'：'}
            {rulesLabel(language, snap.review.join(','))}
          </span>
        ) : null}
        <span className={`ml-auto ${ui.muted}`}>
          {loaded
            ? tr(
                language,
                `${ago(language, Date.parse(snap.updated_at))}更新`,
                `updated ${ago(language, Date.parse(snap.updated_at))}`
              )
            : ''}
        </span>
        <button
          type="button"
          className={`${ui.btnGhost} inline-flex items-center gap-1`}
          onClick={() => void unfollow()}
          aria-label={tr(language, '取消关注', 'Unfollow')}
        >
          <Trash2 className="h-3 w-3" />
        </button>
      </header>
      {snap.error ? (
        <p role="alert" className="mb-2 text-xs text-nofx-danger">
          <NoticeText text={snap.error} />
        </p>
      ) : null}
      {t.private ? (
        <div className="rounded bg-nofx-gold/10 px-2 py-2 text-xs text-nofx-text">
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
        <p className={`${ui.muted} flex items-center gap-1 py-3`}>
          <Loader2 className="h-3 w-3 animate-spin" />
          {tr(
            language,
            '正在拉取最近 30 天的成交…',
            'Fetching the last 30 days of trades…'
          )}
        </p>
      ) : (
        <>
          <div className="mb-1 flex items-center gap-2 text-xs font-medium text-nofx-text">
            {tr(language, '当前持仓', 'Positions')}
            <span
              className={`rounded px-1.5 text-[10px] font-normal ${snap.positions_source === 'public' ? 'bg-nofx-success/10 text-nofx-success' : 'bg-nofx-gold/10 text-nofx-gold'}`}
            >
              {snap.positions_source === 'public'
                ? tr(language, '公开', 'Public')
                : tr(language, '推算', 'Inferred')}
            </span>
          </div>
          {positions.length ? (
            <table className="mb-2 w-full text-xs">
              <tbody className="text-nofx-text">
                {positions.slice(0, 6).map((p) => (
                  <tr
                    key={p.symbol + p.side}
                    className={p.liquidated ? 'opacity-50' : ''}
                  >
                    <td className="py-0.5">
                      <button
                        type="button"
                        className="font-medium hover:underline"
                        onClick={() => onSymbol(p.symbol)}
                      >
                        {base(p.symbol)}
                      </button>
                    </td>
                    <td className={sideClass(p.side)}>
                      {sideLabel(language, p.side)}
                    </td>
                    <td>{p.leverage ? `${p.leverage}x` : ''}</td>
                    <td className="tabular-nums">{usd(p.entry_value)}</td>
                    <td className="tabular-nums">@ {price(p.entry_price)}</td>
                    <td
                      className={`tabular-nums ${p.unrealized_pnl ? (p.unrealized_pnl >= 0 ? 'text-nofx-success' : 'text-nofx-danger') : ''}`}
                    >
                      {p.unrealized_pnl ? signedUsd(p.unrealized_pnl) : ''}
                    </td>
                    <td>
                      <HealthTag h={p} language={language} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <p className={`${ui.muted} mb-2`}>
              {snap.positions_source === 'public'
                ? tr(language, '没有持仓', 'No positions')
                : tr(
                    language,
                    '监控以来没有看到未平的仓位',
                    'No open positions seen since monitoring started'
                  )}
            </p>
          )}
          {events.length ? (
            <div className="space-y-0.5 border-t border-[rgba(25,36,52,0.08)] pt-1.5 text-xs">
              {events.slice(0, 3).map((e, i) => (
                <div key={i} className="flex flex-wrap gap-x-2 text-nofx-text">
                  <span className={ui.muted}>{clock(e.time)}</span>
                  <span
                    className={`font-medium ${actionClass(e.action, e.side)}`}
                  >
                    {actionLabel(language, e.action, e.side)}
                  </span>
                  <span>{base(e.symbol)}</span>
                  <span className="tabular-nums">
                    {usd(e.value)} @ {price(e.price)}
                  </span>
                  {e.realized_pnl ? (
                    <span
                      className={
                        e.realized_pnl >= 0
                          ? 'text-nofx-success'
                          : 'text-nofx-danger'
                      }
                    >
                      {signedUsd(e.realized_pnl)}
                    </span>
                  ) : null}
                </div>
              ))}
            </div>
          ) : null}
        </>
      )}
    </section>
  )
}

export function FollowingTab({
  language,
  snaps,
  onChanged,
  onSymbol,
}: {
  language: string
  snaps: TraderSnapshot[]
  onChanged: () => void
  onSymbol: (symbol: string) => void
}) {
  const [sort, setSort] = useState<'followed' | 'score' | 'roi'>('score')
  // Filter by trading style: scalp | swing | long | martingale | few_samples.
  const [style, setStyle] = useState('')
  const sorted = useMemo(() => {
    const list = snaps.filter((s) => !style || s.style?.kind === style)
    if (sort === 'score')
      list.sort((a, b) => (b.score?.score || 0) - (a.score?.score || 0))
    if (sort === 'roi') list.sort((a, b) => b.trader.roi - a.trader.roi)
    if (sort === 'followed')
      list.sort(
        (a, b) =>
          new Date(b.trader.followed_at).getTime() -
          new Date(a.trader.followed_at).getTime()
      )
    return list
  }, [snaps, sort, style])
  if (!snaps.length) {
    return (
      <div>
        <AutoFollowPanel language={language} onChanged={onChanged} />
        <p className={`${ui.panel} py-10 text-center ${ui.muted}`}>
          {tr(
            language,
            '还没有关注任何带单员。开启上面的自动关注，或到「发现交易员」里挑选，或直接粘贴带单员主页链接。',
            'Not following anyone yet. Turn on auto-follow above, pick traders under "Discover", or paste a lead trader page link.'
          )}
        </p>
      </div>
    )
  }
  return (
    <div>
      <AutoFollowPanel language={language} onChanged={onChanged} />
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <select
          className={`${ui.inputInline}`}
          value={sort}
          onChange={(e) => setSort(e.target.value as typeof sort)}
          aria-label={tr(language, '排序', 'Sort')}
        >
          <option value="score">
            {tr(language, '可信度最高', 'Highest score')}
          </option>
          <option value="roi">
            {tr(language, '30 天收益最高', 'Best 30-day ROI')}
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
        <span className={ui.muted}>
          {tr(
            language,
            '可信度按 30 天收益与回撤之比、回撤、胜率、带单时长、资金规模、是否公开持仓打分（0–100），鼠标放在分数上看各项得分。',
            'Score (0–100) weighs 30-day ROI vs drawdown, drawdown, win rate, tenure, scale and whether positions are public. Hover a score for its parts.'
          )}
        </span>
      </div>
      <div className="grid gap-3 lg:grid-cols-2">
        {sorted.map((s) => (
          <TraderCard
            key={s.trader.portfolio_id}
            snap={s}
            language={language}
            onChanged={onChanged}
            onSymbol={onSymbol}
          />
        ))}
      </div>
    </div>
  )
}
