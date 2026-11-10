import { NoticeText } from '../NoticeText'
import { useEffect, useState } from 'react'
import { planningAPI } from '../../lib/planning'
import { PlanningTrendBoard } from './PlanningTrendBoard'
import { useLanguage } from '../../contexts/LanguageContext'

function useSafeLanguage() {
  try {
    return useLanguage().language
  } catch {
    return 'en' as const
  }
}

type Market = {
  symbol: string
  base: string
  category: string
  volume_24h: number
  price: string
  execution_supported: boolean
}
type Board = {
  venue: string
  markets: Market[]
  fetched_at: string
  sources: string[]
  warnings?: string[]
}
const volume = (value: number) =>
  new Intl.NumberFormat('en-US', {
    notation: 'compact',
    maximumFractionDigits: 1,
  }).format(value)
export function FrontierMarkets({
  venue,
  selected,
  disabled,
  onSelect,
  onAI500Markets,
}: {
  venue: string
  selected: string
  disabled: boolean
  onSelect: (symbol: string) => void
  onAI500Markets?: (symbols: string[]) => void
}) {
  const language = useSafeLanguage()
  const zh = language === 'zh'
  const [mode, setMode] = useState(venue === 'binance' ? 'ai500' : 'volume')
  const [board, setBoard] = useState<Board | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [search, setSearch] = useState('')
  const [limit, setLimit] = useState(12)
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    setMode(venue === 'binance' ? 'ai500' : 'volume')
    setSearch('')
    setLimit(12)
  }, [venue])
  useEffect(() => {
    let active = true
    setLoading(true)
    setError('')
    planningAPI<Board>('markets?venue=' + encodeURIComponent(venue))
      .then((next) => {
        if (
          next.venue !== venue ||
          !Array.isArray(next.markets) ||
          next.markets.some(
            (m) =>
              typeof m.symbol !== 'string' ||
              !Number.isFinite(m.volume_24h) ||
              m.volume_24h < 0
          )
        )
          throw new Error('Invalid public market list')
        if (active) setBoard(next)
      })
      .catch(() => {
        if (active)
          setError(
            zh
              ? '无法加载公开市场。请重试，或在上方输入原生交易对。'
              : 'Could not load public markets. Retry or enter a native symbol above.'
          )
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
    }
  }, [venue, revision, zh])
  const current = board?.venue === venue ? board : null
  const rows = (current?.markets || [])
    .filter(
      (m) =>
        (mode !== 'stocks' ||
          m.category === 'stocks' ||
          m.category === 'international_stocks') &&
        (m.symbol + ' ' + m.base).toLowerCase().includes(search.toLowerCase())
    )
    .slice()
    .sort(
      (a, b) => b.volume_24h - a.volume_24h || a.symbol.localeCompare(b.symbol)
    )
  const selectedMarket = current?.markets.find((m) => m.symbol === selected)
  const filterLabels: Record<string, string> = zh
    ? { volume: '成交量最高', stocks: '股票', all: '全部市场' }
    : { volume: 'Top volume', stocks: 'Stocks', all: 'All markets' }
  return (
    <section
      className="frontier-markets"
      aria-label={zh ? '快速选择市场' : 'Quick market selection'}
    >
      <div className="frontier-market-filters">
        <b>{zh ? '市场' : 'Markets'}</b>
        {[
          [
            ...(venue === 'binance'
              ? [['ai500', zh ? '趋势榜' : 'Trend']]
              : []),
            ['volume', filterLabels.volume],
            ['stocks', filterLabels.stocks],
            ['all', filterLabels.all],
          ],
        ]
          .flat()
          .map(([id, label]) => (
            <button
              key={id}
              aria-pressed={mode === id}
              onClick={() => {
                setMode(id)
                setSearch('')
                setLimit(id === 'all' ? 50 : 12)
              }}
            >
              {label}
            </button>
          ))}
        {mode !== 'ai500' && (
          <>
            <input
              aria-label={zh ? '搜索交易所市场' : 'Search exchange markets'}
              placeholder={zh ? '搜索交易对，例如 TSLA' : 'Search a symbol, e.g. TSLA'}
              value={search}
              onChange={(e) => {
                setSearch(e.target.value)
                setLimit(50)
              }}
            />
            <button
              disabled={loading}
              onClick={() => setRevision((v) => v + 1)}
            >
              {zh ? '刷新市场' : 'Refresh markets'}
            </button>
            <small>
              {zh
                ? '公开数据 · 24 小时名义成交量 · 缓存 1 分钟'
                : 'Public data · 24h notional volume · cached 1 min'}
            </small>
          </>
        )}
      </div>
      {mode === 'ai500' ? (
        <PlanningTrendBoard
          venue={venue}
          selected={selected}
          disabled={disabled}
          onSelect={onSelect}
          onMarkets={onAI500Markets}
        />
      ) : (
        <>
          {loading && (
            <small role="status">
              {zh ? '正在加载交易所市场…' : 'Loading exchange markets…'}
            </small>
          )}
          {error && <p role="alert"><NoticeText text={error} language={language} /></p>}
          <div className="frontier-market-shortcuts">
            {rows.slice(0, limit).map((m) => (
              <button
                key={m.symbol}
                aria-pressed={selected === m.symbol}
                disabled={disabled}
                onClick={() => onSelect(m.symbol)}
                title={
                  zh
                    ? `${m.symbol} · 24 小时名义成交量 ${volume(m.volume_24h)} · ${m.execution_supported ? '执行需要账户复核' : '仅图表；此部署没有该市场的已验证执行适配器'}`
                    : `${m.symbol} · 24h notional ${volume(m.volume_24h)} · ${m.execution_supported ? 'Execution requires account review' : 'Chart only; this deployment has no verified execution adapter for this market'}`
                }
              >
                <b>{m.symbol}</b>
                <small>
                  {volume(m.volume_24h)}{' '}
                  {venue === 'hyperliquid' ? 'USD' : 'USDT'}
                </small>
                {!m.execution_supported && (
                  <small>{zh ? '仅图表' : 'Chart only'}</small>
                )}
              </button>
            ))}
          </div>
          {rows.length > limit && (
            <button
              className="frontier-show-markets"
              onClick={() => setLimit((n) => n + 50)}
            >
              {zh
                ? `显示更多 · 还剩 ${rows.length - limit} 个`
                : `Show more · ${rows.length - limit} remaining`}
            </button>
          )}
          {!loading && !error && current && !rows.length && (
            <small>
              {zh
                ? `该交易所没有返回匹配的${mode === 'stocks' ? '股票合约' : '市场'}。`
                : `No matching ${mode === 'stocks' ? 'stock contracts' : 'markets'} returned by this venue.`}
            </small>
          )}
          {current && (
            <small>
              {zh ? '截至' : 'As of'}{' '}
              {new Date(current.fetched_at).toLocaleTimeString('en-US')} ·{' '}
              {rows.length} {zh ? '个市场' : 'markets'}
              {mode === 'stocks'
                ? zh
                  ? ' · 包括美国和国际股票合约'
                  : ' · Includes US and international stock contracts'
                : ''}
            </small>
          )}
          {selectedMarket && !selectedMarket.execution_supported && (
            <small>
              {zh
                ? '仅图表 · 此构建器市场没有已验证的 NOFX 执行适配器。'
                : 'Chart only · This builder market does not have a verified NOFX execution adapter.'}
            </small>
          )}
          {!!current?.warnings?.length && (
            <small role="status">{current.warnings.join(' · ')}</small>
          )}
        </>
      )}
    </section>
  )
}
