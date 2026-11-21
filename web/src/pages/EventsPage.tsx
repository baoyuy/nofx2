import { useState } from 'react'
import useSWR from 'swr'
import { api } from '../lib/api'
import { EventLabelPanel } from '../components/events/EventLabelPanel'
import { EventBriefingPanel } from '../components/events/EventBriefingPanel'
import { useAuth } from '../contexts/AuthContext'
import { useLanguage } from '../contexts/LanguageContext'
import { useEffect } from 'react'
import {
  getEvents,
  getEventCards,
  getEventCardStats,
  type EventTraderRef,
  type EventHistoryOptions,
} from '../lib/api/events'
import {
  getEventLabelSettings,
  saveEventLabelSettings,
} from '../lib/api/eventBriefings'

const panel = 'rounded-2xl border border-slate-200 bg-white p-5 shadow-sm'
const chip = 'rounded-full bg-slate-100 px-3 py-1 text-xs text-slate-600'
const eventTypeCn: Record<string, string> = {
  earnings: '财报', listing: '上市', funding: '融资/募资', blackswan: '黑天鹅',
  regulation: '监管', macro: '宏观', mna: '并购/董事会', etf: 'ETF',
  protocol: '协议', security: '安全', adoption: '采用/合作', other: '其他', sentiment: '情绪',
}

function safeLink(value: string): string | undefined {
  try {
    const url = new URL(value)
    return url.protocol === 'https:' || url.protocol === 'http:'
      ? url.href
      : undefined
  } catch {
    return undefined
  }
}
function eventKind(event: { kind?: string; source: string }) {
  return (
    event.kind ||
    (event.source === 'Free Crypto News'
      ? 'news'
      : event.source === 'Hacker News'
        ? 'community'
        : 'prediction')
  )
}

function Relationships({ traders }: { traders: EventTraderRef[] }) {
  return (
    <div className="flex flex-wrap gap-2">
      {traders.map((trader) => (
        <span
          className={chip}
          key={`${trader.trader_id}:${trader.strategy_id}`}
        >
          {trader.trader_name || trader.trader_id} →{' '}
          {trader.strategy_name || trader.strategy_id || '—'}
        </span>
      ))}
    </div>
  )
}

export function EventsPage() {
  const { user, token } = useAuth()
  // Remount filter state at account/session boundaries; never carry data across users.
  return (
    <EventsContent
      key={`${user?.id}:${token}`}
      userId={user?.id}
      token={token}
    />
  )
}

