import { NoticeText } from '../NoticeText'
import { useCallback, useEffect, useMemo, useState } from 'react'
import useSWR from 'swr'
import { LOCAL_SNAPSHOT_OPTIONS } from '../../lib/liveSnapshot'
import { api } from '../../lib/api'
import type {
  CoinScoreBrief,
  CoinScoreResult,
  CoinStatusRow,
  HitRate,
  SideSummary,
  SymbolSummary,
} from '../../lib/api/copyTrade'
import type { MarketKline } from '../../lib/api/market'
import { BinanceChart, BN, type PriceMarker } from '../market/BinanceChart'
import { tr, ui } from '../market/marketUi'
import { priceDecimals } from '../../utils/marketIndicators'
import { buildMarks, recentlyClosed } from './chartMarks'
import { EntryOverlay } from './EntryOverlay'
import {
  ConfidenceBadge,
  explainConfidence,
  HealthTag,
  HitRatePanel,
} from './ConfidenceUi'
import { CrowdingTag, RankBadge, RankPanel } from './RankUi'
import {
  ago,
  base,
  clock,
  price,
  sideClass,
  sideLabel,
  signedUsd,
  styleHint,
  styleLabel,
  usd,
} from './format'

const intervals = ['15m', '1h', '4h', '1d']

function sideHealth(language: string, s: SideSummary): string {
  const parts = []
  if (s.trapped)
    parts.push(tr(language, `${s.trapped} 人被套`, `${s.trapped} trapped`))
  if (s.fresh) parts.push(tr(language, `${s.fresh} 人新开`, `${s.fresh} new`))
  if (s.openers)
    parts.push(
      tr(language, `${s.openers} 人有效新开`, `${s.openers} fresh calls`)
    )
  return parts.length ? `（${parts.join('，')}）` : ''
}

export type CoinSort =
  | 'rank'
  | 'confidence'
  | 'ai'
  | 'stable'
  | 'traders'
  | 'long'
  | 'short'
  | 'value'
  | 'consensus'
export type CoinBias = 'all' | 'long' | 'short'

const traderCount = (s: SymbolSummary) => s.long.traders + s.short.traders
/** 1 = everyone on one side, 0 = evenly split. */
const consensus = (s: SymbolSummary) =>
  Math.abs(s.long.traders - s.short.traders) / Math.max(1, traderCount(s))

export function sortCoins(
  list: SymbolSummary[],
  sort: CoinSort,
  bias: CoinBias,
  aiScore: (symbol: string) => number = () => 0
): SymbolSummary[] {
  const key: Record<CoinSort, (s: SymbolSummary) => number> = {
    rank: (s) => s.rank?.score || 0,
    stable: (s) => s.stable_direction?.score || 0,
    confidence: (s) => s.confidence?.score || 0,
    ai: (s) => aiScore(s.symbol),
    traders: traderCount,
    long: (s) => s.long.traders,
    short: (s) => s.short.traders,
    value: (s) => s.long.value + s.short.value,
    consensus,
  }
  return list
    .filter(
      (s) =>
        bias === 'all' ||
        (bias === 'long'
          ? s.long.traders > s.short.traders
          : s.short.traders > s.long.traders)
    )
    .sort(
      (a, b) =>
        key[sort](b) - key[sort](a) ||
        traderCount(b) - traderCount(a) ||
        b.long.value + b.short.value - (a.long.value + a.short.value)
    )
}

function AiTag({ b, language }: { b?: CoinScoreBrief; language: string }) {
  if (!b) {
    return (
      <span className="text-[11px] text-nofx-text-muted">
        {tr(language, 'AI —', 'AI —')}
      </span>
    )
  }
  const stale = Boolean(b.stale)
  const color = stale
    ? 'text-nofx-text-muted'
    : b.bias === 'long'
      ? 'text-nofx-success'
      : b.bias === 'short'
        ? 'text-nofx-danger'
        : 'text-nofx-text-muted'
  const arrow = b.bias === 'long' ? '▲' : b.bias === 'short' ? '▼' : '•'
  const biasText =
    b.bias === 'long'
      ? tr(language, '看多', 'Long')
      : b.bias === 'short'
        ? tr(language, '看空', 'Short')
        : tr(language, '中性', 'Neutral')
  const sideTag =
    b.bias === 'long'
      ? tr(language, '多', 'L')
      : b.bias === 'short'
        ? tr(language, '空', 'S')
        : tr(language, '中', 'N')
  return (
    <span
      className={`text-[11px] tabular-nums ${color}${stale ? ' opacity-70' : ''}`}
      aria-label={`${biasText} bull ${Math.round(b.bull_score)} bear ${Math.round(b.bear_score)}`}
      title={`${biasText} · bull ${Math.round(b.bull_score)} / bear ${Math.round(b.bear_score)} · ${tr(language, '置信', 'conf')} ${Math.round(b.conviction)}${
        b.horizon ? ' · ' + b.horizon : ''
      } · ${ago(language, b.analyzed_at)}`}
    >
      AI {sideTag} {Math.round(b.score)} {arrow}
      {stale ? (
        <span className="ml-1 text-nofx-gold">
          {tr(language, '过期', 'stale')}
        </span>
      ) : null}
    </span>
  )
}

function reliabilityGrade(s: SymbolSummary): string {
  const sd = s.stable_direction
  if (!sd) return 'D'
  const samples = sd.ai_samples || 0
  const hit = (sd.ai_accuracy || 0) * 100
  const cons = (sd.accuracy || 0) * 100
  if (samples >= 30 && hit >= 55 && sd.fresh && cons >= 55) return 'A'
  if (samples >= 10 && hit >= 50) return 'B'
  if (samples >= 4) return 'C'
  return 'D'
}

