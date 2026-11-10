// Frontier workspace chart on the free, open-source lightweight-charts
// library. It replaces the licensed TradingView Charting Library widget and
// keeps the same WorkspaceChart surface used by TradingPlanningPage.
import {
  CandlestickSeries,
  CrosshairMode,
  HistogramSeries,
  createChart,
  type AutoscaleInfo,
  type IChartApi,
  type IPriceLine,
  type IPrimitivePaneRenderer,
  type IPrimitivePaneView,
  type ISeriesApi,
  type ISeriesPrimitive,
  type Logical,
  type SeriesAttachedParameter,
  type Time,
  type UTCTimestamp,
} from 'lightweight-charts'
import type { ChartContext, Drawing, Plan } from './planning'

const resolutions: Record<string, string> = {
  1: '1m',
  5: '5m',
  15: '15m',
  60: '1h',
  240: '4h',
  '1D': '1d',
}
const stepSeconds: Record<string, number> = {
  1: 60,
  5: 300,
  15: 900,
  60: 3600,
  240: 14400,
  '1D': 86400,
}
const venues = ['binance', 'okx', 'bitget', 'hyperliquid']
const symbolPattern = /^(?:[a-z0-9]{1,16}:)?[A-Z0-9\p{Script=Han}-]{1,40}$/u
const UP = '#2E8B57'
const DOWN = '#D6433A'
const ENTRY = '#9b6a14'
const COMPARE = ['#9b6a14', '#3575a8', '#8050a1', '#237a69']
const MAX_ROWS = 4800

type Row = {
  time: number
  open: string
  high: string
  low: string
  close: string
  volume: string
}
/** Builds the candle query for one venue-native market, validating it first. */
export function chartQuery(
  venue: string,
  symbol: string,
  resolution: string,
  extra: Record<string, string> = {}
): string {
  if (!venues.includes(venue) || !symbolPattern.test(symbol))
    throw new Error('Invalid symbol format')
  if (!resolutions[resolution]) throw new Error('Unsupported chart timeframe')
  return new URLSearchParams({
    venue,
    symbol,
    interval: resolutions[resolution],
    ...extra,
  }).toString()
}

type Shape =
  | {
      kind: 'rect'
      t1: number
      t2: number
      p1: number
      p2: number
      color: string
      alpha: number
    }
  | {
      kind: 'segment'
      t1: number
      t2: number
      p1: number
      p2: number
      color: string
      width: number
      dashed: boolean
    }
  | {
      kind: 'text'
      t: number
      p: number
      text: string
      color: string
      size: number
      bold: boolean
    }
type Target = Parameters<IPrimitivePaneRenderer['draw']>[0]

// Draws plan zones, comparison lanes and AI levels in chart coordinates.
// Rectangles go behind the candles; lines and labels on top.
class Overlay implements ISeriesPrimitive<Time> {
  shapes: Shape[] = []
  private series: ISeriesApi<'Candlestick'> | null = null
  private requestUpdate: (() => void) | null = null
  constructor(private readonly x: (time: number) => number | null) {}
  attached(param: SeriesAttachedParameter<Time>) {
    this.series = param.series as ISeriesApi<'Candlestick'>
    this.requestUpdate = param.requestUpdate
  }
  detached() {
    this.series = null
    this.requestUpdate = null
  }
  set(shapes: Shape[]) {
    this.shapes = shapes
    this.requestUpdate?.()
  }
  update() {
    this.requestUpdate?.()
  }
  paneViews(): IPrimitivePaneView[] {
    return [
      {
        zOrder: () => 'bottom',
        renderer: () => ({ draw: (target) => this.paint(target, true) }),
      },
      {
        zOrder: () => 'top',
        renderer: () => ({ draw: (target) => this.paint(target, false) }),
      },
    ]
  }
  private paint(target: Target, background: boolean) {
    const series = this.series
    if (!series) return
    const y = (price: number) => series.priceToCoordinate(price)
    target.useMediaCoordinateSpace(({ context: ctx }) => {
      for (const s of this.shapes) {
        if ((s.kind === 'rect') !== background) continue
        if (s.kind === 'text') {
          const px = this.x(s.t),
            py = y(s.p)
          if (px === null || py === null) continue
          ctx.font = `${s.bold ? '600 ' : ''}${s.size}px sans-serif`
          ctx.fillStyle = s.color
          ctx.textBaseline = 'bottom'
          ctx.fillText(s.text, px + 2, py - 2)
          continue
        }
        const x1 = this.x(s.t1),
          x2 = this.x(s.t2),
          y1 = y(s.p1),
          y2 = y(s.p2)
        if (x1 === null || x2 === null || y1 === null || y2 === null) continue
        if (s.kind === 'rect') {
          ctx.globalAlpha = s.alpha
          ctx.fillStyle = s.color
          ctx.fillRect(
            Math.min(x1, x2),
            Math.min(y1, y2),
            Math.abs(x2 - x1),
            Math.abs(y2 - y1)
          )
          ctx.globalAlpha = 1
        } else {
          ctx.strokeStyle = s.color
          ctx.lineWidth = s.width
          ctx.setLineDash(s.dashed ? [4, 4] : [])
          ctx.beginPath()
          ctx.moveTo(x1, y1)
          ctx.lineTo(x2, y2)
          ctx.stroke()
          ctx.setLineDash([])
        }
      }
    })
  }
}
type Options = {
  venue: string
  symbol: string
  resolution?: string
  onStatus?: (message: string, warning: boolean) => void
  onPrice?: (candle: Row, data: unknown) => void
  onContext?: (context: ChartContext) => void
  onError?: (message: string) => void
  onAvailability?: (available: boolean, message?: string) => void
}

