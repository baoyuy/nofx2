import { useEffect, useRef, useState } from 'react'
import type { PlanningEvent } from '../../lib/planning'
import { validationSummary } from '../../lib/planningPresentation'
import { useLanguage } from '../../contexts/LanguageContext'

function useSafeLanguage() {
  try {
    return useLanguage().language
  } catch {
    return 'en' as const
  }
}

function storedLanguage(): string {
  try {
    return localStorage.getItem('language') || 'en'
  } catch {
    return 'en'
  }
}

export interface ActivityItem {
  id: string
  label: string
  detail: string
  calculation?: string
  state: 'running' | 'done' | 'failed'
  started: number
  ended?: number
}
const titles: Record<string, string> = {
  get_candles: 'Read candlesticks',
  get_market: 'Read live order book',
  propose_plans: 'Validate trading candidates',
  draw_chart: 'Draw chart annotations',
  get_data_plugin: 'Read market data',
  get_entry_plugins: 'Inspect entry calculators',
  develop_entry_plugin: 'Build and test entry calculator',
  run_entry_plugin: 'Run entry calculator',
}
const titlesZh: Record<string, string> = {
  get_candles: '读取 K 线',
  get_market: '读取实时盘口',
  propose_plans: '验证交易候选方案',
  draw_chart: '绘制图表标注',
  get_data_plugin: '读取市场数据',
  get_entry_plugins: '检查入场计算器',
  develop_entry_plugin: '构建并测试入场计算器',
  run_entry_plugin: '运行入场计算器',
}
export const structureNames: Record<string, string> = {
  market: 'Market',
  limit: 'Single limit',
  ladder: 'Weighted ladder',
  twap: 'TWAP',
}
export const structureNamesZh: Record<string, string> = {
  market: '市价',
  limit: '单笔限价',
  ladder: '加权阶梯',
  twap: 'TWAP',
}
export function recordActivity(
  items: ActivityItem[],
  event: PlanningEvent,
  now = Date.now(),
  language?: string
): ActivityItem[] {
  const zh = (language ?? storedLanguage()) === 'zh'
  const L = (en: string, zhText: string) => (zh ? zhText : en)
  const toolTitles = zh ? titlesZh : titles
  const names = zh ? structureNamesZh : structureNames
  const d = event.data
  if (event.type === 'entry_policy')
    return [
      ...items.filter((item) => item.id !== 'entry_policy'),
      {
        id: 'entry_policy',
        label: L('Trend board · Long-only planning', '趋势榜 · 仅做多规划'),
        detail: String(d.message),
        state: 'done' as const,
        started: now,
        ended: now,
      },
    ]
  if (event.type === 'planning_sizing')
    return [
      ...items.filter((item) => item.id !== 'planning_sizing'),
      {
        id: 'planning_sizing',
        label: L('Plan sizing · equity preview', '计划定仓 · 权益预览'),
        detail: String(d.message),
        state: 'done' as const,
        started: now,
        ended: now,
      },
    ]
  if (event.type === 'model_transport')
    return [
      ...items,
      {
        id: 'model_transport',
        label: L('Model interface', '模型接口'),
        detail:
          d.transport === 'text_tools'
            ? L(
                'Structured text tool compatibility',
                '结构化文本工具兼容模式'
              )
            : L('Native tool calls', '原生工具调用'),
        state: 'done' as const,
        started: now,
        ended: now,
      },
    ]
  if (event.type === 'plugin_data') {
    const namesMap: Record<string, string> = zh
      ? {
          funding: '资金费率',
          open_interest: '未平仓量',
          hyperdata_cost: '成本与强平',
          hyperdata_profit: '盈利排行榜',
          hyperdata_winrate: '持有人胜率',
        }
      : {
          funding: 'Funding rate',
          open_interest: 'Open interest',
          hyperdata_cost: 'Cost and liquidation',
          hyperdata_profit: 'Profit leaderboard',
          hyperdata_winrate: 'Holder win rate',
        }
    const metrics = Array.isArray(d.metrics)
      ? (d.metrics as { label: string; value: string; unit: string }[])
      : []
    const id = `plugin_${String(d.plugin)}`
    const previous = items.find((row) => row.id === id)
    const row: ActivityItem = {
      id,
      label: namesMap[String(d.plugin)] || L('Market data plugin', '市场数据插件'),
      state: d.error ? 'failed' : 'done',
      started: previous?.started || now,
      ended: now,
      detail: d.error
        ? String(d.error)
        : metrics
            .slice(0, 3)
            .map((metric) => {
              const value = Number(metric.value)
              const display = Number.isFinite(value)
                ? metric.unit === 'fraction'
                  ? `${(value * 100).toFixed(5)}%`
                  : `${value.toLocaleString('en-US', { maximumFractionDigits: 6 })} ${metric.unit}`
                : metric.value
              return `${metric.label}: ${display}`
            })
            .join(' · '),
    }
    return previous
      ? items.map((item) => (item.id === id ? row : item))
      : [...items, row].slice(-100)
  }
  const endModels = (rows: ActivityItem[]) =>
    rows.map((row) =>
      row.id.startsWith('model_') && row.state === 'running'
        ? {
            ...row,
            state: 'done' as const,
            ended: now,
            detail: L('Model response received', '已收到模型响应'),
          }
        : row
    )
  const upsert = (
    rows: ActivityItem[],
    item: Partial<ActivityItem> & {
      id: string
      label: string
      state: ActivityItem['state']
    }
  ) => {
    const index = rows.findIndex((row) => row.id === item.id)
    if (index < 0)
      return [...rows, { started: now, detail: '', ...item }].slice(-100)
    return rows.map((row, i) => (i === index ? { ...row, ...item } : row))
  }
  if (event.type === 'iteration')
    return upsert(endModels(items), {
      id: `model_${d.round}`,
      label: zh
        ? `模型规划 · 第 ${d.round} 轮`
        : `Model planning · round ${d.round}`,
      detail: L(
        'Evaluating the observed market and available tools',
        '正在评估观察到的市场和可用工具'
      ),
      state: 'running',
    })
  if (event.type === 'status') {
    if (typeof d.elapsed_seconds !== 'number') return items
    return items.map((row) =>
      row.id.startsWith('model_') && row.state === 'running'
        ? {
            ...row,
            detail: zh
              ? `等待模型响应 · ${d.elapsed_seconds} 秒`
              : `Waiting for model response · ${d.elapsed_seconds}s`,
          }
        : row
    )
  }
  if (event.type === 'tool_start')
    return upsert(endModels(items), {
      id: String(d.id),
      label: toolTitles[String(d.name)] || L('Run planning tool', '运行规划工具'),
      detail: L('In progress', '进行中'),
      state: 'running',
    })
  if (event.type === 'tool_progress')
    return upsert(items, {
      id: String(d.id),
      label: toolTitles[String(d.name)] || L('Run planning tool', '运行规划工具'),
      detail: zh
        ? `${d.interval} · ${d.count} / ${d.target_count} 根 K 线 · ${d.scope}`
        : `${d.interval} · ${d.count} / ${d.target_count} candles · ${d.scope}`,
      state: 'running',
    })
  if (event.type === 'tool_result') {
    const detail = [
      d.interval,
      typeof d.count === 'number'
        ? zh
          ? `${d.count}${typeof d.target_count === 'number' ? ` / ${d.target_count}` : ''} 根 K 线`
          : `${d.count}${typeof d.target_count === 'number' ? ` / ${d.target_count}` : ''} candles`
        : undefined,
      d.truncated ? L('Limited exchange history', '交易所历史有限') : undefined,
      d.scope,
    ]
      .filter(Boolean)
      .join(' · ')
    return upsert(items, {
      id: String(d.id),
      label: toolTitles[String(d.name)] || L('Run planning tool', '运行规划工具'),
      state: d.ok ? 'done' : 'failed',
      ended: now,
      detail: d.reason
        ? validationSummary(String(d.reason))
        : detail ||
          (d.ok
            ? L('Completed', '已完成')
            : L('Could not complete this step', '无法完成此步骤')),
      calculation: d.reason ? String(d.reason) : undefined,
    })
  }
  if (event.type === 'candidate_start' || event.type === 'candidate_result')
    return upsert(endModels(items), {
      id: String(d.id),
      label:
        d.status === 'deferred'
          ? zh
            ? `${names[String(d.kind)] || '入场'} · 等待`
            : `${structureNames[String(d.kind)] || 'Entry'} · Wait`
          : zh
            ? `${names[String(d.kind)] || '候选方案'} · ${String(d.name)}`
            : `${structureNames[String(d.kind)] || 'Candidate'} · ${String(d.name)}`,
      state:
        event.type === 'candidate_start'
          ? 'running'
          : d.status === 'rejected'
            ? 'failed'
            : 'done',
      ...(event.type === 'candidate_result' ? { ended: now } : {}),
      calculation: d.reason ? String(d.reason) : undefined,
      detail: d.reason
        ? validationSummary(String(d.reason))
        : event.type === 'candidate_start'
          ? L(
              'Checking costs, child orders and risk',
              '正在检查成本、子订单和风险'
            )
          : d.status === 'reused'
            ? L('Already saved; duplicate skipped', '已保存；跳过重复项')
            : L('Passed mathematical validation', '已通过数学验证'),
    })
  if (event.type === 'plans') {
    const count = Array.isArray(d.plans) ? d.plans.length : 0
    const deferred = Array.isArray(d.checks)
      ? d.checks.filter((check) => check.status === 'deferred').length
      : 0
    return upsert(items, {
      id: `save_${items.filter((row) => row.id.startsWith('save_')).length}`,
      label: d.waiting
        ? L('Waiting for entry conditions', '等待入场条件')
        : count
          ? L('Save validated drafts', '保存已验证草稿')
          : L('No new drafts', '没有新草稿'),
      state: 'done',
      ended: now,
      detail: d.waiting
        ? zh
          ? `${deferred} 种入场类型等待中 · 未提交订单`
          : `${deferred} entry types waiting · no orders submitted`
        : zh
          ? `${count} 份新草稿${deferred ? ` · ${deferred} 种入场类型等待中` : ''} · ${Array.isArray(d.rejected) ? d.rejected.length : 0} 个候选方案被拒绝 · 未提交订单`
          : `${count} new drafts${deferred ? ` · ${deferred} entry types waiting` : ''} · ${Array.isArray(d.rejected) ? d.rejected.length : 0} rejected candidates · no orders submitted`,
    })
  }
  if (
    event.type === 'outcome' ||
    event.type === 'error' ||
    event.type === 'cancelled'
  ) {
    const ended = (event.type === 'outcome' ? endModels(items) : items).map(
      (row) =>
        row.state === 'running'
          ? {
              ...row,
              state: 'failed' as const,
              ended: now,
              detail:
                event.type === 'cancelled'
                  ? L('Stopped by you', '已被你停止')
                  : L('Stopped before completion', '在完成前停止'),
            }
          : row
    )
    return upsert(ended, {
      id: 'outcome',
      label:
        event.type === 'error'
          ? L('Planning interrupted', '规划已中断')
          : event.type === 'cancelled'
            ? L('Planning stopped', '规划已停止')
            : d.waiting && d.stop_reason === 'completed'
              ? L('Waiting for an entry', '等待入场')
              : L('Planning complete', '规划完成'),
      state: event.type === 'error' ? 'failed' : 'done',
      ended: now,
      detail:
        event.type === 'error'
          ? String(d.message)
          : event.type === 'cancelled'
            ? L(
                'Saved drafts remain available. No orders were submitted.',
                '已保存的草稿仍可用。未提交任何订单。'
              )
            : Number(d.drafts) > 0
              ? zh
                ? `${d.drafts} 份草稿已保存 · 等待你复核`
                : `${d.drafts} drafts saved · awaiting your review`
              : d.waiting
                ? L(
                    'No order to approve. Review the entry conditions below.',
                    '没有可批准的订单。请查看下方入场条件。'
                  )
                : L(
                    'No drafts saved. Review the explanation; no orders submitted.',
                    '没有保存草稿。请查看说明；未提交任何订单。'
                  ),
    })
  }
  return items
}
export function PlanningActivity({
  items,
  busy,
}: {
  items: ActivityItem[]
  busy: boolean
}) {
  const language = useSafeLanguage()
  const zh = language === 'zh'
  const [now, setNow] = useState(Date.now)
  const [expanded, setExpanded] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  const follow = useRef(true)
  useEffect(() => {
    if (!busy) return
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [busy])
  useEffect(() => {
    if (follow.current && box.current)
      box.current.scrollTop = box.current.scrollHeight
  }, [items, now])
  if (!busy && !items.length) return null
  const rows = busy || expanded ? items : items.slice(-4)
  return (
    <section
      className="planning-live-activity"
      aria-label={zh ? '实时规划动态' : 'Live planning activity'}
    >
      <header>
        <strong>
          <i className={busy ? 'activity-live-dot' : ''} />
          {busy ? (zh ? '实时动态' : 'Live activity') : zh ? '动态' : 'Activity'}
        </strong>
        <span>
          {items.filter((row) => row.state === 'done').length}{' '}
          {zh ? '已完成' : 'completed'}
          {busy ? (zh ? ' · 流式更新中' : ' · streaming') : ''}
        </span>
        {!busy && items.length > 4 && (
          <button onClick={() => setExpanded(!expanded)}>
            {expanded ? (zh ? '收起' : 'Show less') : zh ? '全部步骤' : 'All steps'}
          </button>
        )}
      </header>
      <div
        className="planning-activity-list"
        ref={box}
        onScroll={() => {
          const node = box.current
          if (node)
            follow.current =
              node.scrollHeight - node.scrollTop - node.clientHeight < 40
        }}
        role="log"
        aria-live="polite"
        aria-relevant="additions text"
      >
        {rows.map((row) => (
          <article
            key={row.id}
            className={`planning-activity-item ${row.state}`}
          >
            <span className="activity-state" aria-label={row.state}>
              {row.state === 'running' ? (
                <i className="planning-spinner" />
              ) : row.state === 'done' ? (
                '✓'
              ) : (
                '!'
              )}
            </span>
            <div>
              <b>{row.label}</b>
              <p title={row.calculation}>{row.detail}</p>
            </div>
            <time>
              {Math.max(
                0,
                Math.floor(((row.ended || now) - row.started) / 1000)
              )}
              s
            </time>
          </article>
        ))}
      </div>
    </section>
  )
}
