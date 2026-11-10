import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import {
  CandlestickSeries,
  ColorType,
  CrosshairMode,
  HistogramSeries,
  LineSeries,
  LineStyle,
  createChart,
  type IChartApi,
  type IPriceLine,
  type ISeriesApi,
  type MouseEventParams,
  type SeriesType,
  type Time,
  type UTCTimestamp,
} from 'lightweight-charts'
import {
  boll,
  ema,
  formatCompact,
  kdj,
  macd,
  rsi,
  sma,
  type Candle,
  type Point,
} from '../../utils/marketIndicators'

// Binance app layout (K-line on top, stacked sub-panes below) in the NOFX
// light theme. up/down match --color-nofx-success / --color-nofx-danger.
export const BN = {
  bg: '#ffffff',
  grid: '#edf1f7',
  border: 'rgba(25,36,52,0.14)',
  text: '#707e92',
  up: '#2e8b57',
  down: '#d6433a',
  line1: '#d97706',
  line2: '#c026d3',
  line3: '#295be8',
}

export type MainIndicator = 'MA' | 'EMA' | 'BOLL' | 'NONE'
export type SubIndicator = 'VOL' | 'MACD' | 'RSI' | 'KDJ'

export interface ChartLevels {
  entry?: number
  stopLoss?: number
  takeProfit?: number
}

/** An extra horizontal price line, e.g. a lead trader's entry. */
export interface PriceMarker {
  price: number
  color: string
  title: string
  /** 1-4 px */
  width?: number
  dashed?: boolean
  /** only the price-axis label, no line across the chart */
  labelOnly?: boolean
}

interface Props {
  candles: Candle[]
  decimals: number
  main: MainIndicator
  subs: SubIndicator[]
  levels?: ChartLevels
  markers?: PriceMarker[]
  /** Draws on top of the K-line pane, re-rendered on pan, zoom and resize. */
  overlay?: (p: ChartProjector) => ReactNode
  resetKey: string // changes when symbol/interval changes, to refit the view
  height?: number
}

/** Maps candle time and price to pixels in the K-line pane. */
export interface ChartProjector {
  /** x of the candle at unix seconds `time` (must be a candle's time) */
  x: (time: number) => number | null
  y: (price: number) => number | null
  width: number
  height: number
  /** pixels per candle */
  barSpacing: number
}

interface LegendItem {
  label: string
  value: string
  color: string
}

interface Line {
  label: string
  color: string
  points: Point[]
  hist?: boolean // MACD histogram
}

interface PaneSpec {
  lines: Line[]
  volume?: boolean
}

const toTime = (t: number) => t as UTCTimestamp

function mainLines(candles: Candle[], main: MainIndicator): Line[] {
  if (main === 'MA')
    return [7, 25, 99].map((p, i) => ({
      label: `MA(${p})`,
      color: [BN.line1, BN.line2, BN.line3][i],
      points: sma(candles, p),
    }))
  if (main === 'EMA')
    return [7, 25, 99].map((p, i) => ({
      label: `EMA(${p})`,
      color: [BN.line1, BN.line2, BN.line3][i],
      points: ema(candles, p),
    }))
  if (main === 'BOLL') {
    const b = boll(candles)
    return [
      { label: 'UP', color: BN.line2, points: b.upper },
      { label: 'MB', color: BN.line1, points: b.middle },
      { label: 'DN', color: BN.line3, points: b.lower },
    ]
  }
  return []
}

function subPane(candles: Candle[], sub: SubIndicator): PaneSpec {
  switch (sub) {
    case 'VOL':
      return {
        volume: true,
        lines: [
          {
            label: 'MA(5)',
            color: BN.line1,
            points: sma(candles, 5, (c) => c.volume),
          },
          {
            label: 'MA(10)',
            color: BN.line2,
            points: sma(candles, 10, (c) => c.volume),
          },
        ],
      }
    case 'MACD': {
      const m = macd(candles)
      return {
        lines: [
          { label: 'MACD', color: BN.text, points: m.hist, hist: true },
          { label: 'DIF', color: BN.line1, points: m.dif },
          { label: 'DEA', color: BN.line2, points: m.dea },
        ],
      }
    }
    case 'RSI':
      return {
        lines: [6, 12, 24].map((p, i) => ({
          label: `RSI(${p})`,
          color: [BN.line1, BN.line2, BN.line3][i],
          points: rsi(candles, p),
        })),
      }
    case 'KDJ': {
      const k = kdj(candles)
      return {
        lines: [
          { label: 'K', color: BN.line1, points: k.k },
          { label: 'D', color: BN.line2, points: k.d },
          { label: 'J', color: BN.line3, points: k.j },
        ],
      }
    }
  }
}