/** AI cell: never shows "AI 0/—" for coins filtered out of AI scope. */
function CoinScoreCell({
  s,
  status,
  b,
  language,
}: {
  s: SymbolSummary
  status?: CoinStatusRow
  b?: CoinScoreBrief
  language: string
}) {
  if (status && status.status === 'out_of_scope') {
    return (
      <span className="text-[11px] text-nofx-text-muted" title={tr(language, '未通过成交量筛选，不参与 AI 评分（不是 AI 低分）', 'Filtered out by volume, not AI-scored (not a low score)')}>
        {tr(language, '未进入AI评分范围', 'Not AI-scored')}
        <span className="ml-1 opacity-80">
          · 24h {usd(status.volume_24h)} / {tr(language, '门槛', 'min')} {usd(status.min_volume)}
        </span>
      </span>
    )
  }
  if (status && status.status === 'waiting') {
    return <span className="text-[11px] text-nofx-text-muted">{tr(language, 'AI 待评分', 'AI pending')}</span>
  }
  if (status && status.status === 'scoring') {
    return <span className="text-[11px] text-nofx-gold">{tr(language, 'AI 评分中…', 'AI scoring…')}</span>
  }
  if (status && status.status === 'failed') {
    return <span className="text-[11px] text-nofx-danger">{tr(language, 'AI 评分失败', 'AI failed')}</span>
  }
  const grade = reliabilityGrade(s)
  const gcolor = grade === 'A' ? 'text-nofx-success' : grade === 'B' ? 'text-nofx-text' : 'text-nofx-text-muted'
  return (
    <span className="inline-flex items-center gap-1">
      <AiTag b={b} language={language} />
      {b ? (
        <span className={`text-[10px] ${gcolor}`} title={tr(language, '可靠度', 'Reliability')}>
          {tr(language, '可靠', 'Rel')}{grade}
        </span>
      ) : null}
    </span>
  )
}

function Bar({ s }: { s: SymbolSummary }) {
  const total = s.long.value + s.short.value || 1
  return (
    <div className="flex h-1.5 w-full overflow-hidden rounded bg-nofx-bg-deeper">
      <div
        className="bg-nofx-success"
        style={{ width: `${(s.long.value / total) * 100}%` }}
      />
      <div
        className="bg-nofx-danger"
        style={{ width: `${(s.short.value / total) * 100}%` }}
      />
    </div>
  )
}


