import { NoticeText } from '../NoticeText'
import { useEffect, useState } from 'react'
import { api } from '../../lib/api'
import type {
  Confidence,
  HitRate,
  SymbolSummary,
  TraderPosition,
  TraderScore,
} from '../../lib/api/copyTrade'
import { tr, ui } from '../market/marketUi'

export function confidenceLevel(language: string, score: number): string {
  if (score >= 70) return tr(language, '高', 'high')
  if (score >= 40) return tr(language, '中', 'mid')
  return tr(language, '低', 'low')
}

/** "偏多 · 72 高" with the side's color. */
export function ConfidenceBadge({
  c,
  language,
  compact,
}: {
  c: Confidence
  language: string
  compact?: boolean
}) {
  if (!c || c.direction === 'neutral' || c.score <= 0) {
    return (
      <span className="rounded bg-nofx-bg-deeper px-1.5 text-[11px] text-nofx-text-muted">
        {tr(language, '多空分歧', 'Split')}
      </span>
    )
  }
  const long = c.direction === 'long'
  const tone =
    c.score >= 70
      ? long
        ? 'bg-nofx-success text-white'
        : 'bg-nofx-danger text-white'
      : long
        ? 'bg-nofx-success/10 text-nofx-success'
        : 'bg-nofx-danger/10 text-nofx-danger'
  return (
    <span
      className={`whitespace-nowrap rounded px-1.5 text-[11px] font-semibold tabular-nums ${tone}`}
    >
      {long ? tr(language, '偏多', 'Long') : tr(language, '偏空', 'Short')}{' '}
      {Math.round(c.score)}
      {compact ? '' : ` ${confidenceLevel(language, c.score)}`}
    </span>
  )
}

/** One line on why the confidence is what it is. */
export function explainConfidence(language: string, s: SymbolSummary): string {
  const c = s.confidence
  if (!c || !c.traders) return ''
  const longPct = Math.round(c.long_share * 100)
  const parts = [
    tr(
      language,
      `${s.long.traders} 人做多、${s.short.traders} 人做空`,
      `${s.long.traders} long, ${s.short.traders} short`
    ),
    tr(
      language,
      `按可信度和仓位加权，多方 ${longPct}%、空方 ${100 - longPct}%`,
      `weighted by score and stake: ${longPct}% long, ${100 - longPct}% short`
    ),
  ]
  const trapped = (s.long.trapped || 0) + (s.short.trapped || 0)
  const fresh = (s.long.fresh || 0) + (s.short.fresh || 0)
  if (trapped)
    parts.push(
      tr(
        language,
        `${trapped} 人被套，权重按亏损打折（可能只是在扛单）`,
        `${trapped} trapped in loss, weighted down (may just be holding on)`
      )
    )
  if (c.health !== undefined && c.health < 0.95)
    parts.push(
      tr(
        language,
        `${c.direction === 'short' ? '空方' : '多方'}整体浮亏，分数乘以 ${c.health.toFixed(2)}`,
        `the ${c.direction} side is under water overall: score × ${c.health.toFixed(2)}`
      )
    )
  if (fresh)
    parts.push(
      tr(
        language,
        `${fresh} 人 24 小时内开仓或加仓，权重加 30%`,
        `${fresh} opened or added within 24h, weighted up 30%`
      )
    )
  if (s.high_freq)
    parts.push(
      tr(
        language,
        `${s.high_freq} 人是高频交易员，权重只算 0.4`,
        `${s.high_freq} high-frequency, weighted 0.4`
      )
    )
  if (s.liquidated)
    parts.push(
      tr(
        language,
        `${s.liquidated} 人可能已被强平，不计入`,
        `${s.liquidated} likely liquidated, left out`
      )
    )
  parts.push(
    tr(
      language,
      `持仓者平均可信度 ${Math.round(c.quality * 100)}`,
      `holders' average score ${Math.round(c.quality * 100)}`
    )
  )
  if (c.traders < 3)
    parts.push(
      tr(
        language,
        `只有 ${c.traders} 人，样本太少，分数打了折`,
        `only ${c.traders} trader(s): sample too small, discounted`
      )
    )
  return parts.join('；')
}