interface SeriesRefs {
  candle: ISeriesApi<'Candlestick'>
  main: ISeriesApi<'Line'>[]
  subs: { volume?: ISeriesApi<'Histogram'>; lines: ISeriesApi<SeriesType>[] }[]
}

const lineOpts = (color: string) => ({
  color,
  lineWidth: 1 as const,
  priceLineVisible: false,
  lastValueVisible: false,
  crosshairMarkerVisible: false,
})

export function BinanceChart({
  candles,
  decimals,
  main,
  subs,
  levels,
  markers,
  overlay,
  resetKey,
  height = 620,
}: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<IChartApi | null>(null)
  const seriesRef = useRef<SeriesRefs | null>(null)
  const priceLinesRef = useRef<IPriceLine[]>([])
  const markerLinesRef = useRef<IPriceLine[]>([])
  const fittedKeyRef = useRef('')
  const specRef = useRef<{
    main: Line[]
    subs: PaneSpec[]
    candles: Candle[]
    maps: Map<number, number>[][]
  }>({
    main: [],
    subs: [],
    candles: [],
    maps: [],
  })
  const [legends, setLegends] = useState<LegendItem[][]>([])
  // Bumped when the view moves so the overlay re-projects.
  const [viewTick, setViewTick] = useState(0)
  const [paneTops, setPaneTops] = useState<number[]>([])

  const fmtPrice = useCallback((v: number) => v.toFixed(decimals), [decimals])

  const updateLegend = useCallback(
    (time?: number) => {
      const { main: mLines, subs: sPanes, candles: cs, maps } = specRef.current
      if (cs.length === 0) return
      const idx =
        time === undefined
          ? cs.length - 1
          : cs.findIndex((c) => c.time === time)
      const c = cs[idx >= 0 ? idx : cs.length - 1]
      const prev = cs[(idx >= 0 ? idx : cs.length - 1) - 1]
      const change = prev ? ((c.close - prev.close) / prev.close) * 100 : 0
      const color = c.close >= c.open ? BN.up : BN.down
      const at = (pane: number, line: number) => maps[pane]?.[line]?.get(c.time)

      const top: LegendItem[] = [
        { label: '开', value: fmtPrice(c.open), color },
        { label: '高', value: fmtPrice(c.high), color },
        { label: '低', value: fmtPrice(c.low), color },
        { label: '收', value: fmtPrice(c.close), color },
        {
          label: '涨跌',
          value: `${change >= 0 ? '+' : ''}${change.toFixed(2)}%`,
          color,
        },
        ...mLines.map((l, i) => ({
          label: l.label,
          value: at(0, i) !== undefined ? fmtPrice(at(0, i)!) : '--',
          color: l.color,
        })),
      ]
      const rest = sPanes.map((p, pi) => {
        const fmt = (v: number | undefined) =>
          v === undefined
            ? '--'
            : p.volume
              ? formatCompact(v)
              : subs[pi] === 'MACD'
                ? v.toFixed(decimals)
                : v.toFixed(2)
        const items: LegendItem[] = p.volume
          ? [{ label: 'VOL', value: formatCompact(c.volume), color }]
          : []
        return items.concat(
          p.lines.map((l, li) => ({
            label: l.label,
            value: fmt(at(pi + 1, li)),
            color: l.color,
          }))
        )
      })
      setLegends([top, ...rest])
    },
    [decimals, fmtPrice, subs]
  )

  const measurePanes = useCallback(() => {
    const chart = chartRef.current
    const box = containerRef.current?.getBoundingClientRect()
    if (!chart || !box) return
    setPaneTops(
      chart
        .panes()
        .map(
          (p) =>
            (p.getHTMLElement()?.getBoundingClientRect().top ?? box.top) -
            box.top
        )
    )
  }, [])

  // Pushes the current props into the existing series.
  const apply = () => {
    const s = seriesRef.current
    const chart = chartRef.current
    if (!s || !chart) return
    const mLines = mainLines(candles, main)
    const sPanes = subs.map((sub) => subPane(candles, sub))
    const toMap = (pts: Point[]) => new Map(pts.map((p) => [p.time, p.value]))
    specRef.current = {
      main: mLines,
      subs: sPanes,
      candles,
      maps: [
        mLines.map((l) => toMap(l.points)),
        ...sPanes.map((p) => p.lines.map((l) => toMap(l.points))),
      ],
    }
    const pts = (points: Point[]) =>
      points.map((p) => ({ time: toTime(p.time), value: p.value }))

    s.candle.setData(
      candles.map((c) => ({
        time: toTime(c.time),
        open: c.open,
        high: c.high,
        low: c.low,
        close: c.close,
      }))
    )
    mLines.forEach((l, i) => s.main[i]?.setData(pts(l.points)))
    sPanes.forEach((p, pi) => {
      const refs = s.subs[pi]
      if (!refs) return
      refs.volume?.setData(
        candles.map((c) => ({
          time: toTime(c.time),
          value: c.volume,
          color: (c.close >= c.open ? BN.up : BN.down) + '99',
        }))
      )
      p.lines.forEach((l, li) => {
        const series = refs.lines[li]
        if (!series) return
        if (l.hist) {
          series.setData(
            l.points.map((pt) => ({
              time: toTime(pt.time),
              value: pt.value,
              color: pt.value >= 0 ? BN.up : BN.down,
            }))
          )
        } else {
          series.setData(pts(l.points))
        }
      })
    })
    if (candles.length > 0 && fittedKeyRef.current !== resetKey) {
      fittedKeyRef.current = resetKey
      chart.timeScale().setVisibleLogicalRange({
        from: Math.max(0, candles.length - 120),
        to: candles.length + 5,
      })
    }
    legendRef.current()
  }

  // Latest-props callbacks for chart event handlers and effects. Layout
  // effects run before the passive effects below, so these are always fresh.
  const legendRef = useRef(updateLegend)
  const applyRef = useRef(apply)
  useLayoutEffect(() => {
    legendRef.current = updateLegend
    applyRef.current = apply
  })

  const subsKey = subs.join(',')

  // Rebuild panes and series when the indicator selection changes.
  useEffect(() => {
    const el = containerRef.current
    if (!el) return
    const chart = createChart(el, {
      autoSize: true,
      layout: {
        background: { type: ColorType.Solid, color: BN.bg },
        textColor: BN.text,
        fontSize: 11,
        panes: {
          separatorColor: BN.border,
          separatorHoverColor: '#295be8',
          enableResize: true,
        },
      },
      grid: { vertLines: { color: BN.grid }, horzLines: { color: BN.grid } },
      crosshair: { mode: CrosshairMode.Normal },
      rightPriceScale: { borderColor: BN.border },
      timeScale: {
        borderColor: BN.border,
        timeVisible: true,
        secondsVisible: false,
        rightOffset: 6,
      },
    })
    chartRef.current = chart
    const priceFormat = {
      type: 'price' as const,
      precision: decimals,
      minMove: 1 / 10 ** decimals,
    }
    const ratioFormat = { type: 'price' as const, precision: 2, minMove: 0.01 }
    const candle = chart.addSeries(CandlestickSeries, {
      upColor: BN.up,
      downColor: BN.down,
      borderUpColor: BN.up,
      borderDownColor: BN.down,
      wickUpColor: BN.up,
      wickDownColor: BN.down,
      priceFormat,
    })
    const mainSeries = mainLines([], main).map((l) =>
      chart.addSeries(LineSeries, { ...lineOpts(l.color), priceFormat }, 0)
    )
    const subSeries = subs.map((sub, i) => {
      const spec = subPane([], sub)
      const pane = i + 1
      const volume = spec.volume
        ? chart.addSeries(
            HistogramSeries,
            {
              priceFormat: { type: 'volume' },
              priceLineVisible: false,
              lastValueVisible: false,
            },
            pane
          )
        : undefined
      const lines: ISeriesApi<SeriesType>[] = spec.lines.map((l) =>
        l.hist
          ? chart.addSeries(
              HistogramSeries,
              { priceFormat, priceLineVisible: false, lastValueVisible: false },
              pane
            )
          : chart.addSeries(
              LineSeries,
              {
                ...lineOpts(l.color),
                priceFormat: sub === 'MACD' ? priceFormat : ratioFormat,
              },
              pane
            )
      )
      return { volume, lines }
    })
    const panes = chart.panes()
    panes[0]?.setStretchFactor(3)
    panes.slice(1).forEach((p) => p.setStretchFactor(1))
    chart.subscribeCrosshairMove((param: MouseEventParams<Time>) =>
      legendRef.current(param.time as number | undefined)
    )
    let raf = 0
    const bump = () => {
      cancelAnimationFrame(raf)
      raf = requestAnimationFrame(() => setViewTick((t) => t + 1))
    }
    chart.timeScale().subscribeVisibleLogicalRangeChange(bump)
    chart.timeScale().subscribeSizeChange(bump)

    seriesRef.current = { candle, main: mainSeries, subs: subSeries }
    fittedKeyRef.current = ''
    applyRef.current()
    const ro = new ResizeObserver(() => {
      measurePanes()
      bump()
    })
    ro.observe(el)
    const timer = window.setTimeout(measurePanes, 0)
    return () => {
      window.clearTimeout(timer)
      cancelAnimationFrame(raf)
      ro.disconnect()
      chart.remove()
      chartRef.current = null
      seriesRef.current = null
      priceLinesRef.current = []
      markerLinesRef.current = []
    }
  }, [main, subsKey, decimals, measurePanes])

  useEffect(() => {
    applyRef.current()
  }, [candles, resetKey])

  // AI suggested levels as dashed price lines.
  useEffect(() => {
    const s = seriesRef.current
    if (!s) return
    priceLinesRef.current.forEach((l) => s.candle.removePriceLine(l))
    priceLinesRef.current = []
    const add = (price: number | undefined, color: string, title: string) => {
      if (price && price > 0) {
        priceLinesRef.current.push(
          s.candle.createPriceLine({
            price,
            color,
            lineWidth: 1,
            lineStyle: LineStyle.Dashed,
            axisLabelVisible: true,
            title,
          })
        )
      }
    }
    add(levels?.entry, BN.line1, '入场')
    add(levels?.stopLoss, BN.down, '止损')
    add(levels?.takeProfit, BN.up, '止盈')
  }, [
    levels?.entry,
    levels?.stopLoss,
    levels?.takeProfit,
    main,
    subsKey,
    decimals,
  ])

  // Extra price lines (e.g. lead traders' entries), solid.
  const markersKey = JSON.stringify(markers || [])
  useEffect(() => {
    const s = seriesRef.current
    if (!s) return
    markerLinesRef.current.forEach((l) => s.candle.removePriceLine(l))
    markerLinesRef.current = (markers || [])
      .filter((m) => m.price > 0)
      .map((m) =>
        s.candle.createPriceLine({
          price: m.price,
          color: m.color,
          lineWidth: Math.min(4, Math.max(1, Math.round(m.width || 1))) as
            | 1
            | 2
            | 3
            | 4,
          lineStyle: m.dashed ? LineStyle.Dashed : LineStyle.Solid,
          lineVisible: !m.labelOnly,
          axisLabelVisible: true,
          title: m.title,
        })
      )
    // markersKey stands for markers; main/subs/decimals rebuild the series.
  }, [markersKey, main, subsKey, decimals])

  // The price scale autoscales to new data on the next frame; re-project then.
  useEffect(() => {
    const id = requestAnimationFrame(() => setViewTick((t) => t + 1))
    return () => cancelAnimationFrame(id)
  }, [candles])

  // Re-project after every view move and data change (the chart updates
  // its coordinates synchronously in setData and range changes).
  const [proj, setProj] = useState<ChartProjector | null>(null)
  const hasOverlay = Boolean(overlay)
  useEffect(() => {
    const chart = chartRef.current
    const s = seriesRef.current
    const pane = chart?.panes()[0]
    if (!hasOverlay || !chart || !s || !pane || !candles.length) {
      setProj(null)
      return
    }
    const ts = chart.timeScale()
    const range = ts.getVisibleLogicalRange()
    const width = ts.width()
    setProj({
      x: (time) => ts.timeToCoordinate(toTime(time)),
      y: (price) => s.candle.priceToCoordinate(price),
      width,
      height: pane.getHeight(),
      barSpacing: range ? width / Math.max(1, range.to - range.from) : 6,
    })
  }, [hasOverlay, viewTick, candles, main, subsKey, decimals])

  return (
    <div className="relative w-full" style={{ height, background: BN.bg }}>
      <div ref={containerRef} className="absolute inset-0" />
      {overlay && proj ? (
        <div
          className="pointer-events-none absolute left-0 top-0 z-20 overflow-hidden"
          style={{ width: proj.width, height: proj.height }}
        >
          {overlay(proj)}
        </div>
      ) : null}
      {legends.map((items, i) => (
        <div
          key={i}
          className="pointer-events-none absolute left-2 right-16 z-10 flex flex-wrap gap-x-3 text-[11px] leading-4"
          style={{ top: (paneTops[i] ?? 0) + 4, color: BN.text }}
        >
          {items.map((it) => (
            <span key={it.label}>
              {it.label} <span style={{ color: it.color }}>{it.value}</span>
            </span>
          ))}
        </div>
      ))}
    </div>
  )
}
