import { useEffect, useRef, useState } from 'react'
import useSWR from 'swr'
import { useLanguage } from '../../contexts/LanguageContext'
import {
  generateEventBriefing,
  getEventBriefing,
  type BriefingStance,
} from '../../lib/api/eventBriefings'
import type { EventsResponse } from '../../lib/api/events'

const stances: { id: BriefingStance; zh: string; en: string; color: string }[] =
  [
    {
      id: 'favorable',
      zh: '偏利好',
      en: 'Potentially favorable',
      color: 'border-emerald-200 bg-emerald-50',
    },
    {
      id: 'unfavorable',
      zh: '偏利空',
      en: 'Potentially unfavorable',
      color: 'border-rose-200 bg-rose-50',
    },
    {
      id: 'mixed',
      zh: '分歧 / 中性',
      en: 'Mixed / neutral',
      color: 'border-slate-200 bg-slate-50',
    },
    {
      id: 'unconfirmed',
      zh: '待确认',
      en: 'Unconfirmed',
      color: 'border-amber-200 bg-amber-50',
    },
    {
      id: 'low_relevance',
      zh: '低相关',
      en: 'Low relevance',
      color: 'border-slate-200 bg-white',
    },
  ]
function safeURL(raw: string) {
  try {
    const u = new URL(raw)
    return ['http:', 'https:'].includes(u.protocol) &&
      !u.username &&
      !u.password
      ? u.href
      : undefined
  } catch {
    return undefined
  }
}

