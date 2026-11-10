import { NoticeText } from '../components/NoticeText'
import { useEffect, useMemo, useState } from 'react'
import { startVisiblePolling } from '../lib/visiblePolling'
import { useLanguage } from '../contexts/LanguageContext'
import { api } from '../lib/api'
import type { MarketAnalyzeResult, MarketTicker } from '../lib/api/market'
import type { AIModel, Exchange } from '../types'
import {
  BN,
  BinanceChart,
  type MainIndicator,
  type SubIndicator,
} from '../components/market/BinanceChart'
import { AiPanel } from '../components/market/AiPanel'
import { AutoPanel } from '../components/market/AutoPanel'
import {
  TradePanel,
  type OrderSuggestion,
} from '../components/market/TradePanel'
import { MarketList } from '../components/market/MarketList'
import {
  clearLegacyAnalyses,
  loadPref,
  savePref,
  mergeAnalyses,
  tr,
} from '../components/market/marketUi'
import {
  formatCompact,
  priceDecimals,
  type Candle,
} from '../utils/marketIndicators'

const intervals = ['1m', '5m', '15m', '30m', '1h', '4h', '1d', '1w']
const mains: MainIndicator[] = ['MA', 'EMA', 'BOLL', 'NONE']
const subOptions: SubIndicator[] = ['VOL', 'MACD', 'RSI', 'KDJ']
const normalize = (raw: string) => {
  const s = raw
    .trim()
    .toUpperCase()
    .replace(/[-_/\s]/g, '')
  return s && !s.endsWith('USDT') ? s + 'USDT' : s
}