export class WorkspaceChart {
  venue: string
  symbol: string
  resolution: string
  private readonly onStatus: (message: string, warning: boolean) => void
  private readonly onPrice: (candle: Row, data: unknown) => void
  private readonly onContext: (context: ChartContext) => void
  private readonly onError: (message: string) => void
  private readonly onAvailability: (
    available: boolean,
    message?: string
  ) => void
  private chart: IChartApi | null = null
  private candles: ISeriesApi<'Candlestick'> | null = null
  private volume: ISeriesApi<'Histogram'> | null = null
  private overlay: Overlay | null = null
  private host: HTMLElement | null = null
  private toolbar: HTMLElement | null = null
  private rows: Row[] = []
  private times: number[] = []
  private exhausted = false
  private loadingOlder = false
  private reloading = false
  private source: EventSource | null = null
  private session = 0
  private lastReceived = 0
  private ageTimer: ReturnType<typeof setInterval> | undefined
  private disposed = false
  private loadError = ''
  private plan: Plan | null = null
  private planPhase = 'draft'
  private comparisons: Plan[] = []
  private planKey = ''
  private planLines: IPriceLine[] = []
  private aiShapes: Shape[] = []
  private manualShapes: Shape[] = []
  private focusLevels: number[] = []
  private readonly ready: Promise<void>

  constructor(options: Options) {
    this.venue = options.venue
    this.symbol = options.symbol
    this.resolution =
      options.resolution && resolutions[options.resolution]
        ? options.resolution
        : '60'
    this.onStatus = options.onStatus || (() => {})
    this.onPrice = options.onPrice || (() => {})
    this.onContext = options.onContext || (() => {})
    this.onError = options.onError || (() => {})
    this.onAvailability = options.onAvailability || (() => {})
    const container = document.getElementById('chart-container')
    if (!container) {
      this.loadError = 'Chart container is missing. Reload the page.'
      this.onAvailability(false, this.loadError)
      this.onError(this.loadError)
      this.ready = Promise.resolve()
      return
    }
    this.toolbar = document.createElement('div')
    this.toolbar.className = 'planning-chart-toolbar'
    this.toolbar.addEventListener('click', (event) => {
      const button = (event.target as HTMLElement).closest('button')
      const r = button?.dataset.resolution
      if (r) void this.changeResolution(r)
    })
    this.host = document.createElement('div')
    this.host.className = 'planning-chart-canvas'
    container.replaceChildren(this.toolbar, this.host)
    this.renderToolbar()
    this.chart = createChart(this.host, {
      autoSize: true,
      layout: {
        background: { color: '#FFFFFF' },
        textColor: '#7a8275',
        fontSize: 11,
      },
      grid: {
        vertLines: { color: '#eef0eb' },
        horzLines: { color: '#eef0eb' },
      },
      rightPriceScale: { borderVisible: false },
      timeScale: {
        borderVisible: false,
        timeVisible: true,
        secondsVisible: false,
        rightOffset: 8,
      },
      crosshair: { mode: CrosshairMode.Normal },
    })
    this.candles = this.chart.addSeries(CandlestickSeries, {
      upColor: UP,
      downColor: DOWN,
      borderUpColor: UP,
      borderDownColor: DOWN,
      wickUpColor: UP,
      wickDownColor: DOWN,
      // After "focus plan", keep every plan level inside the price scale.
      autoscaleInfoProvider: (base: () => AutoscaleInfo | null) => {
        const info = base()
        if (!info?.priceRange || !this.focusLevels.length) return info
        return {
          ...info,
          priceRange: {
            minValue: Math.min(info.priceRange.minValue, ...this.focusLevels),
            maxValue: Math.max(info.priceRange.maxValue, ...this.focusLevels),
          },
        }
      },
    })
    this.volume = this.chart.addSeries(HistogramSeries, {
      priceScaleId: 'volume',
      priceFormat: { type: 'volume' },
      lastValueVisible: false,
      priceLineVisible: false,
    })
    this.chart
      .priceScale('volume')
      .applyOptions({ scaleMargins: { top: 0.82, bottom: 0 } })
    this.overlay = new Overlay((time) => this.timeToX(time))
    this.candles.attachPrimitive(this.overlay)
    this.chart.timeScale().subscribeVisibleLogicalRangeChange((range) => {
      if (range && range.from < 20) void this.loadOlder()
    })
    this.ageTimer = setInterval(() => {
      if (this.lastReceived && Date.now() - this.lastReceived > 15000)
        this.onStatus('Market updates interrupted · stale data', true)
    }, 1000)
    this.ready = Promise.resolve()
  }

