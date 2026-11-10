import type { Plan } from '../../lib/planning'
import { validationSummary } from '../../lib/planningPresentation'
import { structureNames } from './PlanningActivity'
import { PlanFunding } from './PlanFunding'
import { useLanguage } from '../../contexts/LanguageContext'

function useSafeLanguage() {
  try {
    return useLanguage().language
  } catch {
    return 'en' as const
  }
}

const structureNamesZh: Record<string, string> = {
  market: '市价',
  limit: '单笔限价',
  ladder: '加权阶梯',
  twap: 'TWAP',
}
export interface PlanCheck {
  id: string
  kind: string
  name: string
  status: string
  reason?: string
  plan_id?: string
}
export function PlanStructures({
  checks,
  plans,
  busy,
  selected,
  onSelect,
  onReview,
  reviewDisabled = false,
  connected = true,
  now = 0,
  showAllTypes = true,
}: {
  checks: PlanCheck[]
  plans: Plan[]
  busy: boolean
  selected: string
  onSelect: (plan: Plan) => void
  onReview?: (plan: Plan) => void
  reviewDisabled?: boolean
  connected?: boolean
  showAllTypes?: boolean
  now?: number
}) {
  const language = useSafeLanguage()
  const zh = language === 'zh'
  const names = zh ? structureNamesZh : structureNames
  const price = (value: string) =>
    Number(value).toLocaleString('en-US', { maximumFractionDigits: 8 })
  const money = (value: string) =>
    Number(value).toLocaleString('en-US', {
      maximumFractionDigits: Math.abs(Number(value)) < 0.01 ? 4 : 2,
    })
  if (!checks.length && !plans.length && !busy) return null
  return (
    <section
      className="planning-structures"
      aria-label={zh ? '入场结构对比' : 'Entry structure comparison'}
    >
      {['market', 'limit', 'ladder', 'twap']
        .filter(
          (kind) =>
            showAllTypes ||
            checks.some((check) => check.kind === kind) ||
            plans.some((plan) => plan.idea.execution.kind === kind)
        )
        .map((kind) => {
          const plan = plans.find((p) => p.idea.execution.kind === kind)
          const stale = plan ? Date.parse(plan.expires) <= now : false
          const check = checks.filter((row) => row.kind === kind).slice(-1)[0]
          return (
            <article
              key={kind}
              data-empty={!plan}
              className={plan?.id === selected ? 'selected' : ''}
            >
              <header>
                <strong>{names[kind]}</strong>
                <span
                  className={
                    plan
                      ? stale
                        ? 'frontier-draft-stale'
                        : 'target-text'
                      : check?.status === 'rejected'
                        ? 'stop-text'
                        : ''
                  }
                >
                  {plan
                    ? stale
                      ? zh
                        ? '交易前请刷新'
                        : 'Refresh before trading'
                      : zh
                        ? '已验证草稿'
                        : 'Validated draft'
                    : check?.status === 'deferred'
                      ? zh
                        ? '等待入场'
                        : 'Wait for entry'
                      : check?.status === 'rejected'
                        ? zh
                          ? '不可行'
                          : 'Not feasible'
                        : busy
                          ? zh
                            ? '评估中'
                            : 'Evaluating'
                          : zh
                            ? '未提出'
                            : 'Not proposed'}
                </span>
              </header>
              {plan ? (
                <>
                  <b>{plan.idea.name}</b>
                  <span
                    className={`frontier-plan-side ${plan.idea.side === 'long' ? 'target-text' : 'stop-text'}`}
                  >
                    {plan.idea.side === 'long'
                      ? zh
                        ? '做多'
                        : 'Long'
                      : zh
                        ? '做空'
                        : 'Short'}{' '}
                    · {plan.snapshot.symbol}
                  </span>
                  <p>
                    {plan.orders.length}{' '}
                    {kind === 'twap'
                      ? zh
                        ? '个定时分片'
                        : 'timed slices'
                      : kind === 'ladder'
                        ? zh
                          ? '个价格档位'
                          : 'price levels'
                        : zh
                          ? '笔入场'
                          : 'entry'}{' '}
                    · {zh ? '净盈亏比' : 'Net R:R'}{' '}
                    {Number(plan.reward_risk).toFixed(2)} : 1
                  </p>
                  <dl className="frontier-plan-levels">
                    <div>
                      <dt>
                        {kind === 'market'
                          ? zh
                            ? '预计入场'
                            : 'Est. entry'
                          : zh
                            ? '入场'
                            : 'Entry'}
                      </dt>
                      <dd>{price(plan.idea.entry)}</dd>
                    </div>
                    <div>
                      <dt>{zh ? '止损' : 'Stop'}</dt>
                      <dd className="stop-text">{price(plan.idea.stop)}</dd>
                    </div>
                    <div>
                      <dt>{zh ? '目标' : 'Target'}</dt>
                      <dd className="target-text">{price(plan.idea.target)}</dd>
                    </div>
                  </dl>
                  <p>
                    {kind === 'market'
                      ? zh
                        ? '批准后以市价入场。实际成交价可能不同。'
                        : 'Enters at market after approval. Actual fill may differ.'
                      : kind === 'twap'
                        ? zh
                          ? `${plan.orders.length} 个分片 · 间隔 ${plan.idea.execution.interval_seconds || 0} 秒`
                          : `${plan.orders.length} slices · ${plan.idea.execution.interval_seconds || 0}s apart`
                        : kind === 'ladder'
                          ? zh
                            ? '在不同价格一并下单。'
                            : 'Orders placed together at different prices.'
                          : zh
                            ? '等待你的入场价格。'
                            : 'Waits for your entry price.'}
                  </p>
                  {!connected && (
                    <div className="frontier-illustrative">
                      {zh
                        ? '示意规模 · 连接账户后为此交易定仓'
                        : 'Illustrative size · connect an account to size this trade'}
                    </div>
                  )}
                  <PlanFunding plan={plan} compact />
                  <div className="frontier-plan-outcomes">
                    <span>
                      {zh ? '计划亏损' : 'Planned loss'}{' '}
                      <b className="stop-text">−{money(plan.risk)}</b>
                    </span>
                    <span>
                      {zh ? '目标收益' : 'Target return'}{' '}
                      <b className="target-text">+{money(plan.reward)}</b>
                    </span>
                    <small>
                      {plan.snapshot.quote_asset} ·{' '}
                      {zh ? '已含成本' : 'costs included'}
                    </small>
                  </div>
                  <div className="frontier-plan-actions">
                    <button
                      onClick={() => onSelect(plan)}
                      aria-label={
                        zh
                          ? `在图表上查看：${plan.idea.name}`
                          : `View on chart: ${plan.idea.name}`
                      }
                    >
                      {plan.id === selected
                        ? zh
                          ? '已在图表上 ✓'
                          : 'On chart ✓'
                        : zh
                          ? '在图表上查看'
                          : 'View on chart'}
                    </button>
                    {onReview && (
                      <button
                        className="planning-primary"
                        disabled={reviewDisabled}
                        onClick={() => onReview(plan)}
                        aria-label={
                          zh ? `复核 ${names[kind]}` : `Review ${names[kind]}`
                        }
                      >
                        {!connected
                          ? zh
                            ? '连接后交易'
                            : 'Connect to trade'
                          : stale
                            ? zh
                              ? '刷新并复核'
                              : 'Refresh & review'
                            : zh
                              ? '复核订单'
                              : 'Review order'}
                      </button>
                    )}
                  </div>
                </>
              ) : (
                <p>
                  {(check?.reason ? validationSummary(check.reason) : '') ||
                    (busy
                      ? zh
                        ? '正在读取市场背景并检查此结构。'
                        : 'Reading market context and checking this structure.'
                      : zh
                        ? '没有提出此类型的候选方案。'
                        : 'No candidate of this type was proposed.')}
                </p>
              )}
            </article>
          )
        })}
    </section>
  )
}
