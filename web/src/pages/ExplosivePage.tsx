import { useEffect, useMemo, useRef, useState } from 'react'
import useSWR from 'swr'
import { Loader2, RefreshCw, ArrowDown, ArrowUp, Sparkles } from 'lucide-react'
import { api } from '../lib/api'
import { explosiveApi } from '../lib/api/explosive'
import type { ExplosiveRow } from '../lib/api/explosive'
import type { MarketKline } from '../lib/api/market'
import {
  BinanceChart,
  BN,
  type MainIndicator,
  type SubIndicator,
} from '../components/market/BinanceChart'
import { tr, ui } from '../components/market/marketUi'
import { useLanguage } from '../contexts/LanguageContext'
import { priceDecimals } from '../utils/marketIndicators'
import { AccuracyPanel } from '../components/explosive/AccuracyPanel'
import { stateColor, stateLabel, tagLabel } from '../components/explosive/labels'

const signed = (v: number) => `${v >= 0 ? '+' : ''}${v.toFixed(2)}%`


function fmtTracked(min: number): string {
  if (!(min >= 0)) return '—'
  if (min < 60) return `${Math.round(min)}m`
  return `${(min / 60).toFixed(1)}h`
}

function PotentialBar({ value }: { value: number }) {
  const width = Math.min(100, (value / 40) * 100)
  const color = value >= 20 ? BN.up : value >= 8 ? '#b45309' : '#94a3b8'
  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 w-14 overflow-hidden rounded bg-[rgba(25,36,52,0.08)]">
        <div className="h-full" style={{ width: `${width}%`, background: color }} />
      </div>
      <span className="w-14 text-right text-xs font-semibold tabular-nums" style={{ color }}>
        +{value.toFixed(1)}%
      </span>
    </div>
  )
}

function Part({ label, value }: { label: string; value: number }) {
  return (
    <div>
      <div className={ui.muted}>{label}</div>
      <div className="flex items-center gap-1">
        <div className="h-1 w-12 overflow-hidden rounded bg-[rgba(25,36,52,0.08)]">
          <div className="h-full" style={{ width: `${Math.round(value * 100)}%`, background: BN.up }} />
        </div>
        <span className="tabular-nums text-nofx-text">{Math.round(value * 100)}</span>
      </div>
    </div>
  )
}

// PriceTag flashes green/red for a moment whenever the price ticks, so a 1s
// board refresh is visible to the eye instead of silently re-rendering.
function PriceTag({ price, decimals }: { price: number; decimals: number }) {
  const prev = useRef(price)
  const [dir, setDir] = useState<'' | 'up' | 'down'>('')
  useEffect(() => {
    if (price > prev.current) setDir('up')
    else if (price < prev.current) setDir('down')
    prev.current = price
    const t = setTimeout(() => setDir(''), 650)
    return () => clearTimeout(t)
  }, [price])
  const color = dir === 'up' ? BN.up : dir === 'down' ? BN.down : undefined
  const background = dir === 'up' ? 'rgba(22,163,74,0.14)' : dir === 'down' ? 'rgba(220,38,38,0.14)' : 'transparent'
  return (
    <span className="rounded px-1 tabular-nums transition-colors duration-500" style={{ color, background }}>
      {price.toFixed(decimals)}
    </span>
  )
}

const stateHelp: Record<string, [string, string]> = {
  starting: ['刚启动：涨幅和量能刚起来', 'just started moving'],
  accelerating: ['加速中：涨幅、量能都在放大', 'gain and volume both expanding'],
  overheated: ['过热：涨幅/费率过高，风险大', 'overheated: high gain/funding'],
  fading: ['衰减：量能退潮', 'volume fading'],
  falling: ['回落：正在下跌', 'falling now'],
}

type SortKey = 'expected' | 'day' | 'h24' | 'rise' | 'vol' | 'oi' | 'offhigh'

