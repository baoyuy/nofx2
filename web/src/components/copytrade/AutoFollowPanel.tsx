import { useEffect, useState } from 'react'
import useSWR from 'swr'
import { ChevronDown, Loader2 } from 'lucide-react'
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
        : tr(language, '清出', 'Trimmed')

// The rules used to be one 250-character paragraph under the controls, which
// everyone scrolled past. Remember whether this browser wanted them open.
const RULES_KEY = 'nofx.copytrade.rulesOpen'

const loadRulesOpen = () => {
  try {
    return localStorage.getItem(RULES_KEY) === '1'
  } catch {
    return false
  }
}

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
  const [rulesOpen, setRulesOpen] = useState(loadRulesOpen)
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
  const following = data.auto_follows + data.manual_follows
  const progress = cfg.target
    ? Math.min(100, Math.round((following / cfg.target) * 100))
    : 0
  const log = data.log || []
  const exclusions = data.exclusions || []
  const ran = st.last_run && !st.last_run.startsWith('0001')
  const lastRun = ran
    ? [
        st.added ? tr(language, `新关注 ${st.added}`, `+${st.added}`) : '',
        st.dropped
          ? tr(language, `淘汰 ${st.dropped}`, `${st.dropped} dropped`)
          : '',
        st.trimmed
          ? tr(language, `清出 ${st.trimmed}`, `${st.trimmed} trimmed`)
          : '',
      ]
        .filter(Boolean)
        .join(' · ')
    : ''

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
  const toggleRules = () => {
    const next = !rulesOpen
    setRulesOpen(next)
    try {
      localStorage.setItem(RULES_KEY, next ? '1' : '0')
    } catch {
      // storage unavailable: the toggle still works for this visit
    }
  }
  const ruleBlock = (title: string, items: string[]) => (
    <div>
      <div className="mb-0.5 font-medium text-nofx-text">{title}</div>
      <ul className="list-disc space-y-0.5 pl-4">
        {items.map((item) => (
          <li key={item}>{item}</li>
        ))}
      </ul>
    </div>
  )

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
              {tr(language, '应用', 'Apply')}
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
        <span className={`${ui.muted} tabular-nums`}>
          {tr(
            language,
            `当前 ${following} / 目标 ${cfg.target}`,
            `${following} of ${cfg.target}`
          )}
        </span>
        <button
          type="button"
          onClick={toggleRules}
          aria-expanded={rulesOpen}
          className="inline-flex items-center gap-1 text-xs text-nofx-text-muted hover:text-nofx-gold"
        >
          {tr(language, '规则说明', 'How it picks')}
          <ChevronDown
            className={`h-3 w-3 transition-transform ${rulesOpen ? 'rotate-180' : ''}`}
          />
        </button>
        {log.length || exclusions.length ? (
          <button
            type="button"
            className="ml-auto inline-flex items-center gap-1 rounded-lg border border-[rgba(25,36,52,0.14)] px-2 py-1 text-xs text-nofx-gold hover:border-nofx-gold"
            onClick={() => setShowLog(!showLog)}
          >
            {showLog
              ? tr(language, '收起记录', 'Hide history')
              : tr(
                  language,
                  `查看记录（${log.length} 条操作，${exclusions.length} 人排除）`,
                  `History (${log.length} actions, ${exclusions.length} excluded)`
                )}
          </button>
        ) : null}
      </div>
      <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1">
        <span
          className="h-1.5 w-32 overflow-hidden rounded-full bg-nofx-bg-deeper"
          title={tr(
            language,
            `已关注 ${following} 人，目标 ${cfg.target} 人`,
            `${following} followed of a ${cfg.target} target`
          )}
        >
          <span
            className="block h-full rounded-full bg-nofx-gold"
            style={{ width: `${progress}%` }}
          />
        </span>
        <span className={ui.muted}>
          {tr(
            language,
            `自动 ${data.auto_follows} 人 · 手动 ${data.manual_follows} 人`,
            `${data.auto_follows} auto · ${data.manual_follows} manual`
          )}
        </span>
        {ran ? (
          <span className={ui.muted}>
            {tr(
              language,
              `· 上次检查 ${ago(language, Date.parse(st.last_run))}`,
              `· checked ${ago(language, Date.parse(st.last_run))}`
            )}
            {lastRun ? ` · ${lastRun}` : ''}
          </span>
        ) : null}
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
      {rulesOpen ? (
        <div
          className={`${ui.muted} mt-2 space-y-1.5 border-t border-[rgba(25,36,52,0.08)] pt-2`}
        >
          <p>
            {tr(
              language,
              '每小时检查一次，每次最多补 10 人、淘汰目标人数的 10%。',
              'Runs hourly: at most 10 picks and 10% of the target dropped per run.'
            )}
          </p>
          {ruleBlock(tr(language, '入选条件', 'To be picked'), [
            tr(language, '带单满 60 天', 'Leading for 60+ days'),
            tr(
              language,
              '90 天收益为正，回撤不超过 30%',
              'Positive 90-day ROI, drawdown ≤ 30%'
            ),
            tr(
              language,
              '近 30 天也赚钱，且跟单者赚钱',
              'A profitable last 30 days, with copiers in profit'
            ),
            tr(
              language,
              '不是私域账户，余额至少 5000 U（不公开持仓也可以，按成交推算）',
              'Not private, balance ≥ 5,000 USDT (hidden positions are fine, they are inferred)'
            ),
          ])}
          {ruleBlock(tr(language, '淘汰规则', 'To be dropped'), [
            tr(
              language,
              '摊平型、两周没交易、过半持仓是一周以上的被套单',
              'Averages down, no trades for two weeks, or over half the book is week-old trapped positions'
            ),
            tr(
              language,
              '30 天收益为负、回撤超过 40%',
              'Negative 30-day ROI, drawdown over 40%'
            ),
            tr(language, '余额低于 1000 U', 'Balance under 1,000 USDT'),
            tr(
              language,
              '成交记录两天都没读完',
              'Trade history unreadable for two days'
            ),
          ])}
          {ruleBlock(tr(language, '节奏与你', 'Timing and your choices'), [
            tr(
              language,
              '新关注的人观察 7 天，期间投票只算一半',
              'New picks are watched for 7 days; their votes count half'
            ),
            tr(
              language,
              '淘汰后 30 天内不再选他（已关闭或私域的 90 天）',
              'Dropped traders stay out for 30 days (90 for closed or private)'
            ),
            tr(
              language,
              '你手动关注的人只标记不淘汰；你手动取关的人不会再被自动选回来',
              'Your manual follows are only flagged; traders you unfollow are never picked again'
            ),
          ])}
        </div>
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
