import { NoticeText } from '../NoticeText'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Loader2, RefreshCw } from 'lucide-react'
import { api } from '../../lib/api'
import type {
  CoinPoolCoin,
  HornBoard,
  HornRow,
  HornStage,
} from '../../lib/api/coinPool'
import { hornOnBoard } from '../../lib/api/coinPool'
import { formatCompact } from '../../utils/marketIndicators'
import { BN } from '../market/BinanceChart'
import { tr, ui } from '../market/marketUi'

const intervals = ['15m', '1h', '4h'] as const

const stageLabel = (language: string, stage: HornStage) =>
  ({
    starting: tr(language, '刚启动', 'Starting'),
    opening: tr(language, '正在张口', 'Opening'),
    mature: tr(language, '开口已大', 'Wide open'),
    closing: tr(language, '开始收口', 'Closing'),
    none: tr(language, '未开口', 'No expansion'),
  })[stage]

function OnBoard({ row, language }: { row: HornRow; language: string }) {
  const { label, isNew } = hornOnBoard(row, language === 'zh')
  if (!label) return <span className="text-nofx-text-muted">--</span>
  return (
    <span
      className={isNew ? 'rounded px-1.5 py-0.5 font-semibold' : 'text-nofx-text-muted'}
      style={isNew ? { color: BN.up, background: 'rgba(46,139,87,0.1)' } : undefined}
      title={row.matched_since ? new Date(row.matched_since).toLocaleString() : undefined}
    >
      {label}
    </span>
  )
}

export function directionBadge(language: string, direction?: string) {
  if (direction === 'long')
    return (
      <span
        className="rounded px-1.5 py-0.5 text-xs font-semibold"
        style={{ color: BN.up, background: 'rgba(46,139,87,0.1)' }}
      >
        {tr(language, '做多', 'Long')}
      </span>
    )
  if (direction === 'short')
    return (
      <span
        className="rounded px-1.5 py-0.5 text-xs font-semibold"
        style={{ color: BN.down, background: 'rgba(214,67,58,0.1)' }}
      >
        {tr(language, '做空', 'Short')}
      </span>
    )
  return <span className={ui.muted}>{tr(language, '观望', 'Neutral')}</span>
}

function ScoreBar({ score }: { score: number }) {
  const color = score >= 75 ? BN.up : score >= 50 ? '#b45309' : '#94a3b8'
  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 w-16 overflow-hidden rounded bg-[rgba(25,36,52,0.08)]">
        <div
          className="h-full"
          style={{ width: `${score}%`, background: color }}
        />
      </div>
      <span
        className="w-7 text-right text-xs font-semibold tabular-nums"
        style={{ color }}
      >
        {score.toFixed(0)}
      </span>
    </div>
  )
}

const note = (language: string, row: HornRow, interval: string) =>
  tr(
    language,
    `${interval} 布林开口 ${row.expansion.toFixed(1)} 倍 · ${stageLabel(language, row.stage)}`,
    `${interval} Bollinger ${row.expansion.toFixed(1)}x · ${stageLabel(language, row.stage)}`
  )