function sortValue(r: ExplosiveRow, key: SortKey): number {
  switch (key) {
    case 'expected':
      return r.predicted_final ?? 0
    case 'day':
      return r.day_gain
    case 'h24':
      return r.change_24h
    case 'rise':
      return r.rise_h ?? 0
    case 'vol':
      return r.vol_spike_1h ?? 0
    case 'oi':
      return r.oi_known ? r.oi_change_1h : -1e9
    case 'offhigh':
      return -r.distance_high
  }
}

export function ExplosivePage() {
  const { language } = useLanguage()
  const [view, setView] = useState<'board' | 'accuracy'>('board')
  const [selected, setSelected] = useState('')
  const [chartInterval, setChartInterval] = useState('15m')
  const [sort, setSort] = useState<{ key: SortKey; dir: 'desc' | 'asc' }>({ key: 'expected', dir: 'desc' })
  const [filters, setFilters] = useState<Set<string>>(new Set())
  const [alert, setAlert] = useState<string[]>([])

  const { data, error, mutate, isValidating } = useSWR(
    'market/explosive',
    () => explosiveApi.board(),
    { refreshInterval: 4000, revalidateOnFocus: false, keepPreviousData: true, dedupingInterval: 3000 }
  )
  const rows = data?.rows || []
  const blasted = data?.blasted || []
  const radar = data?.radar || []
  const current = selected || rows[0]?.symbol || ''
  const row = useMemo(() => rows.find((r) => r.symbol === current) || null, [rows, current])

  const { data: candles = [] } = useSWR(
    current ? ['explosive/klines', current, chartInterval] : null,
    () => api.getMarketKlines(current, chartInterval, 300) as Promise<MarketKline[]>,
    { revalidateOnFocus: false, dedupingInterval: 60000, keepPreviousData: true }
  )

  // Flag coins that just entered the radar since the previous poll.
  const prevRadar = useRef<Set<string>>(new Set())
  useEffect(() => {
    const now = new Set(radar.map((r) => r.symbol))
    if (prevRadar.current.size > 0) {
      const added = [...now].filter((s) => !prevRadar.current.has(s))
      if (added.length) setAlert(added)
    }
    prevRadar.current = now
  }, [radar])
  useEffect(() => {
    if (!alert.length) return
    const t = setTimeout(() => setAlert([]), 15000)
    return () => clearTimeout(t)
  }, [alert])

  useEffect(() => {
    if (selected && !rows.some((r) => r.symbol === selected)) setSelected('')
  }, [rows, selected])

  const main: MainIndicator = 'EMA'
  const subs: SubIndicator[] = ['VOL']
  const last = candles[candles.length - 1]?.close || row?.price || 0
  const decimals = priceDecimals(last)
  const refresh = () => void mutate(() => explosiveApi.board(true), { revalidate: false })

  const toggleFilter = (k: string) =>
    setFilters((prev) => {
      const next = new Set(prev)
      next.has(k) ? next.delete(k) : next.add(k)
      return next
    })

  const visible = useMemo(() => {
    let out = rows.slice()
    if (filters.has('new')) out = out.filter((r) => r.is_new)
    if (filters.has('vol3')) out = out.filter((r) => (r.vol_spike_1h ?? 0) >= 3)
    if (filters.has('oiUp')) out = out.filter((r) => r.oi_known && r.oi_change_1h > 0)
    if (filters.has('under30')) out = out.filter((r) => r.day_gain < 0.3)
    if (filters.has('good')) out = out.filter((r) => r.tradability === 'good')
    out.sort((a, b) => {
      const d = sortValue(a, sort.key) - sortValue(b, sort.key)
      return sort.dir === 'asc' ? d : -d
    })
    return out
  }, [rows, filters, sort])

  const head = (key: SortKey, label: string, title: string, pl = '') => {
    const active = sort.key === key
    return (
      <th
        className={`px-2 ${pl} text-right font-normal ${active ? 'text-nofx-gold' : ''}`}
        title={title}
      >
        <button
          type="button"
          className="inline-flex items-center gap-0.5 hover:text-nofx-text"
          onClick={() => setSort((s) => (s.key === key ? { key, dir: s.dir === 'desc' ? 'asc' : 'desc' } : { key, dir: 'desc' }))}
        >
          {label}
          {active ? (sort.dir === 'desc' ? <ArrowDown className="h-3 w-3" /> : <ArrowUp className="h-3 w-3" />) : null}
        </button>
      </th>
    )
  }

  const filterChip = (k: string, label: string) => (
    <button
      type="button"
      onClick={() => toggleFilter(k)}
      className={`rounded-full border px-2.5 py-0.5 text-xs transition-colors ${
        filters.has(k) ? 'border-nofx-gold text-nofx-gold' : `${ui.border} ${ui.muted} hover:text-nofx-text`
      }`}
    >
      {label}
    </button>
  )

  const stateBadge = (s: ExplosiveRow['state']) => (
    <span
      className="rounded px-1.5 py-0.5 text-xs font-semibold"
      style={{ color: stateColor[s], background: 'rgba(25,36,52,0.06)' }}
      title={tr(language, stateHelp[s]?.[0] || '', stateHelp[s]?.[1] || '')}
    >
      {stateLabel(language, s)}
    </span>
  )

  const coinCell = (r: ExplosiveRow) => (
    <div className="leading-tight">
      <div className="flex items-center gap-1">
        <span className="font-medium">{r.symbol.replace('USDT', '')}</span>
        <span
          className="h-1.5 w-1.5 rounded-full"
          title={tr(language, `可交易性：${r.tradability}`, `Tradability: ${r.tradability}`)}
          style={{ background: r.tradability === 'good' ? BN.up : r.tradability === 'fair' ? '#b45309' : BN.down }}
        />
        {r.is_new ? (
          <span className="rounded bg-nofx-gold/20 px-1 text-[10px] font-semibold text-nofx-gold">
            {tr(language, '新', 'NEW')}
          </span>
        ) : null}
      </div>
      <div className="text-[10px] text-nofx-text-muted">
        {tr(language, '跟踪', 'tracked')} {fmtTracked(r.tracked_min)}
      </div>
    </div>
  )

  return (
    <div className="space-y-4">
      <section className={ui.panel}>
        <div className="mb-1 flex flex-wrap items-center gap-3">
          <h1 className="text-lg font-semibold text-nofx-text">{tr(language, '爆发榜', 'Explosive board')}</h1>
          {data ? (
            <span className={ui.muted}>
              {tr(language, `扫描 ${data.scanned} 个合约，${data.candidates} 个通过初筛`, `${data.scanned} scanned, ${data.candidates} passed`)}
              {typeof data.age_seconds === 'number'
                ? tr(language, ` · 数据 ${data.age_seconds} 秒前`, ` · data ${data.age_seconds}s old`)
                : ''}
              {data.refreshing ? tr(language, ' · 后台刷新中', ' · refreshing') : ''}
            </span>
          ) : null}
          <div className="ml-auto flex gap-1">
            {(['board', 'accuracy'] as const).map((v) => (
              <button
                key={v}
                type="button"
                aria-pressed={view === v}
                className={`${ui.btnGhost} ${view === v ? 'border-nofx-gold text-nofx-gold' : ''}`}
                onClick={() => setView(v)}
              >
                {v === 'board' ? tr(language, '榜单', 'Board') : tr(language, '准确率', 'Accuracy')}
              </button>
            ))}
          </div>
          <button type="button" className={`flex items-center gap-1 ${ui.btnGhost}`} onClick={refresh} disabled={isValidating}>
            {isValidating ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
            {tr(language, '刷新', 'Refresh')}
          </button>
        </div>
        <p className={`text-xs ${ui.muted}`}>
          {tr(
            language,
            '按"预期最终涨幅"从高到低排：现在只有 +10%、但预计能走到 +30% 的币，会排在已经 +20%、预计只到 +22% 的币前面。点击表头可换排序，点下方标签可筛选。',
            'Sorted by expected final gain: a coin at +10% heading for +30% outranks one at +20% heading for +22%. Click a header to re-sort, chips to filter.'
          )}
        </p>
      </section>

      {algebraGuard(error)}

      {view === 'accuracy' ? (
        <AccuracyPanel language={language} />
      ) : (
        <div className="grid items-start gap-4 lg:grid-cols-[minmax(0,1fr)_380px]">
          <div className="min-w-0 space-y-4">
            <section className={`${ui.panel} min-w-0`}>
              <div className="mb-2 flex flex-wrap items-center gap-2 text-xs">
                <span className={ui.muted}>{tr(language, '筛选', 'Filter')}</span>
                {filterChip('new', tr(language, '新上榜', 'New'))}
                {filterChip('vol3', tr(language, '量能≥3x', 'Vol≥3x'))}
                {filterChip('oiUp', tr(language, 'OI 增加', 'OI up'))}
                {filterChip('under30', tr(language, '今日<30%', 'Day<30%'))}
                {filterChip('good', tr(language, '可交易', 'Tradable'))}
                {filters.size ? (
                  <button type="button" className={`text-xs ${ui.muted} underline`} onClick={() => setFilters(new Set())}>
                    {tr(language, '清除', 'Clear')}
                  </button>
                ) : null}
                <details className="ml-auto">
                  <summary className={`cursor-pointer text-xs ${ui.muted}`}>{tr(language, '状态/标签说明', 'Legend')}</summary>
                  <div className={`mt-1 max-w-md space-y-0.5 text-[11px] ${ui.muted}`}>
                    {(['starting', 'accelerating', 'overheated', 'fading', 'falling'] as const).map((s) => (
                      <div key={s}>
                        <span className="font-medium" style={{ color: stateColor[s] }}>{stateLabel(language, s)}</span>
                        {' — '}
                        {tr(language, stateHelp[s]?.[0]?.replace(/^.*：/, '') || '', stateHelp[s]?.[1] || '')}
                      </div>
                    ))}
                    <div>{tr(language, '标签：突破=破前高；放量=量能突增；OI 增/流出=持仓量变化；多头排列=EMA 多头；站上 EMA20=价在均线上；费率过热=资金费率过高；追高=涨幅过大', 'Tags: breakout, volume, OI up/drain, EMA uptrend, above EMA20, hot funding, chasing')}</div>
                  </div>
                </details>
              </div>

              {alert.length ? (
                <div className="mb-2 flex items-center gap-2 rounded border border-nofx-gold/40 bg-nofx-gold/10 px-2 py-1 text-xs text-nofx-text">
                  <Sparkles className="h-3.5 w-3.5 text-nofx-gold" />
                  {tr(language, '新点火：', 'New ignition: ')}
                  {alert.map((s) => s.replace('USDT', '')).join('、')}
                </div>
              ) : null}

              {visible.length ? (
                <>
                  {/* Desktop table */}
                  <table className="hidden w-full min-w-[940px] whitespace-nowrap text-sm lg:table">
                    <thead className="text-left text-xs text-nofx-text-muted">
                      <tr>
                        <th className="px-2 py-1.5 font-normal">#</th>
                        <th className="px-2 font-normal">{tr(language, '币种 / 跟踪', 'Coin / tracked')}</th>
                        {head('expected', tr(language, '预期最终', 'Expected'), tr(language, '预期收盘总涨幅 = 今日涨幅 + 预测剩余', 'expected close gain'))}
                        <th className="border-l border-[rgba(25,36,52,0.18)] px-2 pl-4 font-normal">{tr(language, '状态', 'State')}</th>
                        {head('day', tr(language, '今日涨幅', 'Day'), tr(language, '北京时间 0 点至今', 'since Beijing midnight'))}
                        {head('h24', tr(language, '滚动24h', '24h'), tr(language, '与币安一致的滚动 24h 涨幅', 'rolling 24h, matches Binance'))}
                        {head('rise', tr(language, '启动', 'Rise'), tr(language, '从今日最低点起算的时长', 'hours since today low'))}
                        {head('vol', tr(language, '量能突增', 'Vol x'), tr(language, '最近1小时成交量 ÷ 过去几天同一时段中位', 'last hour vol vs same-hour norm'))}
                        {head('oi', 'OI 1h', tr(language, '1 小时持仓量变化', 'open interest change, 1h'))}
                        {head('offhigh', tr(language, '距高点', 'Off high'), tr(language, '当前价低于今日高点的幅度，越小越强势', 'below day high, smaller is stronger'))}
                        <th className="border-l border-[rgba(25,36,52,0.18)] px-2 pl-4 font-normal">{tr(language, '触发', 'Signals')}</th>
                      </tr>
                    </thead>
                    <tbody className="text-nofx-text">
                      {visible.map((r, i) => (
                        <tr
                          key={r.symbol}
                          onClick={() => setSelected(r.symbol)}
                          className={`cursor-pointer border-t ${ui.border} ${r.symbol === current ? 'bg-nofx-gold/5' : 'hover:bg-nofx-bg-deeper'}`}
                        >
                          <td className="px-2 py-1.5 text-nofx-text-muted tabular-nums">{i + 1}</td>
                          <td className="px-2">{coinCell(r)}</td>
                          <td className="px-2 text-right align-middle">
                            <div className="font-semibold tabular-nums" style={{ color: BN.up }}>+{(r.predicted_final ?? 0).toFixed(1)}%</div>
                            <div className="text-[10px] text-nofx-text-muted">{tr(language, '空间', 'room')} +{(r.predicted_remaining ?? 0).toFixed(1)}%</div>
                          </td>
                          <td className="border-l border-[rgba(25,36,52,0.18)] px-2 pl-4">{stateBadge(r.state)}</td>
                          <td className="px-2 text-right tabular-nums" style={{ color: r.day_gain >= 0 ? BN.up : BN.down }}>{signed(r.day_gain * 100)}</td>
                          <td className="px-2 text-right tabular-nums" style={{ color: r.change_24h >= 0 ? BN.up : BN.down }}>{signed(r.change_24h)}</td>
                          <td className="px-2 text-right tabular-nums">{fmtTracked((r.rise_h ?? 0) * 60)}</td>
                          <td className="px-2 text-right font-semibold tabular-nums" style={{ color: (r.vol_spike_1h ?? 0) >= 3 ? BN.up : undefined }}>{(r.vol_spike_1h ?? 0).toFixed(1)}x</td>
                          <td className="px-2 text-right tabular-nums" style={{ color: r.oi_known ? (r.oi_change_1h >= 0 ? BN.up : BN.down) : undefined }}>{r.oi_known ? signed(r.oi_change_1h * 100) : '—'}</td>
                          <td className="px-2 text-right tabular-nums">{(r.distance_high ?? 0).toFixed(1)}%</td>
                          <td className="border-l border-[rgba(25,36,52,0.18)] px-2 pl-4">
                            <span className="flex flex-wrap gap-1">
                              {(r.tags ?? []).slice(0, 3).map((t) => (
                                <span key={t} className="rounded bg-nofx-bg-deeper px-1 text-[10px] text-nofx-text-muted">{tagLabel(language, t)}</span>
                              ))}
                            </span>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>

                  {/* Mobile cards */}
                  <div className="space-y-2 lg:hidden">
                    {visible.map((r) => (
                      <button
                        key={r.symbol}
                        type="button"
                        onClick={() => setSelected(r.symbol)}
                        className={`w-full rounded border ${ui.border} p-2 text-left ${r.symbol === current ? 'bg-nofx-gold/5' : ''}`}
                      >
                        <div className="flex items-center justify-between">
                          <div className="flex items-center gap-1.5">
                            <span className="font-semibold text-nofx-text">{r.symbol.replace('USDT', '')}</span>
                            {r.is_new ? <span className="rounded bg-nofx-gold/20 px-1 text-[10px] font-semibold text-nofx-gold">{tr(language, '新', 'NEW')}</span> : null}
                            {stateBadge(r.state)}
                          </div>
                          <span className="font-semibold tabular-nums" style={{ color: BN.up }}>+{(r.predicted_final ?? 0).toFixed(1)}%</span>
                        </div>
                        <div className="mt-1 grid grid-cols-4 gap-1 text-[11px] text-nofx-text-muted">
                          <span>{tr(language, '今日', 'Day')} <b style={{ color: r.day_gain >= 0 ? BN.up : BN.down }}>{signed(r.day_gain * 100)}</b></span>
                          <span>24h <b style={{ color: r.change_24h >= 0 ? BN.up : BN.down }}>{signed(r.change_24h)}</b></span>
                          <span>{tr(language, '量能', 'Vol')} <b>{(r.vol_spike_1h ?? 0).toFixed(1)}x</b></span>
                          <span>{tr(language, '启动', 'Rise')} <b>{fmtTracked((r.rise_h ?? 0) * 60)}</b></span>
                        </div>
                      </button>
                    ))}
                  </div>
                </>
              ) : (
                <p className={`py-8 text-center ${ui.muted}`}>
                  {rows.length
                    ? tr(language, '当前筛选没有结果，试试清除筛选。', 'No rows match the filters.')
                    : data?.reason
                      ? tr(language, `暂时没有符合条件的币：${data.reason}`, `Nothing qualifies: ${data.reason}`)
                      : tr(language, '正在扫描…', 'Scanning…')}
                </p>
              )}
            </section>

            <section className={`${ui.panel} min-w-0`}>
              <div className="mb-2 flex flex-wrap items-baseline gap-2">
                <h2 className="text-base font-semibold text-nofx-text">{tr(language, '妖币雷达', 'Monster radar')}</h2>
                <span className={`text-xs ${ui.muted}`}>
                  {tr(language, '成交量突增 + 高波动 + 持仓量增加，仍在早期。实测约 8% 会在当日涨到 50%+，多数日子可能为空，仅作观察。', 'Volume spike + volatility + rising OI, still early. About 8% reach +50% today; often empty. Watch only.')}
                </span>
              </div>
              {radar.length ? (
                <div className="max-w-[820px]">
                  <table className="w-full text-sm">
                    <thead className="text-left text-xs text-nofx-text-muted">
                      <tr>
                        <th className="px-2 py-1.5 font-normal">{tr(language, '币种', 'Coin')}</th>
                        <th className="px-2 text-right font-normal" title={tr(language, '最近1小时成交量 ÷ 同段中位', 'last hour vol vs same-hour norm')}>{tr(language, '量能突增', 'Vol x')}</th>
                        <th className="px-2 text-right font-normal">ATR</th>
                        <th className="px-2 text-right font-normal">{tr(language, '今日涨幅', 'Day')}</th>
                        <th className="px-2 text-right font-normal">OI 1h</th>
                        <th className="px-2 text-right font-normal">{tr(language, '预期最终', 'Expected')}</th>
                        <th className="px-2 text-right font-normal">{tr(language, '跟踪', 'Tracked')}</th>
                      </tr>
                    </thead>
                    <tbody className="text-nofx-text">
                      {radar.map((r) => (
                        <tr key={r.symbol} onClick={() => setSelected(r.symbol)} className={`cursor-pointer border-t ${ui.border} hover:bg-nofx-bg-deeper`}>
                          <td className="px-2 py-1.5 font-medium">{r.symbol.replace('USDT', '')}</td>
                          <td className="px-2 text-right font-semibold tabular-nums" style={{ color: (r.vol_spike_1h ?? 0) >= 3 ? BN.up : undefined }}>{(r.vol_spike_1h ?? 0).toFixed(1)}x</td>
                          <td className="px-2 text-right tabular-nums">{(r.atr_pct ?? 0).toFixed(1)}%</td>
                          <td className="px-2 text-right tabular-nums" style={{ color: r.day_gain >= 0 ? BN.up : BN.down }}>{signed(r.day_gain * 100)}</td>
                          <td className="px-2 text-right tabular-nums" style={{ color: r.oi_known ? (r.oi_change_1h >= 0 ? BN.up : BN.down) : undefined }}>{r.oi_known ? signed(r.oi_change_1h * 100) : '—'}</td>
                          <td className="px-2 text-right font-semibold tabular-nums" style={{ color: BN.up }}>+{(r.predicted_final ?? 0).toFixed(1)}%</td>
                          <td className="px-2 text-right tabular-nums">{fmtTracked(r.tracked_min)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <p className={`py-4 text-center text-xs ${ui.muted}`}>
                  {tr(language, '当前没有点火信号（多数时候如此，属正常）。', 'No ignition signal right now (normal most of the time).')}
                </p>
              )}
            </section>

            {blasted.length ? (
              <section className={`${ui.panel} min-w-0`}>
                <div className="mb-2 flex items-baseline gap-2">
                  <h2 className="text-base font-semibold text-nofx-text">{tr(language, '已爆发', 'Already blasted')}</h2>
                  <span className={`text-xs ${ui.muted}`}>{tr(language, '今日已涨 50% 以上，不宜再追，仅供参考', 'Up 50%+ today: not an entry, reference only')}</span>
                </div>
                <table className="whitespace-nowrap text-sm">
                  <thead className="text-left text-xs text-nofx-text-muted">
                    <tr>
                      <th className="px-2 py-1.5 font-normal">{tr(language, '币种', 'Coin')}</th>
                      <th className="px-2 text-right font-normal">{tr(language, '今日涨幅', 'Day')}</th>
                      <th className="px-2 text-right font-normal">{tr(language, '滚动24h', '24h')}</th>
                      <th className="px-2 text-right font-normal">OI 1h</th>
                      <th className="px-2 text-right font-normal">{tr(language, '状态', 'State')}</th>
                    </tr>
                  </thead>
                  <tbody className="text-nofx-text">
                    {blasted.map((r) => (
                      <tr key={r.symbol} onClick={() => setSelected(r.symbol)} className={`cursor-pointer border-t ${ui.border} hover:bg-nofx-bg-deeper`}>
                        <td className="px-2 py-1.5 font-medium">{r.symbol.replace('USDT', '')}</td>
                        <td className="px-2 text-right tabular-nums" style={{ color: r.day_gain >= 0 ? BN.up : BN.down }}>{signed(r.day_gain * 100)}</td>
                        <td className="px-2 text-right tabular-nums" style={{ color: r.change_24h >= 0 ? BN.up : BN.down }}>{signed(r.change_24h)}</td>
                        <td className="px-2 text-right tabular-nums" style={{ color: r.oi_known ? (r.oi_change_1h >= 0 ? BN.up : BN.down) : undefined }}>{r.oi_known ? signed(r.oi_change_1h * 100) : '—'}</td>
                        <td className="px-2 text-right">{stateBadge(r.state)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </section>
            ) : null}
          </div>

          <aside className="space-y-3 lg:sticky lg:top-4">
            <section className={ui.panel}>
              <div className="mb-2 flex items-baseline gap-2">
                <h2 className="text-base font-semibold text-nofx-text">{current ? current.replace('USDT', '') : '--'}</h2>
                {row ? <PriceTag price={row.price} decimals={decimals} /> : null}
                <div className="ml-auto flex gap-1">
                  {['5m', '15m', '1h', '4h'].map((iv) => (
                    <button key={iv} type="button" className={`${ui.btnGhost} ${iv === chartInterval ? 'border-nofx-gold text-nofx-gold' : ''}`} onClick={() => setChartInterval(iv)}>
                      {iv}
                    </button>
                  ))}
                </div>
              </div>
              {candles.length ? (
                <BinanceChart candles={candles} decimals={decimals} main={main} subs={subs} levels={{}} resetKey={`${current}:${chartInterval}`} height={320} />
              ) : (
                <p className={`py-10 text-center ${ui.muted}`}>{tr(language, '加载 K 线…', 'Loading candles…')}</p>
              )}
            </section>

            {row ? (
              <section className={ui.panel}>
                <div className="mb-2 grid grid-cols-2 gap-x-3 gap-y-1 text-xs">
                  <div className="flex justify-between">
                    <span className={ui.muted}>{tr(language, '预期最终', 'Expected')}</span>
                    <span className="font-semibold tabular-nums" style={{ color: BN.up }}>+{(row.predicted_final ?? 0).toFixed(1)}%</span>
                  </div>
                  <div className="flex justify-between">
                    <span className={ui.muted}>{tr(language, '潜在空间', 'Room left')}</span>
                    <span className="tabular-nums" style={{ color: BN.up }}>+{(row.predicted_remaining ?? 0).toFixed(1)}%</span>
                  </div>
                  <div className="col-span-2">
                    <PotentialBar value={row.predicted_remaining ?? 0} />
                  </div>
                  <div className="flex justify-between">
                    <span className={ui.muted}>{tr(language, '今日涨幅', 'Day gain')}</span>
                    <span className="tabular-nums">{signed(row.day_gain * 100)}</span>
                  </div>
                  <div className="flex justify-between">
                    <span className={ui.muted}>{tr(language, '启动时长', 'Rise time')}</span>
                    <span className="tabular-nums">{fmtTracked((row.rise_h ?? 0) * 60)}</span>
                  </div>
                  <div className="flex justify-between">
                    <span className={ui.muted}>{tr(language, '跟踪时长', 'Tracked')}</span>
                    <span className="tabular-nums">{fmtTracked(row.tracked_min)}</span>
                  </div>
                  <div className="flex justify-between">
                    <span className={ui.muted}>{tr(language, '量能突增', 'Vol x')}</span>
                    <span className="tabular-nums">{(row.vol_spike_1h ?? 0).toFixed(1)}x</span>
                  </div>
                  <div className="flex justify-between">
                    <span className={ui.muted}>{tr(language, '可交易性', 'Tradability')}</span>
                    <span className="tabular-nums" style={{ color: row.tradability === 'good' ? BN.up : row.tradability === 'fair' ? '#b45309' : BN.down }}>
                      {row.tradability === 'good' ? tr(language, '好', 'Good') : row.tradability === 'fair' ? tr(language, '一般', 'Fair') : tr(language, '偏薄', 'Thin')}
                    </span>
                  </div>
                </div>
                <h3 className={`${ui.muted} mb-2`}>{tr(language, '动量构成', 'Momentum parts')}</h3>
                <div className="grid grid-cols-5 gap-2 text-xs">
                  <Part label={tr(language, '速度', 'Speed')} value={row.speed} />
                  <Part label={tr(language, '量能', 'Volume')} value={row.volume} />
                  <Part label={tr(language, '持仓量', 'OI')} value={row.open_interest} />
                  <Part label={tr(language, '结构', 'Structure')} value={row.structure} />
                  <Part label={tr(language, '相对强度', 'Rel. str.')} value={row.rel_strength} />
                </div>
                <dl className="mt-3 grid grid-cols-2 gap-x-3 gap-y-1 text-xs">
                  <div className="flex justify-between"><dt className={ui.muted}>ATR(5m)</dt><dd className="tabular-nums">{row.atr_pct.toFixed(2)}%</dd></div>
                  <div className="flex justify-between"><dt className={ui.muted}>{tr(language, '距 24h 高点', 'Off 24h high')}</dt><dd className="tabular-nums">{row.distance_high.toFixed(1)}%</dd></div>
                  <div className="flex justify-between"><dt className={ui.muted}>EMA20</dt><dd className="tabular-nums">{row.ema20.toFixed(2)}</dd></div>
                  <div className="flex justify-between"><dt className={ui.muted}>EMA60</dt><dd className="tabular-nums">{row.ema60.toFixed(2)}</dd></div>
                </dl>
                <p className={`${ui.muted} mt-2`}>
                  {tr(language, '分数只说明"现在像不像一个刚开始的爆发"，不构成入场建议；过热和追高的币会被压分。', 'The score says how much this looks like a fresh burst, not a buy. Overheated and chased coins are discounted.')}
                </p>
              </section>
            ) : null}
          </aside>
        </div>
      )}
    </div>
  )
}

// algebraGuard renders the fetch error as a panel, or nothing.
function algebraGuard(error: unknown) {
  if (!error) return null
  return (
    <p className={`${ui.panel} text-sm text-nofx-danger`}>
      {'行情拉取失败：'}
      {error instanceof Error ? error.message : String(error)}
    </p>
  )
}
