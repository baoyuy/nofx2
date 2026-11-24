import { useState } from 'react'
import useSWR from 'swr'
import { api } from '../../lib/api'
import type {
  BaselineStat,
  OutcomeStat,
  OutcomesResp,
} from '../../lib/api/copyTrade'
import { tr, ui } from '../market/marketUi'

// AI readings vs rules that need no model, over the same samples and after the
// same assumed cost. The point is to make it obvious when an AI number only
// reflects the market: if "short everything" scores as well as the AI, the AI
// has not shown it can pick coins.

const fmtPct = (v: number, digits = 2) =>
  `${v > 0 ? '+' : ''}${v.toFixed(digits)}%`

const tone = (v: number) =>
  v > 0 ? 'text-nofx-success' : v < 0 ? 'text-nofx-danger' : ''

// The AI's own row must beat every baseline on net return to show an edge.
export function aiBeatsBaselines(
  ai: OutcomeStat | undefined,
  baselines: BaselineStat[] | undefined
): 'yes' | 'no' | 'unknown' {
  if (!ai || ai.samples === 0 || !baselines || baselines.length === 0)
    return 'unknown'
  const measured = baselines.filter((b) => b.samples > 0)
  if (measured.length === 0) return 'unknown'
  return measured.every((b) => ai.net_avg_ret > b.net_avg_ret) ? 'yes' : 'no'
}