  private renderToolbar() {
    if (!this.toolbar) return
    this.toolbar.replaceChildren(
      ...Object.entries(resolutions).map(([r, label]) => {
        const button = document.createElement('button')
        button.type = 'button'
        button.dataset.resolution = r
        button.textContent = label
        button.setAttribute('aria-pressed', String(r === this.resolution))
        return button
      })
    )
  }

  // Maps a unix time to an x pixel, interpolating between candles and
  // extrapolating past the last one (plan zones extend into the future).
  private timeToX(time: number): number | null {
    const chart = this.chart,
      times = this.times
    if (!chart || times.length < 2) return null
    let lo = 0,
      hi = times.length - 1
    let logical: number
    if (time <= times[0]) logical = (time - times[0]) / (times[1] - times[0])
    else if (time >= times[hi])
      logical = hi + (time - times[hi]) / (times[hi] - times[hi - 1])
    else {
      while (hi - lo > 1) {
        const mid = (lo + hi) >> 1
        if (times[mid] <= time) lo = mid
        else hi = mid
      }
      logical = lo + (time - times[lo]) / (times[hi] - times[lo])
    }
    return chart.timeScale().logicalToCoordinate(logical as Logical)
  }

  context(): ChartContext {
    const out: ChartContext = {
      venue: this.venue,
      symbol: this.symbol,
      interval: resolutions[this.resolution],
      resolution: this.resolution,
    }
    try {
      const range = this.chart?.timeScale().getVisibleRange()
      if (range) {
        const from = Math.max(0, Math.floor(Number(range.from)))
        const to = Math.min(
          Math.floor(Number(range.to)),
          Math.floor(Date.now() / 1000) + 1
        )
        if (to > from) {
          out.visible_from = from
          out.visible_to = to
        }
      }
    } catch {
      /* the time scale has no data yet */
    }
    return out
  }

  async whenReady() {
    await this.ready
    if (!this.chart || this.disposed)
      throw new Error(this.loadError || 'Chart is not ready yet. Try again.')
  }
  async setContext(
    venue: string,
    symbol: string,
    resolution = this.resolution
  ) {
    chartQuery(venue, symbol, resolutions[resolution] ? resolution : '60')
    await this.whenReady()
    this.plan = null
    this.comparisons = []
    this.planKey = ''
    this.manualShapes = []
    this.venue = venue
    this.symbol = symbol
    await this.reload(resolutions[resolution] ? resolution : '60')
  }

  private async changeResolution(r: string) {
    if (!resolutions[r] || r === this.resolution || this.disposed) return
    try {
      await this.reload(r)
    } catch (e) {
      this.onError((e as Error).message)
    }
  }