function EventsContent({
  userId,
  token,
}: {
  userId?: string
  token: string | null
}) {
  const { language } = useLanguage()
  const zh = language === 'zh'
  const text = (cn: string, en: string) => (zh ? cn : en)
  const [coinChoice, setCoin] = useState('')
  const [kind, setKind] = useState('')
  const [rawOpen, setRawOpen] = useState(false)
  const [traderChoice, setTrader] = useState('')
  const [platform, setPlatform] = useState('')
  const [view, setView] = useState<'all' | 'new' | 'changed' | 'ending'>('all')
  const [windowMinutes, setWindowMinutes] = useState<15 | 60 | 360>(60)
  const [thresholdPP, setThresholdPP] = useState<1 | 5 | 10>(5)
  const [endingHours, setEndingHours] = useState<6 | 24 | 72>(24)
  const [modelChoice, setModelChoice] = useState('')
  const {
    data: configuredModels,
    error: modelsError,
    mutate: retryModels,
  } = useSWR(
    userId && token ? ['event-label-models', userId, token] : null,
    () => api.getModelConfigs(),
    { shouldRetryOnError: false }
  )
  const models = configuredModels?.filter((m) => m.enabled) ?? []
  const modelId = models.some((m) => m.id === modelChoice) ? modelChoice : ''
  const { data: savedSettings, mutate: mutateSettings } = useSWR(
    userId && token ? ['event-label-settings', userId, token] : null,
    () => getEventLabelSettings(),
    { shouldRetryOnError: false }
  )
  const [autoLabel, setAutoLabel] = useState(false)
  useEffect(() => {
    if (savedSettings) {
      setModelChoice(savedSettings.model_id)
      setAutoLabel(savedSettings.auto_label)
    }
  }, [savedSettings?.model_id, savedSettings?.auto_label])
  const saveLabelSettings = async (nextModel: string, nextAuto: boolean) => {
    try {
      const saved = await saveEventLabelSettings({
        model_id: nextModel,
        auto_label: nextAuto,
      })
      setModelChoice(saved.model_id)
      setAutoLabel(saved.auto_label)
      await mutateSettings(saved)
    } catch {
      /* keep local setting unchanged on failed save */
    }
  }
  const options: EventHistoryOptions = {
    windowMinutes,
    thresholdPP,
    endingHours,
    ...(modelId ? { labelModelId: modelId } : {}),
  }
  const { data, error, isLoading, isValidating, mutate } = useSWR(
    userId && token
      ? [
          'events',
          userId,
          token,
          windowMinutes,
          thresholdPP,
          endingHours,
          modelId,
        ]
      : null,
    () => getEvents(options),
    {
      refreshInterval: 60000,
      dedupingInterval: 10000,
      keepPreviousData: false,
      shouldRetryOnError: false,
    }
  )
  const { data: cardData } = useSWR(
    userId && token ? ['event-cards', userId, token] : null,
    () => getEventCards(),
    { refreshInterval: 60000, shouldRetryOnError: false }
  )
  const eventCards = cardData?.cards ?? []
  const { data: cardStats } = useSWR(
    userId && token ? ['event-card-stats', userId, token] : null,
    () => getEventCardStats(),
    { refreshInterval: 120000, shouldRetryOnError: false }
  )
  const coins = [...new Set(data?.watches.map((watch) => watch.coin) ?? [])]
  const traders = [
    ...new Map(
      (data?.watches.flatMap((watch) => watch.traders) ?? []).map((trader) => [
        trader.trader_id,
        trader,
      ])
    ).values(),
  ]
  // Clamp selections to the latest scope, including during background refreshes.
  const coin = coins.includes(coinChoice) ? coinChoice : ''
  const trader = traders.some((ref) => ref.trader_id === traderChoice)
    ? traderChoice
    : ''
  const scopedEvents =
    data?.events.filter(
      (event) =>
        (!coin || event.coins.includes(coin)) &&
        (!trader || event.traders.some((ref) => ref.trader_id === trader)) &&
        (!platform || event.source === platform) &&
        (!kind || eventKind(event) === kind)
    ) ?? []
  const tabs = [
    {
      id: 'all',
      label: text('全部事件', 'All events'),
      count: scopedEvents.length,
    },
    {
      id: 'new',
      label: text('新增事件', 'New events'),
      count: scopedEvents.filter((e) => e.is_new).length,
    },
    {
      id: 'changed',
      label: text('明显变化', 'Significant changes'),
      count: scopedEvents.filter(
        (e) => eventKind(e) === 'prediction' && e.significant_change
      ).length,
    },
    {
      id: 'ending',
      label: text('即将截止', 'Ending soon'),
      count: scopedEvents.filter(
        (e) => eventKind(e) === 'prediction' && e.ending_soon
      ).length,
    },
  ] as const
  const events = scopedEvents.filter(
    (event) =>
      view === 'all' ||
      (view === 'new' && event.is_new) ||
      (view === 'changed' &&
        eventKind(event) === 'prediction' &&
        event.significant_change) ||
      (view === 'ending' &&
        eventKind(event) === 'prediction' &&
        event.ending_soon)
  )
  const displayMarkets = (markets: (typeof scopedEvents)[number]['markets']) =>
    [...markets].sort(
      (a, b) =>
        Number(b.changes?.some((c) => c.significant) ?? false) -
        Number(a.changes?.some((c) => c.significant) ?? false)
    )
  const historyLabel = (status?: string) => {
    if (status === 'available')
      return text('本地快照变化', 'Local snapshot changes')
    if (status === 'stale')
      return text(
        '快照已过期，暂停变化判断',
        'Stale snapshot; change detection paused'
      )
    if (status === 'unavailable')
      return text(
        '历史存储不可用，无法核验变化',
        'History unavailable; changes cannot be verified'
      )
    return text(
      '历史不足，等待比较窗口内的有效快照',
      'Insufficient history; waiting for a valid baseline'
    )
  }
  const time = (value: string) => {
    const date = new Date(value)
    return Number.isNaN(date.getTime()) || date.getUTCFullYear() < 1970
      ? text('时间未知', 'Unknown time')
      : date.toLocaleString(zh ? 'zh-CN' : 'en-US')
  }
  const percent = (value: number) =>
    `${(value * 100).toLocaleString(zh ? 'zh-CN' : 'en-US', { maximumFractionDigits: 2 })}%`
  const empty = !data?.running_traders
    ? text(
        '没有运行中的交易员。启动交易员后，这里会展示其策略币种相关的资讯与事件。',
        'No running traders. News and events appear here for strategy coins once a trader is running.'
      )
    : !coins.length
      ? text(
          '运行中的策略暂无可关注币种。请检查策略币种配置。',
          'No coins in the running strategies. Check strategy coin configuration.'
        )
      : !data.events.length
        ? text(
            '关注范围内暂无资讯或事件，请稍后刷新。',
            'No news or events in your watch scope yet. Refresh later.'
          )
        : text(
            '没有符合筛选条件的资讯或事件。',
            'No news or events match these filters.'
          )

  return (
    <div className="mx-auto max-w-6xl space-y-6 px-4 py-8 text-slate-800">
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold">
            {text('资讯与事件', 'News & events')}
          </h1>
          <p className="mt-2 text-sm text-slate-500">
            {text(
              '来自 Polymarket、Kalshi、Free Crypto News 和 Hacker News，仅关注当前账号运行中交易员的策略币种，每 60 秒刷新。',
              'Polymarket, Kalshi, Free Crypto News and Hacker News, scoped to strategy coins of your running traders. Refreshes every 60 seconds.'
            )}
          </p>
        </div>
        <button
          className="rounded-xl border border-slate-200 bg-white px-4 py-2 text-sm disabled:opacity-50"
          disabled={isValidating || !userId || !token}
          onClick={() => void mutate()}
        >
          {isValidating
            ? text('刷新中…', 'Refreshing…')
            : text('刷新', 'Refresh')}
        </button>
      </header>
      <section
        className={panel}
        aria-label={text('事件打标模型设置', 'Event labeling model settings')}
      >
        <label className="text-sm font-medium">
          {text('打标模型', 'Labeling model')}
          <select
            className="ml-3 rounded-lg border border-slate-200 bg-white p-2"
            value={modelId}
            onChange={(e) => {
              setModelChoice(e.target.value)
              void saveLabelSettings(e.target.value, autoLabel)
            }}
          >
            <option value="">
              {text('请选择已启用的模型', 'Select an enabled model')}
            </option>
            {models.map((m) => (
              <option key={m.id} value={m.id}>
                {m.name}
              </option>
            ))}
          </select>
        </label>
        <label className="ml-4 inline-flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={autoLabel}
            disabled={!modelId}
            onChange={(e) => void saveLabelSettings(modelId, e.target.checked)}
          />
          {text('自动更新简报', 'Auto-update briefing')}
        </label>
        <p className="mt-2 text-xs text-slate-500">
          {text(
            '仅在点击或启用自动更新后调用模型，可能产生费用。自动更新只对新输入尝试一次，失败不会循环重试；项目保护为每 10 秒最多一次、每小时最多 120 次，失败也计入。模型供应商仍可能有自己的限额。标签和简报是模型解释，不是事实核验或交易指令。',
            'Models run on click or when auto-update is enabled; charges may apply. Auto-update tries once per new input and never loops on failure. Project protection allows one attempt every 10 seconds and up to 120 per hour; provider limits may still apply. Labels and briefings are interpretations, not fact checks or trade instructions.'
          )}
        </p>
        {modelsError ? (
          <p role="alert" className="mt-2 text-sm text-red-700">
            {text('模型列表加载失败', 'Could not load models')}{' '}
            <button
              type="button"
              className="underline"
              onClick={() => void retryModels()}
            >
              {text('重试模型列表', 'Retry models')}
            </button>
          </p>
        ) : configuredModels && !models.length ? (
          <p className="mt-2 text-sm text-amber-800">
            {text(
              '请先在配置页面启用模型。不会自动使用交易员模型。',
              'Enable a model in configuration first. Trader models are not selected automatically.'
            )}
          </p>
        ) : null}
      </section>
      <p className="rounded-xl border border-amber-200 bg-amber-50 p-4 text-sm text-amber-900">
        {text(
          '市场价格隐含概率，非事实或收益保证。不构成投资建议，不会自动下单。请核实平台的市场规则及结算条件。',
          'Market-price implied probabilities, not facts or guaranteed returns. Not investment advice. No automatic orders. Verify platform market rules and resolution conditions.'
        )}
      </p>
      {error && (
        <div
          role="alert"
          className="rounded-xl border border-red-200 bg-red-50 p-4 text-red-800"
        >
          <p>
            {text(
              '资讯与事件加载失败，请重试。',
              'Could not load news and events. Please retry.'
            )}
          </p>
          <button
            className="mt-2 underline"
            disabled={isValidating}
            onClick={() => void mutate()}
          >
            {text('重试', 'Retry')}
          </button>
        </div>
      )}
      {isLoading && (
        <p role="status">
          {text('正在加载资讯与事件…', 'Loading news and events…')}
        </p>
      )}
      {data && (
        <>

          {eventCards.length > 0 && (
            <section
              className={panel}
              aria-label={text('事件卡（信息打标）', 'Event cards (information tagging)')}
            >
              <h2 className="text-lg font-semibold">
                {text('事件卡 · 信息打标（每轮自动更新）', 'Event cards - information tagging (updated each cycle)')}
              </h2>
              <p className="mt-1 text-xs text-slate-500">
                {text(
                  '打标模型把资讯/预测市场提炼成催化剂事件：类型、涉及币、利好利空、是否超预期、重要度、关键节点。未核实，仅作情报参考，不构成交易指令；关键节点前宜先落袋、只用小仓探测。',
                  'The tagging model turns news/prediction markets into catalyst events: type, coin, direction, surprise, importance, and key node. Unverified intelligence, not trade instructions.'
                )}
              </p>
              {cardStats && (
                <p className="mt-1 text-xs text-slate-600">
                  {cardStats.hit + cardStats.miss > 0 ? (
                    <>
                      {text('近期方向命中率', 'Direction hit rate')}：
                      {cardStats.hit}/{cardStats.hit + cardStats.miss}（
                      {Math.round((cardStats.hit / (cardStats.hit + cardStats.miss)) * 100)}%）
                      {' · '}
                      {text('待评估', 'pending')} {cardStats.pending}
                    </>
                  ) : (
                    <>
                      {text('方向命中率', 'Direction hit rate')}：
                      {text('暂无已结算样本', 'no resolved samples yet')} ·{' '}
                      {text('待评估', 'pending')} {cardStats.pending}
                    </>
                  )}
                </p>
              )}
              <div className="mt-3 space-y-2">
                {eventCards.map((card) => {
                  const dirLabel =
                    card.direction === 'bullish'
                      ? text('利好', 'Bullish')
                      : card.direction === 'bearish'
                        ? text('利空', 'Bearish')
                        : text('中性', 'Neutral')
                  const dirColor =
                    card.direction === 'bullish'
                      ? 'bg-emerald-50 text-emerald-700'
                      : card.direction === 'bearish'
                        ? 'bg-rose-50 text-rose-700'
                        : 'bg-slate-100 text-slate-600'
                  const supLabel =
                    card.surprise === 'above'
                      ? text('超预期', 'Above')
                      : card.surprise === 'below'
                        ? text('不及预期', 'Below')
                        : card.surprise === 'inline'
                          ? text('符合预期', 'Inline')
                          : text('未知', 'Unknown')
                  return (
                    <div
                      key={`${card.trader_id}:${card.coin}:${card.card_id}`}
                      className="rounded-xl border border-slate-200 p-3 text-sm"
                    >
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="font-semibold">{card.coin}</span>
                        <span className={chip}>
                          {eventTypeCn[card.type] ?? card.type}
                        </span>
                        <span className={`rounded-full px-2 py-0.5 text-xs ${dirColor}`}>
                          {dirLabel}
                        </span>
                        <span className={chip}>{supLabel}</span>
                        <span className={chip}>
                          {text('重要度', 'Importance')} {card.importance}
                        </span>
                        {card.is_node && card.node_at && (
                          <span className="rounded-full bg-amber-50 px-2 py-0.5 text-xs text-amber-800">
                            {text('关键节点', 'Node')} {time(card.node_at)}
                          </span>
                        )}
                      </div>
                      <p className="mt-1 font-medium">{card.title}</p>
                      {card.summary && (
                        <p className="mt-1 text-xs text-slate-600">{card.summary}</p>
                      )}
                      {card.reason && (
                        <p className="mt-1 text-xs text-slate-500">
                          {text('打标理由', 'Reason')}：{card.reason}
                        </p>
                      )}
                      <p className="mt-1 text-xs text-slate-400">
                        {card.tagged_at ? time(card.tagged_at) : ''}
                      </p>
                    </div>
                  )
                })}
              </div>
            </section>
          )}
          <EventBriefingPanel
            key={`${userId}:${token}:${modelId}`}
            sessionKey={`${userId}:${token}`}
            modelId={modelId}
            events={data.events}
            enabled={!!data.running_traders && !!data.events.length}
            autoLabel={autoLabel}
          />
          <section
            aria-label={text('关注统计', 'Scope statistics')}
            className="grid grid-cols-1 gap-4 sm:grid-cols-3"
          >
            {[
              [text('运行中交易员', 'Running traders'), data.running_traders],
              [text('关注币种', 'Watched coins'), coins.length],
              [text('资讯与事件', 'News & events'), data.events.length],
            ].map(([label, value]) => (
              <div className={panel} key={label}>
                <p className="text-sm text-slate-500">{label}</p>
                <p className="mt-2 text-3xl font-semibold">{value}</p>
              </div>
            ))}
          </section>
          {data.warnings.length > 0 && (
            <section
              aria-label={text('警告', 'Warnings')}
              className="rounded-xl border border-amber-200 bg-amber-50 p-4 text-sm text-amber-900"
            >
              <h2 className="font-semibold">
                {text('数据提示', 'Data warnings')}
              </h2>
              <ul className="list-disc pl-5">
                {data.warnings.map((warning, index) => (
                  <li key={index}>{warning}</li>
                ))}
              </ul>
            </section>
          )}
          <details
            open={rawOpen}
            onToggle={(e) => setRawOpen(e.currentTarget.open)}
            className="space-y-5"
          >
            <summary className="cursor-pointer rounded-xl border border-slate-200 bg-white p-4 text-sm font-semibold">
              {text(
                `原始信息流与逐条工具（${data.events.length} 条，点击展开）`,
                `Raw information & individual tools (${data.events.length} items, expand)`
              )}
            </summary>
            <section className={panel}>
              <h2 className="mb-4 font-semibold">
                {text(
                  '关注范围 · 交易员 → 策略',
                  'Watch scope · Trader → Strategy'
                )}
              </h2>
              <div className="flex flex-wrap gap-3">
                {data.watches.map((watch) => (
                  <div
                    key={watch.symbol}
                    className="space-y-2 rounded-xl border border-slate-200 p-3"
                  >
                    <p className="text-sm font-semibold">
                      {watch.coin}{' '}
                      <span className="font-normal text-slate-500">
                        {watch.symbol}
                      </span>
                    </p>
                    <Relationships traders={watch.traders} />
                  </div>
                ))}
              </div>
            </section>
            <div className="flex flex-wrap gap-4">
              <label className="text-sm">
                {text('币种', 'Coin')}
                <select
                  className="ml-2 rounded-lg border border-slate-200 bg-white p-2"
                  value={coin}
                  onChange={(e) => setCoin(e.target.value)}
                >
                  <option value="">{text('全部币种', 'All coins')}</option>
                  {coins.map((item) => (
                    <option key={item} value={item}>
                      {item}
                    </option>
                  ))}
                </select>
              </label>
              <label className="text-sm">
                {text('交易员', 'Trader')}
                <select
                  className="ml-2 rounded-lg border border-slate-200 bg-white p-2"
                  value={trader}
                  onChange={(e) => setTrader(e.target.value)}
                >
                  <option value="">{text('全部交易员', 'All traders')}</option>
                  {traders.map((item) => (
                    <option key={item.trader_id} value={item.trader_id}>
                      {item.trader_name || item.trader_id}
                    </option>
                  ))}
                </select>
              </label>
              <label className="text-sm">
                {text('平台', 'Platform')}
                <select
                  className="ml-2 rounded-lg border border-slate-200 bg-white p-2"
                  value={platform}
                  onChange={(e) => setPlatform(e.target.value)}
                >
                  <option value="">{text('全部平台', 'All platforms')}</option>
                  <option value="Polymarket">Polymarket</option>
                  <option value="Kalshi">Kalshi</option>
                  <option value="Free Crypto News">Free Crypto News</option>
                  <option value="Hacker News">Hacker News</option>
                  <option value="Telegram">Telegram</option>
                  <option value="Macro">Macro</option>
                  <option value="Earnings">Earnings</option>
                  <option value="Crypto Calendar">Crypto Calendar</option>
                  <option value="Fear & Greed">Fear & Greed</option>
                  <option value="OKX">OKX</option>
                  <option value="Bybit">Bybit</option>
                  <option value="CoinDesk">CoinDesk</option>
                  <option value="The Block">The Block</option>
                  <option value="Decrypt">Decrypt</option>
                  <option value="Cointelegraph">Cointelegraph</option>
                  <option value="CryptoPanic">CryptoPanic</option>
                </select>
              </label>
              <label className="text-sm">
                {text('信息类型', 'Information type')}
                <select
                  className="ml-2 rounded-lg border border-slate-200 bg-white p-2"
                  value={kind}
                  onChange={(e) => setKind(e.target.value)}
                >
                  <option value="">{text('全部类型', 'All types')}</option>
                  <option value="prediction">
                    {text('预测市场', 'Prediction markets')}
                  </option>
                  <option value="news">
                    {text('媒体报道与观点', 'Media reports & opinions')}
                  </option>
                  <option value="community">
                    {text('科技社区', 'Tech community')}
                  </option>
                  <option value="macro">{text('宏观数据', 'Macro')}</option>
                  <option value="earnings">{text('财报', 'Earnings')}</option>
                  <option value="crypto_calendar">
                    {text('币圈日历', 'Crypto calendar')}
                  </option>
                  <option value="sentiment">{text('情绪指标', 'Sentiment')}</option>
                  <option value="announcement">{text('交易所公告', 'Exchange announcements')}</option>
                  <option value="listing">{text('上新/下架', 'Listing / delisting')}</option>
                </select>
              </label>
            </div>
            <section
              className={panel}
              aria-label={text('变化检测设置', 'Change detection settings')}
            >
              <div className="flex flex-wrap gap-4 text-sm">
                <label>
                  {text('比较窗口', 'Comparison window')}
                  <select
                    className="ml-2 rounded-lg border border-slate-200 p-2"
                    value={windowMinutes}
                    onChange={(e) =>
                      setWindowMinutes(Number(e.target.value) as 15 | 60 | 360)
                    }
                  >
                    <option value={15}>{text('15 分钟', '15 minutes')}</option>
                    <option value={60}>{text('1 小时', '1 hour')}</option>
                    <option value={360}>{text('6 小时', '6 hours')}</option>
                  </select>
                </label>
                <label>
                  {text('变化阈值', 'Change threshold')}
                  <select
                    className="ml-2 rounded-lg border border-slate-200 p-2"
                    value={thresholdPP}
                    onChange={(e) =>
                      setThresholdPP(Number(e.target.value) as 1 | 5 | 10)
                    }
                  >
                    {[1, 5, 10].map((n) => (
                      <option key={n} value={n}>
                        {n} {text('个百分点', 'percentage points')}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  {text('截止窗口', 'Deadline window')}
                  <select
                    className="ml-2 rounded-lg border border-slate-200 p-2"
                    value={endingHours}
                    onChange={(e) =>
                      setEndingHours(Number(e.target.value) as 6 | 24 | 72)
                    }
                  >
                    {[6, 24, 72].map((n) => (
                      <option key={n} value={n}>
                        {n} {text('小时', 'hours')}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
              <p className="mt-3 text-xs text-slate-500">
                {text(
                  '新增指本账号首次观测后 24 小时内，非平台新建。变化比较同平台、同市场、同结果的有效快照；按需采集，请保持页面开启以积累数据。历史不足不生成信号，变化不等于交易建议。',
                  'New means first observed by this account within 24 hours, not newly created on the platform. Changes compare valid snapshots of the same platform, market and outcome. Collection is on demand: keep this page open to accumulate history. No baseline means no signal; changes are not trade recommendations.'
                )}
              </p>
              <p className="mt-2 text-xs text-slate-500">
                {text(
                  '明显变化与即将截止仅适用于预测市场。新闻与社区内容可能是观点、转载或旧文再次讨论，未经事实核验；HN 分数和评论数仅代表社区关注度。',
                  'Significant changes and deadlines apply only to prediction markets. Reports and community posts may be opinions, reposts or renewed discussions of old articles; they are not fact-verified. HN scores and comments indicate community attention only.'
                )}
              </p>
            </section>
            <div
              className="flex flex-wrap gap-2"
              role="group"
              aria-label={text('事件视图', 'Event views')}
            >
              {tabs.map((tab) => (
                <button
                  key={tab.id}
                  type="button"
                  aria-pressed={view === tab.id}
                  onClick={() => setView(tab.id)}
                  className={`rounded-xl border px-4 py-2 text-sm ${view === tab.id ? 'border-blue-600 bg-blue-50 text-blue-700' : 'border-slate-200 bg-white text-slate-600'}`}
                >
                  {tab.label} · {tab.count}
                </button>
              ))}
            </div>
            <section
              aria-label={text('资讯与事件列表', 'News and event feed')}
              className="space-y-4"
            >
              {!data.running_traders || !coins.length || !events.length ? (
                <p className={panel} role="status">
                  {empty}
                </p>
              ) : (
                events.map((event) => {
                  const href = safeLink(event.url)
                  const prediction = eventKind(event) === 'prediction'
                  const discussion = event.discussion_url
                    ? safeLink(event.discussion_url)
                    : undefined
                  return (
                    <article className={panel} key={event.id}>
                      <div className="mb-2 text-xs text-slate-500">
                        {event.source} ·{' '}
                        {prediction
                          ? text('创建于', 'Created')
                          : eventKind(event) === 'community'
                            ? text('帖子发布于', 'Posted')
                            : text('报道发布于', 'Published')}{' '}
                        <time dateTime={event.published_at}>
                          {time(event.published_at)}
                        </time>
                        {' · '}
                        {text('获取于', 'Retrieved')}{' '}
                        <time dateTime={event.received_at}>
                          {time(event.received_at)}
                        </time>
                      </div>
                      <h2 className="mb-3 text-lg font-semibold">
                        {href ? (
                          <a
                            className="text-blue-700 hover:underline"
                            href={href}
                            target="_blank"
                            rel="noopener noreferrer"
                          >
                            {event.title}
                          </a>
                        ) : (
                          event.title
                        )}
                      </h2>
                      <div className="mb-3 flex flex-wrap gap-2 text-xs">
                        <span className={chip}>
                          {prediction
                            ? text('预测市场', 'Prediction market')
                            : eventKind(event) === 'community'
                              ? text(
                                  '社区讨论 · 未核实',
                                  'Community discussion · unverified'
                                )
                              : event.article_type === 'opinion'
                                ? text(
                                    '观点/分析 · 未核实',
                                    'Opinion / analysis · unverified'
                                  )
                                : text(
                                    '媒体报道 · 未核实',
                                    'Media report · unverified'
                                  )}
                        </span>
                        {event.publisher && (
                          <span className={chip}>
                            {text('原始来源', 'Original source')}:{' '}
                            {event.publisher}
                          </span>
                        )}
                      </div>
                      {!prediction && (
                        <div className="mb-4 space-y-2">
                          {event.summary && (
                            <p className="whitespace-pre-line text-sm text-slate-700">
                              {event.summary}
                            </p>
                          )}
                          {eventKind(event) === 'community' && (
                            <p className="text-xs text-slate-500">
                              {text('社区分数', 'Community score')}:{' '}
                              {event.score ?? '—'} ·{' '}
                              {text('评论数', 'Comments')}:{' '}
                              {event.comments ?? '—'}
                            </p>
                          )}
                          {discussion && (
                            <a
                              className="text-sm text-blue-700 hover:underline"
                              href={discussion}
                              target="_blank"
                              rel="noopener noreferrer"
                            >
                              {text('查看 HN 讨论', 'Read HN discussion')}
                            </a>
                          )}
                        </div>
                      )}
                      <div className="mb-3 flex flex-wrap gap-2 text-xs">
                        {event.is_new && (
                          <span className="rounded-full bg-blue-50 px-3 py-1 text-blue-700">
                            {text(
                              '本账号首次观测后 24h 内',
                              'First observed within 24h'
                            )}
                          </span>
                        )}
                        {prediction && event.significant_change && (
                          <span className="rounded-full bg-rose-50 px-3 py-1 text-rose-700">
                            {text(
                              '本地变化达到阈值',
                              'Local change reached threshold'
                            )}
                          </span>
                        )}
                        {prediction && event.ending_soon && (
                          <span className="rounded-full bg-amber-50 px-3 py-1 text-amber-800">
                            {text(
                              `距截止不超过 ${endingHours} 小时`,
                              `Deadline within ${endingHours} hours`
                            )}
                          </span>
                        )}
                      </div>
                      {event.first_seen_at && (
                        <p className="mb-2 text-xs text-slate-500">
                          {text(
                            '本账号首次观测',
                            'First observed by this account'
                          )}
                          :{' '}
                          <time dateTime={event.first_seen_at}>
                            {time(event.first_seen_at)}
                          </time>
                        </p>
                      )}
                      {prediction && (
                        <>
                          <p className="mb-3 text-sm text-slate-600">
                            {text('截止时间', 'Deadline')}:{' '}
                            {event.end_at ? (
                              <time dateTime={event.end_at}>
                                {time(event.end_at)}
                              </time>
                            ) : (
                              text('未提供', 'Not provided')
                            )}
                          </p>
                          <div className="mb-4 space-y-3">
                            {event.markets.length ? (
                              displayMarkets(event.markets)
                                .slice(0, 8)
                                .map((market) => (
                                  <div
                                    key={market.id}
                                    className="rounded-xl border border-slate-200 p-3"
                                  >
                                    <h3 className="font-medium">
                                      {market.question}
                                    </h3>
                                    <div className="my-2 flex flex-wrap gap-2">
                                      {market.outcomes.map((outcome, index) => (
                                        <span
                                          className={chip}
                                          key={`${outcome.name}:${index}`}
                                        >
                                          {outcome.name}:{' '}
                                          {(outcome.price * 100).toLocaleString(
                                            zh ? 'zh-CN' : 'en-US',
                                            { maximumFractionDigits: 2 }
                                          )}
                                          %
                                        </span>
                                      ))}
                                    </div>
                                    {!market.outcomes.length && (
                                      <p className="mb-2 text-xs text-slate-500">
                                        {text(
                                          '暂无有效成交价格',
                                          'No valid traded price available'
                                        )}
                                      </p>
                                    )}
                                    <p className="text-xs text-slate-500">
                                      {text('价格依据', 'Price basis')}:{' '}
                                      {market.price_basis}
                                    </p>
                                    {market.change_24h !== undefined && (
                                      <p className="mt-1 text-xs text-slate-500">
                                        {text(
                                          '首个结果 24 小时价格变化',
                                          'First outcome 24h price change'
                                        )}
                                        : {market.change_24h > 0 ? '+' : ''}
                                        {(
                                          market.change_24h * 100
                                        ).toLocaleString(
                                          zh ? 'zh-CN' : 'en-US',
                                          { maximumFractionDigits: 2 }
                                        )}{' '}
                                        {text('个百分点', 'percentage points')}
                                      </p>
                                    )}
                                    <div className="mt-3 rounded-lg bg-indigo-50/60 p-3 text-sm">
                                      <p className="text-xs text-slate-500">
                                        {historyLabel(market.history_status)}
                                      </p>
                                      {(market.changes ?? []).map((change) => (
                                        <div
                                          className="mt-2"
                                          key={change.outcome}
                                        >
                                          <p
                                            className={
                                              change.significant
                                                ? 'font-medium text-rose-700'
                                                : 'text-slate-700'
                                            }
                                          >
                                            {change.outcome}:{' '}
                                            {percent(change.previous_price)} →{' '}
                                            {percent(change.current_price)} ·{' '}
                                            {change.delta_pp > 0 ? '+' : ''}
                                            {change.delta_pp.toLocaleString(
                                              zh ? 'zh-CN' : 'en-US',
                                              { maximumFractionDigits: 2 }
                                            )}{' '}
                                            {text(
                                              '个百分点',
                                              'percentage points'
                                            )}
                                          </p>
                                          <p className="mt-1 text-xs text-slate-500">
                                            <time dateTime={change.from_at}>
                                              {time(change.from_at)}
                                            </time>{' '}
                                            →{' '}
                                            <time dateTime={change.to_at}>
                                              {time(change.to_at)}
                                            </time>
                                          </p>
                                          {change.significant && (
                                            <p className="mt-1 text-xs text-rose-700">
                                              {text(
                                                `触发原因：同结果价格变化绝对值 ≥ ${thresholdPP} 个百分点`,
                                                `Reason: absolute change in the same outcome ≥ ${thresholdPP} percentage points`
                                              )}
                                            </p>
                                          )}
                                        </div>
                                      ))}
                                    </div>
                                  </div>
                                ))
                            ) : (
                              <p className="text-sm text-slate-500">
                                {text(
                                  '暂无市场价格数据。',
                                  'No market prices available.'
                                )}
                              </p>
                            )}
                            {event.markets.length > 8 && (
                              <p className="text-xs text-slate-500">
                                {text(
                                  '仅展示前 8 个市场，完整市场请查看平台。',
                                  'Showing the first 8 markets. See the platform for all markets.'
                                )}
                              </p>
                            )}
                          </div>
                        </>
                      )}
                      <div className="mb-3 flex flex-wrap gap-2">
                        {event.coins.map((item) => (
                          <span className={chip} key={item}>
                            {item}
                          </span>
                        ))}
                      </div>
                      <Relationships traders={event.traders} />
                      <EventLabelPanel
                        key={`${event.id}:${modelId}:${windowMinutes}:${thresholdPP}:${endingHours}`}
                        options={options}
                        eventId={event.id}
                        modelId={modelId}
                        label={event.label}
                        onLabeled={async () => {
                          await mutate()
                        }}
                      />
                    </article>
                  )
                })
              )}
            </section>
            <p className="text-xs text-slate-500">
              {text('数据更新于', 'Data generated at')}{' '}
              {time(data.generated_at)}
            </p>
          </details>
        </>
      )}
    </div>
  )
}
