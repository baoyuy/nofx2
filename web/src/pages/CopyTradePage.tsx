import { useMemo, useState } from 'react'
import useSWR from 'swr'
import { LOCAL_SNAPSHOT_OPTIONS } from '../lib/liveSnapshot'
import { useNavigate } from 'react-router-dom'
import { Settings } from 'lucide-react'
import { useLanguage } from '../contexts/LanguageContext'
import { api } from '../lib/api'
import type { CopyTradeStatus, TraderSnapshot } from '../lib/api/copyTrade'
import { ROUTES } from '../router/paths'
import { tr, ui } from '../components/market/marketUi'
import { DiscoverTab } from '../components/copytrade/DiscoverTab'
import { FollowingTab } from '../components/copytrade/FollowingTab'
import { FeedTab } from '../components/copytrade/FeedTab'
import { CoinsTab } from '../components/copytrade/CoinsTab'
import { ago } from '../components/copytrade/format'
import { NoticeText } from '../components/NoticeText'

type Tab = 'discover' | 'following' | 'feed' | 'coins'
const EMPTY_SNAPS: TraderSnapshot[] = []

function StatusBar({
  language,
  status,
}: {
  language: string
  status: CopyTradeStatus | null
}) {
  const navigate = useNavigate()
  if (!status) return null
  const c = status.client
  const paused = c.paused && c.paused_until
  const formatErrors = c.format_errors || []
  const dataPending = status.ai_ready !== undefined && status.ai_ready < (status.ai_total ?? 0)
  const dot =
    paused || formatErrors.length
      ? 'bg-nofx-danger'
      : c.last_error || dataPending
        ? 'bg-nofx-gold'
        : 'bg-nofx-success'
  return (
    <div
      className={`${ui.panel} mb-4 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm`}
    >
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
      {status.ai_ready !== undefined && (
        <span className={dataPending ? 'text-nofx-gold' : ui.muted} title={tr(
          language,
          '仅表示带单数据完整可用；策略开启带单上下文后才会发送给 AI，并不代表所有交易员已开启带单参考。',
          'Data readiness only. A trader strategy must enable copy-trade context before these data are sent to AI.'
        )}>
          {tr(language,
            `AI 数据就绪 ${status.ai_ready}/${status.ai_total ?? 0} · 持仓 ${status.positions_fresh ?? 0} · 成交 ${status.fills_fresh ?? 0} · 资料 ${status.profiles_fresh ?? 0}`,
            `AI data ready ${status.ai_ready}/${status.ai_total ?? 0} · Positions ${status.positions_fresh ?? 0} · Fills ${status.fills_fresh ?? 0} · Profiles ${status.profiles_fresh ?? 0}`
          )}
        </span>
      )}
      <span className={ui.muted}>
        {tr(
          language,
          `每分钟 ${c.requests_per_min} 次请求`,
          `${c.requests_per_min} requests/min`
        )}
      </span>
      {status.oldest_update && !status.oldest_update.startsWith('0001') ? (
        <span className={ui.muted}>
          {tr(
            language,
            `最久未更新：${ago(language, Date.parse(status.oldest_update))}`,
            `Stalest: ${ago(language, Date.parse(status.oldest_update))}`
          )}
        </span>
      ) : null}
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
      {formatErrors.length ? (
        <p role="alert" className="w-full text-xs text-nofx-danger">
          {tr(
            language,
            '币安返回的数据缺少价格、时间等字段，可能改了接口格式。在修好之前，受影响的数据不会更新，页面上的数字可能是旧的：',
            'Binance answered without fields such as price or time; the format may have changed. Until fixed, the affected data stops updating and figures may be stale: '
          )}
          {formatErrors.map((error, i) => <span key={i}><NoticeText text={error} />{i < formatErrors.length - 1 ? '；' : ''}</span>)}
        </p>
      ) : null}
      {c.last_error && !paused && !formatErrors.length ? (
        <p role="alert" className="w-full text-xs text-nofx-danger">
          <NoticeText text={c.last_error} />
        </p>
      ) : null}
    </div>
  )
}

export function CopyTradePage() {
  const { language } = useLanguage()
  const [tab, setTab] = useState<Tab>('following')
  // These endpoints read local monitor snapshots. Do not tie their completion
  // together: a slow/failing status request must not hold positions or feed up.
  const { data: status } = useSWR(
    'copytrade/status',
    api.getCopyTradeStatus,
    LOCAL_SNAPSHOT_OPTIONS
  )
  const { data: follows, mutate: refreshFollows } = useSWR(
    'copytrade/follows',
    api.getFollows,
    LOCAL_SNAPSHOT_OPTIONS
  )
  const snaps = follows?.follows || EMPTY_SNAPS
  const [coin, setCoin] = useState('')

  const followed = useMemo(
    () => new Set(snaps.map((s) => s.trader.portfolio_id)),
    [snaps]
  )
  const openCoin = (symbol: string) => {
    setCoin(symbol)
    setTab('coins')
  }
  const tabs: { id: Tab; zh: string; en: string }[] = [
    { id: 'discover', zh: '发现交易员', en: 'Discover' },
    {
      id: 'following',
      zh: `我的关注 (${snaps.length})`,
      en: `Following (${snaps.length})`,
    },
    { id: 'feed', zh: '实时动态', en: 'Live feed' },
    { id: 'coins', zh: '按币种', en: 'By coin' },
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
            '关注币安合约带单员，查看持仓、开平仓动作及各币多空分布。页面每 4 秒同步本地快照；源数据更新时间受币安、关注人数及限流影响，不保证秒级实时，仅供参考。',
            'Follow Binance futures lead traders: positions, actions and per-coin long/short exposure. Local snapshots sync every 4 seconds; source freshness depends on Binance, follow count and rate limits. Not tick-real-time; for reference only.'
          )}
        </p>
      </header>
      <StatusBar language={language} status={status ?? null} />
      <nav
        className="mb-4 flex flex-wrap gap-2"
        aria-label={tr(language, '带单监控', 'Copy-trade monitor')}
      >
        {tabs.map((t) => (
          <button
            key={t.id}
            type="button"
            aria-pressed={t.id === tab}
            onClick={() => setTab(t.id)}
            className={`rounded-lg border px-3 py-2 text-sm font-semibold ${t.id === tab ? 'border-nofx-gold bg-nofx-gold/10 text-nofx-text' : `${ui.border} bg-nofx-bg-lighter text-nofx-text hover:border-nofx-gold`}`}
          >
            {tr(language, t.zh, t.en)}
          </button>
        ))}
      </nav>
      {tab === 'discover' ? (
        <DiscoverTab
          language={language}
          followed={followed}
          onFollowed={() => void refreshFollows()}
        />
      ) : null}
      {tab === 'following' ? (
        <FollowingTab
          language={language}
          snaps={snaps}
          onChanged={() => void refreshFollows()}
          onSymbol={openCoin}
        />
      ) : null}
      {tab === 'feed' ? (
        <FeedTab
          language={language}
          snaps={snaps}
          onSymbol={openCoin}
        />
      ) : null}
      {tab === 'coins' ? (
        <CoinsTab
          language={language}
          selected={coin}
          onSelect={setCoin}
        />
      ) : null}
    </div>
  )
}