/** AI reading for one coin: direction + 0-100 score + reasons/risks. Read-only. */
function CoinAiScore({
  language,
  symbol,
}: {
  language: string
  symbol: string
}) {
  const { data: saved, mutate } = useSWR(
    ['copytrade/coin-score', symbol],
    () => api.getCoinScore(symbol),
    { revalidateOnFocus: false }
  )
  const { data: modelInfo } = useSWR(
    'copytrade/coin-score/models',
    () => api.getCoinScoreModels(),
    { revalidateOnFocus: false }
  )
  const [result, setResult] = useState<CoinScoreResult | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [modelID, setModelID] = useState('')
  useEffect(() => {
    if (modelInfo && !modelID)
      setModelID(modelInfo.selected || modelInfo.default || '')
  }, [modelInfo, modelID])
  useEffect(() => {
    setResult(null)
    setErr('')
  }, [symbol])

  const savedResult =
    saved && 'analyzed_at' in (saved as object)
      ? (saved as CoinScoreResult)
      : null
  const shown = result || savedResult
  const a = shown?.analysis
  const biasColor =
    a?.bias === 'long'
      ? 'text-nofx-success'
      : a?.bias === 'short'
        ? 'text-nofx-danger'
        : 'text-nofx-text-muted'
  const biasLabel = (b?: string) =>
    b === 'long'
      ? tr(language, '看多', 'Long')
      : b === 'short'
        ? tr(language, '看空', 'Short')
        : tr(language, '中性/观望', 'Neutral')
  const hasModels = Boolean(modelInfo?.models?.length)

  const run = async () => {
    setBusy(true)
    setErr('')
    try {
      const r = await api.analyzeCoinScore(symbol, modelID || undefined, language)
      setResult(r)
      void mutate()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="mb-2 rounded bg-nofx-bg-deeper px-2 py-1.5 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium text-nofx-text">
          {tr(language, 'AI 研判', 'AI read')}
        </span>
        {a ? (
          <>
            <span className={`font-semibold ${biasColor}`}>
              {biasLabel(a.bias)}
            </span>
            <span className="tabular-nums text-nofx-text">
              {tr(language, '多', 'B')} {Math.round(a.bull_score ?? a.score)}
              {' · '}
              {tr(language, '空', 'S')} {Math.round(a.bear_score ?? 0)}
            </span>
            {a.horizon ? (
              <span className={ui.muted}>
                {a.horizon === 'short'
                  ? tr(language, '短线', 'Short')
                  : tr(language, '波段', 'Swing')}
              </span>
            ) : null}
            <span className={ui.muted}>
              {tr(
                language,
                `强度 ${Math.round(a.conviction ?? a.confidence)}`,
                `conv ${Math.round(a.conviction ?? a.confidence)}`
              )}
            </span>
            {a.changed ? (
              <span className="text-nofx-gold" title={a.change_reason || ''}>
                {tr(language, '已修订', 'revised')}
              </span>
            ) : null}
          </>
        ) : (
          <span className={ui.muted}>
            {tr(language, '尚未分析', 'not analyzed yet')}
          </span>
        )}
        {shown?.analyzed_at ? (
          <span className={ui.muted}>· {ago(language, shown.analyzed_at)}</span>
        ) : null}
        {hasModels ? (
          <select
            value={modelID}
            onChange={(e) => setModelID(e.target.value)}
            className={ui.inputInline}
            aria-label={tr(language, '评分模型', 'Scoring model')}
          >
            {modelInfo!.models.map((m) => (
              <option key={m.id} value={m.id}>
                {m.name}
              </option>
            ))}
          </select>
        ) : null}
        <button
          type="button"
          className={`${ui.btnGhost} ml-auto`}
          disabled={busy || !hasModels}
          onClick={() => void run()}
        >
          {busy
            ? tr(language, '分析中…', 'Analyzing…')
            : tr(language, 'AI 分析', 'Analyze')}
        </button>
      </div>
      {shown?.parse_error ? (
        <p role="alert" className="mt-1 text-nofx-gold">
          <NoticeText text={shown.parse_error} />
        </p>
      ) : null}
      {err ? (
        <p role="alert" className="mt-1 text-nofx-danger">
          <NoticeText text={err} />
        </p>
      ) : null}
      {a ? (
        <div className="mt-1 grid gap-1 md:grid-cols-2">
          <div>
            {a.reasons?.length ? (
              <>
                <div className="font-medium text-nofx-text">
                  {tr(language, '依据', 'Reasons')}
                </div>
                <ul className="list-disc pl-4 text-nofx-text">
                  {a.reasons.map((x, i) => (
                    <li key={i}>{x}</li>
                  ))}
                </ul>
              </>
            ) : null}
          </div>
          <div>
            {a.risks?.length ? (
              <>
                <div className="font-medium text-nofx-text">
                  {tr(language, '风险', 'Risks')}
                </div>
                <ul className="list-disc pl-4 text-nofx-danger">
                  {a.risks.map((x, i) => (
                    <li key={i}>{x}</li>
                  ))}
                </ul>
              </>
            ) : null}
            {a.invalidates ? (
              <div className="mt-1 text-nofx-text-muted">
                {tr(language, '失效条件：', 'Invalidates: ')}
                {a.invalidates}
              </div>
            ) : null}
            <div className="mt-1 text-nofx-text-muted">
              {tr(
                language,
                '仅供参考，不构成下单指令。',
                'Reference only, not an order instruction.'
              )}
            </div>
            <div className="mt-0.5 text-nofx-text-muted">
              {tr(
                language,
                '使用 15m/1h/4h 多周期研判，与上方周期切换无关。',
                'Uses 15m/1h/4h multi-timeframe; independent of the interval buttons above.'
              )}
            </div>
          </div>
        </div>
      ) : null}
      {!hasModels ? (
        <p className="mt-1 text-nofx-gold">
          {tr(
            language,
            '还没有可用的模型，请先在设置里启用一个模型。',
            'No enabled AI model; enable one in settings first.'
          )}
        </p>
      ) : null}
    </div>
  )
}

function CoinDetail({
  language,
  symbol,
}: {
  language: string
  symbol: string
}) {
  const [interval, setChartInterval] = useState('1h')
  const [candles, setCandles] = useState<MarketKline[]>([])
  const [chartError, setChartError] = useState('')
  const { data: detail } = useSWR(
    ['copytrade/symbol', symbol],
    () => api.getCopyTradeSymbol(symbol),
    LOCAL_SNAPSHOT_OPTIONS
  )
  const holders = detail?.holders || []
  const summary = detail?.summary ?? null
  // The measured record of this score's band, for the expected excess. The
  // same SWR key as the hit-rate panel, so the page fetches it once.
  const { data: hitRate } = useSWR(
    'copytrade/hit-rate:30',
    () => api.getHitRate(30),
    // The 30-day hit rate reads the signal table; it does not need 4s polling.
    { refreshInterval: 60000, revalidateOnFocus: false }
  )
  const expected = useMemo(() => {
    const r = summary?.rank
    if (!r || r.direction === 'neutral') return null
    const band: HitRate['band'] =
      r.score >= 70 ? 'high' : r.score >= 40 ? 'mid' : 'low'
    const hit = (hitRate?.rows || []).find(
      (x) =>
        (x.group ?? 'confidence') === 'rank' &&
        x.horizon === '24h' &&
        x.band === band &&
        (x.direction ?? 'all') === r.direction
    )
    if (!hit) return null
    const samples = hit.excess_samples || 0
    // Below 30 calls a band's average is mostly noise; say so instead.
    if (samples < 30) return { samples }
    return { samples, excess: (hit.avg_excess || 0) - r.cost_pct / 100 }
  }, [hitRate, summary])
  const events = detail?.events || []
  const traders = detail?.traders || {}
  const [showAvg, setShowAvg] = useState(false)
  const [openKey, setOpenKey] = useState('')

  // A click anywhere outside an avatar card closes it.
  useEffect(() => {
    if (!openKey) return
    const close = () => setOpenKey('')
    document.addEventListener('click', close)
    return () => document.removeEventListener('click', close)
  }, [openKey])
  useEffect(() => setOpenKey(''), [symbol, interval])

  useEffect(() => {
    let cancelled = false
    setCandles([])
    const load = () =>
      api
        .getMarketKlines(symbol, interval, 300)
        .then((k) => {
          if (!cancelled) {
            setCandles(k)
            setChartError('')
          }
        })
        .catch(
          (e) =>
            !cancelled &&
            setChartError(e instanceof Error ? e.message : String(e))
        )
    void load()
    // Keep the chart live, but do not poll while the tab is hidden.
    const timer = setInterval(() => {
      if (!document.hidden) void load()
    }, 12000)
    const onVis = () => {
      if (!document.hidden) void load()
    }
    document.addEventListener('visibilitychange', onVis)
    return () => {
      cancelled = true
      clearInterval(timer)
      document.removeEventListener('visibilitychange', onVis)
    }
  }, [symbol, interval])

  const last = candles[candles.length - 1]?.close || 0
  // The newest candle's time stands for now (pure, and current enough).
  const lastTime = candles[candles.length - 1]?.time || 0
  const closedRecently = useMemo(
    () =>
      lastTime ? recentlyClosed(events, holders, (lastTime + 3600) * 1000) : [],
    [events, holders, lastTime]
  )
  const marks = useMemo(
    () =>
      buildMarks(
        events,
        holders,
        candles.map((c) => c.time)
      ),
    [events, holders, candles]
  )
  const openMark = marks.entries.find((m) => m.key === openKey)
  const markers = useMemo<PriceMarker[]>(() => {
    const lines: PriceMarker[] = []
    // The clicked avatar's entry on the price axis; the overlay draws the line.
    if (openMark)
      lines.push({
        price: openMark.price,
        color: openMark.side === 'long' ? BN.up : BN.down,
        title: tr(language, '入场', 'Entry'),
        labelOnly: true,
      })
    if (!showAvg) return lines
    if (summary?.long.avg_entry)
      lines.push({
        price: summary.long.avg_entry,
        color: BN.line1,
        width: 2,
        title: tr(language, '多头均价', 'Long avg'),
      })
    if (summary?.short.avg_entry)
      lines.push({
        price: summary.short.avg_entry,
        color: BN.line2,
        width: 2,
        title: tr(language, '空头均价', 'Short avg'),
      })
    return lines
  }, [showAvg, summary, language, openMark])

  return (
    <div className="space-y-3">
      <section className={ui.panel}>
        <div className="mb-2 flex flex-wrap items-center gap-3">
          <h2 className="text-lg font-semibold text-nofx-text">
            {base(symbol)}
          </h2>
          <span className="tabular-nums text-nofx-text">{price(last)}</span>
          {summary ? (
            <ConfidenceBadge c={summary.confidence} language={language} />
          ) : null}
          {summary ? (
            <span className="flex items-center gap-1">
              <RankBadge r={summary.rank} language={language} />
              <CrowdingTag r={summary.rank} language={language} />
            </span>
          ) : null}
          {summary?.stable_direction ? (
            <span className={`rounded px-2 py-1 text-xs tabular-nums ${summary.stable_direction.direction === 'long' ? 'bg-nofx-success/10 text-nofx-success' : summary.stable_direction.direction === 'short' ? 'bg-nofx-danger/10 text-nofx-danger' : 'bg-nofx-bg-deeper text-nofx-text-muted'}`} title={tr(language, `4h 稳定分 ${summary.stable_direction.score.toFixed(1)} · 校准准确率 ${Math.round(summary.stable_direction.accuracy * 100)}% · AI ${summary.stable_direction.ai_samples} 样本 · 带单 ${summary.stable_direction.trader_samples} 样本 · 风险 ${Math.round(summary.stable_direction.risk * 100)}%`, `4h stable score ${summary.stable_direction.score.toFixed(1)} · calibrated accuracy ${Math.round(summary.stable_direction.accuracy * 100)}% · AI ${summary.stable_direction.ai_samples} samples · traders ${summary.stable_direction.trader_samples} samples · risk ${Math.round(summary.stable_direction.risk * 100)}%`)}>
              4h {summary.stable_direction.direction === 'long' ? tr(language, '偏多', 'Long') : summary.stable_direction.direction === 'short' ? tr(language, '偏空', 'Short') : tr(language, '观望', 'Neutral')} · {summary.stable_direction.score.toFixed(0)}
              {!summary.stable_direction.eligible ? ` · ${tr(language, '低样本', 'low sample')}` : ''}
            </span>
          ) : null}
          {summary ? (
            <span className={ui.muted}>
              <span className="text-nofx-success">
                {tr(
                  language,
                  `做多 ${summary.long.traders} 人持仓中`,
                  `${summary.long.traders} holding long`
                )}{' '}
                · {usd(summary.long.value)}
                {summary.long.avg_entry
                  ? ` · ${tr(language, '均价', 'avg')} ${price(summary.long.avg_entry)}`
                  : ''}
                {sideHealth(language, summary.long)}
              </span>
              {'　'}
              <span className="text-nofx-danger">
                {tr(
                  language,
                  `做空 ${summary.short.traders} 人持仓中`,
                  `${summary.short.traders} holding short`
                )}{' '}
                · {usd(summary.short.value)}
                {summary.short.avg_entry
                  ? ` · ${tr(language, '均价', 'avg')} ${price(summary.short.avg_entry)}`
                  : ''}
                {sideHealth(language, summary.short)}
              </span>
              {summary.liquidated ? (
                <span
                  className="ml-2 text-nofx-text-muted"
                  title={tr(
                    language,
                    '推算的仓位亏损已超过他的账户余额，应该已经被强平，但成交记录里看不出强平，所以单独列出，不算进多空和置信度。',
                    'The inferred loss exceeds the trader’s balance, so the position was most likely liquidated; trade history does not mark liquidations. Left out of the counts and confidence.'
                  )}
                >
                  {tr(
                    language,
                    `· ${summary.liquidated} 人可能已被强平`,
                    `· ${summary.liquidated} likely liquidated`
                  )}
                </span>
              ) : null}
              {closedRecently.length ? (
                <span
                  className="ml-2 text-nofx-text-muted"
                  title={closedRecently
                    .map(
                      (e) =>
                        `${e.nickname} ${clock(e.time)} ${e.side === 'long' ? tr(language, '平多', 'closed long') : tr(language, '平空', 'closed short')}${e.realized_pnl ? ' ' + signedUsd(e.realized_pnl) : ''}`
                    )
                    .join('\n')}
                >
                  {tr(
                    language,
                    `· 另有 ${closedRecently.length} 人近 24 小时已平仓`,
                    `· ${closedRecently.length} more closed in the last 24h`
                  )}
                </span>
              ) : null}
            </span>
          ) : null}
          <div className="ml-auto flex gap-1">
            {intervals.map((iv) => (
              <button
                key={iv}
                type="button"
                aria-pressed={iv === interval}
                className={`${ui.btnGhost} ${iv === interval ? 'border-nofx-gold text-nofx-gold' : ''}`}
                onClick={() => setChartInterval(iv)}
              >
                {iv}
              </button>
            ))}
          </div>
        </div>
        {summary && explainConfidence(language, summary) ? (
          <p className="mb-2 rounded bg-nofx-bg-deeper px-2 py-1 text-xs text-nofx-text">
            {tr(language, '置信度：', 'Confidence: ')}
            {explainConfidence(language, summary)}
            {tr(
              language,
              '。置信度表示关注的人看法有多一致、记录有多可信，不是胜率。',
              '. It measures how much followed traders agree and how trustworthy they are, not a win probability.'
            )}
          </p>
        ) : null}
        {summary && summary.rank && summary.rank.direction !== 'neutral' ? (
          <div className="mb-2 rounded bg-nofx-bg-deeper px-2 py-1">
            <RankPanel
              r={summary.rank}
              expected={expected}
              language={language}
            />
          </div>
        ) : null}
        {summary?.stable_direction ? (
          <div className="mb-2 rounded border border-nofx-border bg-nofx-bg-deeper px-3 py-2 text-xs">
            <div className="font-semibold text-nofx-text">{tr(language, '4 小时稳定方向排名', '4-hour stable direction ranking')}</div>
            <div className="mt-1 text-nofx-text-muted">{tr(language, `风格带单方向：${summary.style_reading?.direction || 'neutral'} · 有效交易员 ${summary.style_reading?.traders || 0}`, `Style-adjusted trader direction: ${summary.style_reading?.direction || 'neutral'} · effective traders ${summary.style_reading?.traders || 0}`)}</div>
            <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-nofx-text-muted">
              <span>{tr(language, '方向', 'Direction')}: {summary.stable_direction.direction}</span>
              <span>{tr(language, '稳定分', 'Stable score')}: {summary.stable_direction.score.toFixed(1)}</span>
              <span>{tr(language, '校准命中', 'Calibrated hit rate')}: {Math.round(summary.stable_direction.accuracy * 100)}%</span>
              <span>{tr(language, 'AI 样本', 'AI samples')}: {summary.stable_direction.ai_samples}</span>
              <span>{tr(language, '带单样本', 'Trader samples')}: {summary.stable_direction.trader_samples}</span>
              <span>{tr(language, '风险折扣', 'Risk adjustment')}: {Math.round(summary.stable_direction.risk * 100)}%</span>
              {!summary.stable_direction.eligible ? <span className="text-nofx-gold">{tr(language, '样本不足，仅供观察', 'Insufficient samples; observe only')}</span> : null}
            </div>
          </div>
        ) : null}
        <CoinAiScore language={language} symbol={symbol} />
        <p className={`${ui.muted} mb-2`}>
          {tr(
            language,
            '头像标在带单员开仓、加仓的那根 K 线和价格上：绿框做多，红框做空，半透明灰框表示这笔仓位已经平掉，点头像看入场价和收益。小三角是减仓或平仓（绿色赚、红色亏）。虚线框头像在最左边，表示入场早于图上的时间，换大周期可以看到。',
            'Avatars sit on the candle and price where a trader opened or added: green border long, red short, faded grey once that position is closed. Click one for the entry and results. Small triangles are reductions and closes (green profit, red loss). Dashed avatars at the left entered before the chart; switch to a longer interval.'
          )}
        </p>
        <label className="mb-2 flex items-center gap-1.5 text-xs text-nofx-text">
          <input
            type="checkbox"
            checked={showAvg}
            onChange={(e) => setShowAvg(e.target.checked)}
          />
          {tr(
            language,
            '显示多空平均开仓价（橙色做多、紫色做空）',
            'Show long/short average entries (orange long, purple short)'
          )}
        </label>
        {chartError ? (
          <p
            role="alert"
            className="py-10 text-center text-sm text-nofx-danger"
          >
            <NoticeText text={chartError} language={language} />
          </p>
        ) : (
          <BinanceChart
            candles={candles}
            decimals={priceDecimals(last || 1)}
            main="NONE"
            subs={['VOL']}
            markers={markers}
            overlay={(p) => (
              <EntryOverlay
                p={p}
                entries={marks.entries}
                exits={marks.exits}
                events={events}
                traders={traders}
                lastPrice={last}
                language={language}
                openKey={openKey}
                onOpen={setOpenKey}
              />
            )}
            resetKey={`${symbol}:${interval}`}
            height={560}
          />
        )}
      </section>

      <section className={ui.panel}>
        <h3 className={ui.title}>
          {tr(language, '持有这个币的带单员', 'Followed traders holding it')}
        </h3>
        {holders.length ? (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[820px] text-sm">
              <thead className="text-left text-xs text-nofx-text-muted">
                <tr>
                  <th className="py-1.5 font-normal">
                    {tr(language, '带单员', 'Trader')}
                  </th>
                  <th className="font-normal">
                    {tr(language, '方向', 'Side')}
                  </th>
                  <th className="font-normal">
                    {tr(language, '开仓价', 'Entry')}
                  </th>
                  <th className="font-normal">
                    {tr(language, '仓位价值', 'Value')}
                  </th>
                  <th className="font-normal">
                    {tr(language, '杠杆', 'Lev.')}
                  </th>
                  <th className="font-normal">
                    {tr(language, '保证金', 'Margin')}
                  </th>
                  <th className="font-normal">
                    {tr(language, '浮盈亏', 'uPnL')}
                  </th>
                  <th className="font-normal">
                    {tr(language, '状态', 'State')}
                  </th>
                  <th className="font-normal">
                    {tr(language, '入场时间', 'Opened')}
                  </th>
                  <th className="font-normal">
                    {tr(language, '来源', 'Source')}
                  </th>
                </tr>
              </thead>
              <tbody className="text-nofx-text">
                {holders.map((h, index) => {
                  // Unpriced inferred positions: estimate from the last candle.
                  const upnl =
                    h.unrealized_pnl ||
                    (last
                      ? (h.side === 'long' ? 1 : -1) *
                        (last - h.entry_price) *
                        h.qty
                      : 0)
                  return (
                    <tr
                      key={`${h.portfolio_id}:${h.side}:${index}`}
                      className={`border-t ${ui.border} ${h.liquidated ? 'opacity-50' : ''}`}
                    >
                      <td className="py-1.5">
                        <a
                          href={`https://www.binance.com/${language === 'zh' ? 'zh-CN' : 'en'}/copy-trading/lead-details/${encodeURIComponent(h.portfolio_id)}`}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="hover:text-nofx-gold"
                          title={tr(
                            language,
                            '查看币安带单主页',
                            'View Binance lead portfolio'
                          )}
                        >
                          {h.nickname || h.portfolio_id}
                        </a>
                        {h.high_freq ? (
                          <span
                            className="ml-1 rounded bg-nofx-bg-deeper px-1 text-[10px] text-nofx-text-muted"
                            title={tr(
                              language,
                              '高频交易员：每天 48 单以上，置信度里权重只算 0.4',
                              'High-frequency: 48+ orders a day, weighted 0.4 in confidence'
                            )}
                          >
                            {tr(language, '高频', 'HF')}
                          </span>
                        ) : null}
                        {styleLabel(language, h.style) ? (
                          <span
                            className={`ml-1 rounded bg-nofx-bg-deeper px-1 text-[10px] ${
                              h.style === 'martingale'
                                ? 'text-nofx-danger'
                                : 'text-nofx-text-muted'
                            }`}
                            title={styleHint(language, h.style)}
                          >
                            {styleLabel(language, h.style)}
                          </span>
                        ) : null}
                        <div className="text-[10px] tabular-nums text-nofx-text-muted">
                          ID: {h.portfolio_id}
                        </div>
                      </td>
                      <td className={sideClass(h.side)}>
                        {sideLabel(language, h.side)}
                      </td>
                      <td className="tabular-nums">{price(h.entry_price)}</td>
                      <td className="tabular-nums">{usd(h.entry_value)}</td>
                      <td>{h.leverage ? `${h.leverage}x` : '--'}</td>
                      <td className="tabular-nums">
                        {h.margin ? usd(h.margin) : '--'}
                      </td>
                      <td
                        className={`tabular-nums ${upnl >= 0 ? 'text-nofx-success' : 'text-nofx-danger'}`}
                      >
                        {upnl ? signedUsd(upnl) : '--'}
                        {h.priced ? (
                          <span className="ml-1 text-xs">
                            ({h.return_pct >= 0 ? '+' : ''}
                            {h.return_pct.toFixed(1)}%
                            {h.pct_of_margin
                              ? ''
                              : tr(language, ' 价格', ' price')}
                            )
                          </span>
                        ) : null}
                      </td>
                      <td>
                        <HealthTag h={h} language={language} />
                      </td>
                      <td
                        className="tabular-nums"
                        title={h.opened_at ? ago(language, h.opened_at) : ''}
                      >
                        {h.opened_at
                          ? clock(h.opened_at)
                          : tr(language, '早于监控', 'Before monitoring')}
                      </td>
                      <td>
                        <span
                          className={`rounded px-1.5 text-[10px] ${h.source === 'public' ? 'bg-nofx-success/10 text-nofx-success' : 'bg-nofx-gold/10 text-nofx-gold'}`}
                        >
                          {h.source === 'public'
                            ? tr(language, '公开', 'Public')
                            : tr(language, '推算', 'Inferred')}
                        </span>
                        {h.source === 'inferred' &&
                        h.history_complete === false ? (
                          <span
                            className="ml-1 text-xs text-nofx-danger"
                            title={tr(
                              language,
                              '历史成交回填未完成，仓位数量和开仓价可能不准确',
                              'Trade backfill is incomplete; quantity and entry may be inaccurate'
                            )}
                          >
                            {tr(language, '回填中', 'Backfilling')}
                          </span>
                        ) : null}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        ) : (
          <p className={ui.muted}>
            {tr(
              language,
              '关注的带单员现在都没有持有这个币。',
              'No followed trader holds this coin now.'
            )}
          </p>
        )}
        <p className={`${ui.muted} mt-2`}>
          {tr(
            language,
            '按带单账号 ID 识别持仓归属，昵称可能重复；点击昵称可到币安主页核对。币安明确关联的信号与公开账户同时被关注时，仅统计公开账户，不按昵称合并其他账号。',
            'Positions belong to portfolio IDs, not nicknames. Click a name to verify on Binance. When explicitly linked signal and public accounts are both followed, only the public account is counted; unrelated namesakes stay separate.'
          )}
        </p>
        <p className={`${ui.muted} mt-2`}>
          {tr(
            language,
            '「推算」的仓位由可获取的成交记录重建，不等于已核实的当前持仓；历史不完整、漏单或强平都可能影响开仓价和金额，杠杆和保证金未知，浮盈亏按最新标记价格估算。',
            '"Inferred" positions are rebuilt from available fills, not verified current holdings. Incomplete history, missing fills or liquidation can affect entry and value; leverage and margin are unknown, and uPnL uses the latest mark price.'
          )}
          {tr(
            language,
            '「被套」只看本表当前浮亏：保证金亏损达到 50%，或没有保证金数据时价格反向达到 5%；不使用历史首笔价格。「历史入场偏离」单独提示首笔成交记录的参考偏离，不代表当前实际浮亏，也不计入被套人数或额外降低置信度。悬停或聚焦标签可查看数值、口径和阈值。未到被套的浮亏仍按当前亏损降低置信度。「新开」：24 小时内首次开仓；「加仓」：24 小时内给已有仓位加仓，不算新开，评分权重减半，浮亏时不计票。「可能已强平」：推算亏损已超过账户余额。',
            ' "Trapped" uses only the current return shown here: a loss of 50% of margin, or a 5% adverse price move when margin is unavailable. Historical entry is excluded. "Historical entry deviation" is a separate reference from the first recorded fill, not current PnL; it adds no trapped count or separate confidence penalty. Hover or focus a tag for values, basis and thresholds. Smaller current losses still reduce confidence. "New": opened or added within 24h. "Likely liquidated": inferred loss exceeds the account balance.'
          )}
        </p>
      </section>
    </div>
  )
}

export function CoinsTab({
  language,
  selected,
  onSelect,
}: {
  language: string
  selected: string
  onSelect: (symbol: string) => void
}) {
  const { data } = useSWR(
    'copytrade/symbols',
    () => api.getCopyTradeSymbols(),
    LOCAL_SNAPSHOT_OPTIONS
  )
  const symbols = data?.symbols || []
  const { data: scoreData } = useSWR(
    'copytrade/coin-scores',
    () => api.getCoinScores(),
    { refreshInterval: 30000 }
  )
  const scoreMap = useMemo(() => {
    const mp = new Map<string, CoinScoreBrief>()
    for (const it of scoreData?.items || []) mp.set(it.symbol, it)
    return mp
  }, [scoreData])
  const { data: statusData } = useSWR('copytrade/coin-status', () => api.getCoinStatus(), {
    refreshInterval: 30000,
  })
  const statusMap = useMemo(() => {
    const m = new Map<string, CoinStatusRow>()
    for (const it of statusData?.coins || []) m.set(it.symbol, it)
    return m
  }, [statusData])
  const { data: accData } = useSWR('copytrade/coin-accuracy', () => api.getCoinAccuracy(), {
    refreshInterval: 60000,
  })
  const aiScore = useCallback(
    (symbol: string) => {
      const b = scoreMap.get(symbol)
      if (!b) return -1
      // A stale reading (older than the coin-pool TTL) must NOT keep ranking on
      // an old high score: sink it below every fresh coin.
      if (b.stale) return -1
      // Rank by conviction (|edge|) so strong shorts surface too, not just longs.
      // Old rows may lack smoothed_edge (0): fall back to raw conviction.
      if (typeof b.smoothed_edge === 'number' && b.smoothed_edge !== 0) {
        return Math.abs(b.smoothed_edge)
      }
      return b.conviction ?? Math.abs((b.bull_score || 0) - (b.bear_score || 0))
    },
    [scoreMap]
  )
  const [sort, setSort] = useState<CoinSort>('stable')
  const [bias, setBias] = useState<CoinBias>('all')
  const needRank = sort === 'stable' || sort === 'ai'
  const { data: rankData } = useSWR(
    needRank ? ['coin-ranking', sort] : null,
    () => api.getCoinRanking(sort === 'ai' ? 'ai' : 'stable'),
    { refreshInterval: 60000 }
  )
  const shown = useMemo(() => {
    let list = sortCoins(symbols, sort, bias, aiScore)
    // Both the "4h stable" and "AI" rankings use the SAME backend set (scored +
    // clear direction) and are ORDERED by the backend composite for that mode.
    if (sort === 'stable' || sort === 'ai') {
      const items = ((rankData?.items as { symbol: string }[] | undefined) ?? [])
      const order = new Map<string, number>()
      items.forEach((it, i) => order.set(it.symbol, i))
      list = list
        .filter((x) => order.has(x.symbol))
        .sort((a, b) => (order.get(a.symbol) ?? 0) - (order.get(b.symbol) ?? 0))
    }
    return list
  }, [symbols, sort, bias, aiScore, statusMap, rankData])
  // Freeze the default detail coin once, so a ranking reshuffle does not swap
  // the chart under the user; an explicit selection still wins.
  const [pinned, setPinned] = useState('')
  useEffect(() => {
    if (!selected && !pinned && shown.length) setPinned(shown[0].symbol)
  }, [selected, pinned, shown])
  const current = selected || pinned || shown[0]?.symbol || ''

  return (
    <div className="grid gap-3 lg:grid-cols-[280px_minmax(0,1fr)]">
      <div>
        <aside className={`${ui.panel} max-h-[760px] overflow-y-auto`}>
          <div className="mb-2 flex items-baseline justify-between gap-2">
            <h2 className={ui.title}>
              {tr(language, '关注的人在做的币', 'Coins followed traders hold')}
            </h2>
            <span className={`${ui.muted} whitespace-nowrap text-xs tabular-nums`}>
              {bias === 'all'
                ? tr(language, `共 ${symbols.length} 个币`, `${symbols.length} coins`)
                : tr(
                    language,
                    `${shown.length}/${symbols.length} 个币`,
                    `${shown.length}/${symbols.length} coins`
                  )}
            </span>
          </div>
          <p className={`${ui.muted} mb-2 text-[11px]`}>
            {tr(
              language,
              '「偏多/偏空」= 带单员的人气方向；「AI 多/空」= 模型研判（仅供参考）。',
              '"偏多/偏空" = copy-trader crowd; "AI 多/空" = model read (reference only).'
            )}
          </p>
          {statusData?.counts ? (
            <div className={`${ui.muted} mb-2 rounded border border-[rgba(25,36,52,0.08)] p-2 text-[11px] leading-5`}>
              <div className="flex flex-wrap gap-x-3 gap-y-1 tabular-nums">
                <span>{tr(language, '全部带单币', 'All')}: <b className="text-nofx-text">{statusData.counts.all}</b></span>
                <span>{tr(language, '符合AI筛选', 'In scope')}: <b className="text-nofx-text">{statusData.counts.in_scope}</b></span>
                <span>{tr(language, '已完成评分', 'Scored')}: <b className="text-nofx-success">{statusData.counts.scored}</b></span>
                <span>{tr(language, '待评分/失败', 'Pending')}: <b className="text-nofx-gold">{statusData.counts.pending}</b></span>
              </div>
              <div className="flex flex-wrap gap-x-3 gap-y-1 opacity-90">
                <span>{tr(language, '门槛', 'Min vol')}: {usd(statusData.snapshot.min_volume)}</span>
                <span>{statusData.snapshot.config_version}</span>
                <span>{tr(language, '快照', 'Snap')}: {ago(language, statusData.snapshot.taken_at)}</span>
                {accData?.overall && accData.overall.valid > 0 ? (
                  <span>{tr(language, '4h命中率', '4h hit')}: <b className="text-nofx-text">{accData.overall.hit_rate.toFixed(2)}%</b> ({accData.overall.hit}/{accData.overall.valid})</span>
                ) : null}
              </div>
            </div>
          ) : null}
          <div className="mb-2 grid grid-cols-2 gap-2">
            <select
              className={ui.input}
              value={sort}
              onChange={(e) => setSort(e.target.value as CoinSort)}
              aria-label={tr(language, '排序', 'Sort')}
            >
              <option value="stable">
                {tr(language, '4 小时稳定分', '4h stable score')}
              </option>
              <option value="rank">
                {tr(language, '带单综合评分', 'Copy-trader rank')}
              </option>
              <option value="confidence">
                {tr(language, '置信度最高', 'Highest confidence')}
              </option>
              <option value="ai">
                {tr(language, 'AI 分最高', 'Highest AI score')}
              </option>
              <option value="traders">
                {tr(language, '关注的人最多', 'Most traders')}
              </option>
              <option value="long">
                {tr(language, '做多人数最多', 'Most long')}
              </option>
              <option value="short">
                {tr(language, '做空人数最多', 'Most short')}
              </option>
              <option value="value">
                {tr(language, '持仓金额最大', 'Largest value')}
              </option>
              <option value="consensus">
                {tr(language, '多空最一致', 'Most one-sided')}
              </option>
            </select>
            <select
              className={ui.input}
              value={bias}
              onChange={(e) => setBias(e.target.value as CoinBias)}
              aria-label={tr(language, '方向', 'Direction')}
            >
              <option value="all">
                {tr(language, '全部方向', 'All sides')}
              </option>
              <option value="long">
                {tr(language, '偏多', 'Mostly long')}
              </option>
              <option value="short">
                {tr(language, '偏空', 'Mostly short')}
              </option>
            </select>
          </div>
          {shown.length ? (
            <ul className="space-y-1">
              {shown.map((s) => (
                <li key={s.symbol}>
                  <button
                    type="button"
                    aria-pressed={s.symbol === current}
                    onClick={() => onSelect(s.symbol)}
                    className={`w-full rounded-lg border px-2 py-1.5 text-left ${s.symbol === current ? 'border-nofx-gold bg-nofx-gold/10' : 'border-transparent hover:border-nofx-gold'}`}
                  >
                    <div className="mb-1 flex items-center justify-between text-sm">
                      <span className="font-medium text-nofx-text">
                        {base(s.symbol)}
                      </span>
                      <span className="text-xs">
                        <span className="text-nofx-success">
                          {s.long.traders}
                          {tr(language, '多', 'L')}
                        </span>
                        {' / '}
                        <span className="text-nofx-danger">
                          {s.short.traders}
                          {tr(language, '空', 'S')}
                        </span>
                      </span>
                    </div>
                    <Bar s={s} />
                    <div
                      className={`${ui.muted} mt-1 flex items-center justify-between`}
                    >
                      <span>
                        {usd(s.long.value + s.short.value)}
                        {s.long.trapped + s.short.trapped ? (
                          <span className="ml-1 text-nofx-gold">
                            {tr(
                              language,
                              `${s.long.trapped + s.short.trapped} 人被套`,
                              `${s.long.trapped + s.short.trapped} trapped`
                            )}
                          </span>
                        ) : null}
                      </span>
                      <span className="flex items-center gap-2">
                        <CoinScoreCell s={s} status={statusMap.get(s.symbol)} b={scoreMap.get(s.symbol)} language={language} />
                        <ConfidenceBadge
                          c={s.confidence}
                          language={language}
                          compact
                        />
                      </span>
                    </div>
                    <div className="mt-1 flex items-center justify-between gap-1">
                      {s.stable_direction ? <span className="text-[11px] tabular-nums text-nofx-gold">4h {s.stable_direction.score.toFixed(0)} · {s.stable_direction.direction}</span> : <span />}
                      {s.rank && s.rank.direction !== 'neutral' ? <><RankBadge r={s.rank} language={language} compact /><CrowdingTag r={s.rank} language={language} /></> : null}
                    </div>
                  </button>
                </li>
              ))}
            </ul>
          ) : (
            <p className={ui.muted}>
              {symbols.length
                ? tr(
                    language,
                    '没有符合这个方向的币。',
                    'No coin leans this way.'
                  )
                : tr(
                    language,
                    '关注的带单员现在都没有持仓。',
                    'No followed trader holds anything now.'
                  )}
            </p>
          )}
        </aside>
        <HitRatePanel language={language} />
      </div>
      <div className="min-w-0">
        {current ? (
          <CoinDetail language={language} symbol={current} />
        ) : (
          <p className={`${ui.panel} py-10 text-center ${ui.muted}`}>
            {tr(
              language,
              '有持仓后，这里会显示每个币的多空分布和各人的开仓价。',
              'Once traders hold positions, each coin shows its long/short split and entries here.'
            )}
          </p>
        )}
      </div>
    </div>
  )
}