  // Loads a fresh market or timeframe. The plan stays, AI levels are
  // timeframe-specific and are cleared.
  private async reload(resolution: string) {
    this.closeFeed()
    const session = ++this.session
    this.resolution = resolution
    this.aiShapes = []
    this.focusLevels = []
    this.planKey = ''
    this.clearPlanLines()
    this.overlay?.set([])
    this.rows = []
    this.times = []
    this.exhausted = false
    this.lastReceived = 0
    this.candles?.setData([])
    this.volume?.setData([])
    this.renderToolbar()
    this.onContext(this.context())
    this.onAvailability(false, 'Loading ' + this.symbol + ' candles…')
    this.onStatus('Connecting market feed…', false)
    void this.loadPrecision(session)
    let timer: ReturnType<typeof setTimeout> | undefined
    const timeout = new Promise<never>((_, reject) => {
      timer = setTimeout(
        () =>
          reject(
            new Error(
              'Market chart timed out. Check the connection and try again.'
            )
          ),
        20000
      )
    })
    try {
      await Promise.race([this.loadHistory(session), timeout])
    } finally {
      clearTimeout(timer)
    }
  }

  private async json<T>(path: string): Promise<T> {
    const response = await fetch(path, { credentials: 'same-origin' })
    const data = await response.json()
    if (!response.ok)
      throw new Error(data.error || 'Market data request failed')
    return data
  }

  // The candle endpoint caps pages at 300; walk backwards for more.
  private async history(to: number, count: number) {
    const { venue, symbol, resolution } = this
    const rows = new Map<number, Row>()
    let cursor = Math.min(to, Math.floor(Date.now() / 1000) + 1),
      exhausted = false,
      data: { candles?: Row[] } = {}
    for (let page = 0; page < 16 && rows.size < count; page++) {
      const query = chartQuery(venue, symbol, resolution, {
        limit: String(Math.min(300, count - rows.size)),
        to: String(cursor),
      })
      const next = await this.json<{ candles?: Row[] }>(
        '/api/planning/candles?' + query
      )
      if (!page) data = next
      const batch = (next.candles || [])
        .filter((r) => r.time < cursor)
        .sort((a, b) => a.time - b.time)
      if (!batch.length) {
        exhausted = true
        break
      }
      for (const row of batch) rows.set(row.time, row)
      cursor = batch[0].time
    }
    return {
      data,
      candles: [...rows.values()].sort((a, b) => a.time - b.time),
      exhausted,
    }
  }

  private async loadHistory(session: number) {
    try {
      const result = await this.history(Math.floor(Date.now() / 1000) + 1, 500)
      if (session !== this.session || this.disposed) return
      this.exhausted = result.exhausted
      if (!result.candles.length) {
        this.onStatus('No candles available for this market', true)
        this.onAvailability(
          false,
          'The exchange returned no candles for this market. Check the symbol and try again.'
        )
        this.connectFeed(session)
        return
      }
      this.apply(result.candles)
      const last = result.candles.length - 1
      this.chart
        ?.timeScale()
        .setVisibleLogicalRange({ from: Math.max(0, last - 150), to: last + 8 })
      this.onAvailability(true)
      this.onPrice(result.candles[last], result.data)
      this.onContext(this.context())
      this.render()
      this.connectFeed(session)
    } catch (e) {
      if (session !== this.session || this.disposed) return
      const message = (e as Error).message
      this.onAvailability(
        false,
        'Could not load candles for this market:' + message
      )
      this.onStatus('Market data read failed', true)
      this.onError(message)
    }
  }

  private apply(rows: Row[]) {
    this.rows = rows.slice(-MAX_ROWS)
    this.times = this.rows.map((r) => r.time)
    this.candles?.setData(
      this.rows.map((r) => ({
        time: r.time as UTCTimestamp,
        open: Number(r.open),
        high: Number(r.high),
        low: Number(r.low),
        close: Number(r.close),
      }))
    )
    this.volume?.setData(this.rows.map((r) => this.volumeBar(r)))
  }

  private volumeBar(r: Row) {
    return {
      time: r.time as UTCTimestamp,
      value: Number(r.volume),
      color:
        Number(r.close) >= Number(r.open)
          ? 'rgba(46,139,87,0.35)'
          : 'rgba(214,67,58,0.35)',
    }
  }

