import { useRef, useState } from 'react'
import { api } from '../../lib/api'
import type { AIModel } from '../../types'
import { useLanguage } from '../../contexts/LanguageContext'
import { AI_DRAWING_META, buildAIDrawingPrimitives, viewportKey, type AIDrawingKind, type AIChartDrawingResult, type ChartViewport, type DrawingPrimitive } from '../../utils/aiChartDrawings'
import { tr } from './marketUi'

const kinds = Object.keys(AI_DRAWING_META) as AIDrawingKind[]
interface Props {
  symbol: string; interval: string; models: AIModel[]; viewport: ChartViewport | null
  onDraw: (key: string, shapes: DrawingPrimitive[]) => void
}
export function AiDrawingPanel({ symbol, interval, models, viewport, onDraw }: Props) {
  const { language } = useLanguage()
  const [model, setModel] = useState('')
  const [enabled, setEnabled] = useState<AIDrawingKind[]>(kinds)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState<{ key: string; response: AIChartDrawingResult; shapes: DrawingPrimitive[]; snapshot: ChartViewport } | null>(null)
  const key = viewportKey(symbol, interval, viewport)
  const latestKey = useRef(key)
  latestKey.current = key
  const flight = useRef(false)
  const selectedModel = models.some(m => m.id === model) ? model : models[0]?.id ?? ''
  const stale = result && result.key !== key
  const analyze = async () => {
    if (flight.current || !viewport || !selectedModel || viewport.candles.length < 30 || viewport.candles.length > 500 || !enabled.length) return
    flight.current = true
    setBusy(true); setError('')
    const snapshot = structuredClone(viewport)
    const requestKey = key
    // Explicit request, no retries and no state from a previous model reply.
    try {
      const response = await api.analyzeChartDrawings({ symbol, interval, model_id: selectedModel, lang: language, candles: snapshot.candles, kinds: enabled })
      if (response.symbol !== symbol || response.interval !== interval || response.from !== snapshot.from || response.to !== snapshot.to ||
          typeof response.summary !== 'string' || !Array.isArray(response.drawings) || !Array.isArray(response.warnings) || response.warnings.some(w => typeof w !== 'string')) throw new Error('AI response does not match the requested window')
      if (response.drawings.some(d => !enabled.includes(d.kind))) throw new Error('AI returned a disabled drawing type')
      const shapes = buildAIDrawingPrimitives(response.drawings, snapshot.candles)
      setResult({ key: requestKey, response, shapes, snapshot })
      if (latestKey.current === requestKey) onDraw(requestKey, shapes.filter(s => enabled.includes(s.kind)))
    } catch (e) {
      setError(e instanceof Error ? e.message : 'AI analysis failed')
    } finally { setBusy(false); flight.current = false }
  }
  const toggle = (kind: AIDrawingKind) => {
    const next = enabled.includes(kind) ? enabled.filter(k => k !== kind) : [...enabled, kind]
    setEnabled(next)
    if (result?.key === key) onDraw(key, result.shapes.filter(s => next.includes(s.kind)))
  }
  return <section className="mt-2 rounded-lg border border-[rgba(25,36,52,0.12)] p-3 text-xs" aria-label={tr(language, 'AI 窗口划线', 'AI viewport drawings')}>
    <div className="flex flex-wrap items-center gap-2">
      <select aria-label={tr(language, '划线分析模型', 'Drawing analysis model')} value={selectedModel} onChange={e => setModel(e.target.value)} disabled={busy} className="rounded border bg-nofx-bg px-2 py-1">
        {!models.length && <option value="">{tr(language, '请先在配置中启用模型', 'Enable a model in settings first')}</option>}
        {models.map(m => <option key={m.id} value={m.id}>{m.name}</option>)}
      </select>
      <button type="button" onClick={analyze} disabled={busy || !selectedModel || !viewport || viewport.candles.length < 30 || viewport.candles.length > 500 || !enabled.length} className="rounded bg-nofx-gold/10 px-3 py-1 font-semibold text-nofx-gold disabled:opacity-40">
        {busy ? tr(language, 'AI 正在分析…', 'Analyzing…') : tr(language, 'AI 分析当前窗口', 'Analyze current window')}
      </button>
      <button type="button" onClick={() => { setResult(null); onDraw(key, []) }} disabled={busy} className="rounded border px-2 py-1">{tr(language, '清空划线', 'Clear drawings')}</button>
      <span className="text-nofx-text-muted">{viewport?.candles.length ?? 0} {tr(language, '根已收盘K线', 'closed bars')}</span>
    </div>
    <p className="mt-2 text-nofx-text-muted">{tr(language, '仅在点击时调用已配置模型，可能产生 API 费用。AI 选择锚点，程序校验并绘制；不是交易信号，不会下单。仅分析当前窗口，30–500 根K线，不足依据可不画。', 'Calls your configured model only on click; API charges may apply. AI selects anchors; code validates and draws. No orders or trade signals. Current window only, 30–500 bars; no structure may mean no drawings.')}</p>
    <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1">{kinds.map(kind => <label key={kind} className="flex items-center gap-1"><input type="checkbox" checked={enabled.includes(kind)} onChange={() => toggle(kind)} disabled={busy} />{AI_DRAWING_META[kind]}</label>)}</div>
    {error && <p role="alert" className="mt-2 text-red-600">{error}</p>}
    {stale && <p role="status" className="mt-2 text-amber-600">{tr(language, '窗口、币种或周期已变化，旧划线已隐藏，请重新分析。', 'Window, symbol or timeframe changed. Old drawings are hidden; analyze again.')}</p>}
    {result && !stale && <div className="mt-2 space-y-1">
      <p>{result.response.summary}</p>
      {!result.response.drawings.length && <p>{tr(language, 'AI 未找到足够可靠的结构，未生成划线。', 'No sufficiently supported structure was found; no drawings generated.')}</p>}
      {result.response.warnings.map((w, i) => <p key={i} className="text-amber-600">{w}</p>)}
      <details><summary className="cursor-pointer">{tr(language, '查看划线依据与失效条件', 'Drawing evidence and invalidation')} ({result.response.drawings.length})</summary>
        <ul className="mt-1 max-h-56 space-y-2 overflow-auto">{result.response.drawings.map((drawing, i) => <li key={i} className="border-t pt-1"><strong>{AI_DRAWING_META[drawing.kind]}</strong> · {drawing.reason}<br /><span className="text-nofx-text-muted">{tr(language, '失效条件：', 'Invalidation: ')}{drawing.invalidation}<br />{drawing.anchors.map(a => `${new Date(result.snapshot.candles[a.index].time * 1000).toLocaleString()} ${a.field}`).join(' → ')}</span></li>)}</ul>
      </details>
    </div>}
  </section>
}