export function ScoreBadge({
  score,
  language,
}: {
  score: TraderScore | number
  language: string
}) {
  const v = typeof score === 'number' ? score : score?.score || 0
  const tone =
    v >= 70
      ? 'bg-nofx-success/10 text-nofx-success'
      : v >= 45
        ? 'bg-nofx-gold/10 text-nofx-gold'
        : 'bg-nofx-danger/10 text-nofx-danger'
  const title =
    typeof score === 'number' || !score
      ? undefined
      : tr(
          language,
          `收益质量 ${pctPart(score.quality)} · 回撤 ${pctPart(score.drawdown)} · 胜率 ${pctPart(score.win_rate)} · 带单时长 ${pctPart(score.tenure)} · 资金规模 ${pctPart(score.scale)} · 数据可靠 ${pctPart(score.reliability)}`,
          `quality ${pctPart(score.quality)} · drawdown ${pctPart(score.drawdown)} · win ${pctPart(score.win_rate)} · tenure ${pctPart(score.tenure)} · scale ${pctPart(score.scale)} · data ${pctPart(score.reliability)}`
        )
  return (
    <span
      title={title}
      className={`whitespace-nowrap rounded px-1.5 text-[11px] font-semibold tabular-nums ${tone}`}
    >
      {tr(language, '可信度', 'Score')} {Math.round(v)}
    </span>
  )
}

const pctPart = (v: number) => `${Math.round((v || 0) * 100)}%`

type HitGroup = NonNullable<HitRate['group']>

const bandLabel = (
  language: string,
  group: HitGroup,
  band: HitRate['band']
) => {
  if (band === 'all') return tr(language, '全部', 'All')
  if (group === 'openers')
    return tr(language, `${band} 人新开仓`, `${band} fresh openers`)
  if (group === 'calls')
    return tr(language, `${band} 人独立开仓`, `${band} independent callers`)
  return (
    {
      high: tr(language, '高（≥70）', 'High (≥70)'),
      mid: tr(language, '中（40–70）', 'Mid (40–70)'),
      low: tr(language, '低（<40）', 'Low (<40)'),
    } as Record<string, string>
  )[band]
}

const GROUP_BANDS: Record<HitGroup, HitRate['band'][]> = {
  confidence: ['high', 'mid', 'low', 'all'],
  openers: ['3+', '2', '1', '0', 'all'],
  shadow: ['high', 'mid', 'low', 'all'],
  calls: ['3+', '2', '1', 'all'],
  rank: ['high', 'mid', 'low', 'all'],
}

/**
 * History of how often signals moved the called way: the holdings confidence,
 * the same signals by fresh openers, and the fresh-call reading measured next
 * to it. "vs BTC" takes out the market's own move.
 */