  // Scrolling to the left edge pages in older history.
  private async loadOlder() {
    if (
      this.loadingOlder ||
      this.exhausted ||
      !this.rows.length ||
      this.rows.length >= MAX_ROWS
    )
      return
    this.loadingOlder = true
    const session = this.session
    try {
      const result = await this.history(this.rows[0].time, 300)
      if (session !== this.session || this.disposed) return
      const first = this.rows[0].time
      const merged = new Map(this.rows.map((r) => [r.time, r]))
      for (const r of result.candles) merged.set(r.time, r)
      if (merged.size === this.rows.length) {
        this.exhausted = true
        return
      }
      const range = this.chart?.timeScale().getVisibleLogicalRange()
      this.apply([...merged.values()].sort((a, b) => a.time - b.time))
      // Keep the viewport on the same candles after prepending.
      const added = this.times.indexOf(first)
      if (range && added > 0)
        this.chart?.timeScale().setVisibleLogicalRange({
          from: range.from + added,
          to: range.to + added,
        })
      this.exhausted = result.exhausted
      this.overlay?.update()
    } catch {
      /* older history is optional; the next scroll retries */
    } finally {
      this.loadingOlder = false
    }
  }

  private async loadPrecision(session: number) {
    try {
      const quote = await this.json<{ tick?: string }>(
        '/api/planning/instrument?' +
          new URLSearchParams({ venue: this.venue, symbol: this.symbol })
      )
      if (session !== this.session || !quote.tick) return
      const tick = Number(quote.tick)
      const precision = (
        String(quote.tick).replace(/0+$/, '').split('.')[1] || ''
      ).length
      if (tick > 0)
        this.candles?.applyOptions({
          priceFormat: { type: 'price', precision, minMove: tick },
        })
    } catch {
      /* Axis precision is display-only; plans validate native rules separately. */
    }
  }

  private connectFeed(session: number) {
    if (this.disposed || session !== this.session) return
    const source = new EventSource(
      '/api/planning/feed?' +
        chartQuery(this.venue, this.symbol, this.resolution, { limit: '5' })
    )
    this.source = source
    source.onmessage = (event) => this.receive(source, session, event)
    source.onerror = () => {
      if (this.source === source)
        this.onStatus('Market feed disconnected · reconnecting', true)
    }
  }

  private closeFeed() {
    this.source?.close()
    this.source = null
  }

  private receive(source: EventSource, session: number, event: MessageEvent) {
    if (this.source !== source || session !== this.session || this.disposed)
      return
    try {
      const message = JSON.parse(event.data)
      if (message.type === 'feed_error') {
        this.onStatus('Market feed unavailable · reconnecting', true)
        if (!this.rows.length)
          this.onAvailability(
            false,
            'Could not load candles for this market:' +
              (message.data?.message || 'Market feed connection failed')
          )
        return
      }
      if (message.type !== 'candles') return
      const data = message.data
      const rows: Row[] = (data.candles || [])
        .slice()
        .sort((a: Row, b: Row) => a.time - b.time)
      if (!rows.length) return
      if (!this.rows.length) {
        void this.backfill(session)
        return
      }
      const last = this.rows[this.rows.length - 1].time
      const fresh = rows.filter((r) => r.time >= last)
      if (!fresh.length) return
      if (fresh[0].time > last + stepSeconds[this.resolution]) {
        // Never fabricate OHLC across a missing interval: reload instead.
        this.onStatus('Backfilling missing candles…', true)
        void this.backfill(session)
        return
      }
      for (const r of fresh) {
        if (r.time === last) this.rows[this.rows.length - 1] = r
        else {
          this.rows.push(r)
          this.times.push(r.time)
        }
        this.candles?.update({
          time: r.time as UTCTimestamp,
          open: Number(r.open),
          high: Number(r.high),
          low: Number(r.low),
          close: Number(r.close),
        })
        this.volume?.update(this.volumeBar(r))
      }
      this.lastReceived = Date.now()
      this.onAvailability(true)
      this.onStatus(
        data.stale
          ? 'Market data is stale'
          : data.mode === 'demo'
            ? 'Demo data · not live prices'
            : 'Market feed active · updates every 3 seconds',
        !!data.stale
      )
      this.onPrice(fresh[fresh.length - 1], data)
      this.overlay?.update()
    } catch {
      this.onStatus('Invalid market data', true)
    }
  }

