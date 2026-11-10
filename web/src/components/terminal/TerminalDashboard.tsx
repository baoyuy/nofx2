import { useEffect, useMemo, useState } from 'react'
import { createPortal } from 'react-dom'
import type { CSSProperties } from 'react'
import useSWR, { mutate } from 'swr'
import { api } from '../../lib/api'
import { confirmToast, notify } from '../../lib/notify'
import type {
  SystemStatus,
  AccountInfo,
  Position,
  DecisionRecord,
  TraderInfo,
} from '../../types'
import { OrchestrationTopology } from './OrchestrationTopology'
import { OrderBook } from './OrderBook'
import { LiquidationMap } from './LiquidationMap'
import { KlineChart } from './KlineChart'
import { ExecutionLog } from './ExecutionLog'
import { formatRequestError } from '../../lib/requestErrors'
import { SignalMatrix } from './SignalMatrix'
import { RiskRadar } from './RiskRadar'
import { EdgeProfile } from './EdgeProfile'
import { useDemoEngine } from '../../lib/demo/useDemoEngine'
import { useLanguage } from '../../contexts/LanguageContext'
import { TerminalTime } from './TerminalTime'
import { MirrorPanel } from '../trader/MirrorPanel'

// Public market snapshots are shared across traders; focus must not trigger
// expensive scans. Keep the existing refresh budgets and hidden-page pause.
const SNAPSHOT_READ_OPTIONS = {
  revalidateOnFocus: false,
  revalidateOnReconnect: false,
  refreshWhenHidden: false,
  refreshWhenOffline: false,
  shouldRetryOnError: false,
  dedupingInterval: 30000,
}

// fixed height for the three row-1 panels so the row stays balanced at any width
const ROW1_H = 500
import { FlowMarkets, FLOW_ROWS } from './FlowMarkets'
import './terminal.css'

interface TerminalDashboardProps {
  selectedTrader?: TraderInfo
  traders?: TraderInfo[]
  selectedTraderId?: string
  onTraderSelect: (traderId: string) => void
  status?: SystemStatus
  account?: AccountInfo
  positions?: Position[]
  decisions?: DecisionRecord[]
}

function fmtUsd(n: number | undefined, signed = false): string {
  if (n == null || Number.isNaN(n)) return '—'
  const sign = signed && n > 0 ? '+' : n < 0 ? '-' : ''
  return `${sign}$${Math.abs(n).toLocaleString('en-US', { maximumFractionDigits: 2 })}`
}
function fmtPct(n: number | undefined): string {
  if (n == null || Number.isNaN(n)) return '—'
  return `${n >= 0 ? '+' : ''}${n.toFixed(2)}%`
}
/** Price with magnitude-aware precision: 64,416 · 184.2 · 2.3775 · 0.0067 */
function fmtPx(n: number | undefined): string {
  if (n == null || Number.isNaN(n) || n === 0) return '—'
  const dp = n >= 1000 ? 0 : n >= 100 ? 1 : n >= 1 ? 2 : 4
  return n.toLocaleString('en-US', { minimumFractionDigits: dp, maximumFractionDigits: dp })
}
function baseLabel(raw?: string): string {
  if (!raw) return ''
  return raw.toUpperCase().replace(/^XYZ:/, '').replace(/[-_]/g, '').replace(/(USDT|USDC|USD)$/, '')
}
function parseScanMinutes(scan?: string): number {
  if (!scan) return 15
  const m = scan.match(/(\d+)\s*m/i)
  if (m) return parseInt(m[1], 10)
  const h = scan.match(/(\d+)\s*h/i)
  if (h) return parseInt(h[1], 10) * 60
  const n = parseInt(scan, 10)
  return Number.isFinite(n) && n > 0 ? n : 15
}
function fmtTime(raw?: string | number): string {
  if (raw == null || raw === '') return ''
  let n = typeof raw === 'number' ? raw : Number(raw)
  if (Number.isFinite(n)) {
    if (n < 1e12) n *= 1000
    return new Date(n).toLocaleString('en-GB', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false })
  }
  const d = new Date(raw as string)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString('en-GB', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false })
}

/** Hold duration from entry/exit epoch-ms as a compact 45m / 2h10 / 1d3h. */
function fmtHold(entry?: number, exit?: number): string {
  if (!entry || !exit || exit <= entry) return '—'
  const mins = Math.round((exit - entry) / 60000)
  if (mins < 60) return `${mins}m`
  const h = Math.floor(mins / 60)
  const m = mins % 60
  if (h < 24) return m ? `${h}h${m}` : `${h}h`
  const d = Math.floor(h / 24)
  return `${d}d${h % 24}h`
}


