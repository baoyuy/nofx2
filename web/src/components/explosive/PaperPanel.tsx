import useSWR from 'swr'
import { explosiveApi, type PaperTrade } from '../../lib/api/explosive'
import { tr, ui } from '../market/marketUi'
import { BN } from '../market/BinanceChart'

const fmtTime = (ms: number) => {
  if (!ms) return '—'
  const d = new Date(ms)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
const money = (v: number) => `${v >= 0 ? '+' : ''}${v.toFixed(2)}`
const signedPct = (v: number) => `${v >= 0 ? '+' : ''}${v.toFixed(2)}%`

function EquitySpark({ points }: { points: { equity: number }[] }) {
  if (points.length < 2) return null
  const vals = points.map((p) => p.equity)
  const min = Math.min(...vals)
  const max = Math.max(...vals)
  const span = max - min || 1
  const w = 600
  const h = 80
  const d = vals
    .map((v, i) => {
      const x = (i / (vals.length - 1)) * w
      const y = h - ((v - min) / span) * h
      return `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
  const up = vals[vals.length - 1] >= vals[0]
  return (
    <svg viewBox={`0 0 ${w} ${h}`} className="h-20 w-full" preserveAspectRatio="none">
      <path d={d} fill="none" stroke={up ? BN.up : BN.down} strokeWidth={1.5} />
    </svg>
  )
}

function Stat({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div className="rounded border border-[rgba(25,36,52,0.10)] px-2 py-1.5">
      <div className={`text-[10px] ${ui.muted}`}>{label}</div>
      <div className="text-sm font-semibold tabular-nums" style={{ color }}>
        {value}
      </div>
    </div>
  )
}

const reasonLabel = (language: string, r: string) => {
  switch (r) {
    case 'stop':
      return tr(language, '止损', 'Stop')
    case 'target':
      return tr(language, '止盈', 'Target')
    case 'time':
      return tr(language, '超时', 'Timeout')
    default:
      return r || '—'
  }
}

function TradesTable({ language, trades, closed }: { language: string; trades: PaperTrade[]; closed: boolean }) {
  return (
    <table className="w-full whitespace-nowrap text-xs">
      <thead className="text-left text-nofx-text-muted">
        <tr>
          <th className="py-1 pr-2 font-normal">{tr(language, '币种', 'Coin')}</th>
          <th className="text-right font-normal">{tr(language, '开仓', 'Open')}</th>
          <th className="text-right font-normal">{tr(language, '入场→出场', 'Entry→Exit')}</th>
          <th className="text-right font-normal">{tr(language, '仓位', 'Notional')}</th>
          {closed ? <th className="text-right font-normal">{tr(language, '原因', 'Reason')}</th> : null}
          <th className="text-right font-normal">PnL</th>
          <th className="text-right font-normal">{tr(language, '时间', 'Time')}</th>
        </tr>
      </thead>
      <tbody className="text-nofx-text">
        {trades.map((t) => (
          <tr key={t.id} className={`border-t ${ui.border}`}>
            <td className="py-1 pr-2 font-medium">{t.symbol.replace('USDT', '')}</td>
            <td className="text-right tabular-nums">{t.entry_price < 0.01 ? t.entry_price.toFixed(6) : t.entry_price.toFixed(4)}</td>
            <td className="text-right tabular-nums">
              {closed ? (t.exit_price < 0.01 ? t.exit_price.toFixed(6) : t.exit_price.toFixed(4)) : '—'}
            </td>
            <td className="text-right tabular-nums">{Math.round(t.notional)}</td>
            {closed ? <td className="text-right">{reasonLabel(language, t.exit_reason)}</td> : null}
            <td
              className="text-right font-semibold tabular-nums"
              style={{ color: (closed ? t.realized_pnl : 0) >= 0 ? BN.up : BN.down }}
            >
              {closed ? money(t.realized_pnl) : '—'}
            </td>
            <td className="text-right tabular-nums text-nofx-text-muted">{fmtTime(closed ? t.closed_at : t.opened_at)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

export function PaperPanel({ language }: { language: string }) {
  const { data, error } = useSWR('market/explosive/paper', () => explosiveApi.paper(), {
    refreshInterval: 15000,
    revalidateOnFocus: false,
    keepPreviousData: true,
  })

  if (error) {
    return (
      <p className={`${ui.panel} text-sm text-nofx-danger`}>
        {tr(language, '模拟盘加载失败：', 'Could not load paper account: ')}
        {error instanceof Error ? error.message : String(error)}
      </p>
    )
  }
  if (!data) return <p className={`${ui.panel} py-8 text-center ${ui.muted}`}>{tr(language, '加载中…', 'Loading…')}</p>

  const retColor = data.return_pct >= 0 ? BN.up : BN.down
  const closedRecent = data.recent.filter((t) => t.status === 'closed')

  return (
    <div className="space-y-4">
      <section className={ui.panel}>
        <div className="mb-2 flex flex-wrap items-baseline gap-2">
          <h2 className="text-base font-semibold text-nofx-text">{data.label || tr(language, '规则模拟盘', 'Paper engine')}</h2>
          <span className={`text-xs ${ui.muted}`}>
            {tr(
              language,
              '规则量化、模拟成交、不花真钱。用真实行情检验这套规则扣费后到底有没有正期望。',
              'Rule-based, simulated fills, no real money. Measures whether the rule has an edge after fees.'
            )}
          </span>
        </div>
        <div className="grid grid-cols-3 gap-2 sm:grid-cols-4 lg:grid-cols-6">
          <Stat label={tr(language, '当前权益', 'Equity')} value={data.equity.toFixed(2)} />
          <Stat label={tr(language, '收益率', 'Return')} value={signedPct(data.return_pct)} color={retColor} />
          <Stat label={tr(language, '已实现', 'Realized')} value={money(data.realized)} color={data.realized >= 0 ? BN.up : BN.down} />
          <Stat label={tr(language, '未实现', 'Unrealized')} value={money(data.unrealized)} color={data.unrealized >= 0 ? BN.up : BN.down} />
          <Stat label={tr(language, '交易数', 'Trades')} value={String(data.trades)} />
          <Stat label={tr(language, '胜率', 'Win rate')} value={`${(data.win_rate * 100).toFixed(0)}%`} />
          <Stat label={tr(language, '平均盈利', 'Avg win')} value={money(data.avg_win)} color={BN.up} />
          <Stat label={tr(language, '平均亏损', 'Avg loss')} value={money(data.avg_loss)} color={BN.down} />
          <Stat label={tr(language, '盈亏比', 'Profit factor')} value={data.profit_factor ? data.profit_factor.toFixed(2) : '—'} />
          <Stat label={tr(language, '持仓', 'Open')} value={String(data.open_count)} />
          <Stat label={tr(language, '单笔风险', 'Risk/trade')} value={`${(data.config.risk_per_trade_pct ?? 0).toFixed(1)}%`} />
          <Stat label={tr(language, '止损/止盈', 'Stop/Target')} value={`${data.config.stop_atr ?? 0}/${data.config.target_atr ?? 0} ATR`} />
        </div>
        {data.equity_series.length > 1 ? (
          <div className="mt-3">
            <EquitySpark points={data.equity_series} />
          </div>
        ) : null}
      </section>

      {data.open.length ? (
        <section className={ui.panel}>
          <h3 className="mb-2 text-sm font-semibold text-nofx-text">{tr(language, '当前持仓', 'Open positions')}</h3>
          <TradesTable language={language} trades={data.open} closed={false} />
        </section>
      ) : null}

      <section className={ui.panel}>
        <h3 className="mb-2 text-sm font-semibold text-nofx-text">{tr(language, '最近成交', 'Recent trades')}</h3>
        {closedRecent.length ? (
          <TradesTable language={language} trades={closedRecent.slice(0, 30)} closed />
        ) : (
          <p className={`py-4 text-center text-xs ${ui.muted}`}>
            {tr(language, '还没有平仓的交易（模拟盘会随行情慢慢积累）。', 'No closed trades yet (the paper account builds up over time).')}
          </p>
        )}
      </section>
    </div>
  )
}