  private async backfill(session: number) {
    if (this.reloading) return
    this.reloading = true
    try {
      const result = await this.history(Math.floor(Date.now() / 1000) + 1, 500)
      if (session !== this.session || this.disposed || !result.candles.length)
        return
      const merged = new Map(this.rows.map((r) => [r.time, r]))
      for (const r of result.candles) merged.set(r.time, r)
      const range = this.chart?.timeScale().getVisibleLogicalRange()
      const before = this.rows.length
      this.apply([...merged.values()].sort((a, b) => a.time - b.time))
      if (range && before) this.chart?.timeScale().setVisibleLogicalRange(range)
      this.lastReceived = Date.now()
      this.onAvailability(true)
      this.onPrice(result.candles[result.candles.length - 1], result.data)
      this.render()
    } catch (e) {
      this.onStatus('Candle gap detected · ' + (e as Error).message, true)
    } finally {
      this.reloading = false
    }
  }
  async plot(plan: Plan | null, phase = 'draft', comparisons: Plan[] = []) {
    await this.ready
    if (this.disposed) return
    this.comparisons = comparisons
      .filter(
        (p) =>
          p.snapshot.kind === 'perpetual' &&
          p.snapshot.venue === this.venue &&
          p.snapshot.symbol === this.symbol
      )
      .slice(0, 4)
    if (plan?.hash !== this.plan?.hash) this.focusLevels = []
    this.plan = plan
    this.planPhase = phase
    this.render()
  }

  private clearPlanLines() {
    for (const line of this.planLines) {
      try {
        this.candles?.removePriceLine(line)
      } catch {
        /* series already removed */
      }
    }
    this.planLines = []
  }

  // Rebuilds plan lines and overlay shapes from the current plan state.
  private render() {
    if (!this.candles || this.disposed) return
    const plan = this.plan
    const key =
      (plan ? plan.hash + this.planPhase : '') +
      this.comparisons.map((p) => p.hash).join(':') +
      '|' +
      this.times.length
    if (key === this.planKey) return
    this.planKey = key
    this.clearPlanLines()
    const shapes: Shape[] = []
    const comparing = this.comparisons.length >= 2
    const onChart =
      plan &&
      (!plan.snapshot.kind || plan.snapshot.kind === 'perpetual') &&
      plan.snapshot.venue === this.venue &&
      plan.snapshot.symbol === this.symbol
    if (onChart && this.times.length >= 2) {
      try {
        if (comparing) shapes.push(...this.comparisonShapes(plan.id))
        else shapes.push(...this.planShapes(plan))
      } catch (e) {
        this.onError('Could not draw the plan overlay:' + (e as Error).message)
      }
    }
    // AI levels are hidden while comparing structures, as before.
    if (!comparing) shapes.push(...this.aiShapes)
    shapes.push(...this.manualShapes)
    this.overlay?.set(shapes)
  }

  private planShapes(plan: Plan): Shape[] {
    const times = this.times
    const start = times[Math.max(0, times.length - 12)]
    const end = times[times.length - 1] + 8 * stepSeconds[this.resolution]
    const kind = plan.idea.execution?.kind
    const entries: [string, string, string][] =
      kind === 'ladder'
        ? plan.orders.map((o) => [
            'Limit order ' +
              o.index +
              ' · ' +
              Number(o.quantity).toLocaleString('en-US', {
                maximumFractionDigits: 8,
              }),
            o.price,
            ENTRY,
          ])
        : [
            [
              kind === 'twap'
                ? 'TWAP · ' +
                  plan.orders.length +
                  ' slices · T+0s to T+' +
                  Math.max(0, ...plan.orders.map((o) => o.offset_seconds)) +
                  's'
                : kind === 'market'
                  ? 'Est. market entry'
                  : 'Entry',
              plan.idea.entry,
              ENTRY,
            ],
          ]
    for (const [title, price, color] of [
      ...entries,
      ['Stop loss −' + Number(plan.risk).toFixed(2), plan.idea.stop, DOWN],
      ['Take profit +' + Number(plan.reward).toFixed(2), plan.idea.target, UP],
    ] as [string, string, string][]) {
      const value = Number(price)
      if (!Number.isFinite(value) || !this.candles) continue
      this.planLines.push(
        this.candles.createPriceLine({
          price: value,
          color,
          lineWidth: 2,
          lineStyle: 0,
          axisLabelVisible: true,
          title,
        })
      )
    }
    const entry = Number(plan.idea.entry)
    return [
      [plan.idea.stop, DOWN],
      [plan.idea.target, UP],
    ].map(([price, color]) => ({
      kind: 'rect' as const,
      t1: start,
      t2: end,
      p1: entry,
      p2: Number(price),
      color,
      alpha: 0.14,
    }))
  }