export function BaselineComparePanel({ language }: { language: string }) {
  const [version, setVersion] = useState('')
  const [horizon, setHorizon] = useState('4h')
  const { data } = useSWR<OutcomesResp>(
    ['copytrade/coin-score/outcomes', version],
    () => api.getCoinScoreOutcomes(version || undefined),
    { refreshInterval: 120000 }
  )
  if (!data) return null
  const ai = data.rows.find(
    (r) => r.horizon === horizon && r.side === 'all' && r.band === 'all'
  )
  const sideRows = data.rows.filter(
    (r) => r.horizon === horizon && r.band === 'all' && r.side !== 'all'
  )
  const baselines = data.baselines?.[horizon] || []
  const verdict = aiBeatsBaselines(ai, baselines)
  const w = data.window
  const oldestDays = w.coverage_days || 0
  return (
    <div className="border-t border-[rgba(25,36,52,0.08)] pt-3">
      <div className="mb-1 flex flex-wrap items-center gap-2">
        <span className="text-sm font-medium text-nofx-text">
          {tr(
            language,
            'AI 对比无模型规则（扣成本后）',
            'AI vs no-model rules (after cost)'
          )}
        </span>
        <select
          aria-label={tr(language, '周期', 'Horizon')}
          className={ui.input}
          value={horizon}
          onChange={(e) => setHorizon(e.target.value)}
        >
          {['4h', '24h', '72h'].map((h) => (
            <option key={h} value={h}>
              {h}
            </option>
          ))}
        </select>
        <select
          aria-label={tr(language, '评分版本', 'Scoring version')}
          className={ui.input}
          value={version}
          onChange={(e) => setVersion(e.target.value)}
        >
          <option value="">{tr(language, '全部版本', 'All versions')}</option>
          {(data.versions || []).map((v) => (
            <option key={v} value={v}>
              {v === 'legacy' ? tr(language, '旧版本(未记录)', 'legacy') : v}
            </option>
          ))}
        </select>
      </div>

      {!ai || ai.samples === 0 ? (
        <p className={`${ui.muted} text-xs`}>
          {tr(
            language,
            `这个周期还没有成熟样本（${horizon} 窗口结束后才计入）。`,
            `No mature samples for ${horizon} yet.`
          )}
        </p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-xs tabular-nums">
            <thead>
              <tr className={ui.muted}>
                <th className="py-1 pr-2 text-left">
                  {tr(language, '规则', 'Rule')}
                </th>
                <th className="py-1 pr-2 text-right">
                  {tr(language, '样本', 'n')}
                </th>
                <th className="py-1 pr-2 text-right">
                  {tr(language, '方向命中', 'Hit')}
                </th>
                <th className="py-1 pr-2 text-right">
                  {tr(language, '扣成本胜率', 'Net win')}
                </th>
                <th className="py-1 pr-2 text-right">
                  {tr(language, '扣成本均值', 'Net avg')}
                </th>
                <th className="py-1 text-right">
                  {tr(language, '超额下限', 'Excess LB')}
                </th>
              </tr>
            </thead>
            <tbody>
              <tr className="border-t border-[rgba(25,36,52,0.06)] font-medium">
                <td className="py-1 pr-2">
                  {tr(language, 'AI（全部）', 'AI (all)')}
                </td>
                <td className="py-1 pr-2 text-right">{ai.samples}</td>
                <td className="py-1 pr-2 text-right">
                  {ai.hit_rate.toFixed(1)}%
                </td>
                <td className="py-1 pr-2 text-right">
                  {ai.net_win_rate.toFixed(1)}%
                </td>
                <td className={`py-1 pr-2 text-right ${tone(ai.net_avg_ret)}`}>
                  {fmtPct(ai.net_avg_ret)}
                </td>
                <td className={`py-1 text-right ${tone(ai.net_excess_lb)}`}>
                  {fmtPct(ai.net_excess_lb)}
                </td>
              </tr>
              {sideRows.map((r) => (
                <tr
                  key={r.side}
                  className="border-t border-[rgba(25,36,52,0.06)]"
                >
                  <td className="py-1 pr-2 pl-3">
                    {r.side === 'long'
                      ? tr(language, 'AI 做多', 'AI long')
                      : tr(language, 'AI 做空', 'AI short')}
                  </td>
                  <td className="py-1 pr-2 text-right">{r.samples}</td>
                  <td className="py-1 pr-2 text-right">
                    {r.samples ? `${r.hit_rate.toFixed(1)}%` : '--'}
                  </td>
                  <td className="py-1 pr-2 text-right">
                    {r.samples ? `${r.net_win_rate.toFixed(1)}%` : '--'}
                  </td>
                  <td className={`py-1 pr-2 text-right ${tone(r.net_avg_ret)}`}>
                    {r.samples ? fmtPct(r.net_avg_ret) : '--'}
                  </td>
                  <td className={`py-1 text-right ${tone(r.net_excess_lb)}`}>
                    {r.samples ? fmtPct(r.net_excess_lb) : '--'}
                  </td>
                </tr>
              ))}
              {baselines.map((b) => (
                <tr
                  key={b.name}
                  className={`border-t border-[rgba(25,36,52,0.06)] ${ui.muted}`}
                >
                  <td className="py-1 pr-2">{b.label}</td>
                  <td className="py-1 pr-2 text-right">{b.samples}</td>
                  <td className="py-1 pr-2 text-right">
                    {b.samples ? `${b.hit_rate.toFixed(1)}%` : '--'}
                  </td>
                  <td className="py-1 pr-2 text-right">
                    {b.samples ? `${b.net_win_rate.toFixed(1)}%` : '--'}
                  </td>
                  <td className={`py-1 pr-2 text-right ${tone(b.net_avg_ret)}`}>
                    {b.samples ? fmtPct(b.net_avg_ret) : '--'}
                  </td>
                  <td className="py-1 text-right">--</td>
                </tr>
              ))}
            </tbody>
          </table>
          <p
            role="status"
            className={`mt-1 text-xs ${
              verdict === 'yes'
                ? 'text-nofx-success'
                : verdict === 'no'
                  ? 'text-nofx-gold'
                  : ui.muted
            }`}
          >
            {verdict === 'yes'
              ? tr(
                  language,
                  '这段时间内，AI 扣成本后的平均收益高于所有无模型规则。仍需更长、跨不同行情的样本确认。',
                  'In this window, AI net return beats every no-model rule. Needs longer, multi-regime data to confirm.'
                )
              : verdict === 'no'
                ? tr(
                    language,
                    'AI 没有跑赢至少一条无模型规则：这段时间的收益更可能来自大盘方向，而不是 AI 选币。',
                    'AI does not beat at least one no-model rule: returns here more likely reflect market direction than AI selection.'
                  )
                : tr(
                    language,
                    '样本不足，无法比较。',
                    'Not enough samples to compare.'
                  )}
          </p>
        </div>
      )}
      <p className={`${ui.muted} mt-1 text-[11px]`}>
        {tr(
          language,
          `统计最近 ${w.days} 天（现有数据约 ${oldestDays.toFixed(1)} 天，保留 ${w.retention_days} 天）· 扣往返成本 ${w.cost_pct}%（不含资金费率）· 涨跌不足 ${w.neutral_pct}% 不计命中 · 出场价按 ${w.exit_interval} K线`,
          `Last ${w.days} days (data covers ~${oldestDays.toFixed(1)} days; kept ${w.retention_days} days) · ${w.cost_pct}% round-trip cost (funding excluded) · moves under ${w.neutral_pct}% not counted as hits · exits priced on ${w.exit_interval} candles`
        )}
        {w.truncated
          ? tr(
              language,
              ' · 数据量超出上限，窗口已截断',
              ' · row limit hit, window truncated'
            )
          : ''}
      </p>
    </div>
  )
}
