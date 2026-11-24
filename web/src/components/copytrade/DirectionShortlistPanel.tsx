import useSWR from 'swr'
import { api } from '../../lib/api'
import { tr, ui } from '../market/marketUi'
import { ago, base } from './format'

export function DirectionShortlistPanel({ language, onSelect }: {
  language: string; onSelect: (symbol: string) => void
}) {
  const { data, error, isLoading } = useSWR('copytrade/direction-shortlist',
    () => api.getDirectionShortlist(), { refreshInterval: 60000, revalidateOnFocus: false })
  // SWR may retain an old success after a failed read. Do not present it as live.
  const stale = !!data && Date.now() - data.as_of > 120000
  return <section className={`${ui.panel} mb-3`}>
    <h2 className={ui.title}>{tr(language, '四小时方向候选 · 多空合计最多十个', '4h directional candidates · up to ten total')}</h2>
    <p className={`${ui.muted} text-xs my-2`}>{tr(language,
      '研究验证中：按设置的成交额筛选，要求近期独立带单信号、AI 与风格方向一致，并通过历史方向和当前行情检查。不足十个不补齐。',
      'Research: configured volume scope, independent recent calls, AI/style agreement, historical direction evidence and market confirmation. Empty slots stay empty.')}</p>
    {isLoading ? <p role="status">{tr(language, '检查方向证据中…', 'Checking evidence…')}</p> : null}
    {error || stale ? <p role="alert" className="text-nofx-danger text-sm">{tr(language, '方向榜暂不可用或已过期，请等待刷新。', 'Shortlist unavailable or expired; waiting for refresh.')}</p> : data ? <>
      <p className={`${ui.muted} text-xs mb-2`}>{tr(language, '成交额门槛', 'Volume threshold')}: {(data.min_volume / 1e8).toFixed(2)} {tr(language, '亿 USDT', '×100M USDT')} · {data.candidates} {tr(language, '个候选', 'candidates')} · {ago(language, data.as_of)}</p>
      <div className="grid gap-3 md:grid-cols-2">{(['short', 'long'] as const).map(side => <div key={side}>
        <h3 className={`text-sm font-semibold ${side === 'short' ? 'text-nofx-danger' : 'text-nofx-success'}`}>{side === 'short' ? tr(language, '看空候选', 'Short candidates') : tr(language, '看多候选', 'Long candidates')} ({data[side].length}/10)</h3>
        {data[side].length ? <ol className="space-y-2 mt-2">{data[side].map(p => <li key={p.symbol}>
          <button type="button" className="text-nofx-text font-semibold hover:underline" onClick={() => onSelect(p.symbol)}>{base(p.symbol)}</button>
          <p className={`${ui.muted} text-xs`}>{tr(language, 'AI / 带单历史命中', 'AI / trader historical hit')}: {(p.ai_history.hit_rate * 100).toFixed(1)}% / {(p.trader_history.hit_rate * 100).toFixed(1)}% · {tr(language, '样本', 'samples')} {p.ai_history.samples}/{p.trader_history.samples}</p>
          <p className={`${ui.muted} text-xs`}>{tr(language, '近期独立信号', 'Recent independent calls')}: {p.calls} · {tr(language, '方向失效参考价', 'Invalidation reference')}: {p.invalidation_price}</p>
        </li>)}</ol> : <p className={`${ui.muted} text-sm mt-2`}>{tr(language, '目前没有证据足够的币，保持观望。', 'No coin has sufficient evidence; stand aside.')}</p>}
      </div>)}</div>
      <details className="mt-2 text-xs"><summary className={`cursor-pointer ${ui.muted}`}>{tr(language, '查看未入选原因', 'Exclusion breakdown')}</summary>
        {Object.entries(data.excluded).map(([reason, count]) => <p key={reason}>{({volume: '成交额不达标', ai_not_fresh: 'AI 无有效新评分', direction_conflict: '方向分歧', few_independent_calls: '独立新信号不足', insufficient_evidence: '历史证据不足', history_unavailable: '历史不可用', market_unavailable: '行情不可用', trend_unconfirmed: '行情未确认', market_check_deferred: '本轮行情检查延后', stale_trader_source: '带单源数据过期'} as Record<string,string>)[reason] || reason}: {count}</p>)}
      </details>
    </> : null}
    <p className={`${ui.muted} text-xs mt-2`}>{tr(language, '历史命中率不是下一次上涨或下跌的概率。四小时内反向波动尚未验证；此榜不连接自动下单。', 'Historical hit rates are not next-trade probabilities. Intrawindow adverse moves are not validated; this list does not place orders.')}</p>
  </section>
}