  // Up to four plans side by side in lanes across the visible candles.
  private comparisonShapes(selected: string): Shape[] {
    let times = this.times
    const visible = this.context()
    if (visible.visible_from && visible.visible_to) {
      const inView = times.filter(
        (t) => t >= visible.visible_from! && t <= visible.visible_to!
      )
      if (inView.length >= 6) times = inView
    }
    const plans = this.comparisons
    const span = Math.min(
      times.length - 1,
      Math.max(2, Math.floor((times.length - 1) * 0.72))
    )
    const base = times.length - 1 - span
    const lane = Math.max(2, Math.floor(span / plans.length))
    const fmt = (x: string | number) =>
      Number(x).toLocaleString('en-US', { maximumFractionDigits: 4 })
    const shapes: Shape[] = []
    plans.forEach((p, i) => {
      const orders = p.orders?.length
        ? p.orders
        : [{ price: p.idea.entry, quantity: p.quantity }]
      const total = orders.reduce((n, o) => n + Number(o.quantity), 0)
      const entry = total
        ? orders.reduce((n, o) => n + Number(o.price) * Number(o.quantity), 0) /
          total
        : Number(p.idea.entry)
      const t1 = times[Math.min(times.length - 2, base + i * lane)]
      const t2 = times[Math.min(times.length - 1, base + (i + 1) * lane - 1)]
      const active = p.id === selected
      const color = COMPARE[i]
      const tag = 'ABCD'[i]
      for (const [price, c] of [
        [p.idea.stop, DOWN],
        [p.idea.target, UP],
      ] as [string, string][])
        shapes.push({
          kind: 'rect',
          t1,
          t2,
          p1: entry,
          p2: Number(price),
          color: c,
          alpha: active ? 0.16 : 0.07,
        })
      const levels: [number, string][] = active
        ? orders.map((o) => [Number(o.price), color])
        : [[entry, color]]
      levels.push([Number(p.idea.stop), DOWN], [Number(p.idea.target), UP])
      for (const [price, c] of levels)
        shapes.push({
          kind: 'segment',
          t1,
          t2,
          p1: price,
          p2: price,
          color: c,
          width: active ? 2 : 1,
          dashed: !active,
        })
      const entryLabel =
        orders.length > 1 && p.idea.execution?.kind === 'ladder'
          ? 'Average entry ≈ '
          : p.idea.execution?.kind === 'market'
            ? 'Est. market entry '
            : 'Entry '
      const stopAt =
        times[
          Math.min(times.length - 1, base + i * lane + Math.floor(lane * 0.45))
        ]
      for (const [t, price, text, c, size] of [
        [
          t1,
          Number(p.idea.target),
          tag +
            ' · ' +
            (p.idea.side === 'long' ? 'Long' : 'Short') +
            ' Target ' +
            fmt(p.idea.target),
          UP,
          14,
        ],
        [t1, entry, entryLabel + fmt(entry), color, 14],
        [
          stopAt,
          Number(p.idea.stop),
          'Stop loss ' + fmt(p.idea.stop),
          DOWN,
          12,
        ],
      ] as [number, number, string, string, number][])
        shapes.push({
          kind: 'text',
          t,
          p: price,
          text,
          color: c,
          size,
          bold: active,
        })
    })
    return shapes
  }

  async focusPlan() {
    await this.whenReady()
    if (!this.plan || !this.chart) return
    const last = this.times.length - 1
    if (last > 0)
      this.chart
        .timeScale()
        .setVisibleLogicalRange({ from: Math.max(0, last - 64), to: last + 10 })
    this.focusLevels = [
      this.plan.idea.entry,
      this.plan.idea.stop,
      this.plan.idea.target,
      ...(this.plan.orders || []).map((o) => o.price),
      ...this.comparisons.flatMap((p) => [
        p.idea.entry,
        p.idea.stop,
        p.idea.target,
        ...(p.orders || []).map((o) => o.price),
      ]),
    ]
      .map(Number)
      .filter((n) => Number.isFinite(n) && n > 0)
    this.chart.priceScale('right').setAutoScale(true)
  }

