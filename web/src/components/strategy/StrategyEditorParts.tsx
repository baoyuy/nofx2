import { NoticeText } from '../NoticeText'
import { useState, type ReactNode } from 'react'
import { ChevronDown, Info, Loader2, Play, RotateCcw } from 'lucide-react'
import { api } from '../../lib/api'
import type { StrategyTestRun } from '../../lib/api/strategies'
import type { AIModel, StrategyConfig } from '../../types'
import {
  LIMITS,
  ruleSections,
  type FreeForm,
  type RuleSection,
} from '../../lib/strategyForm'

export const text = (language: string, zh: string, en: string) =>
  language === 'zh' ? zh : en

export const inputClass =
  'w-full rounded-lg border border-[rgba(25,36,52,0.14)] bg-nofx-bg-deeper px-3 py-2 text-sm text-nofx-text outline-hidden focus:border-nofx-gold'

// Section is one card of the editor, with an anchor for the side menu.
export function Section({
  id,
  title,
  intro,
  children,
}: {
  id: string
  title: string
  intro?: string
  children: ReactNode
}) {
  return (
    <section
      id={id}
      className="scroll-mt-24 rounded-lg border border-[rgba(25,36,52,0.14)] bg-nofx-bg-lighter p-4"
    >
      <h2 className="text-sm font-semibold text-nofx-text">{title}</h2>
      {intro ? (
        <p className="mt-1 text-xs text-nofx-text-muted">{intro}</p>
      ) : null}
      <div className="mt-3 space-y-4">{children}</div>
    </section>
  )
}

// Field shows a label, what the setting does, and whether the program
// enforces it or the AI is only asked to follow it.
export function Field({
  label,
  hint,
  enforced,
  language,
  children,
}: {
  label: string
  hint?: string
  enforced?: 'code' | 'ai'
  language: string
  children: ReactNode
}) {
  return (
    <label className="block">
      <span className="mb-1 flex items-center gap-2 text-sm font-medium text-nofx-text">
        {label}
        {enforced ? (
          <span
            title={
              enforced === 'code'
                ? text(
                    language,
                    '程序强制执行，AI 无法突破',
                    'Applied by the program; the AI cannot exceed it'
                  )
                : text(
                    language,
                    '写进提示词，由 AI 自行遵守',
                    'Written into the prompt for the AI to follow'
                  )
            }
            className={`rounded px-1.5 py-0.5 text-[10px] font-normal ${enforced === 'code' ? 'bg-nofx-success/10 text-nofx-success' : 'bg-nofx-gold/10 text-nofx-gold'}`}
          >
            {enforced === 'code'
              ? text(language, '代码强制', 'Code enforced')
              : text(language, 'AI 参考', 'AI guided')}
          </span>
        ) : null}
      </span>
      {children}
      {hint ? (
        <span className="mt-1 block text-xs text-nofx-text-muted">{hint}</span>
      ) : null}
    </label>
  )
}

export function NumberInput({
  value,
  range,
  step = 1,
  suffix,
  onChange,
}: {
  value: number
  range: { min: number; max: number }
  step?: number
  suffix?: string
  onChange: (value: number) => void
}) {
  const [draft, setDraft] = useState<string | null>(null)
  const commit = (raw: string) => {
    setDraft(null)
    const n = Number(raw)
    if (!Number.isFinite(n) || raw.trim() === '') return
    const decimals = String(step).split('.')[1]?.length || 0
    const clamped = Math.min(range.max, Math.max(range.min, n))
    onChange(Number(clamped.toFixed(decimals)))
  }
  return (
    <div className="relative">
      <input
        type="number"
        inputMode="decimal"
        min={range.min}
        max={range.max}
        step={step}
        value={draft ?? String(value)}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={(event) => commit(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Enter')
            commit((event.target as HTMLInputElement).value)
        }}
        className={`${inputClass} ${suffix ? 'pr-12' : ''}`}
      />
      {suffix ? (
        <span className="pointer-events-none absolute inset-y-0 right-3 flex items-center text-xs text-nofx-text-muted">
          {suffix}
        </span>
      ) : null}
    </div>
  )
}