// Bollinger "horn": bands squeeze, then the upper band rises while the lower
// band falls. Screens the Binance USDT-M top 50 gainers.
export function BollingerScreener({
  language,
  onSave,
}: {
  language: string
  onSave: (coins: CoinPoolCoin[], interval: string) => void
}) {
  const [interval, setInterval] = useState<(typeof intervals)[number]>('1h')
  const [board, setBoard] = useState<HornBoard | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [onlyMatches, setOnlyMatches] = useState(true)
  const [picked, setPicked] = useState<Set<string>>(new Set())

  const scan = useCallback(
    async (refresh: boolean) => {
      setLoading(true)
      setError('')
      try {
        const next = await api.screenBollinger(interval, refresh)
        setBoard(next)
        setPicked(
          new Set(next.rows.filter((r) => r.match).map((r) => r.symbol))
        )
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e))
      } finally {
        setLoading(false)
      }
    },
    [interval]
  )

  useEffect(() => {
    void scan(false)
  }, [scan])

  const rows = useMemo(
    () => (board?.rows || []).filter((r) => !onlyMatches || r.match),
    [board, onlyMatches]
  )
  const toggle = (symbol: string) =>
    setPicked((prev) => {
      const next = new Set(prev)
      if (next.has(symbol)) next.delete(symbol)
      else next.add(symbol)
      return next
    })
  const allPicked = rows.length > 0 && rows.every((r) => picked.has(r.symbol))

  const save = () => {
    const coins = (board?.rows || [])
      .filter((r) => picked.has(r.symbol))
      .sort((a, b) => b.score - a.score)
      .map((r) => ({
        symbol: r.symbol,
        direction: r.direction,
        score: r.score,
        note: note(language, r, interval),
      }))
    onSave(coins, interval)
  }

  return (
    <section className={ui.panel}>
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <div className="flex gap-1">
          {intervals.map((i) => (
            <button
              key={i}
              type="button"
              aria-pressed={i === interval}
              className={`rounded-lg px-2 py-1 text-xs ${i === interval ? 'bg-nofx-gold/10 font-semibold text-nofx-gold' : 'text-nofx-text-muted hover:text-nofx-text'}`}
              onClick={() => setInterval(i)}
              disabled={loading}
            >
              {i}
            </button>
          ))}
        </div>
        <label className="ml-2 flex items-center gap-1 text-xs text-nofx-text">
          <input
            type="checkbox"
            checked={onlyMatches}
            onChange={(e) => setOnlyMatches(e.target.checked)}
          />
          {tr(language, '只看喇叭口', 'Horn patterns only')}
        </label>
        <button
          type="button"
          className={`${ui.btnGhost} ml-auto flex items-center gap-1`}
          disabled={loading}
          onClick={() => void scan(true)}
        >
          {loading ? (
            <Loader2 className="h-3 w-3 animate-spin" />
          ) : (
            <RefreshCw className="h-3 w-3" />
          )}
          {tr(language, '重新扫描', 'Rescan')}
        </button>
      </div>

      <p className={`${ui.muted} mb-3`}>
        {tr(
          language,
          '扫描币安 USDT 永续合约 24 小时涨幅前 50，用布林带（20，2）找刚形成的喇叭口：之前收得很紧，带宽刚开始放大。只选「刚启动」（带宽刚抬头，价格压在一侧轨道上）和「正在张口」（上轨上翘、下轨下压，离开收口不超过 12 根 K 线）的币；开口已大或开始收口的不选。评分 0-100：新鲜度 35、之前收得多紧 25、方向是否清晰 20、上下轨是否反向张开 10、开口幅度 10（1.75 倍拿满，再大不加分）。「上榜」是这次形态连续成立的 K 线根数和开始时间。',
          'Scans the top 50 Binance USDT-M perpetual gainers (24h) with Bollinger Bands (20, 2) for horns that are just forming: a tight squeeze whose bandwidth has only just started to expand. Only "Starting" (bandwidth lifting, price pressing on one band) and "Opening" (upper band rising, lower band falling, at most 12 candles since lift-off) match; wide-open or closing horns do not. Score 0-100: freshness 35, squeeze tightness 25, direction clarity 20, bands diverging 10, expansion 10 (full at 1.75x, nothing extra beyond). "On board" is how many candles in a row the pattern has held and since when.'
        )}
      </p>

      {error ? (
        <p role="alert" className="mb-2 text-sm" style={{ color: BN.down }}>
          {tr(language, '扫描失败：', 'Scan failed: ')}
          <NoticeText text={error} />
        </p>
      ) : null}

      {board ? (
        <p className={`${ui.muted} mb-2`}>
          {tr(language, '截至', 'As of')}{' '}
          {new Date(board.fetched_at).toLocaleTimeString()} ·{' '}
          {tr(
            language,
            `扫描 ${board.scanned} 个，${board.matched} 个出现喇叭口`,
            `${board.scanned} scanned, ${board.matched} horn patterns`
          )}
        </p>
      ) : null}

      <div className="overflow-x-auto">
        <table className="w-full min-w-[720px] text-sm">
          <thead>
            <tr className="border-b border-[rgba(25,36,52,0.14)] text-left text-xs text-nofx-text-muted">
              <th className="w-8 py-2">
                <input
                  type="checkbox"
                  aria-label={tr(language, '全选', 'Select all')}
                  checked={allPicked}
                  onChange={() =>
                    setPicked((prev) => {
                      const next = new Set(prev)
                      for (const r of rows) {
                        if (allPicked) next.delete(r.symbol)
                        else next.add(r.symbol)
                      }
                      return next
                    })
                  }
                />
              </th>
              <th className="py-2">{tr(language, '币种', 'Symbol')}</th>
              <th className="py-2">{tr(language, '方向', 'Direction')}</th>
              <th className="py-2">{tr(language, '评分', 'Score')}</th>
              <th className="py-2">{tr(language, '阶段', 'Stage')}</th>
              <th className="py-2">{tr(language, '上榜', 'On board')}</th>
              <th className="py-2 text-right">
                {tr(language, '开口倍数', 'Expansion')}
              </th>
              <th className="py-2 text-right">
                {tr(language, '24h 涨幅', '24h change')}
              </th>
              <th className="py-2 text-right">
                {tr(language, '24h 成交额', '24h volume')}
              </th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr
                key={r.symbol}
                className="border-b border-[rgba(25,36,52,0.06)]"
              >
                <td className="py-2">
                  <input
                    type="checkbox"
                    aria-label={r.symbol}
                    checked={picked.has(r.symbol)}
                    onChange={() => toggle(r.symbol)}
                  />
                </td>
                <td className="py-2 font-semibold text-nofx-text">
                  {r.symbol.replace(/USDT$/, '')}
                </td>
                <td className="py-2">
                  {directionBadge(language, r.direction)}
                </td>
                <td className="py-2">
                  <ScoreBar score={r.score} />
                </td>
                <td className="py-2 text-xs text-nofx-text">
                  {r.error
                    ? tr(language, 'K 线不可用', 'Candles unavailable')
                    : stageLabel(language, r.stage)}
                </td>
                <td className="py-2 whitespace-nowrap text-xs">
                  <OnBoard row={r} language={language} />
                </td>
                <td className="py-2 text-right tabular-nums text-nofx-text">
                  {r.expansion ? r.expansion.toFixed(1) + 'x' : '--'}
                </td>
                <td
                  className="py-2 text-right tabular-nums"
                  style={{ color: r.change_pct >= 0 ? BN.up : BN.down }}
                >
                  {r.change_pct >= 0 ? '+' : ''}
                  {r.change_pct.toFixed(2)}%
                </td>
                <td className="py-2 text-right tabular-nums text-nofx-text-muted">
                  {formatCompact(r.quote_volume)}
                </td>
              </tr>
            ))}
            {!loading && board && rows.length === 0 ? (
              <tr>
                <td
                  colSpan={9}
                  className="py-6 text-center text-sm text-nofx-text-muted"
                >
                  {tr(
                    language,
                    '当前没有出现喇叭口的币，可以换个周期或稍后再扫。',
                    'No horn pattern right now. Try another interval or rescan later.'
                  )}
                </td>
              </tr>
            ) : null}
          </tbody>
        </table>
      </div>

      <div className="mt-3 flex items-center gap-3">
        <span className={ui.muted}>
          {tr(language, `已选 ${picked.size} 个`, `${picked.size} selected`)}
        </span>
        <button
          type="button"
          className={`${ui.btnGold} ml-auto w-auto`}
          disabled={picked.size === 0 || loading}
          onClick={save}
        >
          {tr(language, '保存到币池', 'Save to a coin pool')}
        </button>
      </div>
    </section>
  )
}
