import type { LiveRun } from '../../lib/planning'
import type { AccountObservation } from '../../lib/frontierWorkspace'
import { nativeMarket } from '../../lib/frontierAccount'
import { useLanguage } from '../../contexts/LanguageContext'

function useSafeLanguage() {
  try {
    return useLanguage().language
  } catch {
    return 'en' as const
  }
}

export function FrontierOrderBlocker({
  run,
  now,
  observation,
  checkError,
  busy,
  onCheck,
  onView,
  onRevoke,
  onAccount,
}: {
  run: LiveRun
  now: number
  observation?: AccountObservation | null
  checkError?: string
  busy: boolean
  onCheck: () => void
  onView: () => void
  onRevoke: () => void
  onAccount?: () => void
}) {
  const language = useSafeLanguage()
  const zh = language === 'zh'
  const venue = run.plan.snapshot.venue
  const symbol = run.plan.snapshot.symbol
  const fresh =
    observation?.accountID === run.exchange_id &&
    Math.abs(now - observation.checkedAt) < 30000
  const position = fresh
    ? observation.positions.find(
        (p) => nativeMarket(p.symbol, venue) === nativeMarket(symbol, venue)
      )
    : undefined
  const approved = run.state === 'approved'
  const active = ['starting', 'running', 'stopping'].includes(run.state)
  const needsCheck = !active && !approved
  const title = position
    ? `${symbol} ${position.side === 'long' ? (zh ? '多头' : 'Long') : zh ? '空头' : 'Short'} ${zh ? '持仓中' : 'open'} · ${position.leverage}x`
    : approved
      ? zh
        ? `${symbol} 订单未提交`
        : `${symbol} order not submitted`
      : active
        ? run.state === 'stopping'
          ? zh
            ? '正在停止剩余入场'
            : 'Stopping remaining entries'
          : zh
            ? '入场订单进行中'
            : 'Entry orders in progress'
        : fresh
          ? zh
            ? `无未平 ${symbol} 持仓 · 之前的订单需要核实`
            : `No open ${symbol} position · previous orders need verification`
          : zh
            ? `${symbol} 交易状态需要核实`
            : `${symbol} trade status needs verification`
  const detail = position
    ? zh
      ? `${(position.quantity * position.mark).toLocaleString('en-US', { maximumFractionDigits: 2 })} ${run.plan.snapshot.quote_asset} 持仓 · 你可以继续浏览并生成计划。`
      : `${(position.quantity * position.mark).toLocaleString('en-US', { maximumFractionDigits: 2 })} ${run.plan.snapshot.quote_asset} position · You can keep exploring and generating plans.`
    : approved
      ? zh
        ? '此次批准没有发送任何订单。你可以复核任何新计划；确认新计划会替换这份未使用的批准。'
        : 'No order was sent from this approval. You can review any new plan; confirming it replaces this unused approval.'
      : active
        ? zh
          ? '在下方账户中跟踪成交和计划入场。'
          : 'Track fills and scheduled entries in your account below.'
        : fresh
          ? zh
            ? '你最新的账户快照中该市场没有持仓。下另一笔交易前，请先核实之前的入场和出场订单。你仍可以生成计划。'
            : 'Your latest account snapshot has no position in this market. Verify the previous entry and exit orders before placing another trade. You can still generate plans.'
          : zh
            ? '下另一笔订单前，请先核实上一笔交易。你仍可以浏览市场并生成计划。'
            : 'Verify the previous trade before placing another order. You can still explore the market and generate plans.'
  return (
    <section
      className={`frontier-order-blocker ${(position && run.state !== 'needs_attention') || active ? 'is-active' : ''}`}
      aria-label={zh ? '当前交易状态' : 'Current trade status'}
    >
      <div>
        <strong>{title}</strong>
        <p>{detail}</p>
        {position && run.state === 'needs_attention' && (
          <small>
            {zh
              ? '出场订单状态需要核实。下另一笔交易前，请先查看下方实时持仓和保护订单。'
              : 'Exit order status needs verification. Check the live position and protection below before placing another trade.'}
          </small>
        )}
        {checkError && <p role="alert">{checkError}</p>}
      </div>
      <div className="frontier-blocker-actions">
        {position ? (
          <button onClick={onAccount}>
            {zh ? '查看持仓和订单' : 'View position & orders'}
          </button>
        ) : (
          <button disabled={busy} onClick={onView}>
            {zh ? '查看交易' : 'View trade'}
          </button>
        )}
        {needsCheck && (
          <button disabled={busy} onClick={onCheck}>
            {zh ? '核实状态' : 'Verify status'}
          </button>
        )}
        {approved && (
          <button disabled={busy} onClick={onRevoke}>
            {zh ? '取消批准' : 'Cancel approval'}
          </button>
        )}
      </div>
    </section>
  )
}
