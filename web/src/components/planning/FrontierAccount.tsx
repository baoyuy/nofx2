import { NoticeText } from '../NoticeText'
import { apiKeyVenues, venueName } from '../../lib/frontierAccounts'
import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from '../../lib/api'
import { getAuthHeaders, handleJSONResponse } from '../../lib/api/helpers'
import { randomUUID } from '../../lib/uuid'
import {
  planningAPI,
  CloseNotSubmittedError,
  type LiveRun,
  type PlanningState,
} from '../../lib/planning'
import {
  accountPositions,
  closeAccountPosition,
  positionDecimal,
  nativeMarket,
  type AccountPosition,
} from '../../lib/frontierAccount'
import { accountLabel } from '../../lib/frontierAccounts'
import type { Exchange } from '../../types'
import type { AccountObservation } from '../../lib/frontierWorkspace'
import { useLanguage } from '../../contexts/LanguageContext'

function useSafeLanguage() {
  try {
    return useLanguage().language
  } catch {
    return 'en' as const
  }
}

interface OpenOrder {
  order_id: string
  symbol: string
  side: string
  type: string
  price: number
  stop_price: number
  quantity: number
  status: string
}
const format = (value: number | string) =>
  Number(value).toLocaleString('en-US', { maximumFractionDigits: 8 })
const amount = (value: number, zh = false) =>
  Number.isFinite(value)
    ? value.toLocaleString('en-US', {
        minimumFractionDigits: 2,
        maximumFractionDigits:
          Math.abs(value) > 0 && Math.abs(value) < 0.01 ? 4 : 2,
      })
    : zh
      ? '不可用'
      : 'Unavailable'
const exchangeURL = (venue: string) =>
  venue === 'hyperliquid'
    ? 'https://app.hyperliquid.xyz/'
    : venue === 'okx'
      ? 'https://www.okx.com/trade-swap/btc-usdt-swap'
      : venue === 'bitget'
        ? 'https://www.bitget.com/futures/usdt/BTCUSDT'
        : 'https://www.binance.com/en/futures'
const liveStates = new Set([
  'starting',
  'running',
  'position_open',
  'stopping',
  'needs_attention',
  'approved',
])