export function HitRatePanel({ language }: { language: string }) {
  const [days, setDays] = useState(30)
  const [direction, setDirection] = useState<'all' | 'long' | 'short'>('all')
  const [group, setGroup] = useState<HitGroup>('confidence')
  const [measure, setMeasure] = useState<'raw' | 'excess'>('raw')
  const [rows, setRows] = useState<HitRate[]>([])
  const [error, setError] = useState('')
  useEffect(() => {
    let cancelled = false
    api
      .getHitRate(days)
      .then((r) => {
        if (!cancelled) {
          setRows(r.rows || [])
          setError('')
        }
      })
      .catch(
        (e) =>
          !cancelled && setError(e instanceof Error ? e.message : String(e))
      )
    return () => {
      cancelled = true
    }
  }, [days])
  const find = (band: HitRate['band'], horizon: HitRate['horizon']) =>
    rows.find(
      (x) =>
        (x.group ?? 'confidence') === group &&
        x.band === band &&
        x.horizon === horizon &&
        (x.direction ?? 'all') === direction
    )
  const cell = (band: HitRate['band'], horizon: HitRate['horizon']) => {
    const r = find(band, horizon)
    const excess = measure === 'excess'
    const samples = excess ? r?.excess_samples || 0 : r?.samples || 0
    if (!r || !samples) return <span className="text-nofx-text-muted">--</span>
    const rate = excess ? r.excess_rate || 0 : r.rate
    const avg = excess ? r.avg_excess || 0 : r.avg_return
    const tone =
      samples < 30
        ? 'text-nofx-text-muted'
        : rate >= 0.55
          ? 'text-nofx-success'
          : rate <= 0.45
            ? 'text-nofx-danger'
            : 'text-nofx-text'
    return (
      <span
        className={tone}
        title={
          excess
            ? tr(
                language,
                `相对 BTC 平均多赚 ${(avg * 100).toFixed(2)}%`,
                `avg ${(avg * 100).toFixed(2)}% better than BTC`
              )
            : tr(
                language,
                `平均顺向涨跌 ${(avg * 100).toFixed(2)}%`,
                `avg move in the called direction ${(avg * 100).toFixed(2)}%`
              )
        }
      >
        {Math.round(rate * 100)}%
        <span className="text-[10px] text-nofx-text-muted"> /{samples}</span>
      </span>
    )
  }
  const total =
    rows.find(
      (r) =>
        (r.group ?? 'confidence') === 'confidence' &&
        r.band === 'all' &&
        r.horizon === '4h' &&
        (r.direction ?? 'all') === 'all'
    )?.samples || 0
  // Servers from before the 72h horizon and the groups send neither.
  const has72 = rows.some((r) => r.horizon === '72h')
  const hasGroups = rows.some((r) => r.group && r.group !== 'confidence')
  const select = `${ui.inputInline} py-0.5 text-xs`
  return (
    <section className={`${ui.panel} mt-3`}>
      <div className="mb-2 flex flex-wrap items-center justify-between gap-1">
        <h2 className={`${ui.title} mb-0 whitespace-nowrap`}>
          {tr(language, '历史命中率', 'Hit rate')}
        </h2>
        <select
          className={select}
          value={direction}
          onChange={(e) => setDirection(e.target.value as typeof direction)}
          aria-label={tr(language, '方向', 'Side')}
        >
          <option value="all">{tr(language, '多空合计', 'Both sides')}</option>
          <option value="long">{tr(language, '只看偏多', 'Long calls')}</option>
          <option value="short">
            {tr(language, '只看偏空', 'Short calls')}
          </option>
        </select>
        <select
          className={select}
          value={days}
          onChange={(e) => setDays(Number(e.target.value))}
        >
          {[7, 30, 90].map((d) => (
            <option key={d} value={d}>
              {tr(language, `近 ${d} 天`, `${d} days`)}
            </option>
          ))}
        </select>
      </div>
      <div className="mb-2 flex flex-wrap items-center gap-1">
        {hasGroups ? (
          <select
            className={select}
            value={group}
            onChange={(e) => setGroup(e.target.value as HitGroup)}
            aria-label={tr(language, '统计对象', 'Signal')}
          >
            <option value="confidence">
              {tr(language, '持仓置信度', 'Holdings confidence')}
            </option>
            <option value="openers">
              {tr(
                language,
                '持仓信号 · 按新开仓人数',
                'Holdings · by fresh openers'
              )}
            </option>
            <option value="shadow">
              {tr(language, '新开仓评分（试验）', 'Fresh-call score (trial)')}
            </option>
            <option value="calls">
              {tr(language, '新开仓信号 · 按人数', 'Fresh calls · by callers')}
            </option>
            <option value="rank">
              {tr(language, '综合评分（试验）', 'Cross-coin score (trial)')}
            </option>
          </select>
        ) : null}
        <select
          className={select}
          value={measure}
          onChange={(e) => setMeasure(e.target.value as typeof measure)}
          aria-label={tr(language, '口径', 'Measure')}
        >
          <option value="raw">
            {tr(language, '价格方向', 'Price direction')}
          </option>
          <option value="excess">{tr(language, '相对 BTC', 'vs BTC')}</option>
        </select>
      </div>
      {error ? (
        <p className="text-xs text-nofx-danger">
          <NoticeText text={error} />
        </p>
      ) : null}
      <table className="w-full text-xs">
        <thead className="text-left text-nofx-text-muted">
          <tr>
            <th className="py-1 font-normal">
              {group === 'confidence' ||
              group === 'shadow' ||
              group === 'rank'
                ? tr(language, '置信度', 'Confidence')
                : tr(language, '人数', 'Traders')}
            </th>
            <th className="font-normal">
              {tr(language, '4 小时后', 'After 4h')}
            </th>
            <th className="font-normal">
              {tr(language, '24 小时后', 'After 24h')}
            </th>
            {has72 ? (
              <th className="font-normal">
                {tr(language, '72 小时后', 'After 72h')}
              </th>
            ) : null}
          </tr>
        </thead>
        <tbody className="tabular-nums">
          {GROUP_BANDS[group].map((b) => (
            <tr key={b} className={b === 'all' ? `border-t ${ui.border}` : ''}>
              <td className="py-1 text-nofx-text">
                {bandLabel(language, group, b)}
              </td>
              <td>{cell(b, '4h')}</td>
              <td>{cell(b, '24h')}</td>
              {has72 ? <td>{cell(b, '72h')}</td> : null}
            </tr>
          ))}
        </tbody>
      </table>
      <p className={`${ui.muted} mt-2`}>
        {total
          ? tr(
              language,
              '每小时记录一次各币的方向和置信度，之后看价格有没有朝这个方向走。同一个币连续几小时同一方向只算一次判断，从第一个小时算起。格式：命中率 / 判断次数；少于 30 次的显示为灰色，参考意义不大。50% 相当于抛硬币。',
              'Each hour records every coin’s direction and confidence, then checks whether the price moved that way. A coin called the same way several hours in a row counts once, from its first hour. Format: hit rate / calls; grey means fewer than 30. 50% is a coin flip.'
            )
          : tr(
              language,
              '还没有数据。系统每小时记录一次各币的方向和置信度，4 小时后出第一批结果，样本要攒几周才有参考意义。',
              'No data yet. Each hour the direction and confidence of every coin is recorded; first results arrive 4 hours later, and it takes a few weeks to mean much.'
            )}
      </p>
      {total ? (
        <p className={`${ui.muted} mt-1`}>
          {measure === 'excess'
            ? tr(
                language,
                '「相对 BTC」：同一段时间里，这次判断比 BTC 自己的涨跌多赚还是少赚。大盘下跌时做空几乎都“命中”，这个口径才看得出信号本身有没有用。',
                '"vs BTC": whether the call did better than BTC’s own move over the same hours. In a falling market nearly every short “hits”; this shows what the signal itself added.'
              )
            : tr(
                language,
                '「价格方向」会受大盘影响：大盘跌时做空命中率高、做多低。想看信号本身，切到「相对 BTC」。',
                '"Price direction" follows the market: shorts hit and longs miss while it falls. Switch to "vs BTC" for the signal itself.'
              )}
          {group === 'shadow' || group === 'calls'
            ? tr(
                language,
                ' 「新开仓」只看 24 小时内谁开仓、加仓、离场，不算旧仓，和持仓置信度并行记录，用来比较哪个更准。',
                ' "Fresh calls" reads only who opened, added or left in the last 24h, recorded next to the holdings confidence to compare the two.'
              )
            : ''}
          {group === 'rank'
            ? tr(
                language,
                ' 「综合评分」以新开仓为主、旧仓为辅，按独立人数而不是账户数计票，并对拥挤、流动性差、追高的币打折。它记录在旁边用于比较，目前不影响交易。',
                ' "Cross-coin score" weighs fresh calls over holdings, counts independent voices rather than accounts, and discounts crowded, thin or chased coins. It is recorded next to the others for comparison and does not move trading yet.'
              )
            : ''}
        </p>
      ) : null}
    </section>
  )
}

