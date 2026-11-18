import { useState } from 'react'
import useSWR from 'swr'
import { RefreshCw, ChevronDown } from 'lucide-react'
import { api } from '../../lib/api'
import type { BoardInfo, BoardMember } from '../../lib/api/copyTrade'
import { notify } from '../../lib/notify'
import { tr, ui } from '../market/marketUi'
import { NoticeText } from '../NoticeText'
import { ago } from './format'

// BoardSyncPanel replaces the old rule-based auto-follow: the daily sync follows
// Binance's 90-day copier-PnL leaderboard top public accounts.

function coinMix(raw?: string): { symbol: string; ratio: number }[] {
  if (!raw) return []
  try {
    const rows = JSON.parse(raw) as { symbol: string; ratio: number }[]
    return Array.isArray(rows) ? rows : []
  } catch {
    return []
  }
}

const pct = (v: number) => `${(v * 100).toFixed(1)}%`

function ObserveRow({ language, m }: { language: string; m: BoardMember }) {
  const mix = coinMix(m.coin_share).slice(0, 3)
  const last = m.last_trade_time > 0 ? ago(language, m.last_trade_time) : '—'
  return (
    <div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 border-t border-[rgba(25,36,52,0.06)] py-1 text-xs">
      <span className="font-medium text-nofx-text">{m.nickname || m.portfolio_id}</span>
      <span className={ui.muted}>
        {tr(language, `第 ${m.rank} 名`, `#${m.rank}`)}
      </span>
      <span className={ui.muted}>
        {tr(language, `最后交易 ${last}`, `last trade ${last}`)}
      </span>
      <span className={m.roi30 >= 0 ? 'text-nofx-success' : 'text-nofx-danger'}>
        {tr(language, `30 天 ${m.roi30.toFixed(1)}%`, `30d ${m.roi30.toFixed(1)}%`)}
      </span>
      {mix.length ? (
        <span className={ui.muted}>
          {mix.map((c) => `${c.symbol} ${pct(c.ratio)}`).join(' · ')}
        </span>
      ) : null}
    </div>
  )
}

export function BoardSyncPanel({
  language,
  onChanged,
}: {
  language: string
  onChanged: () => void
}) {
  const { data, mutate } = useSWR<BoardInfo>('copytrade/board', api.getBoard, {
    refreshInterval: 30000,
  })
  const [saving, setSaving] = useState(false)
  const [showObserve, setShowObserve] = useState(true)
  const [rulesOpen, setRulesOpen] = useState(false)
  if (!data) return null
  const st = data.state
  const cfg = st.config
  const members = data.members || []
  const observe = members.filter((m) => m.observe)
  const ran = st.last_run && !st.last_run.startsWith('0001')

  const act = async (fn: () => Promise<unknown>) => {
    setSaving(true)
    try {
      await fn()
      await mutate()
      onChanged()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <section className={`${ui.panel} mb-3`}>
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-semibold text-nofx-text">
          {tr(language, '榜单同步', 'Board sync')}
        </span>
        <span
          className={`rounded px-1.5 text-[11px] ${cfg.enabled ? 'bg-nofx-success/10 text-nofx-success' : 'bg-nofx-bg-deeper text-nofx-text-muted'}`}
        >
          {cfg.enabled
            ? tr(language, '已开启', 'On')
            : tr(language, '已关闭', 'Off')}
        </span>
        <span className={`${ui.muted} tabular-nums`}>
          {tr(
            language,
            `关注 ${data.following} 人 · 只观察 ${data.observe} 人`,
            `${data.following} followed · ${data.observe} observe-only`
          )}
        </span>
        <button
          type="button"
          className={ui.btnGhost}
          disabled={saving || st.running}
          onClick={() => void act(() => api.runBoard())}
        >
          <RefreshCw
            className={`mr-1 inline h-3 w-3 ${st.running ? 'animate-spin' : ''}`}
          />
          {tr(language, '立即同步', 'Sync now')}
        </button>
        <button
          type="button"
          className={ui.btnGhost}
          disabled={saving}
          onClick={() => void act(() => api.setBoard(!cfg.enabled))}
        >
          {cfg.enabled
            ? tr(language, '关闭', 'Turn off')
            : tr(language, '开启', 'Turn on')}
        </button>
        <button
          type="button"
          onClick={() => setRulesOpen(!rulesOpen)}
          aria-expanded={rulesOpen}
          className="ml-auto inline-flex items-center gap-1 text-xs text-nofx-text-muted hover:text-nofx-gold"
        >
          {tr(language, '规则说明', 'How it picks')}
          <ChevronDown
            className={`h-3 w-3 transition-transform ${rulesOpen ? 'rotate-180' : ''}`}
          />
        </button>
      </div>

      <p className={`${ui.muted} mt-1 tabular-nums`}>
        {tr(
          language,
          `每天同步一次；取 90 天跟单者收益榜前 ${data.board_size} 个公开账户。`,
          `Synced daily: the first ${data.board_size} public accounts of the 90-day copier-PnL board.`
        )}
        {ran
          ? tr(
              language,
              `上次同步 ${ago(language, Date.parse(st.last_run))}：扫描 ${st.scanned} 行、新增 ${st.added}、取关 ${st.dropped}。`,
              `Last sync ${ago(language, Date.parse(st.last_run))}: scanned ${st.scanned}, added ${st.added}, dropped ${st.dropped}.`
            )
          : ''}
      </p>

      {st.error ? (
        <p role="alert" className="mt-1 text-xs text-nofx-danger">
          <NoticeText text={st.error} />
        </p>
      ) : null}

      {rulesOpen ? (
        <div
          className={`${ui.muted} mt-2 space-y-1 border-t border-[rgba(25,36,52,0.08)] pt-2 text-xs`}
        >
          <p>
            {tr(
              language,
              '进入前 100 名即关注；掉到 150 名以外，或连续 3 天不在榜上，才取消关注——留出缓冲，避免在边界反复进出。',
              `Followed upon entering the top 100; dropped only after falling past rank 150 or missing 3 days in a row — a buffer against boundary churn.`
            )}
          </p>
          <p>
            {tr(
              language,
              '私域账户读不到成交，列为「只观察」，不关注也不计票，但能看到他们的最后交易时间、30 天业绩和币种成交占比。信号账户会转成对应的公开账户。',
              'Private accounts hide their trades, so they are listed as observe-only: not followed and not voted, but you can see their last trade, 30-day results and coin mix. Signal accounts map to their public portfolio.'
            )}
          </p>
          <p>
            {tr(
              language,
              '手动关注不受榜单同步影响；你手动取关的人不会被同步加回来。',
              'Your manual follows are untouched; traders you unfollow by hand are not added back.'
            )}
          </p>
        </div>
      ) : null}

      {observe.length ? (
        <div className="mt-2">
          <button
            type="button"
            className="text-xs font-medium text-nofx-text hover:text-nofx-gold"
            onClick={() => setShowObserve(!showObserve)}
            aria-expanded={showObserve}
          >
            {tr(
              language,
              `只观察的私域账户 (${observe.length})`,
              `Observe-only private accounts (${observe.length})`
            )}
          </button>
          {showObserve ? (
            <div className="mt-1">
              {observe.map((m) => (
                <ObserveRow key={m.portfolio_id} language={language} m={m} />
              ))}
            </div>
          ) : null}
        </div>
      ) : null}
    </section>
  )
}