export function TerminalDashboard({
  selectedTrader,
  traders,
  selectedTraderId,
  onTraderSelect,
  status: propStatus,
  account: propAccount,
  positions: propPositions,
  decisions: propDecisions,
}: TerminalDashboardProps) {
  const { language } = useLanguage()
  const isZh = language === 'zh'
  const traderId = selectedTrader?.trader_id || selectedTraderId
  const [closing, setClosing] = useState<string | null>(null)

  async function closePositionRow(symbol: string, side: 'LONG' | 'SHORT') {
    if (!traderId || closing) return
    const ok = await confirmToast(
      isZh ? `以市价平仓 ${symbol} ${side}？` : `Market-close ${symbol} ${side}?`,
      {
        title: isZh ? '平仓' : 'Close position',
        okText: isZh ? '平仓' : 'Close',
        cancelText: isZh ? '取消' : 'Cancel',
      }
    )
    if (!ok) return
    setClosing(symbol)
    try {
      await api.closePosition(traderId, symbol, side)
      notify.success(isZh ? `${symbol} ${side} 已平仓` : `${symbol} ${side} closed`)
      await Promise.all([
        mutate(`positions-${traderId}`),
        mutate(`account-${traderId}`),
      ])
    } catch (err) {
      notify.error(err instanceof Error ? err.message : isZh ? '平仓失败' : 'Close failed')
    } finally {
      setClosing(null)
    }
  }

  async function closeAllPositions(open: Position[]) {
    if (!traderId || closing || open.length === 0) return
    const ok = await confirmToast(
      isZh ? `以市价平掉全部 ${open.length} 个持仓？` : `Market-close ALL ${open.length} open positions?`,
      {
        title: isZh ? '全部平仓' : 'Flatten book',
        okText: isZh ? '全部平仓' : 'Close all',
        cancelText: isZh ? '取消' : 'Cancel',
      }
    )
    if (!ok) return
    setClosing('__all__')
    let failed = 0
    // Sequential: parallel closes race on exchange nonces / rate limits.
    for (const p of open) {
      const side = /long|buy/i.test(p.side) ? 'LONG' : 'SHORT'
      try {
        await api.closePosition(traderId, p.symbol, side)
      } catch {
        failed++
      }
    }
    await Promise.all([
      mutate(`positions-${traderId}`),
      mutate(`account-${traderId}`),
    ])
    if (failed === 0) notify.success(isZh ? '全部持仓已平仓' : 'All positions closed')
    else notify.error(isZh ? `${failed}/${open.length} 笔平仓失败` : `${failed}/${open.length} closes failed`)
    setClosing(null)
  }

  const { data: realFullStats } = useSWR(
    traderId ? ['full-stats', traderId] : null,
    () => api.getFullStats(traderId!, true),
    { ...SNAPSHOT_READ_OPTIONS, refreshInterval: 30000 }
  )
  const { data: realHistory } = useSWR(
    traderId ? ['pos-history', traderId] : null,
    () => api.getPositionHistory(traderId!, 50, true),
    { ...SNAPSHOT_READ_OPTIONS, refreshInterval: 60000 }
  )
  const { data: realConfig } = useSWR(
    traderId ? ['trader-config', traderId] : null,
    () => api.getTraderConfig(traderId!, true),
    { ...SNAPSHOT_READ_OPTIONS, refreshInterval: 120000 }
  )
  const { data: realFlow } = useSWR(
    ['flow-markets', '1h', 30],
    () => api.getFlowMarkets('1h', 30, true),
    // Public snapshot: start alongside trader discovery, not after account data.
    { ...SNAPSHOT_READ_OPTIONS, refreshInterval: 120000 }
  )
  // The signal matrix (and through it the cost/liquidation map and the
  // topology's signal column) follows the AI: it scores only this cycle's
  // candidates plus whatever the trader still holds. Hyperliquid synthetic
  // (xyz:) markets have no Binance data, so they are left out.
  const aiSymbols = useMemo(() => {
    const raw = [...(propPositions ?? []).map((p) => p.symbol), ...(propDecisions?.[0]?.candidate_coins ?? [])]
    const seen = new Set<string>()
    return raw
      .filter((s) => s && !/^xyz:/i.test(s))
      .map((s) => baseLabel(s).toUpperCase())
      .filter((s) => s && !seen.has(s) && (seen.add(s), true))
      .sort()
  }, [propPositions, propDecisions])
  const { data: realSignalRank } = useSWR(
    traderId && aiSymbols.length ? ['signal-rank', aiSymbols.join(',')] : null,
    () => api.getDirectionChangeLeaderboard(30, true, aiSymbols),
    { ...SNAPSHOT_READ_OPTIONS, refreshInterval: 120000 }
  )

  // Demo / showcase mode for product walkthroughs. Toggle with Shift+D (or the
  // discreet corner dot). Generates a fast, profitable-looking US-equity dataset
  // entirely client-side — it never touches the backend or any real account.
  // When off, real data flows through unchanged.
  const [demo, setDemo] = useState(false)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.shiftKey && (e.key === 'D' || e.key === 'd')) {
        const el = document.activeElement
        if (el && /^(input|textarea|select)$/i.test(el.tagName)) return
        setDemo((v) => !v)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])
  const sim = useDemoEngine(demo)
  const on = demo && !!sim

  const status = on ? sim!.status : propStatus
  const account = on ? sim!.account : propAccount
  const positions = on ? sim!.positions : propPositions
  const decisions = on ? sim!.decisions : propDecisions
  const fullStats = on ? sim!.fullStats : realFullStats
  const history = on ? sim!.history : realHistory
  const config = on ? (sim!.config as unknown as typeof realConfig) : realConfig
  const flow = on ? sim!.flow : realFlow
  const signalRank = on ? sim!.signalRank : realSignalRank

  const latest = decisions && decisions.length > 0 ? decisions[0] : undefined
  const candidateCoins = latest?.candidate_coins ?? []
  const flowItems = flow?.data?.inflow ?? []

  // When the bot holds nothing, the price panels show the busiest market by
  // 1h trades (per the net-flow board), then BTC.
  const heatmapSymbol = useMemo(() => {
    const busiest = [...flowItems].sort((a, b) => (b.trades || 0) - (a.trades || 0))[0]
    return busiest ? baseLabel(busiest.symbol) : 'BTC'
  }, [flowItems])

  // user can click a signal-matrix cell to drive both the cost/liq map and the
  // order book. Default to the instrument the bot is ACTUALLY holding (first
  // open position, else this cycle's first candidate) so the price panels match
  // the real traded symbol; only fall back to the busiest synthetic if the bot
  // holds nothing.
  const [selectedSym, setSelectedSym] = useState<string | null>(null)
  const defaultSym = useMemo(() => {
    // the bot's actual first open position (else this cycle's first candidate)
    const heldBases = [...(positions ?? []).map((p) => p.symbol), ...candidateCoins].map(baseLabel).filter(Boolean)
    return heldBases[0] || heatmapSymbol
  }, [positions, candidateCoins, heatmapSymbol])
  const activeSym = baseLabel(selectedSym || defaultSym)
  const activeSignal = signalRank?.items?.find((item) => baseLabel(item.symbol) === activeSym)
  const sourceSymbol = activeSignal?.symbol || [...(positions ?? []).map((p) => p.symbol), ...candidateCoins].find((s) => baseLabel(s) === activeSym) || selectedSym || defaultSym
  // xyz: symbols are Hyperliquid synthetic (stock/index) markets; everything
  // else is a crypto perpetual.
  const activeMarketType = /^xyz:/i.test(sourceSymbol) ? 'hip3_perp' : 'core_perp'
  const activeRequestSymbol = activeMarketType === 'hip3_perp' ? `xyz:${activeSym}` : activeSym

  const pnl = account?.total_pnl ?? 0
  const pnlPct = account?.total_pnl_pct ?? 0
  const up = pnl >= 0
  const running = status?.is_running

  // direction per symbol — priority: AI's actual decision > signal bias >
  // net flow > prevailing market majority (never blindly default to long).
  const dirFor = useMemo(() => {
    const dec = new Map<string, 'long' | 'short'>()
    ;(latest?.decisions ?? []).forEach((d) => {
      const b = baseLabel(d.symbol)
      if (d.action === 'open_long' || d.action === 'close_short') dec.set(b, 'long')
      else if (d.action === 'open_short' || d.action === 'close_long') dec.set(b, 'short')
    })
    const sig = new Map<string, 'long' | 'short'>()
    let bull = 0
    let bear = 0
    ;(signalRank?.items ?? []).forEach((s) => {
      const b = baseLabel(s.symbol)
      const bias = (s.bias || '').toLowerCase()
      if (bias === 'bearish') { sig.set(b, 'short'); bear++ }
      else if (bias === 'bullish') { sig.set(b, 'long'); bull++ }
    })
    const fl = new Map<string, 'long' | 'short'>()
    ;(flow?.data?.inflow ?? []).forEach((i) => fl.set(baseLabel(i.symbol), 'long'))
    ;(flow?.data?.outflow ?? []).forEach((i) => fl.set(baseLabel(i.symbol), 'short'))
    const majority: 'long' | 'short' = bear > bull ? 'short' : 'long'
    return (sym: string): 'long' | 'short' => {
      const b = baseLabel(sym)
      return dec.get(b) ?? sig.get(b) ?? fl.get(b) ?? majority
    }
  }, [latest, signalRank, flow])

  const scanMin = config?.scan_interval_minutes || parseScanMinutes(status?.scan_interval)
  const nextCycleMs = useMemo(() => {
    if (!latest?.timestamp) return null
    return new Date(latest.timestamp).getTime() + scanMin * 60_000
  }, [latest?.timestamp, scanMin])

  const recentTrades = (history?.positions ?? []).slice(0, 8)
  const symbolStats = useMemo(
    () => (history?.symbol_stats ?? []).slice().sort((a, b) => b.total_trades - a.total_trades).slice(0, 6),
    [history]
  )
  const maxSymTrades = symbolStats.reduce((m, s) => Math.max(m, s.total_trades), 1)

  const sc: CSSProperties = { padding: '10px 14px' }
  const cellBorder = '1px solid var(--tm-hair)'

  // Portal the trader selector + run status into the global nav so the app has
  // a single top bar (no separate dashboard titlebar).
  const [navSlot, setNavSlot] = useState<HTMLElement | null>(null)
  useEffect(() => {
    setNavSlot(document.getElementById('dash-header-slot'))
  }, [])

  return (
    <div className="nofx-terminal nofx-dashboard" style={{ minHeight: '100vh', padding: 0 }}>
      {/* discreet, unlabelled showcase toggle (Shift+D also works) */}
      <button
        type="button"
        onClick={() => setDemo((v) => !v)}
        aria-label={isZh ? '切换演示模式' : 'toggle presentation mode'}
        style={{
          position: 'fixed',
          right: 10,
          bottom: 10,
          zIndex: 9999,
          width: 12,
          height: 12,
          padding: 0,
          borderRadius: '50%',
          border: 'none',
          cursor: 'pointer',
          background: on ? 'var(--tm-up)' : 'rgba(25,36,52,0.2)',
          opacity: on ? 0.55 : 0.18,
        }}
      />
      {/* centered, capped content column — no border (keeps it from feeling
          embedded) but bounded so the aspect-ratio SVGs don't balloon on wide screens */}
      {navSlot &&
        createPortal(
          <span className="nofx-terminal tm-navslot" style={{ background: 'transparent', display: 'flex', alignItems: 'center', gap: 12, marginLeft: 16, paddingLeft: 16, borderLeft: '1px solid rgba(25,36,52,0.15)', fontSize: 11 }}>
            <span className="tm-sc tm-navslot-extra" style={{ color: 'var(--tm-muted)' }}>{isZh ? '编排' : 'orchestration'}</span>
            {traders && traders.length > 0 && (
              <select value={traderId || ''} onChange={(e) => onTraderSelect(e.target.value)} className="tm-mono"
                style={{ background: 'var(--tm-panel)', color: 'var(--tm-ink)', border: '1px solid var(--tm-hair)', borderRadius: 0, fontSize: 11, padding: '3px 6px' }}>
                {traders.map((t) => (<option key={t.trader_id} value={t.trader_id} style={{ color: '#111' }}>{t.trader_name}</option>))}
              </select>
            )}
            <span style={{ color: status?.trading_blocked ? 'var(--tm-dn)' : running ? 'var(--tm-up)' : 'var(--tm-muted)' }}>
              {status?.trading_blocked
                ? isZh
                  ? '● 交易已阻断'
                  : '● trading blocked'
                : running
                  ? isZh
                    ? '● 运行中'
                    : '● running'
                  : isZh
                    ? '○ 已停止'
                    : '○ stopped'}
            </span>
            <span className="tm-sc tm-navslot-extra" style={{ color: 'var(--tm-muted)' }}>{isZh ? '周期' : 'cycle'}</span><span className="tm-mono tm-navslot-extra" style={{ color: 'var(--tm-ink)' }}>{status?.cycle_number ?? status?.call_count ?? '—'}</span>
            <span className="tm-px tm-navslot-extra" style={{ fontSize: 12, color: 'var(--tm-ink)' }}><TerminalTime /></span>
          </span>,
          navSlot,
        )}
      <div className="nofx-dashboard-heading">
        <div><h1>{isZh ? '仪表盘' : 'Dashboard'}</h1><p>{isZh ? '业绩、持仓与 AI 的最新决策。' : 'Performance, positions and your AI’s latest decisions.'}</p></div>
        <span>{isZh ? '实时账户概览' : 'Live account overview'}</span>
      </div>
      <div className="tm-box nofx-dashboard-body" style={{ maxWidth: 1280, margin: '0 auto', border: 'none' }}>
        {!on && status?.trading_blocked && (
          <div role="alert" className="tm-mono" style={{ margin: '8px 14px 0', padding: '12px', fontSize: 12, border: '1px solid var(--tm-dn)', color: 'var(--tm-dn)', background: 'rgba(200,60,40,0.06)' }}>
            <strong>{isZh ? '交易已阻断' : 'Trading blocked'}</strong>
            <div className="tm-error">{formatRequestError(status.trading_error, isZh ? 'zh' : 'en')}</div>
            <div>{isZh
              ? '自动开仓、平仓暂不可用，请按上面的错误信息检查交易所账户授权。修复前不会为决策轮次调用付费 AI 或数据。'
              : 'Automatic opens and closes are unavailable. Check the exchange account authorization shown above. No paid AI or data calls are made for decision cycles until this is fixed.'}</div>
          </div>
        )}
        {/* runtime health banner — AI fee wallet dry / safe mode would otherwise
            only be visible in server logs while the bot silently idles */}
        {!on && status && (status.safe_mode || status.ai_wallet_status === 'empty' || status.ai_wallet_status === 'low') && (
          <div className="tm-mono" style={{ display: 'flex', gap: 10, alignItems: 'center', margin: '8px 14px 0', padding: '8px 12px', fontSize: 11, border: '1px solid var(--tm-down)', color: 'var(--tm-down)', background: 'rgba(200,60,40,0.06)', flexWrap: 'wrap' }}>
            <span style={{ fontWeight: 600 }}>
              {status.ai_wallet_status === 'empty'
                ? isZh
                  ? 'AI 手续费钱包的 USDC 已用完 — 决策正在失败。'
                  : 'AI fee wallet is out of USDC — decisions are failing.'
                : status.ai_wallet_status === 'low'
                  ? isZh
                    ? `AI 手续费钱包余额较低（${(status.ai_wallet_balance_usdc ?? 0).toFixed(2)} USDC）— 请尽快充值。`
                    : `AI fee wallet is low (${(status.ai_wallet_balance_usdc ?? 0).toFixed(2)} USDC) — top up soon.`
                  : isZh
                    ? '安全模式：AI 连续失败，暂不新开仓位。'
                    : 'Safe mode: AI failed repeatedly, no new positions are being opened.'}
            </span>
            <span style={{ color: 'var(--tm-ink-2)' }}>
              {status.ai_wallet_status === 'empty' || status.ai_wallet_status === 'low'
                ? isZh
                  ? '向 Claw402 钱包存入 Base USDC，交易员将自动恢复。'
                  : 'Deposit Base USDC to the Claw402 wallet, the trader recovers automatically.'
                : status.safe_mode_reason || ''}
            </span>
          </div>
        )}
        {/* first-run reassurance — a fresh autopilot looks idle for its first
            minute (the AI is reading the market); tell newcomers what to expect */}
        {!on && status?.is_running && (status.call_count ?? 0) <= 1 && !status.safe_mode && !status.trading_blocked && status.trading_checked_at && (
          <div className="tm-mono" style={{ display: 'flex', gap: 10, alignItems: 'center', margin: '8px 14px 0', padding: '8px 12px', fontSize: 11, border: '1px solid var(--tm-up)', color: 'var(--tm-ink)', background: 'rgba(40,140,80,0.06)', flexWrap: 'wrap' }}>
            <span style={{ fontWeight: 600, color: 'var(--tm-up)' }}>{isZh ? '你的 AI 已上线。' : 'Your AI is live.'}</span>
            <span style={{ color: 'var(--tm-ink-2)' }}>
              {isZh
                ? '它会先通读整个市场再行动 — 第一个决策通常在一两分钟内产生，并会出现在下方的执行日志中。你可以随时在配置页面停止它。'
                : 'It reads the whole market before acting — the first decision usually lands within a minute or two and will appear in the Execution Log below. You can stop it anytime from the Config page.'}
            </span>
          </div>
        )}
        {/* config / identity strip — first row, flows directly under the global nav */}
        <div className="tm-mono" style={{ display: 'flex', gap: 16, padding: '6px 14px', fontSize: 11, color: 'var(--tm-ink-2)', flexWrap: 'wrap' }}>
          <span style={{ fontWeight: 500 }}>{selectedTrader?.trader_name ?? 'NOFX'}</span>
          <span><span className="tm-sc">{isZh ? '模型 ' : 'model '}</span>{(() => {
            const raw = config?.ai_model || status?.ai_model || ''
            if (!raw) return '—'
            if (/claw402/i.test(raw)) return 'CLAW402'
            return raw.length > 16 ? raw.slice(0, 16).toUpperCase() : raw.toUpperCase()
          })()}</span>
          <span><span className="tm-sc">{isZh ? '策略 ' : 'strategy '}</span>{config?.strategy_name || selectedTrader?.strategy_name || '—'}</span>
          <span><span className="tm-sc">{isZh ? '杠杆 ' : 'lev '}</span>{config?.effective_btc_eth_leverage ?? config?.btc_eth_leverage ?? '—'}× / {config?.effective_altcoin_leverage ?? config?.altcoin_leverage ?? '—'}×</span>
          <span><span className="tm-sc">{isZh ? '扫描 ' : 'scan '}</span>{scanMin}m</span>
          <span><span className="tm-sc">{isZh ? '标的池 ' : 'universe '}</span>{candidateCoins.length}</span>
          <span><span className="tm-sc">{isZh ? '持仓 ' : 'positions '}</span>{positions?.length ?? 0}</span>
          <span style={{ marginLeft: 'auto' }}><span className="tm-sc">{isZh ? '下一周期 ' : 'next cycle '}</span><TerminalTime nextCycleMs={nextCycleMs} isZh={isZh} /></span>
        </div>
        <div className="tm-rule" />

        {/* metric row — "Total P/L" is equity-based (includes unrealized);
            "Realized P/L" is closed-trades only and matches PF/win-rate/sharpe,
            so the two never read as contradicting each other */}
        <div className="nofx-dashboard-metrics" style={{ display: 'grid', gridTemplateColumns: 'repeat(5, 1fr)' }}>
          {[
            { l: isZh ? '净值' : 'Equity', v: fmtUsd(account?.total_equity), c: 'var(--tm-ink)' },
            { l: isZh ? '总盈亏 · 含未实现' : 'Total P/L · incl. unrealized', v: `${fmtUsd(pnl, true)} (${fmtPct(pnlPct)})`, c: up ? 'var(--tm-up)' : 'var(--tm-dn)' },
            {
              l: isZh ? '已实现盈亏 · 已平仓交易' : 'Realized P/L · closed trades',
              v: fullStats != null ? fmtUsd(fullStats.total_pnl, true) : '—',
              c: fullStats != null && fullStats.total_pnl >= 0 ? 'var(--tm-up)' : 'var(--tm-dn)',
            },
            { l: isZh ? '利润因子' : 'Profit factor', v: fullStats != null ? fullStats.profit_factor.toFixed(2) : '—', c: 'var(--tm-ink)' },
            // max_drawdown_pct is already a percent (18.5 = -18.5%)
            { l: isZh ? '最大回撤' : 'Max drawdown', v: fullStats != null ? `-${fullStats.max_drawdown_pct.toFixed(1)}%` : '—', c: 'var(--tm-dn)' },
          ].map((m, i) => (
            <div key={m.l} style={{ padding: '12px 14px', borderRight: i < 4 ? cellBorder : 'none' }}>
              <div className="tm-sc">{m.l}</div>
              <div className="tm-mono" style={{ fontSize: 17, fontWeight: 500, color: m.c, marginTop: 3 }}>{m.v}</div>
            </div>
          ))}
        </div>
        <div className="tm-rule" />

        {/* trades summary */}
        {fullStats != null && (
          <>
            <div className="tm-mono" style={{ display: 'flex', gap: 18, padding: '6px 14px', fontSize: 11, color: 'var(--tm-ink-2)', flexWrap: 'wrap' }}>
              <span className="tm-sc">{isZh ? '交易' : 'trades'} <b style={{ color: 'var(--tm-ink)' }}>{fullStats.total_trades}</b></span>
              <span className="tm-sc tm-up">{isZh ? '胜' : 'win'} {fullStats.win_trades} ({fullStats.win_rate.toFixed(1)}%)</span>
              <span className="tm-sc tm-dn">{isZh ? '负' : 'loss'} {fullStats.loss_trades}</span>
              {/* fee-drag chain: gross realized − fees = net realized */}
              <span className="tm-sc">{isZh ? '毛利' : 'gross'} <b style={{ color: 'var(--tm-ink)' }}>{fmtUsd(fullStats.total_pnl + fullStats.total_fee, true)}</b></span>
              <span className="tm-sc">{isZh ? '手续费' : 'fees'} <b style={{ color: 'var(--tm-ink)' }}>-{fmtUsd(fullStats.total_fee)}</b></span>
              <span className="tm-sc">{isZh ? '净额' : 'net'} <b style={{ color: fullStats.total_pnl >= 0 ? 'var(--tm-up)' : 'var(--tm-dn)' }}>{fmtUsd(fullStats.total_pnl, true)}</b></span>
              <span className="tm-sc">{isZh ? '每笔夏普' : 'sharpe/trade'} <b style={{ color: 'var(--tm-ink)' }}>{fullStats.sharpe_ratio.toFixed(2)}</b></span>
              <span className="tm-sc">{isZh ? '平均盈/亏' : 'avg win/loss'} <b style={{ color: 'var(--tm-ink)' }}>{fullStats.avg_win.toFixed(2)}/{fullStats.avg_loss.toFixed(2)}</b></span>
            </div>
            <div className="tm-rule" />
          </>
        )}

        {/* ── row 1: cost/liq map · live L2 order book · signal matrix (instrument selector)
              all three columns are locked to one fixed height so the row is always
              balanced; the K-line flexes to fill any remaining space ── */}
        <div className="tm-grid-row" style={{ display: 'grid', gridTemplateColumns: 'minmax(0,1.1fr) minmax(0,0.95fr) minmax(0,1.05fr)' }}>
          <div style={{ ...sc, borderRight: cellBorder, height: ROW1_H, overflow: 'hidden' }}>
            {/* estimated from Binance data, so crypto perpetuals only */}
            <LiquidationMap
              symbol={activeSym}
              demo={on}
              marketType={activeMarketType}
              height={ROW1_H - 130}
            />
          </div>
          <div style={{ ...sc, borderRight: cellBorder, height: ROW1_H, overflow: 'hidden' }}>
            <OrderBook symbol={activeRequestSymbol} demo={on} markPrice={positions?.find((p) => baseLabel(p.symbol) === activeSym)?.entry_price} />
          </div>
          <div style={{ ...sc, height: ROW1_H, display: 'flex', flexDirection: 'column', minHeight: 0 }}>
            <SignalMatrix items={signalRank?.items} max={18} active={activeSym} onSelect={setSelectedSym} />
            {/* the live K-line always sits under the selector and flexes to fill */}
            <div className="tm-rule" style={{ margin: '10px 0 8px' }} />
            <div style={{ flex: 1, minHeight: 0 }}>
              <KlineChart symbol={activeRequestSymbol} fill demo={on} />
            </div>
          </div>
        </div>
        <div className="tm-rule" />

        {/* orchestration topology — second row, full width (the agent workflow) */}
        <div style={sc}>
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, marginBottom: 4 }}>
            <span className="tm-px" style={{ fontSize: 12 }}>{isZh ? '编排拓扑' : 'Orchestration topology'}</span>
            <span className="tm-sc">{isZh ? '编排拓扑 · 净流入 → 信号 → 执行 → 持有' : 'Orchestration topology · net inflow → signal → execute → hold'}</span>
          </div>
          <OrchestrationTopology
            layers={[
              {
                key: 'flow',
                title: isZh ? '流量' : 'FLOW',
                zh: '流量',
                items: [
                  ...(flow?.data?.inflow ?? []).map((i) => ({ symbol: i.symbol, dir: 'long' as const })),
                  ...(flow?.data?.outflow ?? []).map((i) => ({ symbol: i.symbol, dir: 'short' as const })),
                ],
              },
              {
                key: 'signal',
                title: isZh ? '信号' : 'SIGNAL',
                zh: '信号',
                items: (signalRank?.items ?? []).map((s) => ({
                  symbol: s.symbol,
                  dir: (() => {
                    const b = (s.bias || '').toLowerCase()
                    return b === 'bearish' ? ('short' as const) : b === 'bullish' ? ('long' as const) : ('flat' as const)
                  })(),
                })),
              },
              {
                // every candidate the AI actually judged this cycle (its full decision set)
                key: 'decision',
                title: isZh ? '决策' : 'DECISION',
                zh: '决策',
                items: candidateCoins.map((c) => ({ symbol: c, dir: dirFor(c) })),
              },
              {
                // executed & live: every open position is an executed order, so
                // EXECUTE mirrors the live book (this cycle's fills plus anything
                // still open from prior cycles) and flows straight into HOLD
                key: 'exec',
                title: isZh ? '执行' : 'EXECUTE',
                zh: '执行',
                items: (positions ?? []).map((p) => ({
                  symbol: p.symbol,
                  dir: (p.side || '').toLowerCase().includes('short') ? ('short' as const) : ('long' as const),
                })),
              },
              {
                key: 'hold',
                title: isZh ? '持有' : 'HOLD',
                zh: '持有',
                items: (positions ?? []).map((p) => ({
                  symbol: p.symbol,
                  dir: (p.side || '').toLowerCase().includes('short') ? ('short' as const) : ('long' as const),
                })),
              },
            ]}
          />
        </div>
        <div className="tm-rule" />

        {/* copy trading: the user's other Binance accounts copy this AI */}
        {traderId && !on && (
          <>
            <div style={sc}>
              <MirrorPanel
                language={language}
                traderId={traderId}
                traderExchangeId={selectedTrader?.exchange_id}
                className="mb-0 p-0"
              />
            </div>
            <div className="tm-rule" />
          </>
        )}

        {/* ── row 3: execution log · risk radar · recent trades ── */}
        <div className="tm-grid-row" style={{ display: 'grid', gridTemplateColumns: 'minmax(0,1.1fr) minmax(0,1fr) minmax(0,1fr)' }}>
          <div style={{ ...sc, borderRight: cellBorder }}>
            <ExecutionLog decisions={decisions} height={432} />
          </div>
          <div style={{ ...sc, borderRight: cellBorder }}>
            <RiskRadar positions={positions} account={account} config={config} fullStats={fullStats} />
          </div>
          <div style={sc}>
            {/* live open positions (the book right now) */}
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, marginBottom: 6 }}>
              <span className="tm-px" style={{ fontSize: 11 }}>{isZh ? '持仓' : 'Positions'}</span>
              <span className="tm-sc">{isZh ? '当前持仓 · 实时' : 'Current positions · live'}</span>
              <span className="tm-sc" style={{ marginLeft: 'auto' }}>{isZh ? `${positions?.length ?? 0} 个未平仓` : `${positions?.length ?? 0} open`}</span>
              {traderId && !on && positions && positions.length > 0 && (
                <button
                  type="button"
                  onClick={() => void closeAllPositions(positions)}
                  disabled={closing !== null}
                  className="tm-mono"
                  style={{
                    background: 'transparent',
                    border: '1px solid var(--tm-dn)',
                    color: 'var(--tm-dn)',
                    borderRadius: 3,
                    fontSize: 9,
                    padding: '1px 6px',
                    cursor: closing ? 'not-allowed' : 'pointer',
                    opacity: closing ? 0.5 : 1,
                  }}
                >
                  {closing === '__all__' ? (isZh ? '平仓中…' : 'closing…') : isZh ? '全部平仓' : 'close all'}
                </button>
              )}
            </div>
            {positions && positions.length > 0 ? (
              <div className="tm-pos-scroll"><table className="tm-mono tm-pos-table" style={{ width: '100%', borderCollapse: 'collapse', fontSize: 11 }}>
                <thead>
                  <tr className="tm-sc" style={{ fontSize: 9 }}>
                    <td style={{ padding: '0 0 3px' }}>{isZh ? '币种' : 'symbol'}</td>
                    <td style={{ padding: '0 0 3px' }}>{isZh ? '方向·杠杆' : 'side·lev'}</td>
                    <td style={{ padding: '0 0 3px', textAlign: 'right' }}>{isZh ? '入场价' : 'entry'}</td>
                    <td style={{ padding: '0 0 3px', textAlign: 'right' }}>{isZh ? '规模' : 'size'}</td>
                    <td style={{ padding: '0 0 3px', textAlign: 'right' }}>{isZh ? '盈亏' : 'PnL'}</td>
                    <td style={{ padding: '0 0 3px', textAlign: 'right' }}>{isZh ? '收益率%' : 'return%'}</td>
                    {traderId && !on && <td style={{ padding: '0 0 3px' }} />}
                  </tr>
                </thead>
                <tbody>
                  {positions.map((p, i) => {
                    const long = /long|buy/i.test(p.side)
                    const win = (p.unrealized_pnl ?? 0) >= 0
                    const notional = Math.abs(p.quantity ?? 0) * (p.mark_price || p.entry_price || 0)
                    return (
                      <tr key={`${p.symbol}-${i}`} style={{ borderTop: '1px solid var(--tm-hair)' }}>
                        <td style={{ padding: '5px 0', fontWeight: 500 }}>{baseLabel(p.symbol)}</td>
                        <td style={{ padding: '5px 0' }} className={long ? 'tm-up' : 'tm-dn'}>{isZh ? (long ? '做多' : '做空') : long ? 'long' : 'short'} <span style={{ color: 'var(--tm-muted)' }}>{p.leverage}×</span></td>
                        <td style={{ padding: '5px 0', textAlign: 'right', color: 'var(--tm-ink-2)' }}>{fmtPx(p.entry_price)}</td>
                        <td style={{ padding: '5px 0', textAlign: 'right', color: 'var(--tm-ink-2)' }}>{fmtUsd(notional)}</td>
                        <td style={{ padding: '5px 0', textAlign: 'right' }} className={win ? 'tm-up' : 'tm-dn'}>{fmtUsd(p.unrealized_pnl, true)}</td>
                        <td style={{ padding: '5px 0', textAlign: 'right' }} className={win ? 'tm-up' : 'tm-dn'}>{(p.unrealized_pnl_pct ?? 0).toFixed(2)}%</td>
                        {traderId && !on && (
                          <td style={{ padding: '5px 0 5px 8px', textAlign: 'right', width: 1 }}>
                            <button
                              type="button"
                              onClick={() => void closePositionRow(p.symbol, long ? 'LONG' : 'SHORT')}
                              disabled={closing !== null}
                              title={isZh ? `平仓 ${p.symbol}` : `Close ${p.symbol}`}
                              className="tm-mono"
                              style={{
                                background: 'transparent',
                                border: '1px solid var(--tm-dn)',
                                color: 'var(--tm-dn)',
                                borderRadius: 3,
                                fontSize: 9,
                                padding: '1px 5px',
                                cursor: closing ? 'not-allowed' : 'pointer',
                                opacity: closing ? 0.5 : 1,
                              }}
                            >
                              {closing === p.symbol ? '…' : isZh ? '平仓' : 'close'}
                            </button>
                          </td>
                        )}
                      </tr>
                    )
                  })}
                </tbody>
              </table></div>
            ) : <div className="tm-sc" style={{ padding: '8px 0' }}>{isZh ? '暂无持仓。' : 'No open positions.'}</div>}

            <div className="tm-rule" style={{ margin: '12px 0 10px' }} />

            <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, marginBottom: 6 }}>
              <span className="tm-px" style={{ fontSize: 11 }}>{isZh ? '最近交易' : 'Recent trades'}</span>
              <span className="tm-sc">{isZh ? '最近平仓 · 币种/方向/持仓时长/盈亏' : 'Recent closes · symbol/side/hold/pnl'}</span>
            </div>
            {recentTrades.length > 0 ? (
              <div className="tm-pos-scroll"><table className="tm-mono tm-pos-table" style={{ width: '100%', borderCollapse: 'collapse', fontSize: 11 }}>
                <thead>
                  <tr className="tm-sc" style={{ fontSize: 9 }}>
                    <td style={{ padding: '0 0 3px' }}>{isZh ? '币种' : 'symbol'}</td>
                    <td style={{ padding: '0 0 3px' }}>{isZh ? '方向' : 'side'}</td>
                    <td style={{ padding: '0 0 3px', textAlign: 'right' }}>{isZh ? '持仓时长' : 'hold'}</td>
                    <td style={{ padding: '0 0 3px' }}>{isZh ? ' 平仓时间' : ' closed'}</td>
                    <td style={{ padding: '0 0 3px', textAlign: 'right' }}>{isZh ? '盈亏' : 'PnL'}</td>
                  </tr>
                </thead>
                <tbody>
                  {recentTrades.map((p) => {
                    // realized_pnl is gross price PnL; show it net of open+close fees
                    const net = (p.realized_pnl || 0) - (p.fee || 0)
                    const win = net >= 0
                    return (
                      <tr key={p.id} style={{ borderTop: '1px solid var(--tm-hair)' }}>
                        <td style={{ padding: '5px 0', fontWeight: 500 }}>{baseLabel(p.symbol)}</td>
                        <td style={{ padding: '5px 0' }} className={p.side === 'long' || p.side === 'LONG' ? 'tm-up' : 'tm-dn'}>{isZh ? (p.side.toLowerCase() === 'long' ? '做多' : '做空') : p.side.toLowerCase()}</td>
                        <td style={{ padding: '5px 0', textAlign: 'right', color: 'var(--tm-ink-2)' }}>{fmtHold(p.entry_time, p.exit_time)}</td>
                        <td style={{ padding: '5px 0 5px 6px', color: 'var(--tm-muted)' }}>{fmtTime(p.exit_time)}</td>
                        <td style={{ padding: '5px 0', textAlign: 'right' }} className={win ? 'tm-up' : 'tm-dn'}>{fmtUsd(net, true)}</td>
                      </tr>
                    )
                  })}
                </tbody>
              </table></div>
            ) : <div className="tm-sc" style={{ padding: '8px 0' }}>{isZh ? '暂无已平仓交易。' : 'No closed trades yet.'}</div>}
          </div>
        </div>
        <div className="tm-rule" />

        {/* market net inflow (Binance taker flow) · by-symbol history · edge profile — footer */}
        <div className="tm-grid-row" style={{ display: 'grid', gridTemplateColumns: 'minmax(0,1.2fr) minmax(0,0.9fr) minmax(0,0.9fr)' }}>
          <div style={{ ...sc, borderRight: cellBorder }}>
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, marginBottom: 8 }}>
              <span className="tm-px" style={{ fontSize: 12 }}>{isZh ? '市场净流入' : 'Market net inflow'}</span>
              <span className="tm-sc">{isZh ? `主动买入 − 主动卖出 · ${flow?.data?.window || '1h'} · 币安` : `Taker buys − taker sells · ${flow?.data?.window || '1h'} · Binance`}</span>
              <span className="tm-sc" style={{ marginLeft: 'auto' }}>{isZh ? `${Math.min(flowItems.length, FLOW_ROWS)} 个市场` : `${Math.min(flowItems.length, FLOW_ROWS)} markets`}</span>
            </div>
            <FlowMarkets items={flowItems} window={flow?.data?.window} />
          </div>
          <div style={{ ...sc, borderRight: cellBorder }}>
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, marginBottom: 8 }}>
              <span className="tm-px" style={{ fontSize: 11 }}>{isZh ? '按币种' : 'By symbol'}</span>
              <span className="tm-sc">{isZh ? '分币种历史 · 交易/胜/盈亏' : 'By-symbol history · trades/win/pnl'}</span>
            </div>
            {symbolStats.length > 0 ? symbolStats.map((s) => (
              <div key={s.symbol} style={{ marginBottom: 7 }}>
                <div className="tm-mono" style={{ display: 'flex', fontSize: 11, marginBottom: 2 }}>
                  <span style={{ fontWeight: 500 }}>{baseLabel(s.symbol)}</span>
                  <span className="tm-sc" style={{ marginLeft: 8 }}>{isZh ? `${s.total_trades} 笔交易 · 胜率 ${s.win_rate.toFixed(0)}%` : `${s.total_trades} trades · ${s.win_rate.toFixed(0)}% win`}</span>
                  <span className={s.total_pnl >= 0 ? 'tm-up' : 'tm-dn'} style={{ marginLeft: 'auto' }}>{fmtUsd(s.total_pnl, true)}</span>
                </div>
                <div style={{ height: 4, background: 'var(--tm-hair)' }}>
                  <div style={{ height: 4, width: `${(s.total_trades / maxSymTrades) * 100}%`, background: s.total_pnl >= 0 ? 'var(--tm-up)' : 'var(--tm-dn)' }} />
                </div>
              </div>
            )) : <div className="tm-sc">{isZh ? '暂无分币种历史。' : 'No symbol history.'}</div>}
          </div>
          <div style={sc}>
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, marginBottom: 8 }}>
              <span className="tm-px" style={{ fontSize: 11 }}>{isZh ? '优势分布' : 'Edge profile'}</span>
              <span className="tm-sc">{isZh ? '按持仓时长与方向的净额 · 扣费后' : 'Net by hold time & side · after fees'}</span>
            </div>
            <EdgeProfile positions={history?.positions} />
          </div>
        </div>
      </div>
    </div>
  )
}

export default TerminalDashboard
