import type { Plan } from '../../lib/planning'
import { useLanguage } from '../../contexts/LanguageContext'

function useSafeLanguage() {
  try {
    return useLanguage().language
  } catch {
    return 'en' as const
  }
}

const number = (value: string) =>
  Number(value).toLocaleString('en-US', { maximumFractionDigits: 8 })
export function ExecutionSchedule({ plan }: { plan: Plan }) {
  const language = useSafeLanguage()
  const zh = language === 'zh'
  const kind = plan.idea.execution.kind
  if (kind === 'market' || kind === 'limit' || !plan.orders.length) return null
  const timed = kind === 'twap'
  const total = Number(plan.quantity)
  return (
    <section
      className="planning-execution-band"
      aria-label={
        timed
          ? zh
            ? 'TWAP 释放计划'
            : 'TWAP release schedule'
          : zh
            ? '阶梯入场价位'
            : 'Ladder entry levels'
      }
    >
      <header>
        <div>
          <strong>
            {timed
              ? zh
                ? 'TWAP · 定时释放'
                : 'TWAP · timed releases'
              : zh
                ? '阶梯 · 价格档位'
                : 'Ladder · price levels'}
          </strong>
          <span>
            {plan.orders.length} {zh ? '个子订单' : 'child orders'} ·{' '}
            {timed
              ? zh
                ? '共用一个限价'
                : 'one shared limit price'
              : zh
                ? '共用一个风险预算'
                : 'one shared risk budget'}
          </span>
        </div>
        <small>{zh ? '预览 · 批准后才开始' : 'Preview · starts only after approval'}</small>
      </header>
      <ol className="planning-execution-steps">
        {plan.orders.map((order) => (
          <li key={order.index}>
            <span className="planning-step-dot">{order.index}</span>
            <div>
              <b>
                {timed
                  ? `T+${order.offset_seconds}s`
                  : `${zh ? '限价' : 'Limit'} ${number(order.price)}`}
              </b>
              <span>
                {number(order.quantity)} {plan.snapshot.base_asset}{' '}
                <small>
                  ·{' '}
                  {total > 0
                    ? ((Number(order.quantity) / total) * 100).toFixed(0)
                    : '0'}
                  %
                </small>
              </span>
              <small>
                {timed
                  ? `${zh ? '限价' : 'Limit'} ${number(order.price)}`
                  : zh
                    ? '开始时一并下单'
                    : 'Placed together at start'}
              </small>
            </div>
          </li>
        ))}
      </ol>
      <p>
        {timed
          ? zh
            ? '这条以秒为单位的释放时间轴与 K 线时间无关。T+0 是执行开始时刻，不是过去的 K 线。每个分片仍是限价单，可能无法成交。'
            : 'This seconds-based release axis is separate from candle time. T+0 is execution start, not a past candle. Each slice remains a limit order and may not fill.'
          : zh
            ? '每个价格档位都是一笔独立的限价单，某个档位可能永远无法成交。所有子订单共用显示的止损和目标价。'
            : 'Each price level is a separate limit order. A level may never fill. All children share the displayed stop and target.'}
      </p>
    </section>
  )
}
