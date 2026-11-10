import { NoticeText } from '../NoticeText'
import { useEffect, useMemo, useState } from 'react'
import useSWR from 'swr'
import { LOCAL_SNAPSHOT_OPTIONS } from '../../lib/liveSnapshot'
import { api } from '../../lib/api'
import type {
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
  | 'confidence'
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
  bias: CoinBias
): SymbolSummary[] {
  const key: Record<CoinSort, (s: SymbolSummary) => number> = {
    confidence: (s) => s.confidence?.score || 0,
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
    return () => {
      cancelled = true
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
                {holders.map((h) => {
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
                      key={h.portfolio_id + h.side}
                      className={`border-t ${ui.border} ${h.liquidated ? 'opacity-50' : ''}`}
                    >
                      <td className="py-1.5">
                        {h.nickname}
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
            '「推算」的仓位由成交记录算出：开仓价和金额是准确的，但看不到杠杆和保证金，浮盈亏按最新标记价格估算。',
            '"Inferred" positions come from trades: entry and value are exact, leverage and margin are unknown, and uPnL is estimated from the latest mark price.'
          )}
          {tr(
            language,
            '「被套」只看本表当前浮亏：保证金亏损达到 50%，或没有保证金数据时价格反向达到 5%；不使用历史首笔价格。「历史入场偏离」单独提示首笔成交记录的参考偏离，不代表当前实际浮亏，也不计入被套人数或额外降低置信度。悬停或聚焦标签可查看数值、口径和阈值。未到被套的浮亏仍按当前亏损降低置信度。「新开」：24 小时内开仓或加仓。「可能已强平」：推算亏损已超过账户余额。',
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
  const [sort, setSort] = useState<CoinSort>('confidence')
  const [bias, setBias] = useState<CoinBias>('all')
  const shown = useMemo(
    () => sortCoins(symbols, sort, bias),
    [symbols, sort, bias]
  )
  const current = selected || shown[0]?.symbol || ''

  return (
    <div className="grid gap-3 lg:grid-cols-[280px_minmax(0,1fr)]">
      <div>
        <aside className={`${ui.panel} max-h-[760px] overflow-y-auto`}>
          <h2 className={ui.title}>
            {tr(language, '关注的人在做的币', 'Coins followed traders hold')}
          </h2>
          <div className="mb-2 grid grid-cols-2 gap-2">
            <select
              className={ui.input}
              value={sort}
              onChange={(e) => setSort(e.target.value as CoinSort)}
              aria-label={tr(language, '排序', 'Sort')}
            >
              <option value="confidence">
                {tr(language, '置信度最高', 'Highest confidence')}
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
                      <ConfidenceBadge
                        c={s.confidence}
                        language={language}
                        compact
                      />
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
