import { useMemo, useState } from 'react'
import useSWR from 'swr'
import { LOCAL_SNAPSHOT_OPTIONS } from '../lib/liveSnapshot'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { AlertTriangle, ChevronDown, Settings } from 'lucide-react'
import { useLanguage } from '../contexts/LanguageContext'
import { api } from '../lib/api'
import type { CopyTradeStatus, TraderSnapshot } from '../lib/api/copyTrade'
import { ROUTES } from '../router/paths'
import { tr, ui } from '../components/market/marketUi'
import { DiscoverTab } from '../components/copytrade/DiscoverTab'
import { FollowingTab } from '../components/copytrade/FollowingTab'
import { FeedTab } from '../components/copytrade/FeedTab'
import { CoinsTab } from '../components/copytrade/CoinsTab'
import { DirectionShortlistPanel } from '../components/copytrade/DirectionShortlistPanel'
import { CoinPoolPanel } from '../components/copytrade/CoinPoolPanel'
import { PaperTab } from '../components/copytrade/PaperTab'
import { ago, staleness } from '../components/copytrade/format'
import { NoticeText } from '../components/NoticeText'

type Tab = 'discover' | 'following' | 'feed' | 'coins' | 'paper'
const EMPTY_SNAPS: TraderSnapshot[] = []

function StatusBar({
  language,
  status,
}: {
  language: string
  status: CopyTradeStatus | null
}) {
  const navigate = useNavigate()
  const [showData, setShowData] = useState(false)
  if (!status) return null
  const c = status.client
  const paused = c.paused && c.paused_until
  const formatErrors = c.format_errors || []
  const dataPending =
    status.ai_ready !== undefined && status.ai_ready < (status.ai_total ?? 0)
  const noFollows = status.follows === 0
  const noUsableData = !noFollows && status.ai_total === 0
  const failing = status.failing ?? 0
  const dot =
    paused || formatErrors.length
      ? 'bg-nofx-danger'
      : c.last_error || dataPending || failing > 0 || noFollows || noUsableData
        ? 'bg-nofx-gold'
        : 'bg-nofx-success'
  // The one number that says "these figures may be old". It used to sit in the
  // same grey as the counters next to it, where nobody reads it.
  const staleFrom =
    status.oldest_update && !status.oldest_update.startsWith('0001')
      ? Date.parse(status.oldest_update)
      : 0
  const stale = staleFrom ? staleness(staleFrom) : null
  const staleClass =
    stale?.level === 'bad'
      ? 'text-nofx-danger'
      : stale?.level === 'warn'
        ? 'text-nofx-gold'
        : ui.muted
  const staleText = stale
    ? tr(
        language,
        `源数据最旧 ${ago(language, staleFrom)}`,
        `stalest data ${ago(language, staleFrom)}`
      )
    : ''
  return (
    <div className={`${ui.panel} mb-4 text-sm`}>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
        <span className="flex items-center gap-1.5 font-medium text-nofx-text">
          <span className={`h-2 w-2 rounded-full ${dot}`} />
          {paused
            ? tr(
                language,
                `被币安限流，暂停到 ${new Date(c.paused_until!).toLocaleTimeString()}`,
                `Rate limited by Binance; paused until ${new Date(c.paused_until!).toLocaleTimeString()}`
              )
            : formatErrors.length
              ? tr(language, '币安数据格式异常', 'Binance data looks wrong')
              : c.last_error
                ? tr(language, '最近一次请求出错', 'Last request failed')
                : noFollows
                  ? tr(language, '尚未关注交易员', 'No traders followed')
                  : noUsableData
                    ? tr(language, '暂无可用数据', 'No usable data yet')
                    : dataPending
                      ? tr(language, '部分数据待同步', 'Data sync incomplete')
                      : tr(language, '正常', 'OK')}
        </span>
        <span className={ui.muted}>
          {tr(
            language,
            `关注 ${status.follows}/${status.max_follows} 人 · ${status.hot} 人活跃`,
            `Following ${status.follows}/${status.max_follows} · ${status.hot} active`
          )}
        </span>
        {status.ai_ready !== undefined ? (
          <button
            type="button"
            aria-expanded={showData}
            onClick={() => setShowData(!showData)}
            className={`inline-flex items-center gap-1 ${dataPending ? 'text-nofx-gold' : 'text-nofx-text-muted hover:text-nofx-gold'}`}
            title={tr(
              language,
              '只表示带单数据完整可用；策略开启带单上下文后才会发送给 AI。点开看各项明细。',
              'Data readiness only; a strategy must enable copy-trade context before these data are sent to AI. Click for the breakdown.'
            )}
          >
            {tr(language, '监控数据可用', 'Monitor data ready')}{' '}
            <span className="tabular-nums">
              {status.ai_ready}/{status.ai_total ?? 0}
            </span>
            <ChevronDown
              className={`h-3 w-3 transition-transform ${showData ? 'rotate-180' : ''}`}
            />
          </button>
        ) : null}
        {stale ? (
          <span
            tabIndex={0}
            className={`inline-flex items-center gap-1 tabular-nums ${staleClass}`}
            title={tr(
              language,
              '这是所有关注者里最久没更新的那份数据。超过 15 分钟说明轮询变慢，超过 1 小时说明可能被限流或出错。',
              'The oldest update among the traders you follow. Over 15 minutes means polling slowed; over an hour suggests rate limiting or an error.'
            )}
          >
            {stale.level !== 'ok' ? (
              <AlertTriangle className="h-3 w-3" />
            ) : null}
            {staleText}
          </span>
        ) : null}
        <span className={`${ui.muted} hidden tabular-nums sm:inline`}>
          {tr(
            language,
            `每分钟 ${c.requests_per_min} 次请求`,
            `${c.requests_per_min} requests/min`
          )}
        </span>
        <button
          type="button"
          onClick={() => navigate(`${ROUTES.settings}?tab=data`)}
          className="ml-auto inline-flex items-center gap-1 text-xs text-nofx-text-muted hover:text-nofx-gold"
        >
          <Settings className="h-3 w-3" />
          {c.proxy
            ? tr(language, `走代理 ${c.proxy}`, `Via proxy ${c.proxy}`)
            : tr(language, '使用服务器 IP', 'Server IP')}
        </button>
      </div>
      {showData && status.ai_ready !== undefined ? (
        <p className={`${ui.muted} mt-1 tabular-nums`}>
          {tr(
            language,
            `持仓 ${status.positions_fresh ?? 0} 人 · 成交 ${status.fills_fresh ?? 0} 人 · 资料 ${status.profiles_fresh ?? 0} 人是新鲜数据，其余在回填或读不到。只有策略开启带单上下文后，这些数据才会发给 AI。`,
            `${status.positions_fresh ?? 0} with fresh positions, ${status.fills_fresh ?? 0} with fresh fills, ${status.profiles_fresh ?? 0} with fresh profiles; the rest are backfilling or unreadable. They reach the AI only when a strategy enables copy-trade context.`
          )}
        </p>
      ) : null}
      {formatErrors.length ? (
        <p role="alert" className="mt-1 w-full text-xs text-nofx-danger">
          {tr(
            language,
            '币安返回的数据缺少价格、时间等字段，可能改了接口格式。在修好之前，受影响的数据不会更新，页面上的数字可能是旧的：',
            'Binance answered without fields such as price or time; the format may have changed. Until fixed, the affected data stops updating and figures may be stale: '
          )}
          {formatErrors.map((error, i) => (
            <span key={i}>
              <NoticeText text={error} />
              {i < formatErrors.length - 1 ? '；' : ''}
            </span>
          ))}
        </p>
      ) : null}
      {c.last_error && !paused && !formatErrors.length ? (
        <p role="alert" className="mt-1 w-full text-xs text-nofx-danger">
          <NoticeText text={c.last_error} />
        </p>
      ) : null}
    </div>
  )
}

