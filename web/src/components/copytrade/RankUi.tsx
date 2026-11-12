import type { Rank, SymbolSummary } from '../../lib/api/copyTrade'
import { tr, ui } from '../market/marketUi'

/** What the direction rests on: fresh calls or holdings only. */
export function rankBasisLabel(language: string, basis: string): string {
  if (basis === 'fresh') return tr(language, '新开仓', 'fresh calls')
  if (basis === 'holding') return tr(language, '仅持仓', 'holdings only')
  return ''
}

/** The long explanation behind a score, used as a tooltip. */
export function rankExplainText(language: string, r: Rank): string {
  const pct = (v: number) => `${Math.round((v || 0) * 100)}%`
  const parts = [
    tr(
      language,
      `综合 ${Math.round(r.score)}：强度 ${Math.round(r.strength)} × 拥挤 ${pct(r.crowding)} × 流动性 ${pct(r.liquidity)} × 入场 ${pct(r.entry)}`,
      `Rank ${Math.round(r.score)}: strength ${Math.round(r.strength)} × crowding ${pct(r.crowding)} × liquidity ${pct(r.liquidity)} × entry ${pct(r.entry)}`
    ),
    tr(
      language,
      `独立 ${r.independence.toFixed(1)} 人，账户 ${r.holders} 人`,
      `${r.independence.toFixed(1)} independent voices, ${r.holders} accounts`
    ),
    tr(
      language,
      `依据：${rankBasisLabel(language, r.basis)}${r.callers ? ` ${r.callers} 人` : ''}`,
      `Basis: ${rankBasisLabel(language, r.basis)}${r.callers ? `, ${r.callers} calls` : ''}`
    ),
    tr(
      language,
      `预计来回成本 ${r.cost_pct.toFixed(2)}%`,
      `Estimated round-trip cost ${r.cost_pct.toFixed(2)}%`
    ),
  ]
  return parts.join('\n')
}

/**
 * The cross-coin score, compact. Unlike the confidence badge it is comparable
 * across coins: it weighs fresh calls over holdings and discounts crowded,
 * thin and chased coins.
 */
export function RankBadge({
  r,
  language,
  compact,
}: {
  r?: Rank | null
  language: string
  compact?: boolean
}) {
  if (!r || r.direction === 'neutral' || r.score <= 0) return null
  const long = r.direction === 'long'
  const tone =
    r.score >= 60
      ? long
        ? 'bg-nofx-gold text-black'
        : 'bg-nofx-danger text-white'
      : long
        ? 'bg-nofx-gold/10 text-nofx-gold'
        : 'bg-nofx-danger/10 text-nofx-danger'
  return (
    <span
      className={`whitespace-nowrap rounded px-1.5 text-[11px] font-semibold tabular-nums ${tone}`}
      title={rankExplainText(language, r)}
    >
      {tr(language, '综合', 'Rank')} {Math.round(r.score)}
      {compact ? '' : ` · ${rankBasisLabel(language, r.basis)}`}
    </span>
  )
}

/** Says the coin is already crowded, which the measured record punishes. */
export function CrowdingTag({
  r,
  language,
}: {
  r?: Rank | null
  language: string
}) {
  if (!r || r.crowding >= 1) return null
  return (
    <span
      className="rounded bg-nofx-gold/10 px-1.5 text-[10px] text-nofx-gold"
      title={tr(
        language,
        `已有 ${r.holders} 人持有这一边。实测里 8 人以上的多头档位表现最差，人数多不是加分项。`,
        `${r.holders} accounts already hold this side. The 8+ holder band measured worst, so a crowd is a cost, not a confirmation.`
      )}
    >
      {tr(language, '拥挤', 'Crowded')}
    </span>
  )
}

/** What the score's band measured over the last 30 days, cost deducted. */
export interface ExpectedExcess {
  samples: number
  /** mean excess against BTC, as a fraction; undefined when samples are thin */
  excess?: number
}

/** The score's parts, for the coin detail header. */
export function RankPanel({
  r,
  expected,
  language,
}: {
  r?: Rank | null
  expected?: ExpectedExcess | null
  language: string
}) {
  if (!r || r.direction === 'neutral' || r.score <= 0) return null
  const pct = (v: number) => `${Math.round((v || 0) * 100)}%`
  const item = (label: string, value: string, hint?: string) => (
    <span className="flex items-baseline gap-1" title={hint}>
      <span className={ui.muted}>{label}</span>
      <span className="tabular-nums text-nofx-text">{value}</span>
    </span>
  )
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px]">
      {item(
        tr(language, '综合', 'Rank'),
        `${Math.round(r.score)}`,
        rankExplainText(language, r)
      )}
      {item(
        tr(language, '强度', 'Strength'),
        `${Math.round(r.strength)}`,
        tr(
          language,
          '新开仓与离场的净流，乘以独立人数饱和和集中开仓加成，尚未打折。',
          'Net fresh calls against exits, scaled by independent callers and bursts, before the discounts.'
        )
      )}
      {item(
        tr(language, '独立', 'Independent'),
        r.independence.toFixed(1),
        tr(
          language,
          '有效独立人数 (Σw)²/Σw²：同一来源的多个账户只算一票。',
          'Effective independent voices (Σw)²/Σw²: accounts of one source count once.'
        )
      )}
      {item(
        tr(language, '流动性', 'Depth'),
        pct(r.liquidity),
        tr(
          language,
          '按 24 小时成交额折算；未知时按 70% 计。',
          'From 24h traded value; unknown counts as 70%.'
        )
      )}
      {item(
        tr(language, '入场', 'Entry'),
        pct(r.entry),
        tr(
          language,
          '现价相对开仓者均价的偏离：贴近或优于均价加分，追高扣分。',
          'Price against the callers’ average entry: at or better than it scores higher, chased scores lower.'
        )
      )}
      {item(
        tr(language, '成本', 'Cost'),
        `${r.cost_pct.toFixed(2)}%`,
        tr(
          language,
          '手续费加滑点的估计值，未从综合分里扣除。',
          'Estimated fees and slippage, not yet deducted from the score.'
        )
      )}
      {expected
        ? item(
            tr(language, '预期超额', 'Expected excess'),
            expected.excess === undefined
              ? tr(
                  language,
                  `样本 ${expected.samples}`,
                  `${expected.samples} samples`
                )
              : `${expected.excess >= 0 ? '+' : ''}${(expected.excess * 100).toFixed(2)}%`,
            expected.excess === undefined
              ? tr(
                  language,
                  `这个分档只有 ${expected.samples} 条历史记录，样本太少，不给预期值。`,
                  `This band has only ${expected.samples} historical calls, too few to promise anything.`
                )
              : tr(
                  language,
                  `近 30 天综合评分落在同一档、同一方向的 ${expected.samples} 次判断，24 小时后相对 BTC 的平均超额，已扣除估计成本。这是历史平均，不是承诺。`,
                  `Across ${expected.samples} calls in the same band and direction over 30 days, the mean 24h excess against BTC, net of the estimated cost. A historical average, not a promise.`
                )
          )
        : null}
    </div>
  )
}

/** The one-line summary used when a coin has no score yet. */
export function rankGapNote(language: string, s: SymbolSummary): string {
  if (s.rank && s.rank.direction !== 'neutral') return ''
  return tr(
    language,
    '没有可用的方向信号，综合分不计。',
    'No direction to score.'
  )
}
