import type { Plan } from '../../lib/planning'
import { useLanguage } from '../../contexts/LanguageContext'

function useSafeLanguage() {
  try {
    return useLanguage().language
  } catch {
    return 'en' as const
  }
}

const money = (value: number) =>
  value.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })

export function planNotional(plan: Plan) {
  return plan.orders?.length
    ? plan.orders.reduce(
        (sum, order) => sum + Number(order.price) * Number(order.quantity),
        0
      )
    : Number(plan.idea.entry) * Number(plan.quantity)
}

export function PlanFunding({
  plan,
  compact = false,
}: {
  plan: Plan
  compact?: boolean
}) {
  const language = useSafeLanguage()
  const zh = language === 'zh'
  const notional = planNotional(plan)
  const leverage = plan.constraints.leverage || 1
  const margin = notional / leverage
  const quote = plan.snapshot.quote_asset
  const available = Number(plan.constraints.available_margin)
  if (compact)
    return (
      <div className="frontier-funding-compact">
        <span>
          {zh ? '持仓价值' : 'Position value'} <b>{leverage}x</b>
        </span>
        <strong>
          {money(notional)} <small>{quote}</small>
        </strong>
        <small>
          {zh ? '预计保证金' : 'Est. margin'} {money(margin)} {quote}
        </small>
        {plan.constraints.planning_only && (
          <small>
            {zh
              ? '权益预览 · 交易前请刷新'
              : 'Equity preview · refresh before trading'}
          </small>
        )}
        {plan.constraints.entry_policy === 'ai500_trend_long' && (
          <small>{zh ? '趋势榜 · 仅做多' : 'Trend board · Long only'}</small>
        )}
      </div>
    )
  return (
    <section
      className="frontier-funding"
      aria-label={zh ? '计划资金' : 'Plan funding'}
    >
      <div className="frontier-funding-equation">
        <div>
          <span>{zh ? '预计保证金' : 'Est. margin'}</span>
          <strong>
            {money(margin)} <small>{quote}</small>
          </strong>
        </div>
        <span aria-hidden="true">×</span>
        <div>
          <span>{zh ? '杠杆' : 'Leverage'}</span>
          <strong>{leverage}x</strong>
        </div>
        <span aria-hidden="true">=</span>
        <div>
          <span>{zh ? '持仓价值' : 'Position value'}</span>
          <strong>
            {money(notional)} <small>{quote}</small>
          </strong>
        </div>
      </div>
      {plan.constraints.entry_policy === 'ai500_trend_long' && (
        <p>
          {zh
            ? '趋势榜 · 仅限做多入场。每种入场方式单独评估。'
            : 'Trend board · Long entries only. Each entry method is assessed separately.'}
        </p>
      )}
      {plan.constraints.planning_only ? (
        <p>
          {zh
            ? '仅为权益预览：当时没有可用保证金。金额以账户权益为参考，而非可用资金。交易前请刷新并以可用保证金复核。'
            : 'Equity preview only: no free margin was available. Amounts use account equity as a reference, not available funds. Refresh and review with available margin before trading.'}
        </p>
      ) : Number.isFinite(available) && available > 0 ? (
        <p>
          {zh
            ? `定仓时可用保证金：${money(available)} ${quote} · 本计划在计入成本前占用 ${money((margin / available) * 100)}%。手续费和滑点另行预留。`
            : `Available margin at sizing: ${money(available)} ${quote} · This plan uses ${money((margin / available) * 100)}% before costs. Fees and slippage are reserved separately.`}
        </p>
      ) : (
        <p>
          {zh
            ? '已保存的计划规模。请重新复核，以便按当前可用保证金为新订单定仓。'
            : 'Saved plan size. Review again to size a new order from current available margin.'}
        </p>
      )}
      {plan.sizing_limit === 'liquidity' && (
        <p className="stop-text">
          {zh
            ? '此已保存计划使用了之前的盘口最优档规模上限。新计划使用可用保证金 × 杠杆，并预留执行成本。'
            : 'This saved plan used the previous top-of-book size cap. New plans use available margin × leverage, with execution costs reserved.'}
        </p>
      )}
      {plan.sizing_limit === 'risk' && (
        <p>
          {zh
            ? '规模已按本计划的最大亏损预算缩减。'
            : 'Size reduced by this plan’s maximum loss budget.'}
        </p>
      )}
    </section>
  )
}