export function CopyTradePage() {
  const { language } = useLanguage()
  const [searchParams, setSearchParams] = useSearchParams()
  const initialTab = (
    ['discover', 'following', 'feed', 'coins', 'paper'] as Tab[]
  ).includes(searchParams.get('tab') as Tab)
    ? (searchParams.get('tab') as Tab)
    : 'following'
  const [tab, setTab] = useState<Tab>(initialTab)
  // These endpoints read local monitor snapshots. Do not tie their completion
  // together: a slow/failing status request must not hold positions or feed up.
  const { data: status } = useSWR(
    'copytrade/status',
    api.getCopyTradeStatus,
    LOCAL_SNAPSHOT_OPTIONS
  )
  const {
    data: follows,
    mutate: refreshFollows,
    isLoading: followsLoading,
    error: followsError,
  } = useSWR('copytrade/follows', api.getFollows, LOCAL_SNAPSHOT_OPTIONS)
  const snaps = follows?.follows || EMPTY_SNAPS
  const [coin, setCoin] = useState(searchParams.get('coin') || '')
  const changeTab = (t: Tab) => {
    setTab(t)
    const next = new URLSearchParams(searchParams)
    next.set('tab', t)
    if (t !== 'coins') next.delete('coin')
    setSearchParams(next, { replace: true })
  }
  const changeCoin = (symbol: string) => {
    setCoin(symbol)
    setTab('coins')
    const next = new URLSearchParams(searchParams)
    next.set('tab', 'coins')
    if (symbol) next.set('coin', symbol)
    else next.delete('coin')
    setSearchParams(next, { replace: true })
  }
  // The long caveat about polling used to sit under the title on every visit.
  const [help, setHelp] = useState(false)

  const followed = useMemo(
    () => new Set(snaps.map((s) => s.trader.portfolio_id)),
    [snaps]
  )
  const openCoin = (symbol: string) => changeCoin(symbol)
  // Keep the primary workflow first: review followed traders, inspect the
  // resulting coin exposure, then drill into events. Discovery and paper
  // tracking are secondary actions and stay at the end of the tab strip.
  const tabs: { id: Tab; zh: string; en: string }[] = [
    {
      id: 'following',
      zh: `我的关注 (${snaps.length})`,
      en: `Following (${snaps.length})`,
    },
    { id: 'coins', zh: '按币种', en: 'By coin' },
    { id: 'feed', zh: '实时动态', en: 'Live feed' },
    { id: 'discover', zh: '发现交易员', en: 'Discover' },
    { id: 'paper', zh: '模拟跟单（仅记录）', en: 'Paper copy (record only)' },
  ]

  return (
    <div className="mx-auto max-w-[1400px] px-3 py-4 md:px-5">
      <header className="mb-4">
        <h1 className="text-2xl font-bold text-nofx-text">
          {tr(language, '带单监控', 'Copy-trade monitor')}
        </h1>
        <p className={ui.muted}>
          {tr(
            language,
            '4 秒同步本地快照 · 源数据靠币安轮询，可能滞后',
            'Local snapshots every 4s · source data lags Binance polling'
          )}{' '}
          <button
            type="button"
            aria-expanded={help}
            className="text-nofx-gold hover:underline"
            onClick={() => setHelp(!help)}
          >
            {help
              ? tr(language, '收起说明', 'Hide')
              : tr(language, '说明', 'Details')}
          </button>
        </p>
        {help ? (
          <p className={`${ui.muted} mt-1`}>
            {tr(
              language,
              '关注币安合约带单员，查看持仓、开平仓动作及各币多空分布。页面每 4 秒同步本地快照；源数据更新时间受币安、关注人数及限流影响，不保证秒级实时。标记「推算」的持仓由成交反推，可能与真实仓位有出入。仅供参考。',
              'Follow Binance futures lead traders: positions, actions and per-coin long/short exposure. Local snapshots sync every 4 seconds; source freshness depends on Binance, follow count and rate limits, so it is not tick-real-time. Positions marked "inferred" are rebuilt from trades and can differ from the real book. For reference only.'
            )}
          </p>
        ) : null}
      </header>
      <StatusBar language={language} status={status ?? null} />
      <nav
        className="mb-4 flex flex-wrap gap-2"
        role="tablist"
        aria-label={tr(language, '带单监控', 'Copy-trade monitor')}
      >
        {tabs.map((t) => (
          <button
            key={t.id}
            type="button"
            id={`copytrade-tab-${t.id}`}
            role="tab"
            aria-selected={t.id === tab}
            aria-controls={`copytrade-panel-${t.id}`}
            onClick={() => changeTab(t.id)}
            className={`rounded-lg border px-3 py-2 text-sm font-semibold ${t.id === tab ? 'border-nofx-gold bg-nofx-gold/10 text-nofx-text' : `${ui.border} bg-nofx-bg-lighter text-nofx-text hover:border-nofx-gold`}`}
          >
            {tr(language, t.zh, t.en)}
          </button>
        ))}
      </nav>
      {tab === 'discover' ? (
        <div
          id="copytrade-panel-discover"
          role="tabpanel"
          aria-labelledby="copytrade-tab-discover"
        >
          <DiscoverTab
            language={language}
            followed={followed}
            onFollowed={() => void refreshFollows()}
          />
        </div>
      ) : null}
      {tab === 'following' ? (
        <div
          id="copytrade-panel-following"
          role="tabpanel"
          aria-labelledby="copytrade-tab-following"
        >
          <FollowingTab
            language={language}
            snaps={snaps}
            onChanged={() => void refreshFollows()}
            onSymbol={openCoin}
            loading={followsLoading && !follows}
            error={Boolean(followsError)}
            onDiscover={() => changeTab('discover')}
          />
        </div>
      ) : null}
      {tab === 'feed' ? (
        <div
          id="copytrade-panel-feed"
          role="tabpanel"
          aria-labelledby="copytrade-tab-feed"
        >
          <FeedTab language={language} snaps={snaps} onSymbol={openCoin} />
        </div>
      ) : null}
      {tab === 'coins' ? (
        <div
          id="copytrade-panel-coins"
          role="tabpanel"
          aria-labelledby="copytrade-tab-coins"
        >
          <DirectionShortlistPanel language={language} onSelect={changeCoin} />
          <details className="mb-3"><summary className="cursor-pointer text-sm text-nofx-text-muted">{tr(language, '原 AI 交易币池', 'Existing AI trading pool')}</summary><CoinPoolPanel language={language} /></details>
          <CoinsTab language={language} selected={coin} onSelect={changeCoin} />
        </div>
      ) : null}
      {tab === 'paper' ? (
        <div
          id="copytrade-panel-paper"
          role="tabpanel"
          aria-labelledby="copytrade-tab-paper"
        >
          <PaperTab language={language} />
        </div>
      ) : null}
    </div>
  )
}
