import { useEffect, useMemo, useState } from 'react'
import useSWR from 'swr'
import { api } from '../lib/api'
import { useLanguage } from '../contexts/LanguageContext'
import { tr, ui } from '../components/market/marketUi'

function num(v: any): number {
  const n = typeof v === 'number' ? v : parseFloat(v)
  return Number.isFinite(n) ? n : 0
}
function pick(obj: any, keys: string[]): number {
  for (const k of keys) {
    if (obj && obj[k] != null && obj[k] !== '') return num(obj[k])
  }
  return 0
}

// Program-trading dashboard: a paper trader driven by the 4h-stable rule
// strategy. No AI decisions / event cards — just account, positions, trades and
// the live 4h-stable ranking.
export function ProgramDashboardPage() {
  const { language } = useLanguage()
  const { data: traders } = useSWR('program-traders', () => api.getTraders(true), {
    refreshInterval: 15000,
  })
  const list = (traders as any[]) || []
  const [traderId, setTraderId] = useState<string>('')
  useEffect(() => {
    if (!traderId && list.length) setTraderId(list[0].trader_id || list[0].id)
  }, [list, traderId])

  const { data: account } = useSWR(traderId ? `pd-acc-${traderId}` : null, () => api.getAccount(traderId, true), { refreshInterval: 15000 })
  const { data: positions } = useSWR(traderId ? `pd-pos-${traderId}` : null, () => api.getPositions(traderId, true), { refreshInterval: 15000 })
  const { data: hist } = useSWR(traderId ? `pd-hist-${traderId}` : null, () => api.getPositionHistory(traderId, 50, true), { refreshInterval: 30000 })
  const { data: equity } = useSWR(traderId ? `pd-eq-${traderId}` : null, () => api.getEquityHistory(traderId, true), { refreshInterval: 60000 })
  const { data: rank } = useSWR('copytrade/coin-ranking:stable', () => api.getCoinRanking('stable'), { refreshInterval: 30000 })

  const eqSeries = useMemo(() => {
    const arr = (equity as any[]) || []
    return arr.map((e) => pick(e, ['total_equity', 'equity', 'value', 'totalEquity', 'balance'])).filter((n) => n > 0)
  }, [equity])
  const eqLine = useMemo(() => {
    if (eqSeries.length < 2) return ''
    const min = Math.min(...eqSeries)
    const max = Math.max(...eqSeries)
    const span = max - min || 1
    return eqSeries
      .map((v, i) => `${(i / (eqSeries.length - 1)) * 100},${100 - ((v - min) / span) * 100}`)
      .join(' ')
  }, [eqSeries])

  const posList = (positions as any[]) || []
  const heldSyms = new Set(posList.map((p) => String(p.symbol || '').toUpperCase()))
  const trades = ((hist as any)?.positions || (hist as any)?.trades || []) as any[]
  const rankItems = ((rank as any)?.items || []) as any[]

  const equityNow = pick(account, ['total_equity', 'equity', 'totalEquity'])
  const totalPnl = pick(account, ['total_pnl', 'totalPnl', 'pnl'])
  const totalPnlPct = pick(account, ['total_pnl_pct', 'totalPnlPct', 'pnl_pct'])
  const dayPnl = pick(account, ['daily_pnl', 'day_pnl', 'today_pnl'])
  const winRate = pick((hist as any) || account, ['win_rate', 'winRate'])
  const maxDD = pick((hist as any) || account, ['max_drawdown', 'maxDrawdown'])
  const posCount = posList.length

  const Card = ({ label, value, sub, tone }: { label: string; value: string; sub?: string; tone?: string }) => (
    <div className={`${ui.panel} p-3`}>
      <div className={`${ui.muted} text-xs`}>{label}</div>
      <div className={`mt-1 text-lg font-semibold tabular-nums ${tone || 'text-nofx-text'}`}>{value}</div>
      {sub ? <div className={`${ui.muted} text-[11px]`}>{sub}</div> : null}
    </div>
  )

  return (
    <div className="mx-auto max-w-6xl space-y-3 p-3">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-lg font-semibold text-nofx-text">{tr(language, '程序交易看板（四小时稳定分·无AI）', 'Program dashboard (4h stable, no AI)')}</h1>
        <select
          className={`${ui.input} ml-auto`}
          value={traderId}
          onChange={(e) => setTraderId(e.target.value)}
        >
          {list.length === 0 ? <option value="">{tr(language, '无交易员', 'no traders')}</option> : null}
          {list.map((t) => (
            <option key={t.trader_id || t.id} value={t.trader_id || t.id}>
              {t.trader_name || t.name || t.trader_id}
            </option>
          ))}
        </select>
      </div>

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
        <Card label={tr(language, '净值', 'Equity')} value={equityNow.toFixed(2)} />
        <Card label={tr(language, '总盈亏', 'Total PnL')} value={totalPnl.toFixed(2)} sub={`${totalPnlPct.toFixed(2)}%`} tone={totalPnl >= 0 ? 'text-nofx-success' : 'text-nofx-danger'} />
        <Card label={tr(language, '当日盈亏', 'Day PnL')} value={dayPnl.toFixed(2)} tone={dayPnl >= 0 ? 'text-nofx-success' : 'text-nofx-danger'} />
        <Card label={tr(language, '持仓数', 'Positions')} value={String(posCount)} />
        <Card label={tr(language, '胜率', 'Win rate')} value={`${winRate.toFixed(1)}%`} />
        <Card label={tr(language, '最大回撤', 'Max DD')} value={`${maxDD.toFixed(1)}%`} />
      </div>

      <div className={`${ui.panel} p-3`}>
        <div className={`${ui.muted} mb-1 text-xs`}>{tr(language, '权益曲线', 'Equity curve')}</div>
        {eqLine ? (
          <svg viewBox="0 0 100 100" preserveAspectRatio="none" className="h-32 w-full">
            <polyline points={eqLine} fill="none" stroke="var(--nofx-success, #2e8b57)" strokeWidth="1.2" vectorEffect="non-scaling-stroke" />
          </svg>
        ) : (
          <div className={`${ui.muted} text-xs`}>{tr(language, '暂无权益历史', 'No equity history yet')}</div>
        )}
      </div>

      <div className="grid gap-3 lg:grid-cols-2">
        <div className={`${ui.panel} p-3`}>
          <div className="mb-1 text-sm font-medium text-nofx-text">{tr(language, '当前持仓', 'Open positions')}</div>
          {posList.length === 0 ? (
            <div className={`${ui.muted} text-xs`}>{tr(language, '无持仓', 'No positions')}</div>
          ) : (
            <table className="w-full text-xs tabular-nums">
              <thead><tr className={ui.muted}><th className="text-left">{tr(language, '币', 'Coin')}</th><th className="text-left">{tr(language, '方向', 'Side')}</th><th className="text-right">{tr(language, '开仓价', 'Entry')}</th><th className="text-right">{tr(language, '数量', 'Qty')}</th><th className="text-right">{tr(language, '浮动盈亏', 'uPnL')}</th></tr></thead>
              <tbody>
                {posList.map((p, i) => {
                  const up = pick(p, ['unrealized_pnl', 'unrealizedPnl', 'pnl', 'profit'])
                  return (
                    <tr key={i} className="border-t border-[rgba(25,36,52,0.06)]">
                      <td>{p.symbol}</td>
                      <td className={p.side === 'short' ? 'text-nofx-danger' : 'text-nofx-success'}>{p.side}</td>
                      <td className="text-right">{num(p.entry_price ?? p.entryPrice).toFixed(4)}</td>
                      <td className="text-right">{num(p.quantity ?? p.qty).toFixed(2)}</td>
                      <td className={`text-right ${up >= 0 ? 'text-nofx-success' : 'text-nofx-danger'}`}>{up.toFixed(2)}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          )}
        </div>

        <div className={`${ui.panel} p-3`}>
          <div className="mb-1 text-sm font-medium text-nofx-text">{tr(language, '最近成交', 'Recent trades')}</div>
          {trades.length === 0 ? (
            <div className={`${ui.muted} text-xs`}>{tr(language, '暂无成交', 'No trades')}</div>
          ) : (
            <table className="w-full text-xs tabular-nums">
              <thead><tr className={ui.muted}><th className="text-left">{tr(language, '币', 'Coin')}</th><th className="text-left">{tr(language, '方向', 'Side')}</th><th className="text-right">{tr(language, '平仓价', 'Close')}</th><th className="text-right">{tr(language, '盈亏', 'PnL')}</th><th className="text-left">{tr(language, '原因', 'Reason')}</th></tr></thead>
              <tbody>
                {trades.slice(0, 10).map((t, i) => {
                  const pnl = pick(t, ['realized_pnl', 'pnl', 'profit', 'close_pnl'])
                  return (
                    <tr key={i} className="border-t border-[rgba(25,36,52,0.06)]">
                      <td>{t.symbol}</td>
                      <td className={t.side === 'short' ? 'text-nofx-danger' : 'text-nofx-success'}>{t.side}</td>
                      <td className="text-right">{num(t.close_price ?? t.exit_price ?? t.closePrice).toFixed(4)}</td>
                      <td className={`text-right ${pnl >= 0 ? 'text-nofx-success' : 'text-nofx-danger'}`}>{pnl.toFixed(2)}</td>
                      <td className={ui.muted}>{t.close_reason || t.reason || t.exit_reason || ''}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          )}
        </div>
      </div>

      <div className={`${ui.panel} p-3`}>
        <div className="mb-1 text-sm font-medium text-nofx-text">
          {tr(language, `四小时稳定分榜单（${rankItems.length}）`, `4h stable ranking (${rankItems.length})`)}
        </div>
        <div className="flex flex-wrap gap-2">
          {rankItems.map((r, i) => {
            const held = heldSyms.has(String(r.symbol || '').toUpperCase())
            const short = r.direction === 'short'
            return (
              <span key={i} className={`rounded border px-2 py-1 text-xs tabular-nums ${short ? 'text-nofx-danger border-nofx-danger/40' : 'text-nofx-success border-nofx-success/40'} ${held ? 'ring-1 ring-nofx-gold' : ''}`}>
                #{i + 1} {String(r.symbol || '').replace(/USDT$/, '')} {short ? tr(language, '空', 'S') : tr(language, '多', 'L')} {Math.round(num(r.stable_score))}
                {held ? ' ★' : ''}
              </span>
            )
          })}
          {rankItems.length === 0 ? <span className={`${ui.muted} text-xs`}>{tr(language, '暂无评分', 'No scored coins')}</span> : null}
        </div>
      </div>
    </div>
  )
}
