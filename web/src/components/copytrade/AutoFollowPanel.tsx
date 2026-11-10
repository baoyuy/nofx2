import { useEffect, useState } from 'react'
import useSWR from 'swr'
import { Loader2 } from 'lucide-react'
import { api } from '../../lib/api'
import type { AutoFollowInfo } from '../../lib/api/copyTrade'
import { notify } from '../../lib/notify'
import { tr, ui } from '../market/marketUi'
import { NoticeText } from '../NoticeText'
import { ago, rulesLabel } from './format'

const actionText = (language: string, a: string) =>
  a === 'follow'
    ? tr(language, '关注', 'Followed')
    : a === 'drop'
      ? tr(language, '淘汰', 'Dropped')
      : a === 'skip'
        ? tr(language, '未通过', 'Not picked')
        : tr(language, '减员', 'Trimmed')

/**
 * Auto-follow: the user sets how many lead traders to follow; the backend
 * picks good ones from Binance's leaderboard and drops weak ones.
 */
export function AutoFollowPanel({
  language,
  onChanged,
}: {
  language: string
  onChanged: () => void
}) {
  const { data, mutate } = useSWR<AutoFollowInfo>(
    'copytrade/auto',
    api.getAutoFollow,
    { refreshInterval: 30000 }
  )
  const [target, setTarget] = useState('')
  const [saving, setSaving] = useState(false)
  const [showLog, setShowLog] = useState(false)
  const cfg = data?.state.config
  const savedTarget = cfg?.target
  useEffect(() => {
    if (savedTarget !== undefined) setTarget(String(savedTarget))
  }, [savedTarget])
  if (!data || !cfg) return null
  const st = data.state
  const n = Number(target)
  const valid =
    Number.isInteger(n) && n >= data.min_target && n <= data.max_target

  const save = async (enabled: boolean) => {
    if (!valid) return
    setSaving(true)
    try {
      await api.setAutoFollow({ enabled, target: n })
      await mutate()
      onChanged()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }
  const unexclude = async (id: string) => {
    try {
      await api.removeAutoExclusion(id)
      await mutate()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : String(e))
    }
  }
  const log = data.log || []
  const exclusions = data.exclusions || []
  const ran = st.last_run && !st.last_run.startsWith('0001')

  return (
    <section className={`${ui.panel} mb-3`}>
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-semibold text-nofx-text">
          {tr(language, '自动关注', 'Auto-follow')}
        </span>
        <span
          className={`rounded px-1.5 text-[11px] ${cfg.enabled ? 'bg-nofx-success/10 text-nofx-success' : 'bg-nofx-bg-deeper text-nofx-text-muted'}`}
        >
          {cfg.enabled
            ? tr(language, '已开启', 'On')
            : tr(language, '已关闭', 'Off')}
        </span>
        <label className={`${ui.muted} flex items-center gap-1`}>
          {tr(language, '目标人数', 'Target')}
          <input
            type="number"
            min={data.min_target}
            max={data.max_target}
            value={target}
            onChange={(e) => setTarget(e.target.value)}
            className={`${ui.inputInline} w-20`}
            aria-label={tr(language, '目标人数', 'Target count')}
          />
        </label>
        {cfg.enabled ? (
          <>
            <button
              type="button"
              className={ui.btnGhost}
              disabled={saving || !valid || n === cfg.target}
              onClick={() => void save(true)}
            >
              {tr(language, '保存人数', 'Save target')}
            </button>
            <button
              type="button"
              className={ui.btnGhost}
              disabled={saving}
              onClick={() => void save(false)}
            >
              {tr(language, '关闭', 'Turn off')}
            </button>
          </>
        ) : (
          <button
            type="button"
            className={ui.btnGhost}
            disabled={saving || !valid}
            onClick={() => void save(true)}
          >
            {tr(language, '开启', 'Turn on')}
          </button>
        )}
        {saving || st.running ? (
          <Loader2 className="h-3 w-3 animate-spin text-nofx-text-muted" />
        ) : null}
        <span className={ui.muted}>
          {tr(
            language,
            `自动 ${data.auto_follows} 人 · 手动 ${data.manual_follows} 人`,
            `${data.auto_follows} auto · ${data.manual_follows} manual`
          )}
          {ran
            ? tr(
                language,
                ` · ${ago(language, Date.parse(st.last_run))}检查：新关注 ${st.added}，淘汰 ${st.dropped}，减员 ${st.trimmed}`,
                ` · checked ${ago(language, Date.parse(st.last_run))}: +${st.added}, dropped ${st.dropped}, trimmed ${st.trimmed}`
              )
            : ''}
        </span>
      </div>
      {!valid && target !== '' ? (
        <p className="mt-1 text-xs text-nofx-danger">
          {tr(
            language,
            `目标人数要在 ${data.min_target} 到 ${data.max_target} 之间`,
            `Target must be ${data.min_target}-${data.max_target}`
          )}
        </p>
      ) : null}
      {st.error ? (
        <p role="alert" className="mt-1 text-xs text-nofx-danger">
          <NoticeText text={st.error} />
        </p>
      ) : null}
      <p className={`${ui.muted} mt-1`}>
        {tr(
          language,
          '每小时检查一次。补人：从币安 90 天跟单收益榜里挑带单满 60 天、90 天收益为正且回撤不超过 30%、跟单者赚钱、不是私域账户、余额至少 5000 U、近 30 天也赚钱的人（不公开持仓的也可以，持仓按公开成交推算），每次最多补 10 人（每人要回填 30 天成交，比较慢）。新关注的人观察 7 天，期间投票只算一半。淘汰：摊平型、30 天收益为负、回撤超过 40%、余额低于 1000 U、过半持仓是一周以上的被套单、两周没交易，每天最多淘汰目标人数的 10%，淘汰后 30 天内不再选他。你手动关注的人只标记，不会被自动取关；你手动取关的人不会再被自动选回来。',
          "Checks hourly. Picks from Binance's 90-day copier-PnL board: leading 60+ days, positive 90-day ROI with drawdown ≤ 30%, copiers in profit, not private (hidden positions are fine, they are inferred from public trades), balance ≥ 5,000 USDT and a profitable last 30 days; at most 10 per run (each backfills 30 days of trades, which is slow). New picks are watched for 7 days and vote half. Drops traders who average down, lose over 30 days, draw down over 40%, fall under 1,000 USDT, sit on week-old trapped positions for over half their book, or stop trading for two weeks; at most 10% of the target a day, kept out for 30 days. Your manual follows are only flagged, never dropped; traders you unfollow are not picked again."
        )}
      </p>
      {log.length || exclusions.length ? (
        <button
          type="button"
          className="mt-1 text-xs text-nofx-gold hover:underline"
          onClick={() => setShowLog(!showLog)}
        >
          {showLog
            ? tr(language, '收起记录', 'Hide history')
            : tr(
                language,
                `查看记录（${log.length} 条操作，${exclusions.length} 人排除）`,
                `Show history (${log.length} actions, ${exclusions.length} excluded)`
              )}
        </button>
      ) : null}
      {showLog ? (
        <div className="mt-2 grid gap-3 text-xs md:grid-cols-2">
          <div>
            <div className="mb-1 font-medium text-nofx-text">
              {tr(language, '最近 30 天的操作', 'Last 30 days')}
            </div>
            {log.map((l) => (
              <div key={l.id} className="flex flex-wrap gap-x-2 text-nofx-text">
                <span className={ui.muted}>{ago(language, l.time)}</span>
                <span
                  className={
                    l.action === 'follow'
                      ? 'text-nofx-success'
                      : l.action === 'skip'
                        ? 'text-nofx-text-muted'
                        : 'text-nofx-danger'
                  }
                >
                  {actionText(language, l.action)}
                </span>
                <span>{l.nickname || l.portfolio_id}</span>
                <span className={ui.muted}>
                  {rulesLabel(language, l.reason)}
                </span>
              </div>
            ))}
          </div>
          <div>
            <div className="mb-1 font-medium text-nofx-text">
              {tr(language, '不会被自动选中的人', 'Excluded from picks')}
            </div>
            {exclusions.map((e) => (
              <div
                key={e.portfolio_id}
                className="flex flex-wrap items-center gap-x-2 text-nofx-text"
              >
                <span>{e.nickname || e.portfolio_id}</span>
                <span className={ui.muted}>
                  {rulesLabel(language, e.reason)}
                  {e.until
                    ? tr(
                        language,
                        `，到 ${new Date(e.until).toLocaleDateString()}`,
                        `, until ${new Date(e.until).toLocaleDateString()}`
                      )
                    : ''}
                </span>
                <button
                  type="button"
                  className="text-nofx-gold hover:underline"
                  onClick={() => void unexclude(e.portfolio_id)}
                >
                  {tr(language, '允许再选', 'Allow again')}
                </button>
              </div>
            ))}
          </div>
        </div>
      ) : null}
    </section>
  )
}