export function EventBriefingPanel({
  modelId,
  sessionKey,
  events,
  enabled,
  autoLabel,
}: {
  modelId: string
  sessionKey: string
  events: EventsResponse['events']
  enabled: boolean
  autoLabel: boolean
}) {
  const { language } = useLanguage()
  const autoFailed = useRef<string>('')
  const text = (zh: string, en: string) => (language === 'zh' ? zh : en)
  const [busy, setBusy] = useState(false)
  const autoTriggered = useRef<string>('')
  const [error, setError] = useState('')
  const flight = useRef(false)
  const {
    data,
    error: loadError,
    isLoading,
    mutate,
  } = useSWR(
    modelId ? ['event-briefing', sessionKey, modelId] : null,
    () => getEventBriefing(modelId),
    {
      refreshInterval: 60000,
      shouldRetryOnError: false,
      keepPreviousData: false,
    }
  )
  const result = data && 'briefings' in data ? data : undefined
  const generate = async () => {
    if (!modelId || !enabled || flight.current) return
    flight.current = true
    setBusy(true)
    setError('')
    try {
      const response = await generateEventBriefing(modelId)
      await mutate(response, { revalidate: true })
    } catch (e) {
      setError(
        e instanceof Error ? e.message : text('简报生成失败', 'Briefing failed')
      )
    } finally {
      flight.current = false
      setBusy(false)
    }
  }
  useEffect(() => {
    const inputKey =
      result?.input_hash ?? (data && 'status' in data ? 'empty' : '')
    if (
      !autoLabel ||
      !modelId ||
      !enabled ||
      !inputKey ||
      autoTriggered.current === inputKey ||
      autoFailed.current === inputKey
    )
      return
    autoTriggered.current = inputKey
    if (!result || result.stale) {
      autoFailed.current = inputKey
      void generate()
    }
  }, [autoLabel, modelId, enabled, result?.input_hash, result?.stale, data])
  const evidence = new Map(events.map((e) => [e.id, e]))
  return (
    <section
      className="space-y-4 rounded-2xl border border-blue-200 bg-white p-5 shadow-sm"
      aria-label={text('币种聚合简报', 'Coin event briefings')}
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold">
            {text('币种聚合简报', 'Coin event briefings')}
          </h2>
          <p className="mt-2 max-w-3xl text-xs text-slate-500">
            {text(
              '一次整理当前关注币种的信息，按偏利好、偏利空、分歧与待确认分类。不直接发送全部原文；先去重筛选，最多 60 条证据。可能产生模型费用，与单条打标共用限额，不是交易信号。',
              'One action summarizes watched coins into favorable, unfavorable, mixed and unconfirmed groups. Deduplicated bounded input, up to 60 evidence items, not all full articles. Model charges may apply; shares labeling limits. Not trade signals.'
            )}
          </p>
        </div>
        <button
          type="button"
          onClick={() => void generate()}
          disabled={!modelId || !enabled || busy}
          className="rounded-xl bg-blue-600 px-4 py-2 text-sm font-medium text-white disabled:opacity-40"
        >
          {busy
            ? text('正在聚合分析…', 'Generating briefing…')
            : result
              ? text('更新关注币种简报', 'Update watched coin briefings')
              : text('一键生成关注币种简报', 'Generate watched coin briefings')}
        </button>
      </div>
      {!modelId && (
        <p className="text-sm text-slate-500">
          {text(
            '请先在上方选择已启用模型，再一键分析，无需逐条打标。',
            'Select an enabled model above to analyze in one action instead of labeling each item.'
          )}
        </p>
      )}
      {isLoading && (
        <p role="status" className="text-sm text-slate-500">
          {text('读取已保存简报…', 'Loading saved briefing…')}
        </p>
      )}
      {(error || loadError) && (
        <div role="alert" className="text-sm text-red-700">
          {error ||
            text(
              '简报读取失败，原始信息仍可查看。',
              'Could not load the briefing. Raw information remains available.'
            )}{' '}
          {loadError && (
            <button className="underline" onClick={() => void mutate()}>
              {text('重试读取', 'Retry loading')}
            </button>
          )}
        </div>
      )}
      {modelId && data && 'status' in data && (
        <p className="text-sm text-slate-500">
          {text(
            '尚无聚合简报。点击按钮后才会调用模型。',
            'No saved briefing. The model runs only after clicking the button.'
          )}
        </p>
      )}
      {result && (
        <>
          <p className="text-xs text-slate-500">
            {result.model_name} ·{' '}
            {new Date(result.analyzed_at).toLocaleString(
              language === 'zh' ? 'zh-CN' : 'en-US'
            )}{' '}
            ·{' '}
            {text(
              `分析 ${result.coverage.included_events} / ${result.coverage.total_events} 条，省略 ${result.coverage.omitted_events} 条`,
              `Analyzed ${result.coverage.included_events} / ${result.coverage.total_events} items; ${result.coverage.omitted_events} omitted`
            )}
          </p>
          {result.stale && (
            <p
              role="status"
              className="rounded-lg bg-amber-50 p-3 text-sm text-amber-900"
            >
              {text(
                '信息或模型配置已变化，以下是旧简报，仅供历史参考。点击更新才会重新分析。',
                'Information or model settings changed. This is an older briefing for historical reference. Click update to rerun.'
              )}
            </p>
          )}
          {!!result.warnings?.length && (
            <ul className="list-disc pl-5 text-xs text-amber-800">
              {result.warnings.map((w, i) => (
                <li key={i}>{w}</li>
              ))}
            </ul>
          )}
          <div className={result.stale ? 'space-y-4 opacity-60' : 'space-y-4'}>
            {result.briefings.map((briefing) => (
              <article
                key={briefing.coin}
                className="space-y-3 rounded-xl border border-slate-200 p-4"
              >
                <h3 className="text-lg font-semibold">{briefing.coin}</h3>
                <p className="text-sm leading-relaxed">{briefing.summary}</p>
                {stances.map((stance) => {
                  const groups = briefing.groups.filter(
                    (g) => g.stance === stance.id
                  )
                  if (!groups.length) return null
                  return (
                    <section
                      key={stance.id}
                      className={`space-y-3 rounded-lg border p-3 ${stance.color}`}
                    >
                      <h4 className="text-sm font-semibold">
                        {text(stance.zh, stance.en)}
                      </h4>
                      {groups.map((group, i) => (
                        <div key={i} className="space-y-2 text-sm">
                          <h5 className="font-medium">{group.title}</h5>
                          <p>{group.reason}</p>
                          {!!group.conditions.length && (
                            <p>
                              <strong>
                                {text('成立条件：', 'Conditions: ')}
                              </strong>
                              {group.conditions.join('；')}
                            </p>
                          )}
                          {!!group.limitations.length && (
                            <p className="text-xs text-slate-600">
                              <strong>{text('局限：', 'Limitations: ')}</strong>
                              {group.limitations.join('；')}
                            </p>
                          )}
                          <details className="text-xs">
                            <summary className="cursor-pointer text-blue-700">
                              {text('查看引用依据', 'View evidence')} (
                              {group.evidence_ids.length})
                            </summary>
                            <ul className="mt-2 space-y-2">
                              {group.evidence_ids.map((id) => {
                                const e = evidence.get(id)
                                const href = e ? safeURL(e.url) : undefined
                                return (
                                  <li key={id} className="break-all">
                                    {href ? (
                                      <a
                                        href={href}
                                        target="_blank"
                                        rel="noopener noreferrer"
                                        className="text-blue-700 underline"
                                      >
                                        {e?.title}
                                      </a>
                                    ) : (
                                      id
                                    )}
                                    {e && (
                                      <span className="ml-2 text-slate-500">
                                        {e.source}
                                        {e.publisher ? ` · ${e.publisher}` : ''}
                                      </span>
                                    )}
                                  </li>
                                )
                              })}
                            </ul>
                          </details>
                        </div>
                      ))}
                    </section>
                  )
                })}
                {!briefing.groups.length && (
                  <p className="text-sm text-slate-500">
                    {text(
                      '依据不足，未生成方向分类。',
                      'Insufficient evidence for directional classification.'
                    )}
                  </p>
                )}
                {!!briefing.watch_points.length && (
                  <div>
                    <h4 className="text-sm font-semibold">
                      {text('接下来关注', 'Watch next')}
                    </h4>
                    <ul className="mt-1 list-disc pl-5 text-sm">
                      {briefing.watch_points.map((p, i) => (
                        <li key={i}>{p}</li>
                      ))}
                    </ul>
                  </div>
                )}
              </article>
            ))}
          </div>
        </>
      )}
    </section>
  )
}
