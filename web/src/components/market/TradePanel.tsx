import { useCallback, useEffect, useState } from 'react'
import { Loader2 } from 'lucide-react'
import { api } from '../../lib/api'
import type { MarketAccount, MarketPosition } from '../../lib/api/market'
import { confirmToast, notify } from '../../lib/notify'
import type { Exchange } from '../../types'
import { BN } from './BinanceChart'
import { loadPref, savePref, tr, ui } from './marketUi'

export interface OrderSuggestion {
  nonce: number
  side: 'long' | 'short'
  leverage: number
  stopLoss: number
  takeProfit: number
}

interface Props {
  symbol: string
  exchanges: Exchange[]
  price: number
  decimals: number
  language: string
  suggestion: OrderSuggestion | null
}

const leverageOptions = [1, 2, 3, 5, 10, 20]

export const isPaper = (e?: Exchange) => e?.exchange_type === 'binance_paper'
export const accountLabel = (e: Exchange, language: string) =>
  `${isPaper(e) ? tr(language, '模拟盘', 'Paper') : tr(language, '币安实盘', 'Binance LIVE')} · ${e.account_name || e.name}`

export function TradePanel({
  symbol,
  exchanges,
  price,
  decimals,
  language,
  suggestion,
}: Props) {
  const [exchangeId, setExchangeId] = useState(() => loadPref('exchange', ''))
  const exchange = exchanges.find((e) => e.id === exchangeId) || exchanges[0]
  const [account, setAccount] = useState<MarketAccount | null>(null)
  const [accountError, setAccountError] = useState('')
  const [tick, setTick] = useState(0)
  const refresh = useCallback(() => setTick((t) => t + 1), [])

  useEffect(() => {
    if (!exchange) return
    let cancelled = false
    const load = async () => {
      try {
        const a = await api.getMarketAccount(exchange.id)
        if (cancelled) return
        setAccount(a)
        setAccountError('')
      } catch (err) {
        if (!cancelled)
          setAccountError(err instanceof Error ? err.message : 'error')
      }
    }
    void load()
    const timer = window.setInterval(
      () => document.visibilityState === 'visible' && void load(),
      10000
    )
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [exchange, tick])

  if (exchanges.length === 0) {
    return (
      <section className={ui.panel}>
        <div className={ui.title}>{tr(language, '下单', 'Trade')}</div>
        <p className={ui.muted}>
          {tr(
            language,
            '还没有币安账户。请到「配置 → 连接」添加「Binance Paper」（模拟盘，不用 API Key）或币安实盘账户。',
            'No Binance account. Add "Binance Paper" (no API key) or a live Binance account under Configuration → Connections.'
          )}
        </p>
      </section>
    )
  }

  const bal =
    account && account.exchange_type === exchange?.exchange_type
      ? account.balance
      : null
  const num = (k: string) =>
    typeof bal?.[k] === 'number' ? (bal[k] as number) : 0

  return (
    <section className={ui.panel} aria-label={tr(language, '下单', 'Trade')}>
      <div className={ui.title}>{tr(language, '下单', 'Trade')}</div>
      <label className={ui.label} htmlFor="market-account">
        {tr(language, '账户', 'Account')}
      </label>
      <select
        id="market-account"
        className={ui.input}
        value={exchange?.id}
        onChange={(e) => {
          setExchangeId(e.target.value)
          savePref('exchange', e.target.value)
          setAccount(null)
        }}
      >
        {exchanges.map((e) => (
          <option key={e.id} value={e.id}>
            {accountLabel(e, language)}
          </option>
        ))}
      </select>
      {!isPaper(exchange) ? (
        <p className="mt-1 text-xs font-semibold" style={{ color: BN.down }}>
          {tr(
            language,
            '⚠ 实盘账户：下单会使用真实资金',
            '⚠ Live account: orders use real funds'
          )}
        </p>
      ) : null}
      {accountError ? (
        <p role="alert" className="mt-1 text-xs" style={{ color: BN.down }}>
          {accountError}
        </p>
      ) : null}
      {bal ? (
        <div className="mt-2 grid grid-cols-2 gap-1 text-xs">
          <span className={ui.muted}>
            {tr(language, '账户权益', 'Equity')}{' '}
            <span className={ui.text}>
              {(
                num('totalWalletBalance') + num('totalUnrealizedProfit')
              ).toFixed(2)}
            </span>
          </span>
          <span className={ui.muted}>
            {tr(language, '可用', 'Available')}{' '}
            <span className={ui.text}>
              {num('availableBalance').toFixed(2)}
            </span>
          </span>
        </div>
      ) : null}

      {exchange ? (
        <OrderForm
          key={`${exchange.id}-${suggestion?.nonce ?? 0}`}
          symbol={symbol}
          exchange={exchange}
          price={price}
          decimals={decimals}
          language={language}
          suggestion={suggestion}
          onDone={refresh}
        />
      ) : null}

      <Positions
        positions={(bal && account?.positions) || []}
        exchange={exchange}
        decimals={decimals}
        language={language}
        onDone={refresh}
      />
    </section>
  )
}

const nearestLeverage = (v: number) =>
  leverageOptions.reduce(
    (best, x) => (Math.abs(x - v) < Math.abs(best - v) ? x : best),
    3
  )

interface OrderFormProps {
  symbol: string
  exchange: Exchange
  price: number
  decimals: number
  language: string
  suggestion: OrderSuggestion | null
  onDone: () => void
}

// Keyed by suggestion nonce, so "fill from AI" simply remounts with new defaults.
function OrderForm({
  symbol,
  exchange,
  price,
  decimals,
  language,
  suggestion,
  onDone,
}: OrderFormProps) {
  const [side, setSide] = useState<'long' | 'short'>(suggestion?.side || 'long')
  const [margin, setMargin] = useState(() => loadPref('margin', '20'))
  const [leverage, setLeverage] = useState(() =>
    nearestLeverage(suggestion?.leverage || Number(loadPref('leverage', '3')))
  )
  const [stopLoss, setStopLoss] = useState(
    suggestion?.stopLoss ? suggestion.stopLoss.toFixed(decimals) : ''
  )
  const [takeProfit, setTakeProfit] = useState(
    suggestion?.takeProfit ? suggestion.takeProfit.toFixed(decimals) : ''
  )
  const [submitting, setSubmitting] = useState(false)

  const marginNum = Number(margin)
  const notional = marginNum > 0 ? marginNum * leverage : 0
  const live = !isPaper(exchange)

  const submit = async () => {
    if (!(marginNum > 0)) {
      notify.error(tr(language, '请填写保证金金额', 'Enter a margin amount'))
      return
    }
    const sideText =
      side === 'long'
        ? tr(language, '开多', 'Long')
        : tr(language, '开空', 'Short')
    if (live) {
      const ok = await confirmToast(
        tr(
          language,
          `这是币安实盘，会用真实资金下单：\n${symbol} ${sideText}，保证金 ${marginNum} USDT，${leverage} 倍杠杆，仓位约 ${notional.toFixed(2)} USDT。`,
          `This is a LIVE Binance order with real funds:\n${symbol} ${sideText}, margin ${marginNum} USDT, ${leverage}x, about ${notional.toFixed(2)} USDT notional.`
        ),
        {
          title: tr(language, '确认实盘下单', 'Confirm live order'),
          okText: tr(language, '确认下单', 'Place order'),
          cancelText: tr(language, '取消', 'Cancel'),
        }
      )
      if (!ok) return
    }
    setSubmitting(true)
    try {
      const r = await api.placeMarketOrder({
        exchange_id: exchange.id,
        symbol,
        side,
        margin_usdt: marginNum,
        leverage,
        stop_loss: Number(stopLoss) || 0,
        take_profit: Number(takeProfit) || 0,
        confirm_live: live,
      })
      savePref('margin', margin)
      savePref('leverage', String(leverage))
      notify.success(
        tr(
          language,
          `${r.paper ? '模拟盘' : '实盘'}已${sideText} ${r.quantity.toFixed(4)} ${symbol}`,
          `${r.paper ? 'Paper' : 'Live'} ${sideText} ${r.quantity.toFixed(4)} ${symbol}`
        )
      )
      r.warnings?.forEach((w) => notify.warning(w))
      onDone()
    } catch (err) {
      notify.error(
        err instanceof Error
          ? err.message
          : tr(language, '下单失败', 'Order failed')
      )
    } finally {
      setSubmitting(false)
    }
  }

  const sideBtn = (
    s: 'long' | 'short',
    zh: string,
    en: string,
    color: string
  ) => (
    <button
      type="button"
      aria-pressed={side === s}
      onClick={() => setSide(s)}
      className="flex-1 rounded-md py-1.5 text-sm font-semibold"
      style={
        side === s
          ? { background: color, color: '#fff' }
          : { background: '#edf1f7', color: BN.text }
      }
    >
      {tr(language, zh, en)}
    </button>
  )

  return (
    <div className="mt-3 space-y-2">
      <div className="flex gap-2">
        {sideBtn('long', '开多', 'Long', BN.up)}
        {sideBtn('short', '开空', 'Short', BN.down)}
      </div>
      <div className="grid grid-cols-2 gap-2">
        <div>
          <label className={ui.label} htmlFor="market-margin">
            {tr(language, '保证金 (USDT)', 'Margin (USDT)')}
          </label>
          <input
            id="market-margin"
            className={ui.input}
            inputMode="decimal"
            value={margin}
            onChange={(e) => setMargin(e.target.value)}
          />
        </div>
        <div>
          <label className={ui.label} htmlFor="market-leverage">
            {tr(language, '杠杆', 'Leverage')}
          </label>
          <select
            id="market-leverage"
            className={ui.input}
            value={leverage}
            onChange={(e) => setLeverage(Number(e.target.value))}
          >
            {leverageOptions.map((l) => (
              <option key={l} value={l}>
                {l}x
              </option>
            ))}
          </select>
        </div>
        <div>
          <label className={ui.label} htmlFor="market-sl">
            {tr(language, '止损价（可选）', 'Stop-loss (optional)')}
          </label>
          <input
            id="market-sl"
            className={ui.input}
            inputMode="decimal"
            value={stopLoss}
            onChange={(e) => setStopLoss(e.target.value)}
          />
        </div>
        <div>
          <label className={ui.label} htmlFor="market-tp">
            {tr(language, '止盈价（可选）', 'Take-profit (optional)')}
          </label>
          <input
            id="market-tp"
            className={ui.input}
            inputMode="decimal"
            value={takeProfit}
            onChange={(e) => setTakeProfit(e.target.value)}
          />
        </div>
      </div>
      <p className={ui.muted}>
        {tr(language, '仓位价值约', 'Notional about')} {notional.toFixed(2)}{' '}
        USDT
        {price > 0 && notional > 0
          ? ` ≈ ${(notional / price).toFixed(4)} ${symbol.replace('USDT', '')}`
          : ''}
        {' · '}
        {tr(language, '市价成交', 'Market order')}
      </p>
      <button
        type="button"
        onClick={submit}
        disabled={submitting}
        className="inline-flex w-full items-center justify-center gap-2 rounded-md py-2 text-sm font-semibold text-white disabled:opacity-50"
        style={{ background: side === 'long' ? BN.up : BN.down }}
      >
        {submitting ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
        {side === 'long'
          ? tr(language, '开多', 'Open long')
          : tr(language, '开空', 'Open short')}{' '}
        {symbol}
        {live
          ? tr(language, '（实盘）', ' (LIVE)')
          : tr(language, '（模拟）', ' (paper)')}
      </button>
    </div>
  )
}

function Positions({
  positions,
  exchange,
  decimals,
  language,
  onDone,
}: {
  positions: MarketPosition[]
  exchange?: Exchange
  decimals: number
  language: string
  onDone: () => void
}) {
  const [closing, setClosing] = useState('')
  if (!exchange) return null

  const close = async (p: MarketPosition) => {
    const live = !isPaper(exchange)
    if (live) {
      const ok = await confirmToast(
        tr(
          language,
          `确认用市价平掉实盘 ${p.symbol} ${p.side === 'long' ? '多单' : '空单'}？`,
          `Close the LIVE ${p.symbol} ${p.side} position at market?`
        ),
        {
          title: tr(language, '确认平仓', 'Confirm close'),
          okText: tr(language, '平仓', 'Close'),
          cancelText: tr(language, '取消', 'Cancel'),
        }
      )
      if (!ok) return
    }
    setClosing(p.symbol + p.side)
    try {
      await api.closeMarketPosition({
        exchange_id: exchange.id,
        symbol: p.symbol,
        side: p.side,
        confirm_live: live,
      })
      notify.success(tr(language, '已平仓', 'Position closed'))
      onDone()
    } catch (err) {
      notify.error(
        err instanceof Error
          ? err.message
          : tr(language, '平仓失败', 'Close failed')
      )
    } finally {
      setClosing('')
    }
  }

  return (
    <div className="mt-4">
      <div className="mb-1 text-xs font-semibold text-nofx-text">
        {tr(language, '当前持仓', 'Positions')} ({positions.length})
      </div>
      {positions.length === 0 ? (
        <p className={ui.muted}>
          {tr(language, '暂无持仓', 'No open positions')}
        </p>
      ) : null}
      <ul className="space-y-1">
        {positions.map((p) => {
          const pnlColor = p.unRealizedProfit >= 0 ? BN.up : BN.down
          return (
            <li
              key={p.symbol + p.side}
              className="rounded border border-[rgba(25,36,52,0.14)] p-2 text-xs"
            >
              <div className="flex items-center justify-between">
                <span
                  className="font-semibold"
                  style={{ color: p.side === 'long' ? BN.up : BN.down }}
                >
                  {p.symbol}{' '}
                  {p.side === 'long'
                    ? tr(language, '多', 'Long')
                    : tr(language, '空', 'Short')}{' '}
                  {p.leverage}x
                </span>
                <button
                  type="button"
                  className={ui.btnGhost}
                  disabled={closing === p.symbol + p.side}
                  onClick={() => close(p)}
                >
                  {tr(language, '市价平仓', 'Close')}
                </button>
              </div>
              <div className="mt-1 grid grid-cols-2 gap-x-2 text-nofx-text-muted">
                <span>
                  {tr(language, '数量', 'Size')}{' '}
                  <span className={ui.text}>{Math.abs(p.positionAmt)}</span>
                </span>
                <span>
                  {tr(language, '未实现盈亏', 'PnL')}{' '}
                  <span style={{ color: pnlColor }}>
                    {p.unRealizedProfit.toFixed(2)}
                  </span>
                </span>
                <span>
                  {tr(language, '开仓价', 'Entry')}{' '}
                  <span className={ui.text}>
                    {p.entryPrice.toFixed(decimals)}
                  </span>
                </span>
                <span>
                  {tr(language, '标记价', 'Mark')}{' '}
                  <span className={ui.text}>
                    {p.markPrice.toFixed(decimals)}
                  </span>
                </span>
              </div>
            </li>
          )
        })}
      </ul>
    </div>
  )
}