export function FrontierAccount({
  account,
  accounts,
  venue,
  symbol,
  runs,
  busy,
  onView,
  onManual,
  onBusy,
  onRefresh,
  onCheck,
  onObserve,
}: {
  account?: Exchange
  accounts: Exchange[]
  venue: string
  symbol: string
  runs: LiveRun[]
  busy: boolean
  onView: (run: LiveRun) => void
  onManual: (side: 'long' | 'short') => void
  onBusy: (value: boolean) => void
  onRefresh: () => Promise<void>
  onCheck?: (run: LiveRun) => Promise<void>
  onObserve?: (snapshot: AccountObservation | null) => void
}) {
  const language = useSafeLanguage()
  const zh = language === 'zh'
  const [snapshot, setSnapshot] = useState<{
    accountID: string
    positions: AccountPosition[]
    at: string
  } | null>(null)
  const [orders, setOrders] = useState<{
    accountID: string
    symbol: string
    rows: OpenOrder[]
  } | null>(null)
  const [loading, setLoading] = useState(false),
    [error, setError] = useState(''),
    [orderError, setOrderError] = useState('')
  const [binding, setBinding] = useState<{
    id: string
    accountID: string
    running: boolean
  } | null>(null)
  const [confirm, setConfirm] = useState<AccountPosition | null>(null),
    [notice, setNotice] = useState('')
  const [attempted, setAttempted] = useState<Set<string>>(new Set())
  const attemptLatch = useRef(new Set<string>())
  const generation = useRef(0),
    inFlight = useRef(false)
  const accountID = account?.id || ''
  const refresh = useCallback(async () => {
    if (!accountID || inFlight.current) return
    const version = generation.current
    inFlight.current = true
    setLoading(true)
    try {
      const traders = (await api.getTraders(true)).filter(
        (t) => t.exchange_id === accountID
      )
      const traderID = traders[0]?.trader_id
      if (version !== generation.current) return
      setBinding(
        traderID
          ? {
              id: traderID,
              accountID,
              running: traders.some((t) => t.is_running),
            }
          : null
      )
      const values = accountPositions(
        await api.getPositions(undefined, true, accountID)
      )
      if (version !== generation.current) return
      setSnapshot({
        accountID,
        positions: values,
        at: new Date().toISOString(),
      })
      for (const attempt of attemptLatch.current) {
        if (
          attempt.startsWith(accountID + '/') &&
          !values.some(
            (p) => attempt === accountID + '/' + p.symbol + '/' + p.side
          )
        )
          attemptLatch.current.delete(attempt)
      }
      setAttempted(new Set(attemptLatch.current))
      setError('')
      const orderRequest = new AbortController()
      const orderDeadline = setTimeout(() => orderRequest.abort(), 30000)
      try {
        const query = new URLSearchParams({ exchange_id: accountID, symbol })
        const response = await fetch('/api/open-orders?' + query, {
          headers: getAuthHeaders(),
          signal: orderRequest.signal,
        })
        const rows = await handleJSONResponse<OpenOrder[]>(response)
        if (
          !Array.isArray(rows) ||
          rows.some(
            (o) =>
              !o ||
              typeof o.order_id !== 'string' ||
              !o.order_id ||
              typeof o.type !== 'string' ||
              !Number.isFinite(Number(o.quantity))
          )
        )
          throw new Error('Incomplete order response')
        if (version !== generation.current) return
        setOrders({ accountID, symbol, rows })
        setOrderError('')
      } catch {
        if (version === generation.current) {
          setOrders(null)
          setOrderError(
            zh
              ? '无法核实当前交易所订单。'
              : 'Current exchange orders could not be verified.'
          )
        }
      } finally {
        clearTimeout(orderDeadline)
      }
    } catch (e) {
      if (version === generation.current)
        setError(e instanceof Error ? e.message : String(e))
    } finally {
      if (version === generation.current) {
        setLoading(false)
        inFlight.current = false
      }
    }
  }, [accountID, symbol, zh])
  useEffect(() => {
    generation.current++
    inFlight.current = false
    setSnapshot((old) => (old?.accountID === accountID ? old : null))
    setOrders(null)
    setError('')
    setBinding((old) => (old?.accountID === accountID ? old : null))
    setConfirm(null)
    setNotice('')
    let cancelled = false
    let timer: ReturnType<typeof setTimeout>
    const poll = async () => {
      if (!document.hidden) await refresh()
      if (!cancelled) timer = setTimeout(poll, 10000)
    }
    void poll()
    return () => {
      cancelled = true
      clearTimeout(timer)
      generation.current++
    }
  }, [refresh])

  const positions = snapshot?.accountID === accountID ? snapshot.positions : []
  const currentBinding = binding?.accountID === accountID ? binding : null
  const hasPosition = positions.some(
    (p) => nativeMarket(p.symbol, venue) === nativeMarket(symbol, venue)
  )
  const open =
    orders?.accountID === accountID && orders.symbol === symbol
      ? orders.rows
      : []
  const importantRuns = runs
    .filter((run) => run.exchange_id === accountID && liveStates.has(run.state))
    .sort(
      (a, b) =>
        Number(b.state === 'needs_attention') -
        Number(a.state === 'needs_attention')
    )
  const key = (p: AccountPosition) => accountID + '/' + p.symbol + '/' + p.side
  useEffect(() => {
    onObserve?.(
      !error && snapshot?.accountID === accountID
        ? {
            accountID,
            symbol,
            positions: snapshot.positions,
            orders:
              !orderError &&
              orders?.accountID === accountID &&
              orders.symbol === symbol
                ? orders.rows
                : null,
            checkedAt: Date.parse(snapshot.at),
          }
        : null
    )
  }, [accountID, symbol, snapshot, orders, error, orderError, onObserve])

  async function closePosition() {
    if (
      !confirm ||
      !accountID ||
      (!apiKeyVenues.includes(venue) && !currentBinding) ||
      busy
    )
      return
    const position = confirm,
      traderID = currentBinding?.id,
      attempt = key(position)
    if (attemptLatch.current.has(attempt)) return
    attemptLatch.current.add(attempt)
    setAttempted(new Set(attemptLatch.current))
    setConfirm(null)
    onBusy(true)
    setNotice(
      zh
        ? '正在停止剩余入场，然后平仓…'
        : 'Stopping remaining entries before closing the position…'
    )
    let closeSent = false
    let closeMessage = ''
    try {
      const traders = await api.getTraders(true)
      if (
        !apiKeyVenues.includes(venue) &&
        !traders.some(
          (t) => t.trader_id === traderID && t.exchange_id === accountID
        )
      )
        throw new Error(
          zh
            ? 'NOFX 账户绑定已变更。平仓前请先刷新。'
            : 'The NOFX account binding changed. Refresh before closing.'
        )
      if (traders.some((t) => t.exchange_id === accountID && t.is_running))
        throw new Error(
          zh
            ? '手动平仓前，请先在仪表盘中停止该账户的 Autopilot。'
            : 'Stop Autopilot for this account in Dashboard before manually closing.'
        )
      await closeAccountPosition(accountID, venue, position, runs, {
        stop: (run) =>
          planningAPI('stop', { id: run.id, hash: run.approval_hash }),
        read: async () => (await planningAPI<PlanningState>('state')).runs,
        wait: () => new Promise((resolve) => setTimeout(resolve, 500)),
        close: async () => {
          const fresh = accountPositions(
            await api.getPositions(undefined, true, accountID)
          ).find(
            (p) => p.symbol === position.symbol && p.side === position.side
          )
          if (!fresh)
            throw new Error(
              zh
                ? '持仓已不存在；未发送平仓。'
                : 'Position is no longer open; no close was sent.'
            )
          if (
            fresh.quantity > position.quantity ||
            fresh.entry !== position.entry
          )
            throw new Error(
              zh
                ? '持仓已变更。平仓前请刷新并复核其规模。'
                : 'Position changed. Refresh and review its size before closing.'
            )
          if (apiKeyVenues.includes(venue)) {
            const quantity = positionDecimal(fresh.quantity)
            const entryPrice = positionDecimal(fresh.entry)
            closeSent = true
            const result = await planningAPI<{ message: string }>(
              'close-position',
              {
                exchange_id: accountID,
                request_id: randomUUID(),
                symbol: position.symbol,
                side: position.side,
                quantity,
                entry_price: entryPrice,
              }
            )
            closeMessage = result.message
            return result
          }
          closeSent = true
          return api.closePosition(
            traderID!,
            position.symbol,
            position.side.toUpperCase()
          )
        },
      })
      setNotice(
        closeMessage ||
          (zh
            ? '平仓请求已接受。请核对下方刷新后的持仓；不会自动重复发送。'
            : 'Close request accepted. Verify the refreshed position below; no automatic repeat will be sent.')
      )
    } catch (e) {
      const notSubmitted = !closeSent || e instanceof CloseNotSubmittedError
      if (notSubmitted) {
        attemptLatch.current.delete(attempt)
        setAttempted(new Set(attemptLatch.current))
      }
      setNotice(
        (e instanceof Error ? e.message : String(e)) +
          (notSubmitted
            ? zh
              ? ' 未提交任何平仓订单。请刷新账户，然后重新复核持仓。'
              : ' No close order was submitted. Refresh the account, then review the position again.'
            : zh
              ? ' 再次发送平仓前，请先查看交易所。'
              : ' Check the exchange before sending another close.')
      )
    } finally {
      onBusy(false)
      await Promise.allSettled([refresh(), onRefresh()])
    }
  }

  return (
    <section
      className="frontier-account"
      aria-label={zh ? '账户持仓和订单' : 'Account positions and orders'}
    >
      <header>
        <div>
          <h2>
            {zh ? '持仓和订单' : 'Positions & orders'}{' '}
            <span className="frontier-count">
              {snapshot?.accountID === accountID ? positions.length : '—'}
            </span>
          </h2>
          <p>
            {account
              ? `${accountLabel(account, accounts)} · ${venueName(venue)}${account.hyperliquidWalletAddr ? ' · ' + account.hyperliquidWalletAddr.slice(0, 8) + '…' + account.hyperliquidWalletAddr.slice(-4) : ''}`
              : zh
                ? '选择一个交易账户以查看其全部持仓。'
                : 'Select a trading account to see all its positions.'}
          </p>
        </div>
        <div className="frontier-account-actions">
          {accountID && (
            <a href={exchangeURL(venue)} target="_blank" rel="noreferrer">
              {zh ? '打开交易所 ↗' : 'Open exchange ↗'}
            </a>
          )}
          <button
            disabled={!accountID || busy || loading}
            onClick={() => void refresh()}
          >
            {loading ? (zh ? '检查中…' : 'Checking…') : zh ? '刷新账户' : 'Refresh account'}
          </button>
          <button
            className="frontier-long"
            disabled={
              !accountID || busy || !!currentBinding?.running || hasPosition
            }
            onClick={() => onManual('long')}
          >
            {zh ? '买入 / 做多' : 'Buy / Long'}
          </button>
          <button
            className="frontier-short"
            disabled={
              !accountID || busy || !!currentBinding?.running || hasPosition
            }
            onClick={() => onManual('short')}
          >
            {zh ? '卖出 / 做空' : 'Sell / Short'}
          </button>
        </div>
      </header>
      {currentBinding?.running && (
        <p className="frontier-notice">
          {zh
            ? 'Autopilot 正在运行。手动交易前，请先在仪表盘中停止它。'
            : 'Autopilot is running. Stop it in Dashboard before manual trading.'}
        </p>
      )}
      {hasPosition && (
        <small>
          {zh
            ? `你在 ${symbol} 有持仓。你仍可以生成新草稿。独立入场无法与此持仓同时执行；草稿不会修改现有持仓或出场订单。`
            : `You have a position in ${symbol}. You can still generate new drafts. Independent entries cannot execute alongside this position; drafts do not modify existing positions or exit orders.`}
        </small>
      )}
      {hasPosition &&
        !error &&
        !orderError &&
        orders?.accountID === accountID &&
        orders.symbol === symbol &&
        open.length === 0 && (
          <p role="alert" className="frontier-notice">
            {zh
              ? `${symbol} 持仓已开仓，但没有报告任何活动订单。止损和止盈保护未得到确认。`
              : `${symbol} position is open, but no working orders were reported. Stop-loss and take-profit protection is not confirmed.`}{' '}
            <a href={exchangeURL(venue)} target="_blank" rel="noreferrer">
              {zh ? '在交易所查看保护订单 ↗' : 'Check protection at the exchange ↗'}
            </a>
          </p>
        )}
      {notice && (
        <p role="status" className="frontier-notice">
          {notice}
        </p>
      )}
      {error && (
        <p role="alert" className="frontier-notice">
          {zh ? '持仓不可用：' : 'Positions unavailable: '} <NoticeText text={error} language={language} />
          {snapshot
            ? zh
              ? ' 下方上次接收的值可能已过期。'
              : ' Last received values below may be stale.'
            : zh
              ? ' 未假定账户为空。'
              : ' No empty-account assumption was made.'}
        </p>
      )}
      {accountID && (
        <>
          <div className="frontier-account-subhead">
            <span>{zh ? '全部市场 · 所选账户' : 'All markets · selected account'}</span>
            <span>
              {snapshot?.accountID === accountID
                ? zh
                  ? `${error ? '上次接收' : '接收于'} ${new Date(snapshot.at).toLocaleTimeString()}`
                  : `${error ? 'Last received' : 'Received'} ${new Date(snapshot.at).toLocaleTimeString()}`
                : zh
                  ? '等待交易所响应'
                  : 'Waiting for exchange response'}
            </span>
          </div>
          {positions.length > 0 ? (
            <div className="frontier-table-scroll">
              <table>
                <thead>
                  <tr>
                    <th>{zh ? '市场' : 'Market'}</th>
                    <th>{zh ? '持仓价值' : 'Position value'}</th>
                    <th
                      title={
                        zh
                          ? '持仓价值除以杠杆。此估算不是交易所报告的保证金余额。'
                          : 'Position value divided by leverage. This estimate is not the exchange-reported margin balance.'
                      }
                    >
                      {zh ? '预计初始保证金' : 'Est. initial margin'}
                    </th>
                    <th>{zh ? '数量' : 'Quantity'}</th>
                    <th>{zh ? '平均入场价' : 'Average entry'}</th>
                    <th>{zh ? '标记价格' : 'Mark price'}</th>
                    <th>{zh ? '未实现盈亏' : 'Unrealized PnL'}</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  {positions.map((p) => (
                    <tr key={p.symbol + p.side}>
                      <td>
                        <strong>{nativeMarket(p.symbol, venue)}</strong>
                        <span className={'frontier-position-badge ' + p.side}>
                          {p.side === 'long'
                            ? zh
                              ? '做多'
                              : 'Long'
                            : zh
                              ? '做空'
                              : 'Short'}{' '}
                          <b>{p.leverage}x</b>
                        </span>
                      </td>
                      <td className="frontier-position-value">
                        <strong>{amount(p.quantity * p.mark, zh)}</strong>
                        <small>
                          {venue === 'hyperliquid' ? 'USDC' : 'USDT'}{' '}
                          {zh ? '名义价值' : 'notional'}
                        </small>
                      </td>
                      <td
                        className="frontier-position-margin"
                        title={
                          zh
                            ? '当前名义价值 / 杠杆。全仓保证金、追加抵押品和交易所规则可能改变实际保证金要求。'
                            : 'Current notional / leverage. Cross margin, added collateral and exchange rules can change actual margin requirements.'
                        }
                      >
                        <strong>
                          ≈ {amount((p.quantity * p.mark) / p.leverage, zh)}
                        </strong>
                        <small>
                          {venue === 'hyperliquid' ? 'USDC' : 'USDT'} ·{' '}
                          {zh ? '估算' : 'estimated'}
                        </small>
                      </td>
                      <td>
                        {format(p.quantity)}{' '}
                        <small>
                          {nativeMarket(p.symbol, venue).replace(/USDT$/, '')}
                        </small>
                      </td>
                      <td>{format(p.entry)}</td>
                      <td>{format(p.mark)}</td>
                      <td className={p.pnl >= 0 ? 'target-text' : 'stop-text'}>
                        {p.pnl >= 0 ? '+' : ''}
                        {amount(p.pnl, zh)}{' '}
                        {venue === 'hyperliquid' ? 'USDC' : 'USDT'}
                      </td>
                      <td>
                        {!currentBinding && !apiKeyVenues.includes(venue) ? (
                          <a
                            href={exchangeURL(venue)}
                            target="_blank"
                            rel="noreferrer"
                          >
                            {zh ? '在交易所平仓 ↗' : 'Close on exchange ↗'}
                          </a>
                        ) : (
                          <button
                            disabled={
                              busy ||
                              !!error ||
                              !!currentBinding?.running ||
                              attempted.has(key(p))
                            }
                            onClick={() => setConfirm(p)}
                          >
                            {attempted.has(key(p))
                              ? zh
                                ? '在交易所核实平仓'
                                : 'Verify close at exchange'
                              : zh
                                ? '平仓'
                                : 'Close position'}
                          </button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : snapshot?.accountID === accountID && !error ? (
            <p className="frontier-flat">
              {zh
                ? '该账户没有报告任何未平仓持仓。'
                : 'No open positions reported by this account.'}
            </p>
          ) : loading ? (
            <p>{zh ? '正在读取持仓…' : 'Reading positions…'}</p>
          ) : null}
          {positions.length > 0 && (
            <p className="frontier-margin-note">
              {zh
                ? '持仓价值 = 数量 × 标记价格。预计初始保证金 = 持仓价值 ÷ 杠杆；交易所实际保证金可能不同。'
                : 'Position value = quantity × mark price. Estimated initial margin = position value ÷ leverage; actual exchange margin may differ.'}
            </p>
          )}
          {open.filter((o) => /STOP|TAKE_PROFIT/.test(o.type)).length > 0 && (
            <div className="frontier-protection">
              <strong>
                {zh ? '交易所出场订单' : 'Exchange exit orders'} · {symbol}
              </strong>
              {open
                .filter((o) => /STOP|TAKE_PROFIT/.test(o.type))
                .map((o) => (
                  <span key={o.order_id}>
                    {o.type.includes('TAKE_PROFIT')
                      ? zh
                        ? '止盈'
                        : 'Take profit'
                      : zh
                        ? '止损'
                        : 'Stop loss'}{' '}
                    <b>{format(o.stop_price || o.price)}</b> ·{' '}
                    {zh ? '数量' : 'size'}{' '}
                    {format(o.quantity)}
                  </span>
                ))}
            </div>
          )}
          <details className="frontier-native-orders" open>
            <summary>
              {zh ? '交易所订单' : 'Exchange orders'} · {symbol}
              {orders?.accountID === accountID && orders.symbol === symbol
                ? ` · ${open.length}`
                : zh
                  ? ' · 未核实'
                  : ' · not verified'}
            </summary>
            {orderError ? (
              <p>{orderError}</p>
            ) : orders?.accountID === accountID && orders.symbol === symbol ? (
              open.length ? (
                <div className="frontier-table-scroll">
                  <table>
                    <thead>
                      <tr>
                        <th>{zh ? '类型' : 'Type'}</th>
                        <th>{zh ? '方向' : 'Side'}</th>
                        <th>{zh ? '价格 / 触发价' : 'Price / trigger'}</th>
                        <th>{zh ? '数量' : 'Size'}</th>
                        <th>{zh ? '状态' : 'Status'}</th>
                        <th>{zh ? '交易所订单 ID' : 'Exchange order ID'}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {open.map((o) => (
                        <tr key={o.order_id}>
                          <td>{o.type}</td>
                          <td>{o.side}</td>
                          <td>{format(o.stop_price || o.price)}</td>
                          <td>{format(o.quantity)}</td>
                          <td>{o.status}</td>
                          <td>{o.order_id}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <p>
                  {zh
                    ? `没有报告 ${symbol} 的未平订单。`
                    : `No open orders reported for ${symbol}.`}
                </p>
              )
            ) : (
              <p>{zh ? '订单尚未核实。' : 'Orders have not been verified.'}</p>
            )}
          </details>
        </>
      )}
      {importantRuns.map((run) => {
        const entries = run.orders.filter((o) => o.role === 'entry')
        const filled = entries.reduce((sum, o) => sum + Number(o.filled), 0)
        const accepted = entries.some(
          (o) => o.order_id && ['NEW', 'PARTIALLY_FILLED'].includes(o.status)
        )
        const unconfirmed = entries.some((o) =>
          ['UNKNOWN', 'submitting'].includes(o.status)
        )
        const name =
          accounts.find((a) => a.id === run.exchange_id)?.name ||
          (zh ? '账户未加载' : 'Account not loaded')
        const accountVerified =
          snapshot?.accountID === run.exchange_id && !error
        const nativePosition = accountVerified
          ? positions.find(
              (p) =>
                nativeMarket(p.symbol, run.plan.snapshot.venue) ===
                  nativeMarket(
                    run.plan.snapshot.symbol,
                    run.plan.snapshot.venue
                  ) && p.side === run.plan.idea.side
            )
          : undefined
        const label = unconfirmed
          ? zh
            ? '提交未确认 — 请勿重发'
            : 'Submission unconfirmed — do not resend'
          : filled > 0
            ? nativePosition
              ? zh
                ? '持仓已开仓 · 入场已成交'
                : 'Position open · entry filled'
              : accountVerified
                ? zh
                  ? '入场此前已成交 · 最新账户快照中无持仓'
                  : 'Entry filled earlier · no position in latest account snapshot'
                : zh
                  ? '入场已成交 · 当前持仓未核实'
                  : 'Entry filled · current position not verified'
            : accepted
              ? zh
                ? '入场订单活动中'
                : 'Entry order is working'
              : run.state === 'approved' && !entries.length
                ? zh
                  ? '仅已批准 — 未提交订单'
                  : 'Approved only — no order submitted'
                : zh
                  ? '没有确认的入场成交'
                  : 'No confirmed entry fill'
        return (
          <details
            className={
              'frontier-trade-summary' +
              (run.state === 'needs_attention' || unconfirmed
                ? ' attention'
                : '')
            }
            key={run.id}
          >
            <summary className="frontier-record-toggle">
              <span>
                {run.plan.snapshot.symbol} ·{' '}
                {run.plan.idea.side === 'long'
                  ? zh
                    ? '做多'
                    : 'Long'
                  : zh
                    ? '做空'
                    : 'Short'}{' '}
                <span className="frontier-record-label">
                  {zh ? '执行记录' : 'Execution record'} · {name}
                </span>
              </span>
              <span
                className={
                  run.state === 'needs_attention' || unconfirmed
                    ? 'frontier-review-badge'
                    : ''
                }
              >
                {unconfirmed
                  ? zh
                    ? '提交未确认'
                    : 'Submission unconfirmed'
                  : run.state === 'needs_attention'
                    ? zh
                      ? '监控需要复核'
                      : 'Monitor needs review'
                    : label}
              </span>
            </summary>
            <div>
              <strong>
                {run.plan.snapshot.symbol} ·{' '}
                {run.plan.idea.side === 'long'
                  ? zh
                    ? '做多'
                    : 'Long'
                  : zh
                    ? '做空'
                    : 'Short'}
              </strong>
              <p>
                {name} · {run.plan.snapshot.venue} · {label}
                {filled > 0
                  ? ` · ${format(filled)} ${run.plan.snapshot.base_asset}`
                  : ''}
              </p>
              {run.state === 'needs_attention' && (
                <p className="frontier-monitor-alert">
                  {run.error?.startsWith('Service restarted')
                    ? zh
                      ? '服务器已重启，此订单监控已暂停。请查看上方实时持仓和出场订单。'
                      : 'The server restarted, so this order monitor is paused. Check the live position and exit orders above.'
                    : run.error ||
                      (zh
                        ? '订单监控需要复核。请查看上方实时持仓和出场订单。'
                        : 'Order monitoring needs review. Check the live position and exit orders above.')}
                </p>
              )}
            </div>
            <div>
              {onCheck &&
                ['needs_attention', 'position_open', 'stopping'].includes(
                  run.state
                ) && (
                  <button disabled={busy} onClick={() => void onCheck(run)}>
                    {zh ? '检查交易状态' : 'Check trade status'}
                  </button>
                )}
              <button disabled={busy} onClick={() => onView(run)}>
                {zh ? '在图表上查看交易' : 'View trade on chart'}
              </button>
              <a
                href={exchangeURL(run.plan.snapshot.venue)}
                target="_blank"
                rel="noreferrer"
              >
                {zh ? '打开交易所 ↗' : 'Open exchange ↗'}
              </a>
            </div>
            <details>
              <summary>
                {zh ? '已记录订单' : 'Recorded orders'}
                {run.updated_at
                  ? ' · ' + new Date(run.updated_at).toLocaleTimeString()
                  : ''}
              </summary>
              <table>
                <thead>
                  <tr>
                    <th>{zh ? '订单' : 'Order'}</th>
                    <th>{zh ? '价格' : 'Price'}</th>
                    <th>{zh ? '已成交 / 数量' : 'Filled / size'}</th>
                    <th>{zh ? '上次记录状态' : 'Last recorded state'}</th>
                    <th>{zh ? '交易所 ID' : 'Exchange ID'}</th>
                  </tr>
                </thead>
                <tbody>
                  {run.orders.map((o) => (
                    <tr key={o.client_id}>
                      <td>
                        {o.role === 'entry'
                          ? zh
                            ? '入场'
                            : 'Entry'
                          : o.role === 'sl'
                            ? zh
                              ? '止损'
                              : 'Stop loss'
                            : zh
                              ? '止盈'
                              : 'Take profit'}
                      </td>
                      <td>{format(o.average || o.price)}</td>
                      <td>
                        {format(o.filled)} / {format(o.quantity)}
                      </td>
                      <td>{o.status}</td>
                      <td>{o.order_id || (zh ? '未确认' : 'Not confirmed')}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <small>
                {zh
                  ? '订单记录不能替代一次成功的交易所实时读取。'
                  : 'Order records are not a substitute for a successful live exchange read.'}
              </small>
            </details>
          </details>
        )
      })}
      {!importantRuns.length && (
        <small>{zh ? '没有进行中的执行。' : 'No active execution.'}</small>
      )}
      {confirm && (
        <div className="planning-modal-backdrop">
          <section
            className="planning-modal"
            role="dialog"
            aria-modal="true"
            aria-label={zh ? '确认平仓' : 'Confirm position close'}
          >
            <h2>
              {zh
                ? `平仓：${nativeMarket(confirm.symbol, venue)} ${confirm.side === 'long' ? '多头' : '空头'}？`
                : `Close ${nativeMarket(confirm.symbol, venue)} ${confirm.side}?`}
            </h2>
            <p>
              {account ? accountLabel(account, accounts) : ''} · {venue} ·{' '}
              {zh ? '全部持仓' : 'full position'} {format(confirm.quantity)}
            </p>
            <p>
              {zh
                ? '这将通过 NOFX 发送市价平仓。该市场剩余的 Frontier 入场订单会先被停止。会有滑点和手续费。无需输入确认短语。'
                : 'This sends a market close through NOFX. Remaining Frontier entry orders for this market are stopped first. Slippage and fees apply. No typed phrase is needed.'}
            </p>
            <button onClick={() => setConfirm(null)}>
              {zh ? '保留持仓' : 'Keep position'}
            </button>
            <button
              className="planning-primary"
              disabled={busy}
              onClick={() => void closePosition()}
            >
              {zh ? '确认市价平仓' : 'Confirm market close'}
            </button>
          </section>
        </div>
      )}
    </section>
  )
}