// RiskSummary turns the risk settings into money for an example balance,
// so "5x position ratio at 10x leverage" becomes concrete numbers.
export function RiskSummary({
  form,
  language,
}: {
  form: FreeForm
  language: string
}) {
  const [equity, setEquity] = useState(1000)
  const fmt = (v: number) =>
    v.toLocaleString(undefined, { maximumFractionDigits: 0 })
  const rows = [
    {
      label: 'BTC / ETH',
      leverage: form.leverage,
      ratio: form.btcEthPositionRatio ?? 5,
    },
    {
      label: text(language, '山寨币', 'Altcoins'),
      leverage: form.altLeverage ?? form.leverage,
      ratio: form.altPositionRatio ?? 5,
    },
  ]
  const marginCap = equity * form.marginUsage
  const worstMargin = Math.min(
    marginCap,
    form.maxPositions *
      Math.max(...rows.map((r) => (equity * r.ratio) / r.leverage))
  )
  const highRisk =
    form.leverage >= 10 ||
    (form.altLeverage ?? 0) >= 10 ||
    form.marginUsage >= 0.8
  return (
    <div className="rounded-lg border border-[rgba(25,36,52,0.14)] bg-nofx-bg-deeper p-3">
      <div className="mb-2 flex flex-wrap items-center gap-2 text-sm font-medium text-nofx-text">
        <Info className="h-4 w-4 text-nofx-gold" />
        {text(language, '按账户资金估算', 'Estimate for an account of')}
        <input
          type="number"
          min={10}
          value={equity}
          onChange={(e) => setEquity(Math.max(10, Number(e.target.value) || 0))}
          className="w-24 rounded border border-[rgba(25,36,52,0.14)] bg-nofx-bg-lighter px-2 py-0.5 text-sm"
        />
        USDT
      </div>
      <table className="w-full text-xs">
        <thead className="text-nofx-text-muted">
          <tr className="text-left">
            <th className="py-1 font-normal" />
            <th className="py-1 font-normal">
              {text(language, '单笔最大仓位价值', 'Max position value')}
            </th>
            <th className="py-1 font-normal">
              {text(language, '对应保证金', 'Margin needed')}
            </th>
            <th className="py-1 font-normal">
              {text(
                language,
                '约多少反向波动爆仓',
                'Adverse move to liquidation'
              )}
            </th>
          </tr>
        </thead>
        <tbody className="text-nofx-text">
          {rows.map((r) => (
            <tr key={r.label}>
              <td className="py-1 font-medium">{r.label}</td>
              <td className="py-1 tabular-nums">
                {fmt(equity * r.ratio)} USDT
              </td>
              <td className="py-1 tabular-nums">
                {fmt((equity * r.ratio) / r.leverage)} USDT
              </td>
              <td className="py-1 tabular-nums">
                ≈ {Math.max(0.1, 100 / r.leverage - 0.5).toFixed(1)}%
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="mt-2 text-xs text-nofx-text-muted">
        {text(
          language,
          `最多同时 ${form.maxPositions} 个仓位，保证金最多用 ${fmt(marginCap)} USDT（${Math.round(form.marginUsage * 100)}%），满仓时约占用 ${fmt(worstMargin)} USDT。每笔止损和止盈由 AI 决定，盈亏比至少 1:${form.minRiskReward}。`,
          `Up to ${form.maxPositions} positions; margin capped at ${fmt(marginCap)} USDT (${Math.round(form.marginUsage * 100)}%), about ${fmt(worstMargin)} USDT when fully deployed. The AI sets each stop and target, with reward/risk of at least 1:${form.minRiskReward}.`
        )}
      </p>
      {highRisk ? (
        <p role="alert" className="mt-2 text-xs text-nofx-danger">
          {text(
            language,
            '杠杆或保证金使用率偏高：行情反向时亏损会很快，建议先用模拟盘试跑。',
            'Leverage or margin usage is high: losses grow fast in an adverse move. Try it on the paper account first.'
          )}
        </p>
      ) : null}
    </div>
  )
}

const ruleLabels: Record<
  RuleSection,
  { zh: string; en: string; hintZh: string; hintEn: string }
> = {
  role_definition: {
    zh: '角色',
    en: 'Role',
    hintZh: 'AI 是什么样的交易员、交易什么品种',
    hintEn: 'What kind of trader the AI is and what it trades',
  },
  trading_frequency: {
    zh: '交易频率',
    en: 'Trading frequency',
    hintZh: '多久交易一次、单笔持仓多久，防止过度交易',
    hintEn: 'How often to trade and how long to hold; prevents overtrading',
  },
  entry_standards: {
    zh: '开仓标准',
    en: 'Entry standards',
    hintZh: '满足什么条件才开仓；最低把握度会自动追加在后面',
    hintEn:
      'Conditions required to enter; the minimum confidence is appended automatically',
  },
  decision_process: {
    zh: '决策流程',
    en: 'Decision process',
    hintZh: '每轮按什么步骤思考',
    hintEn: 'The steps to follow each cycle',
  },
}

// RuleEditor edits the prompt sections. Empty sections use the engine's
// built-in text.
export function RuleEditor({
  form,
  defaults,
  language,
  onChange,
}: {
  form: FreeForm
  defaults: Record<RuleSection, string>
  language: string
  onChange: (rules: Record<RuleSection, string>, customPrompt?: string) => void
}) {
  const rules = Object.fromEntries(
    ruleSections.map((k) => [k, form.rules?.[k] ?? ''])
  ) as Record<RuleSection, string>
  return (
    <div className="space-y-4">
      {ruleSections.map((key) => (
        <Field
          key={key}
          language={language}
          label={text(language, ruleLabels[key].zh, ruleLabels[key].en)}
          hint={text(language, ruleLabels[key].hintZh, ruleLabels[key].hintEn)}
        >
          <textarea
            value={rules[key]}
            rows={4}
            placeholder={text(
              language,
              '留空 = 使用内置默认规则',
              'Empty = built-in default'
            )}
            onChange={(e) => onChange({ ...rules, [key]: e.target.value })}
            className={`${inputClass} font-mono text-xs`}
          />
          {defaults[key] && rules[key] !== defaults[key] ? (
            <button
              type="button"
              onClick={(e) => {
                e.preventDefault()
                onChange({ ...rules, [key]: defaults[key] })
              }}
              className="mt-1 inline-flex items-center gap-1 text-xs text-nofx-gold hover:underline"
            >
              <RotateCcw className="h-3 w-3" />
              {text(language, '恢复默认', 'Restore default')}
            </button>
          ) : null}
        </Field>
      ))}
      <Field
        language={language}
        label={text(language, '额外要求', 'Extra instructions')}
        hint={text(
          language,
          '例如「只做多」「不在周末开仓」「BTC 跌破 4 小时 EMA50 时不做多」。不能突破上面的风险控制。',
          'E.g. "long only", "no new positions on weekends". Cannot override the risk control.'
        )}
      >
        <textarea
          value={form.customPrompt}
          rows={3}
          onChange={(e) => onChange(rules, e.target.value)}
          className={inputClass}
        />
      </Field>
    </div>
  )
}

function Collapsible({
  title,
  children,
}: {
  title: string
  children: ReactNode
}) {
  const [open, setOpen] = useState(false)
  return (
    <div className="rounded-lg border border-[rgba(25,36,52,0.14)]">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="flex w-full items-center justify-between px-3 py-2 text-left text-sm font-medium text-nofx-text"
      >
        {title}
        <ChevronDown
          className={`h-4 w-4 transition ${open ? 'rotate-180' : ''}`}
        />
      </button>
      {open ? (
        <pre className="max-h-[420px] overflow-auto whitespace-pre-wrap border-t border-[rgba(25,36,52,0.14)] p-3 text-xs text-nofx-text">
          {children}
        </pre>
      ) : null}
    </div>
  )
}

// TestRun builds the real prompts from live market data with the unsaved
// settings, and can ask the AI for one decision. It never places orders.
export function TestRun({
  buildConfig,
  models,
  language,
}: {
  buildConfig: () => StrategyConfig
  models: AIModel[]
  language: string
}) {
  const [modelID, setModelID] = useState('')
  const [busy, setBusy] = useState<'' | 'preview' | 'ai'>('')
  const [result, setResult] = useState<StrategyTestRun | null>(null)
  const [error, setError] = useState('')
  const usable = models.filter(
    (m) => m.enabled && m.has_api_key && m.provider !== 'claw402'
  )
  const run = async (withAI: boolean) => {
    setBusy(withAI ? 'ai' : 'preview')
    setError('')
    try {
      const chosen = modelID || usable[0]?.id || ''
      setResult(
        await api.testRunStrategy({
          config: buildConfig(),
          ai_model_id: withAI ? chosen : '',
          run_real_ai: withAI && Boolean(chosen),
        })
      )
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy('')
    }
  }
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          disabled={Boolean(busy)}
          onClick={() => void run(false)}
          className="inline-flex items-center gap-2 rounded-lg border border-[rgba(25,36,52,0.24)] px-3 py-2 text-sm text-nofx-text hover:border-nofx-gold disabled:opacity-50"
        >
          {busy === 'preview' ? (
            <Loader2 className="h-4 w-4 animate-spin" />
          ) : null}
          {text(language, '预览提示词', 'Preview prompt')}
        </button>
        <select
          value={modelID || usable[0]?.id || ''}
          onChange={(e) => setModelID(e.target.value)}
          className="rounded-lg border border-[rgba(25,36,52,0.14)] bg-nofx-bg-deeper px-2 py-2 text-sm"
          disabled={!usable.length}
        >
          {usable.length ? null : (
            <option value="">
              {text(
                language,
                '先在「配置」里添加 AI',
                'Add an AI under Configuration first'
              )}
            </option>
          )}
          {usable.map((m) => (
            <option key={m.id} value={m.id}>
              {m.name} · {m.customModelName || m.provider}
            </option>
          ))}
        </select>
        <button
          type="button"
          disabled={Boolean(busy) || !usable.length}
          onClick={() => void run(true)}
          className="inline-flex items-center gap-2 rounded-lg bg-nofx-gold px-3 py-2 text-sm font-semibold text-white hover:bg-nofx-gold-highlight disabled:opacity-50"
        >
          {busy === 'ai' ? (
            <Loader2 className="h-4 w-4 animate-spin" />
          ) : (
            <Play className="h-4 w-4" />
          )}
          {text(language, '用 AI 试跑一次', 'Run once with the AI')}
        </button>
      </div>
      <p className="text-xs text-nofx-text-muted">
        {text(
          language,
          '用当前（包括未保存的）设置和实时行情生成 AI 实际会收到的提示词。「试跑」会调用一次所选 AI，消耗它的 token，但不会下单。',
          'Builds the exact prompts the AI receives from the current (including unsaved) settings and live market data. "Run once" calls the selected AI one time and uses its tokens, but never places an order.'
        )}
      </p>
      {busy === 'ai' ? (
        <p role="status" className="text-xs text-nofx-text-muted">
          {text(
            language,
            'AI 正在分析，推理模型可能需要一两分钟…',
            'The AI is analyzing; reasoning models can take a minute or two…'
          )}
        </p>
      ) : null}
      {error ? (
        <p role="alert" className="text-sm text-nofx-danger">
          <NoticeText text={error} />
        </p>
      ) : null}
      {result ? (
        <div className="space-y-2">
          <p className="text-xs text-nofx-text-muted">
            {text(language, '候选币种', 'Candidates')}：
            {result.candidates.map((c) => c.symbol).join(', ') || '--'}
          </p>
          {result.ai_response &&
          (result.ai_error || /successful/.test(result.note || '')) ? (
            <div className="rounded-lg border border-[rgba(25,36,52,0.14)] p-3">
              <div className="mb-1 text-sm font-medium text-nofx-text">
                {text(language, 'AI 的决定', 'AI decision')}
              </div>
              <pre
                className={`max-h-[420px] overflow-auto whitespace-pre-wrap text-xs ${result.ai_error ? 'text-nofx-danger' : 'text-nofx-text'}`}
              >
                {result.ai_response}
              </pre>
            </div>
          ) : null}
          <Collapsible
            title={text(
              language,
              `系统提示词（${result.system_prompt.length} 字）`,
              `System prompt (${result.system_prompt.length} chars)`
            )}
          >
            {result.system_prompt}
          </Collapsible>
          <Collapsible
            title={text(
              language,
              `行情提示词（${result.user_prompt.length} 字）`,
              `Market prompt (${result.user_prompt.length} chars)`
            )}
          >
            {result.user_prompt}
          </Collapsible>
        </div>
      ) : null}
    </div>
  )
}

export { LIMITS }
