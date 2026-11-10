import { useEffect, useMemo, useState } from 'react'
import { Search } from 'lucide-react'
import { api } from '../../lib/api'
import type { MarketTickerLite } from '../../lib/api/market'
import { formatCompact, priceDecimals } from '../../utils/marketIndicators'
import { BN } from './BinanceChart'
import { loadPref, savePref, tr, ui } from './marketUi'

type Tab = 'all' | 'gainers' | 'losers' | 'volume'

interface Props {
  symbol: string
  language: string
  onSelect: (symbol: string) => void
}

// Coin picker + 24h gainers / losers / volume ranking for Binance USDT-M.
export function MarketList({ symbol, language, onSelect }: Props) {
  const [tickers, setTickers] = useState<MarketTickerLite[]>([])
  const [error, setError] = useState('')
  const [query, setQuery] = useState('')
  const [tab, setTab] = useState<Tab>(() => {
    const saved = loadPref('listTab', 'gainers') as Tab
    return ['all', 'gainers', 'losers', 'volume'].includes(saved)
      ? saved
      : 'gainers'
  })

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      try {
        const t = await api.getMarketTickers()
        if (!cancelled) {
          setTickers(t)
          setError('')
        }
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : 'error')
      }
    }
    void load()
    const timer = window.setInterval(
      () => document.visibilityState === 'visible' && void load(),
      10000
    )
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [])

  const rows = useMemo(() => {
    const q = query
      .trim()
      .toUpperCase()
      .replace(/[-_/\s]/g, '')
    const list = q
      ? tickers.filter((t) => t.symbol.includes(q))
      : tickers.slice()
    if (tab === 'gainers')
      list.sort((a, b) => b.price_change_pct - a.price_change_pct)
    else if (tab === 'losers')
      list.sort((a, b) => a.price_change_pct - b.price_change_pct)
    else list.sort((a, b) => b.quote_volume_24h - a.quote_volume_24h)
    // Rankings show the top 50; "all" and search show everything.
    return tab === 'all' || q ? list : list.slice(0, 50)
  }, [tickers, query, tab])

  const tabs: { key: Tab; zh: string; en: string }[] = [
    { key: 'gainers', zh: '涨幅榜', en: 'Gainers' },
    { key: 'losers', zh: '跌幅榜', en: 'Losers' },
    { key: 'volume', zh: '成交额', en: 'Volume' },
    { key: 'all', zh: '全部', en: 'All' },
  ]
  const ranked = tab !== 'all' && !query.trim()

  return (
    <section
      className={`${ui.panel} flex max-h-[calc(100vh-96px)] min-h-[320px] flex-col p-0`}
      aria-label={tr(language, '选币', 'Coins')}
    >
      <div className="p-3 pb-2">
        <div className="relative">
          <Search className="pointer-events-none absolute left-2 top-1/2 h-4 w-4 -translate-y-1/2 text-nofx-text-muted" />
          <input
            aria-label={tr(language, '搜索币种', 'Search coins')}
            placeholder={tr(language, '搜索币种，如 BTC', 'Search, e.g. BTC')}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && rows[0]) onSelect(rows[0].symbol)
            }}
            className={`${ui.input} pl-8`}
          />
        </div>
        <div className="mt-2 flex gap-1" role="tablist">
          {tabs.map((t) => (
            <button
              key={t.key}
              type="button"
              role="tab"
              aria-selected={tab === t.key}
              onClick={() => {
                setTab(t.key)
                savePref('listTab', t.key)
              }}
              className={`flex-1 rounded-lg px-1 py-1 text-xs ${tab === t.key ? 'bg-nofx-gold/10 font-semibold text-nofx-gold' : 'text-nofx-text-muted hover:text-nofx-text'}`}
            >
              {tr(language, t.zh, t.en)}
            </button>
          ))}
        </div>
      </div>
      <div className="flex px-3 pb-1 text-[11px] text-nofx-text-muted">
        <span className="flex-1">
          {tr(language, '币种 / 成交额', 'Coin / Volume')}
        </span>
        <span className="w-24 text-right">
          {tr(language, '最新价', 'Price')}
        </span>
        <span className="w-16 text-right">
          {tr(language, '24h涨跌', '24h')}
        </span>
      </div>
      {error && tickers.length === 0 ? (
        <p role="alert" className="px-3 text-xs" style={{ color: BN.down }}>
          {tr(
            language,
            '列表加载失败，请确认代理已打开。',
            'Failed to load the list. Check your proxy.'
          )}
        </p>
      ) : null}
      {!error && tickers.length === 0 ? (
        <p className="px-3 text-xs text-nofx-text-muted">
          {tr(language, '加载中…', 'Loading…')}
        </p>
      ) : null}
      <ul className="flex-1 overflow-y-auto pb-2">
        {rows.map((t, i) => {
          const color = t.price_change_pct >= 0 ? BN.up : BN.down
          const active = t.symbol === symbol
          return (
            <li key={t.symbol}>
              <button
                type="button"
                onClick={() => onSelect(t.symbol)}
                aria-current={active ? 'true' : undefined}
                className={`flex w-full items-center px-3 py-1.5 text-left text-xs hover:bg-nofx-bg-deeper ${active ? 'bg-nofx-gold/10' : ''}`}
              >
                <span className="flex flex-1 items-center gap-2">
                  {ranked ? (
                    <span className="w-5 text-right text-nofx-text-muted">
                      {i + 1}
                    </span>
                  ) : null}
                  <span className="flex flex-col">
                    <span className="font-semibold text-nofx-text">
                      {t.symbol.replace(/USDT$/, '')}
                      <span className="font-normal text-nofx-text-muted">
                        /USDT
                      </span>
                    </span>
                    <span className="text-[10px] text-nofx-text-muted">
                      {formatCompact(t.quote_volume_24h)}
                    </span>
                  </span>
                </span>
                <span className="w-24 text-right text-nofx-text">
                  {t.last_price.toFixed(priceDecimals(t.last_price))}
                </span>
                <span className="w-16 text-right">
                  <span
                    className="inline-block min-w-[56px] rounded px-1 py-0.5 text-center font-semibold text-white"
                    style={{ background: color }}
                  >
                    {t.price_change_pct >= 0 ? '+' : ''}
                    {t.price_change_pct.toFixed(2)}%
                  </span>
                </span>
              </button>
            </li>
          )
        })}
      </ul>
    </section>
  )
}
