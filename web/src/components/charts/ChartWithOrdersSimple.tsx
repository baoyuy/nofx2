import { NoticeText } from '../NoticeText'
import { useEffect, useState } from 'react'
import { httpClient } from '../../lib/httpClient'
import { useLanguage } from '../../contexts/LanguageContext'

interface ChartWithOrdersSimpleProps {
  symbol: string
  interval?: string
  traderID?: string
  height?: number
}

export function ChartWithOrdersSimple({
  symbol = 'BTCUSDT',
  interval = '5m',
  traderID,
  height = 500,
}: ChartWithOrdersSimpleProps) {
  const { language } = useLanguage()
  const isZh = language === 'zh'
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [klineCount, setKlineCount] = useState(0)
  const [orderCount, setOrderCount] = useState(0)

  useEffect(() => {
    const loadData = async () => {
      console.log('[ChartSimple] Loading data for', symbol, interval, 'trader:', traderID)
      setLoading(true)
      setError(null)

      try {
        // Fetch kline data from our own service
        const limit = 100
        const klineUrl = `/api/klines?symbol=${symbol}&interval=${interval}&limit=${limit}`

        console.log('[ChartSimple] Fetching klines from our service:', klineUrl)
        const klineResult = await httpClient.request(klineUrl, { silent: true })

        if (!klineResult.success || !klineResult.data) {
          throw new Error('Failed to fetch klines from our service')
        }

        console.log('[ChartSimple] Received klines:', klineResult.data.length)
        setKlineCount(klineResult.data.length)

        // Test fetching order data
        if (traderID) {
          const tradesUrl = `/api/trades?trader_id=${traderID}&symbol=${symbol}&limit=100`
          console.log('[ChartSimple] Fetching trades from:', tradesUrl)
          const tradesResult = await httpClient.request(tradesUrl, { silent: true })

          if (tradesResult.success && tradesResult.data) {
            console.log('[ChartSimple] Received trades:', tradesResult.data.length)
            setOrderCount(tradesResult.data.length)
          } else {
            console.warn('[ChartSimple] Failed to fetch trades:', tradesResult.message || 'Unknown error', tradesResult)
          }
        }

        setLoading(false)
      } catch (err: any) {
        console.error('[ChartSimple] Error:', err)
        setError(language === 'zh' ? '加载数据失败' : (err.message || 'Failed to load data'))
        setLoading(false)
      }
    }

    loadData()
  }, [symbol, interval, traderID, language])

  return (
    <div className="relative" style={{ background: '#f6f8fc', borderRadius: '8px', overflow: 'hidden', minHeight: height }}>
      {/* Title bar */}
      <div className="flex items-center justify-between p-4" style={{ borderBottom: '1px solid rgba(25,36,52, 0.14)' }}>
        <div className="flex items-center gap-3">
          <span className="text-xl">📈</span>
          <h3 className="text-lg font-bold" style={{ color: '#192434' }}>
            {symbol} {interval} ({isZh ? '测试模式' : 'Test Mode'})
          </h3>
        </div>
        {loading && (
          <div className="text-sm" style={{ color: '#707e92' }}>
            {isZh ? '加载中...' : 'Loading...'}
          </div>
        )}
      </div>

      {/* Test info */}
      <div className="p-8 space-y-4">
        {error ? (
          <div className="text-center">
            <div className="text-2xl mb-2">⚠️</div>
            <div style={{ color: '#D6433A' }}><NoticeText text={error} /></div>
          </div>
        ) : (
          <>
            <div className="p-4 rounded-sm" style={{ background: '#ffffff', border: '1px solid rgba(25,36,52, 0.14)' }}>
              <div className="text-sm mb-2" style={{ color: '#707e92' }}>{isZh ? 'Binance K线数据' : 'Binance Kline Data'}</div>
              <div className="text-2xl font-bold" style={{ color: '#2E8B57' }}>
                {klineCount} {isZh ? '根K线' : 'klines'}
              </div>
            </div>

            {traderID && (
              <div className="p-4 rounded-sm" style={{ background: '#ffffff', border: '1px solid rgba(25,36,52, 0.14)' }}>
                <div className="text-sm mb-2" style={{ color: '#707e92' }}>{isZh ? '历史订单数据' : 'Historical Order Data'}</div>
                <div className="text-2xl font-bold" style={{ color: '#295be8' }}>
                  {orderCount} {isZh ? '笔订单' : 'orders'}
                </div>
              </div>
            )}

            <div className="p-4 rounded-sm" style={{ background: '#ffffff', border: '1px solid rgba(25,36,52, 0.14)' }}>
              <div className="text-sm mb-2" style={{ color: '#707e92' }}>{isZh ? '状态' : 'Status'}</div>
              <div className="text-lg" style={{ color: '#192434' }}>
                {isZh ? '✅ 数据获取成功，图表组件开发中' : '✅ Data fetched successfully, chart component in development'}
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