  private annotationKind(d: Drawing) {
    if (/support|\bS[1-9]\b/i.test(d.label)) return 'support'
    if (/resistance|\bR[1-9]\b/i.test(d.label)) return 'resistance'
    return 'other'
  }

  // Drops levels that duplicate plan lines; keeps at most two supports and
  // two resistances so the chart stays readable.
  private selectAI(drawings: Drawing[]) {
    const plan = this.plan
    const levels =
      plan?.snapshot.venue === this.venue &&
      plan.snapshot.symbol === this.symbol
        ? [
            plan.idea.entry,
            plan.idea.stop,
            plan.idea.target,
            ...(plan.orders || []).map((o) => o.price),
          ].map(Number)
        : []
    const near = (a: number, b: number) =>
      Math.abs(a - b) <= Math.max(a, b) * 0.00015
    const picked: Drawing[] = []
    for (const d of drawings) {
      const price = Number(d.points[0]?.price)
      if (
        d.kind === 'horizontal_line' &&
        (levels.some((p) => near(p, price)) ||
          picked.some(
            (o) =>
              o.kind === 'horizontal_line' &&
              near(Number(o.points[0]?.price), price)
          ))
      )
        continue
      picked.push(d)
    }
    if (picked.length <= 4) return picked
    const support = picked.filter((d) => this.annotationKind(d) === 'support')
    const resistance = picked.filter(
      (d) => this.annotationKind(d) === 'resistance'
    )
    const balanced = support.slice(0, 2).concat(resistance.slice(0, 2))
    for (const d of picked)
      if (balanced.length < 4 && !balanced.includes(d)) balanced.push(d)
    return balanced
  }

  private drawingShapes(drawings: Drawing[], ai: boolean): Shape[] {
    const shapes: Shape[] = []
    const last = this.times[this.times.length - 1]
    const future = last + 8 * stepSeconds[this.resolution]
    for (const d of drawings) {
      const color = ai
        ? { support: UP, resistance: DOWN, other: '#3575a8' }[
            this.annotationKind(d)
          ]
        : '#3575a8'
      const pts = d.points.map((p) => ({
        time: Number(p.time),
        price: Number(p.price),
      }))
      if (!pts.length || pts.some((p) => !Number.isFinite(p.price))) continue
      if (d.kind === 'horizontal_line') {
        shapes.push({
          kind: 'segment',
          t1: this.times[0],
          t2: future,
          p1: pts[0].price,
          p2: pts[0].price,
          color,
          width: 1,
          dashed: ai,
        })
        if (!ai && d.label)
          shapes.push({
            kind: 'text',
            t: last,
            p: pts[0].price,
            text: d.label,
            color,
            size: 11,
            bold: false,
          })
      } else if (
        pts.length >= 2 &&
        (d.kind === 'trend_line' || d.kind === 'rectangle')
      ) {
        shapes.push(
          d.kind === 'rectangle'
            ? {
                kind: 'rect',
                t1: pts[0].time,
                t2: pts[1].time,
                p1: pts[0].price,
                p2: pts[1].price,
                color,
                alpha: 0.08,
              }
            : {
                kind: 'segment',
                t1: pts[0].time,
                t2: pts[1].time,
                p1: pts[0].price,
                p2: pts[1].price,
                color,
                width: 1,
                dashed: ai,
              }
        )
      }
    }
    return shapes
  }

  async draw(
    drawings: Drawing[],
    source = 'manual',
    interval = this.context().interval
  ) {
    await this.whenReady()
    if (this.disposed || interval !== resolutions[this.resolution]) return
    if (!this.times.length) return
    if (source === 'ai')
      this.aiShapes = this.drawingShapes(
        this.selectAI(drawings.slice(0, 10)),
        true
      )
    else this.manualShapes = this.drawingShapes(drawings.slice(0, 20), false)
    this.planKey = ''
    this.render()
  }

  dispose() {
    this.disposed = true
    this.session++
    clearInterval(this.ageTimer)
    this.closeFeed()
    this.clearPlanLines()
    this.chart?.remove()
    this.chart = null
    this.candles = null
    this.volume = null
    this.toolbar?.remove()
    this.host?.remove()
  }
}
