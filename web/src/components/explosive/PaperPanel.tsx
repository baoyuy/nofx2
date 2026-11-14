import { useEffect, useState } from 'react'
import useSWR from 'swr'
import { Plus, Trash2, Loader2 } from 'lucide-react'
import { explosiveApi, type PaperTrade, type PaperAccountSummary } from '../../lib/api/explosive'
import { configApi } from '../../lib/api/config'
import type { Exchange } from '../../types/config'
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
const price = (v: number) => (v < 0.01 ? v.toFixed(6) : v.toFixed(4))

function EquitySpark({ points }: { points: { equity: number }[] }) {
  const vals = points.map((p) => p.equity).filter((v) => Number.isFinite(v))
  if (vals.length < 2) return null
  const min = Math.min(...vals)
  const max = Math.max(...vals)
  const span = max - min || 1
  const w = 600
  const h = 90
  const d = vals
    .map((v, i) => {
      const x = (i / (vals.length - 1)) * w
      const y = h - ((v - min) / span) * h
      return `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
  const up = vals[vals.length - 1] >= vals[0]
  return (
    <svg viewBox={`0 0 ${w} ${h}`} className="h-24 w-full" preserveAspectRatio="none">
      <path d={d} fill="none" stroke={up ? BN.up : BN.down} strokeWidth={1.5} />
    </svg>
  )
}

function Stat({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div className="rounded-lg border border-[rgba(25,36,52,0.10)] px-2.5 py-2">
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
    <div className="min-w-0 overflow-x-auto">
      <table className="w-full min-w-[560px] whitespace-nowrap text-sm">
        <thead className="text-left text-xs text-nofx-text-muted">
          <tr>
            <th className="px-2 py-1.5 font-normal">{tr(language, '币种', 'Coin')}</th>
            <th className="px-2 text-right font-normal">{tr(language, '入场', 'Entry')}</th>
            <th className="px-2 text-right font-normal">{tr(language, '出场', 'Exit')}</th>
            <th className="px-2 text-right font-normal">{tr(language, '仓位', 'Notional')}</th>
            {closed ? <th className="px-2 text-right font-normal">{tr(language, '原因', 'Reason')}</th> : null}
            <th className="px-2 text-right font-normal">PnL</th>
            <th className="px-2 text-right font-normal">{tr(language, '时间', 'Time')}</th>
          </tr>
        </thead>
        <tbody className="text-nofx-text">
          {trades.map((t) => (
            <tr key={t.id} className={`border-t ${ui.border}`}>
              <td className="px-2 py-1.5 font-medium">{t.symbol.replace('USDT', '')}</td>
              <td className="px-2 text-right tabular-nums">{price(t.entry_price)}</td>
              <td className="px-2 text-right tabular-nums">{closed ? price(t.exit_price) : '—'}</td>
              <td className="px-2 text-right tabular-nums">{Math.round(t.notional)}</td>
              {closed ? <td className="px-2 text-right">{reasonLabel(language, t.exit_reason)}</td> : null}
              <td
                className="px-2 text-right font-semibold tabular-nums"
                style={{ color: (closed ? t.realized_pnl : 0) >= 0 ? BN.up : BN.down }}
              >
                {closed ? money(t.realized_pnl) : '—'}
              </td>
              <td className="px-2 text-right tabular-nums text-nofx-text-muted">{fmtTime(closed ? t.closed_at : t.opened_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export function PaperPanel({ language }: { language: string }) {
  const [accId, setAccId] = useState(0)
  const [adding, setAdding] = useState(false)
  const [exchanges, setExchanges] = useState<Exchange[]>([])
  const [selEx, setSelEx] = useState('')
  const [startEq, setStartEq] = useState(1000)
  const [busy, setBusy] = useState(false)

  const { data: accounts = [], mutate: mutAccounts } = useSWR(
    'market/explosive/paper/accounts',
    () => explosiveApi.paperAccounts(),
    { refreshInterval: 20000, revalidateOnFocus: false }
  )
  const { data, error, mutate } = useSWR(
    ['market/explosive/paper', accId],
    () => explosiveApi.paper(accId),
    { refreshInterval: 15000, revalidateOnFocus: false, keepPreviousData: true }
  )

  useEffect(() => {
    if (!adding || exchanges.length) return
    configApi
      .getExchangeConfigs()
      .then((list) => setExchanges(Array.isArray(list) ? list : []))
      .catch(() => setExchanges([]))
  }, [adding, exchanges.length])

  const openAdd = () => {
    setAdding(true)
    setSelEx('')
    setStartEq(1000)
  }
  const doAdd = async () => {
    setBusy(true)
    try {
      const ex = exchanges.find((e) => e.id === selEx)
      const name = ex ? ex.account_name || ex.name : '模拟账户'
      const id = await explosiveApi.addPaperAccount({ exchange_id: selEx, name, starting_equity: startEq })
      setAdding(false)
      await mutAccounts()
      setAccId(id)
      await mutate()
    } catch (e) {
      alert(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
  const doRemove = async (id: number) => {
    if (!confirm(tr(language, '删除这个模拟账户？（历史成交会保留）', 'Remove this paper account? Trade history is kept.'))) return
    setBusy(true)
    try {
      await explosiveApi.removePaperAccount(id)
      if (accId === id) setAccId(0)
      await mutAccounts()
    } catch (e) {
      alert(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const retColor = (data?.return_pct ?? 0) >= 0 ? BN.up : BN.down

  return (
    <div className="space-y-4">
      <section className={ui.panel}>
        <div className="mb-2 flex flex-wrap items-center gap-2">
          <h2 className="text-base font-semibold text-nofx-text">{tr(language, '规则量化模拟盘', 'Rule-based paper account')}</h2>
          <span className={`text-xs ${ui.muted}`}>
            {tr(language, '模拟成交、不花真钱；用来验证这套规则扣费后有没有正期望。', 'Simulated fills, no real money: tests whether the rule has an edge after fees.')}
          </span>
          <div className="ml-auto flex flex-wrap items-center gap-1">
            {accounts.map((a) => (
              <button
                key={a.id}
                type="button"
                onClick={() => setAccId(a.id)}
                className={`rounded-lg border px-2.5 py-1 text-xs ${accId === a.id ? 'border-nofx-gold text-nofx-gold' : `border-[rgba(25,36,52,0.14)] ${ui.muted}`}`}
              >
                {a.name}
              </button>
            ))}
            <button type="button" className={`flex items-center gap-1 ${ui.btnGhost}`} onClick={openAdd}>
              <Plus className="h-3.5 w-3.5" />
              {tr(language, '添加账户', 'Add account')}
            </button>
          </div>
        </div>

        {adding ? (
          <div className="mb-2 flex flex-wrap items-end gap-2 rounded-lg border border-[rgba(25,36,52,0.12)] p-2 text-xs">
            <label className="flex flex-col gap-1">
              <span className={ui.muted}>{tr(language, '配置页的账户', 'Account from config')}</span>
              <select
                value={selEx}
                onChange={(e) => setSelEx(e.target.value)}
                className="rounded border border-[rgba(25,36,52,0.14)] bg-transparent px-2 py-1"
              >
                <option value="">{tr(language, '选择一个账户…', 'Choose an account…')}</option>
                {exchanges.map((e) => (
                  <option key={e.id} value={e.id}>
                    {(e.account_name || e.name)} · {e.exchange_type}
                    {e.paper ? ' (模拟)' : ''}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1">
              <span className={ui.muted}>{tr(language, '起始资金 USDT', 'Starting equity')}</span>
              <input
                type="number"
                min={100}
                value={startEq}
                onChange={(e) => setStartEq(Number(e.target.value) || 0)}
                className="w-24 rounded border border-[rgba(25,36,52,0.14)] bg-transparent px-2 py-1 text-right"
              />
            </label>
            <button type="button" className={ui.btnGhost} disabled={!selEx || busy} onClick={doAdd}>
              {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : null}
              {tr(language, '确认添加', 'Add')}
            </button>
            <button type="button" className={ui.btnGhost} onClick={() => setAdding(false)}>
              {tr(language, '取消', 'Cancel')}
            </button>
          </div>
        ) : null}
      </section>

      {error ? (
        <p className={`${ui.panel} text-sm text-nofx-danger`}>
          {tr(language, '模拟盘加载失败：', 'Could not load paper account: ')}
          {error instanceof Error ? error.message : String(error)}
        </p>
      ) : null}

      <div className="grid items-start gap-4 lg:grid-cols-[minmax(0,1fr)_300px]">
        <div className="min-w-0 space-y-4">
          {data ? (
            <>
              <section className={ui.panel}>
                <div className="mb-2 flex items-baseline gap-2">
                  <h3 className="text-sm font-semibold text-nofx-text">{data.label}</h3>
                  <span className={`text-xs ${ui.muted}`}>{tr(language, '起始', 'Start')} {data.starting_equity.toFixed(0)} USDT</span>
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
                  <Stat label={tr(language, '单笔风险', 'Risk/trade')} value={`${(data.config?.risk_per_trade_pct ?? 0).toFixed(1)}%`} />
                  <Stat label={tr(language, '止损/止盈', 'Stop/Target')} value={`${data.config?.stop_atr ?? 0}/${data.config?.target_atr ?? 0} ATR`} />
                </div>
                <div className="mt-3">
                  <EquitySpark points={data.equity_series} />
                </div>
              </section>

              {data.open.length ? (
                <section className={ui.panel}>
                  <h3 className="mb-2 text-sm font-semibold text-nofx-text">{tr(language, '当前持仓', 'Open positions')}</h3>
                  <TradesTable language={language} trades={data.open} closed={false} />
                </section>
              ) : null}

              <section className={ui.panel}>
                <h3 className="mb-2 text-sm font-semibold text-nofx-text">{tr(language, '最近成交', 'Recent trades')}</h3>
                {data.recent.filter((t) => t.status === 'closed').length ? (
                  <TradesTable language={language} trades={data.recent.filter((t) => t.status === 'closed').slice(0, 30)} closed />
                ) : (
                  <p className={`py-4 text-center text-xs ${ui.muted}`}>
                    {tr(language, '还没有平仓的交易（模拟盘会随行情慢慢积累）。', 'No closed trades yet (builds up over time).')}
                  </p>
                )}
              </section>
            </>
          ) : (
            <p className={`${ui.panel} py-8 text-center ${ui.muted}`}>{tr(language, '加载中…', 'Loading…')}</p>
          )}
        </div>

        <aside className="space-y-3 lg:sticky lg:top-4">
          <section className={ui.panel}>
            <h3 className="mb-2 text-sm font-semibold text-nofx-text">{tr(language, '账户', 'Accounts')}</h3>
            <div className="space-y-2">
              {accounts.map((a: PaperAccountSummary) => (
                <div
                  key={a.id}
                  className={`rounded-lg border p-2 text-xs ${accId === a.id ? 'border-nofx-gold' : 'border-[rgba(25,36,52,0.12)]'}`}
                >
                  <div className="flex items-center justify-between">
                    <button type="button" className="truncate text-left font-medium text-nofx-text" onClick={() => setAccId(a.id)}>
                      {a.name}
                    </button>
                    {a.id !== 0 ? (
                      <button type="button" className="text-nofx-text-muted hover:text-nofx-danger" onClick={() => doRemove(a.id)} disabled={busy}>
                        <Trash2 className="h-3.5 w-3.5" />
                      </button>
                    ) : null}
                  </div>
                  <div className="mt-1 flex justify-between tabular-nums">
                    <span className={ui.muted}>{a.equity.toFixed(2)}</span>
                    <span style={{ color: a.return_pct >= 0 ? BN.up : BN.down }}>{signedPct(a.return_pct)}</span>
                  </div>
                  <div className={`mt-0.5 flex justify-between text-[10px] ${ui.muted}`}>
                    <span>{a.trades} {tr(language, '笔', 'trades')}</span>
                    <span>{tr(language, '胜率', 'win')} {(a.win_rate * 100).toFixed(0)}%</span>
                    <span>{tr(language, '持仓', 'open')} {a.open_count}</span>
                  </div>
                </div>
              ))}
            </div>
          </section>
        </aside>
      </div>
    </div>
  )
}
