import { useRef, useState } from 'react'
import {
  labelEvent,
  type EventLabel,
  type EventHistoryOptions,
} from '../../lib/api/events'
import { useLanguage } from '../../contexts/LanguageContext'

const types: Record<string, string> = {
  price_target: '价格目标',
  regulation: '监管',
  etf: 'ETF',
  protocol: '协议升级',
  security: '安全事件',
  adoption: '采用与应用',
  other: '其他',
}
const relations: Record<string, string> = {
  direct: '直接关联',
  indirect: '间接关联',
  insufficient: '关联不足',
}

export function EventLabelPanel({
  eventId,
  modelId,
  label,
  onLabeled,
  options,
}: {
  eventId: string
  modelId: string
  label?: EventLabel
  onLabeled: () => Promise<unknown>
  options?: EventHistoryOptions
}) {
  const { language } = useLanguage()
  const zh = language === 'zh'
  const text = (cn: string, en: string) => (zh ? cn : en)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const flight = useRef(false)
  const current = label?.model_id === modelId ? label : undefined
  const analyze = async () => {
    if (!modelId || flight.current) return
    flight.current = true
    setBusy(true)
    setError('')
    try {
      await labelEvent(eventId, modelId, options)
      await onLabeled()
    } catch (e) {
      setError(
        e instanceof Error
          ? e.message
          : text('打标失败，请稍后重试', 'Labeling failed. Try again later.')
      )
    } finally {
      flight.current = false
      setBusy(false)
    }
  }
  return (
    <section
      className="mt-4 space-y-3 rounded-xl border border-blue-100 bg-blue-50/40 p-4"
      aria-label={text('模型事件打标', 'Model event labeling')}
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-sm font-semibold">
          {text('模型事件打标', 'Model event labeling')}
        </h3>
        <button
          type="button"
          className="rounded-lg border border-blue-200 bg-white px-3 py-2 text-sm text-blue-700 disabled:opacity-50"
          disabled={!modelId || busy || (!!current && !current.stale)}
          onClick={() => void analyze()}
        >
          {busy
            ? text('打标中…', 'Labeling…')
            : current && !current.stale
              ? text('已缓存', 'Cached')
              : current?.stale
                ? text('重新打标（可能计费）', 'Relabel (charges may apply)')
                : text(
                    '打标此事件（可能计费）',
                    'Label event (charges may apply)'
                  )}
        </button>
      </div>
      {!modelId && (
        <p className="text-xs text-slate-500">
          {text('请先选择已启用的模型', 'Select an enabled model first')}
        </p>
      )}
      {error && (
        <p role="alert" className="text-sm text-red-700">
          {error}
        </p>
      )}
      {current && (
        <>
          <p className="text-xs text-slate-500">
            {current.model_name} · {text('分析于', 'Analyzed')}{' '}
            {new Date(current.analyzed_at).toLocaleString(
              zh ? 'zh-CN' : 'en-US'
            )}
          </p>
          {current.stale && (
            <p role="status" className="text-sm text-amber-800">
              {text(
                '事件内容、价格或模型配置已变化，以下为旧标签，仅供历史参考。不会自动重新调用。',
                'Event content, prices or model settings changed. Old labels are historical only. No automatic rerun.'
              )}
            </p>
          )}
          <div className={current.stale ? 'space-y-3 opacity-60' : 'space-y-3'}>
            <p className="text-xs font-medium text-blue-800">
              {zh
                ? types[current.analysis.event_type]
                : current.analysis.event_type}
            </p>
            <p className="text-sm">{current.analysis.summary}</p>
            {current.analysis.associations.map((a) => (
              <p key={a.coin} className="text-sm">
                <strong>
                  {a.coin} · {zh ? relations[a.relation] : a.relation}
                </strong>{' '}
                — {a.reason}
              </p>
            ))}
            <div>
              <h4 className="text-xs font-semibold">
                {text('关注原因', 'Why watch')}
              </h4>
              <p className="mt-1 text-sm">
                {current.analysis.attention_reason}
              </p>
            </div>
            <div>
              <h4 className="text-xs font-semibold">
                {text(
                  '条件化影响（非交易指令）',
                  'Conditional impact (not trade instructions)'
                )}
              </h4>
              <ul className="mt-1 list-disc space-y-1 pl-4 text-sm">
                {current.analysis.impact_conditions.map((c, i) => (
                  <li key={i}>{c}</li>
                ))}
              </ul>
            </div>
            <div>
              <h4 className="text-xs font-semibold text-amber-800">
                {text('局限与风险', 'Limitations and risks')}
              </h4>
              <ul className="mt-1 list-disc space-y-1 pl-4 text-sm">
                {current.analysis.limitations.map((c, i) => (
                  <li key={i}>{c}</li>
                ))}
              </ul>
            </div>
            <details className="text-xs text-slate-500">
              <summary className="cursor-pointer">
                {text('引用依据 ID', 'Evidence IDs')}
              </summary>
              <ul className="mt-2 break-all">
                {current.analysis.evidence_ids.map((id) => (
                  <li key={id}>{id}</li>
                ))}
              </ul>
              <p className="mt-2 break-all">
                {text('输入版本', 'Input version')}: {current.input_hash}
              </p>
            </details>
          </div>
        </>
      )}
    </section>
  )
}