/** Current-loss, historical-reference, new and likely-liquidated tags. */
export function HealthTag({
  h,
  language,
}: {
  h: TraderPosition
  language: string
}) {
  const pct = (value: number | undefined) =>
    typeof value === 'number' && Number.isFinite(value) ? `${value.toFixed(1)}%` : '—'
  const basis = tr(language, h.pct_of_margin ? '保证金' : '价格', h.pct_of_margin ? 'margin' : 'price')
  const threshold = h.trapped_threshold_pct ?? (h.pct_of_margin ? 50 : 5)
  const historyThreshold = h.first_entry_threshold_pct ?? (h.first_entry_pct_of_margin ? 50 : 5)
  const historyBasis = tr(
    language,
    h.first_entry_pct_of_margin ? '按当前杠杆折算' : '价格',
    h.first_entry_pct_of_margin ? 'scaled by current leverage' : 'price'
  )
  const tag = (cls: string, text: string, title: string) => (
    <span tabIndex={0} aria-label={`${text}: ${title}`} title={title} className={`mr-1 rounded px-1.5 text-[10px] ${cls}`}>
      {text}
    </span>
  )
  return (
    <>
      {h.liquidated
        ? tag(
            'bg-nofx-text-muted/15 text-nofx-text-muted',
            tr(language, '可能已强平', 'Likely liquidated'),
            tr(
              language,
              '推算的亏损已超过账户余额，不计入多空和置信度',
              'Inferred loss exceeds the balance; left out of counts and confidence'
            )
          )
        : null}
      {h.trapped
        ? tag(
            'bg-nofx-danger/10 text-nofx-danger',
            tr(language, '被套', 'Trapped'),
            tr(
              language,
              `当前浮亏 ${pct(h.return_pct)}（${basis}），达到亏损 ${threshold}% 的被套阈值；只按当前平均开仓价口径判断，不使用历史首笔价格。`,
              `Current return ${pct(h.return_pct)} (${basis}) meets the ${threshold}% loss threshold. Based only on the current average entry, not the first recorded entry.`
            )
          )
        : null}
      {h.historical_entry_warning && !h.liquidated
        ? tag(
            'bg-nofx-gold/10 text-nofx-gold',
            tr(language, '历史入场偏离', 'Historical entry deviation'),
            tr(
              language,
              `记录中的首笔入场价比当前均价更不利，从该价格到当前标记价的参考变动为 ${pct(h.first_entry_return_pct)}（${historyBasis}），达到 ${historyThreshold}% 的反向偏离阈值。这不是当前实际浮亏，不计入被套人数，也不单独降低置信度。历史记录可能不完整。`,
              `The first recorded entry is less favorable than the current average. Reference movement to the current mark is ${pct(h.first_entry_return_pct)} (${historyBasis}), reaching the ${historyThreshold}% adverse threshold. This is not current PnL, does not count as trapped, and adds no separate confidence penalty. History may be incomplete.`
            )
          )
        : null}
      {h.fresh && !h.liquidated
        ? tag(
            'bg-nofx-success/10 text-nofx-success',
            tr(language, '新开', 'New'),
            tr(
              language,
              '24 小时内首次开仓。加仓不算新开，单独标注。',
              'The position was opened within 24h. Adds are not new positions and get their own tag.'
            )
          )
        : null}
      {h.added_recently && !h.liquidated
        ? tag(
            'bg-nofx-gold/10 text-nofx-gold',
            tr(language, '加仓', 'Added'),
            tr(
              language,
              '24 小时内给已有仓位加仓，不是新开仓；评分里权重减半，浮亏时加仓不计票。',
              'Added to an already open position within 24h, not a new open: half weight in scoring, and no vote at all while the position is under water.'
            )
          )
        : null}
    </>
  )
}