export function MarketPage() {
  const { language } = useLanguage()
  const [symbol, setSymbol] = useState(() => loadPref('symbol', 'BTCUSDT'))
  const [interval, setChartInterval] = useState(() =>
    loadPref('interval', '15m')
  )
  const [main, setMain] = useState<MainIndicator>(() => {
    const saved = loadPref('main', 'MA') as MainIndicator
    return mains.includes(saved) ? saved : 'MA'
  })
  const [subs, setSubs] = useState<SubIndicator[]>(() =>
    loadPref('subs', 'VOL,MACD')
      .split(',')
      .filter((s): s is SubIndicator => subOptions.includes(s as SubIndicator))
  )
  const [data, setData] = useState<{ key: string; candles: Candle[] }>({
    key: '',
    candles: [],
  })
  const [ticker, setTicker] = useState<MarketTicker | null>(null)
  const [loadError, setLoadError] = useState('')
  const [models, setModels] = useState<AIModel[]>([])
  const [exchanges, setExchanges] = useState<Exchange[]>([])
  // Persisted per symbol; replaced only when that symbol is analyzed again.
  const [analyses, setAnalyses] = useState<Record<string, MarketAnalyzeResult>>(
    {}
  )
  const analysis = analyses[symbol] ?? null
  const [suggestion, setSuggestion] = useState<OrderSuggestion | null>(null)

  const key = `${symbol}|${interval}`

  useEffect(() => {
    clearLegacyAnalyses()
    api
      .getMarketAnalyses()
      .then((saved) => setAnalyses((prev) => mergeAnalyses(prev, saved)))
      .catch(() => {})
    api
      .getModelConfigs()
      .then((m) => setModels(m.filter((x) => x.enabled)))
      .catch(() => {})
    api
      .getExchangeConfigs()
      .then((list) =>
        setExchanges(
          list.filter(
            (e) =>
              e.enabled &&
              (e.exchange_type === 'binance' ||
                e.exchange_type === 'binance_paper')
          )
        )
      )
      .catch(() => {})
  }, [])

  // Poll candles every 5 s and the ticker every 3 s while the tab is visible.
  useEffect(() => {
    let cancelled = false
    const loadCandles = async () => {
      try {
        const k = await api.getMarketKlines(symbol, interval, 500)
        if (!cancelled) {
          setData({ key, candles: k })
          setLoadError('')
        }
      } catch (err) {
        if (!cancelled)
          setLoadError(err instanceof Error ? err.message : 'error')
      }
    }
    const loadTicker = async () => {
      try {
        const t = await api.getMarketTicker(symbol)
        if (!cancelled) setTicker(t)
      } catch {
        // header keeps the last value
      }
    }
    const stopCandles = startVisiblePolling(loadCandles, 5000)
    const stopTicker = startVisiblePolling(loadTicker, 3000)
    return () => {
      cancelled = true
      stopCandles()
      stopTicker()
    }
  }, [symbol, interval, key])

  const candles = data.key === key ? data.candles : []
  const last = candles[candles.length - 1]
  const price = ticker?.symbol === symbol ? ticker.last_price : last?.close || 0
  const decimals = useMemo(() => priceDecimals(price || 1), [price])
  const a = analysis?.symbol === symbol ? analysis.analysis : undefined
  const levels =
    a && a.signal !== 'wait'
      ? { entry: a.entry, stopLoss: a.stop_loss, takeProfit: a.take_profit }
      : undefined

  const chooseSymbol = (raw: string) => {
    const s = normalize(raw)
    if (!s || s === symbol) return
    setSymbol(s)
    savePref('symbol', s)
  }
  const toggleSub = (s: SubIndicator) => {
    const next = subs.includes(s)
      ? subs.filter((x) => x !== s)
      : subOptions.filter((x) => x === s || subs.includes(x))
    setSubs(next)
    savePref('subs', next.join(','))
  }
  const tab = (active: boolean) =>
    `rounded-lg px-2 py-1 text-xs ${active ? 'bg-nofx-gold/10 font-semibold text-nofx-gold' : 'text-nofx-text-muted hover:text-nofx-text'}`

  const changeColor = (ticker?.price_change_pct ?? 0) >= 0 ? BN.up : BN.down
  const stat = (label: string, value: string, color?: string) => (
    <div className="flex flex-col">
      <span className="text-[11px] text-nofx-text-muted">{label}</span>
      <span
        className="text-xs text-nofx-text"
        style={color ? { color } : undefined}
      >
        {value}
      </span>
    </div>
  )
  const showTicker = ticker?.symbol === symbol

  return (
    <div className="min-h-[calc(100vh-64px)] bg-nofx-bg p-3 text-nofx-text">
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-[280px_1fr_340px]">
        <div className="order-2 lg:order-1">
          <MarketList
            symbol={symbol}
            language={language}
            onSelect={chooseSymbol}
          />
        </div>
        <div className="order-1 min-w-0 space-y-3 lg:order-2">
          <header className="flex flex-wrap items-center gap-x-6 gap-y-2 rounded-lg border border-[rgba(25,36,52,0.14)] bg-nofx-bg-lighter px-4 py-3">
            <div className="flex items-center gap-2">
              <span className="text-lg font-semibold text-nofx-text">
                {symbol.replace(/USDT$/, '')}
                <span className="text-nofx-text-muted">/USDT</span>
              </span>
              <span className="rounded bg-nofx-bg-deeper px-1.5 py-0.5 text-[10px] text-nofx-text-muted">
                {tr(language, '永续', 'Perp')}
              </span>
            </div>
            <div className="flex flex-col">
              <span
                className="text-xl font-semibold"
                style={{ color: changeColor }}
              >
                {price ? price.toFixed(decimals) : '--'}
              </span>
              {showTicker && ticker.mark_price ? (
                <span className="text-[11px] text-nofx-text-muted">
                  {tr(language, '标记', 'Mark')}{' '}
                  {ticker.mark_price.toFixed(decimals)}
                </span>
              ) : null}
            </div>
            {showTicker ? (
              <>
                {stat(
                  tr(language, '24h涨跌', '24h change'),
                  `${ticker.price_change_pct >= 0 ? '+' : ''}${ticker.price_change_pct.toFixed(2)}%`,
                  changeColor
                )}
                {stat(
                  tr(language, '24h最高', '24h high'),
                  ticker.high_24h.toFixed(decimals)
                )}
                {stat(
                  tr(language, '24h最低', '24h low'),
                  ticker.low_24h.toFixed(decimals)
                )}
                {stat(
                  tr(language, '24h成交额', '24h volume'),
                  formatCompact(ticker.quote_volume_24h) + ' USDT'
                )}
                {stat(
                  tr(language, '资金费率', 'Funding'),
                  `${(ticker.funding_rate * 100).toFixed(4)}%`,
                  ticker.funding_rate >= 0 ? BN.up : BN.down
                )}
              </>
            ) : null}
          </header>

          <main className="min-w-0 overflow-hidden rounded-lg border border-[rgba(25,36,52,0.14)] bg-nofx-bg-lighter">
            <div className="flex flex-wrap items-center gap-1 border-b border-[rgba(25,36,52,0.14)] px-2 py-1.5">
              {intervals.map((i) => (
                <button
                  key={i}
                  type="button"
                  aria-pressed={i === interval}
                  className={tab(i === interval)}
                  onClick={() => {
                    setChartInterval(i)
                    savePref('interval', i)
                  }}
                >
                  {i}
                </button>
              ))}
              <span className="mx-2 h-4 w-px bg-[rgba(25,36,52,0.14)]" />
              {mains.map((m) => (
                <button
                  key={m}
                  type="button"
                  aria-pressed={m === main}
                  className={tab(m === main)}
                  onClick={() => {
                    setMain(m)
                    savePref('main', m)
                  }}
                >
                  {m === 'NONE' ? tr(language, '无主图', 'None') : m}
                </button>
              ))}
              <span className="mx-2 h-4 w-px bg-[rgba(25,36,52,0.14)]" />
              {subOptions.map((s) => (
                <button
                  key={s}
                  type="button"
                  aria-pressed={subs.includes(s)}
                  className={tab(subs.includes(s))}
                  onClick={() => toggleSub(s)}
                >
                  {s}
                </button>
              ))}
            </div>
            {loadError && candles.length === 0 ? (
              <div
                role="alert"
                className="flex h-[420px] items-center justify-center p-6 text-center text-sm"
                style={{ color: BN.down }}
              >
                {tr(language, '行情加载失败：', 'Failed to load market data: ')}
                <NoticeText text={loadError} language={language} />
                <br />
                {tr(
                  language,
                  '请确认代理软件已打开。',
                  'Make sure your proxy is running.'
                )}
              </div>
            ) : (
              <BinanceChart
                candles={candles}
                decimals={decimals}
                main={main}
                subs={subs}
                levels={levels}
                resetKey={key}
                height={440 + subs.length * 110}
              />
            )}
          </main>
        </div>

        <aside className="order-3 space-y-3">
          <AiPanel
            symbol={symbol}
            interval={interval}
            models={models}
            language={language}
            decimals={decimals}
            result={analysis}
            onResult={(r) =>
              setAnalyses((prev) => mergeAnalyses(prev, { [r.symbol]: r }))
            }
            onApply={(r) =>
              setSuggestion({
                nonce: Date.now(),
                side: r.signal === 'short' ? 'short' : 'long',
                leverage: r.leverage,
                stopLoss: r.stop_loss,
                takeProfit: r.take_profit,
              })
            }
          />
          <TradePanel
            symbol={symbol}
            exchanges={exchanges}
            price={price}
            decimals={decimals}
            language={language}
            suggestion={suggestion}
          />
          <AutoPanel
            symbol={symbol}
            interval={interval}
            models={models}
            exchanges={exchanges}
            language={language}
          />
        </aside>
      </div>
    </div>
  )
}
