import { NoticeText } from '../NoticeText'
import { useState } from 'react'
import { Loader2, Sparkles } from 'lucide-react'
import { api } from '../../lib/api'
import type { MarketAnalysis, MarketAnalyzeResult } from '../../lib/api/market'
import type { AIModel } from '../../types'
import { BN } from './BinanceChart'
import { loadPref, savePref, tr, ui, timeAgo } from './marketUi'

interface Props {
  symbol: string
  interval: string
  models: AIModel[]
  language: string
  decimals: number
  result: MarketAnalyzeResult | null
  onResult: (r: MarketAnalyzeResult) => void
  onApply: (a: MarketAnalysis) => void
}

const signalStyle: Record<
  MarketAnalysis['signal'],
  { zh: string; en: string; color: string }
> = {
  long: { zh: '做多', en: 'Long', color: BN.up },
  short: { zh: '做空', en: 'Short', color: BN.down },
  wait: { zh: '观望', en: 'Wait', color: BN.text },
}

export function AiPanel({
  symbol,
  interval,
  models,
  language,
  decimals,
  result,
  onResult,
  onApply,
}: Props) {
  const [modelId, setModelId] = useState(() => loadPref('model', ''))
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const selected = models.find((m) => m.id === modelId)
    ? modelId
    : models[0]?.id || ''
  const a = result?.analysis

  const run = async () => {
    if (!selected) return
    setLoading(true)
    setError('')
    try {
      onResult(
        await api.analyzeMarket({
          symbol,
          interval,
          model_id: selected,
          lang: language,
        })
      )
    } catch (err) {
      setError(
        err instanceof Error
          ? err.message
          : tr(language, 'AI 分析失败', 'AI analysis failed')
      )
    } finally {
      setLoading(false)
    }
  }

  const price = (v: number) => (v > 0 ? v.toFixed(decimals) : '--')

  return (
    <section
      className={ui.panel}
      aria-label={tr(language, 'AI 分析', 'AI analysis')}
    >
      <div className={ui.title}>{tr(language, 'AI 分析', 'AI analysis')}</div>
      {models.length === 0 ? (
        <p className={ui.muted}>
          {tr(
            language,
            '还没有可用的 AI。请先到「配置 → 连接」添加一个 AI 模型。',
            'No AI yet. Add one under Configuration → Connections.'
          )}
        </p>
      ) : (
        <>
          <label className={ui.label} htmlFor="market-ai-model">
            {tr(language, '使用的 AI', 'AI model')}
          </label>
          <select
            id="market-ai-model"
            className={ui.input}
            value={selected}
            onChange={(e) => {
              setModelId(e.target.value)
              savePref('model', e.target.value)
            }}
          >
            {models.map((m) => (
              <option key={m.id} value={m.id}>
                {m.name}
                {m.customModelName ? ` · ${m.customModelName}` : ''}
              </option>
            ))}
          </select>
          <button
            type="button"
            className={`${ui.btnGold} mt-2 inline-flex items-center justify-center gap-2`}
            onClick={run}
            disabled={loading}
          >
            {loading ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Sparkles className="h-4 w-4" />
            )}
            {loading
              ? tr(
                  language,
                  'AI 正在分析，约 10–60 秒…',
                  'Analyzing, about 10–60 s…'
                )
              : tr(
                  language,
                  `分析 ${symbol} · ${interval}`,
                  `Analyze ${symbol} · ${interval}`
                )}
          </button>
        </>
      )}
      {error ? (
        <p role="alert" className="mt-2 text-xs" style={{ color: BN.down }}>
          <NoticeText text={error} />
        </p>
      ) : null}

      {result && result.symbol === symbol ? (
        <div className="mt-3 space-y-2 text-sm" aria-live="polite">
          {a ? (
            <>
              <div className="flex items-center justify-between">
                <span
                  className="rounded px-2 py-0.5 text-sm font-semibold"
                  style={{
                    color: signalStyle[a.signal].color,
                    background: signalStyle[a.signal].color + '22',
                  }}
                >
                  {tr(
                    language,
                    signalStyle[a.signal].zh,
                    signalStyle[a.signal].en
                  )}
                </span>
                <span className={ui.muted}>
                  {tr(language, '把握度', 'Confidence')}{' '}
                  <span className={ui.text}>{a.confidence}%</span>
                </span>
              </div>
              <p className={ui.text}>{a.summary}</p>
              {a.signal !== 'wait' ? (
                <div className="grid grid-cols-2 gap-x-3 gap-y-1 text-xs">
                  <span className={ui.muted}>
                    {tr(language, '入场', 'Entry')}{' '}
                    <span className={ui.text}>{price(a.entry)}</span>
                  </span>
                  <span className={ui.muted}>
                    {tr(language, '杠杆', 'Leverage')}{' '}
                    <span className={ui.text}>{a.leverage}x</span>
                  </span>
                  <span className={ui.muted}>
                    {tr(language, '止损', 'Stop')}{' '}
                    <span style={{ color: BN.down }}>{price(a.stop_loss)}</span>
                  </span>
                  <span className={ui.muted}>
                    {tr(language, '止盈', 'Target')}{' '}
                    <span style={{ color: BN.up }}>{price(a.take_profit)}</span>
                  </span>
                </div>
              ) : null}
              {a.reasons?.length ? (
                <ul className="list-disc space-y-0.5 pl-4 text-xs text-nofx-text">
                  {a.reasons.map((r) => (
                    <li key={r}>{r}</li>
                  ))}
                </ul>
              ) : null}
              {a.risks?.length ? (
                <ul
                  className="list-disc space-y-0.5 pl-4 text-xs"
                  style={{ color: ui.warn }}
                >
                  {a.risks.map((r) => (
                    <li key={r}>{r}</li>
                  ))}
                </ul>
              ) : null}
              {a.signal !== 'wait' ? (
                <button
                  type="button"
                  className={ui.btnGhost}
                  onClick={() => onApply(a)}
                >
                  {tr(language, '按此建议填入下单', 'Fill the order form')}
                </button>
              ) : null}
            </>
          ) : (
            <>
              <p className="text-xs" style={{ color: ui.warn }}>
                {result.parse_error}
              </p>
              <pre className="max-h-60 overflow-auto whitespace-pre-wrap rounded bg-nofx-bg-deeper p-2 text-xs text-nofx-text">
                {result.raw}
              </pre>
            </>
          )}
          <p className={ui.muted}>
            {tr(language, '分析于', 'Analyzed')}{' '}
            {new Date(result.analyzed_at * 1000).toLocaleString()}（
            {timeAgo(language, result.analyzed_at)}）· {result.interval}{' '}
            {tr(language, '周期', 'chart')} ·{' '}
            {tr(language, '当时价格', 'price then')}{' '}
            {result.price.toFixed(decimals)}
          </p>
          {result.save_error ? (
            <p role="alert" className="text-xs" style={{ color: ui.warn }}>
              {tr(
                language,
                '这条分析没能保存到服务器，刷新后会消失。',
                result.save_error
              )}
            </p>
          ) : null}
          {result.interval !== interval ? (
            <p className="text-xs" style={{ color: ui.warn }}>
              {tr(
                language,
                `这条分析基于 ${result.interval} 周期，当前图表是 ${interval}。`,
                `This analysis used the ${result.interval} chart; you are viewing ${interval}.`
              )}
            </p>
          ) : null}
          <p className={ui.muted}>
            {tr(
              language,
              'AI 结论仅供参考，不构成投资建议。行情变化后请重新分析。',
              'For reference only, not financial advice. Re-analyze after the market moves.'
            )}
          </p>
        </div>
      ) : null}
    </section>
  )
}
