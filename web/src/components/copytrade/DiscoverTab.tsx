import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  ArrowDown,
  ArrowUp,
  Loader2,
  Plus,
  RefreshCw,
  RotateCcw,
} from 'lucide-react'
import { api } from '../../lib/api'
import type { Leader } from '../../lib/api/copyTrade'
import { NoticeText } from '../NoticeText'
import { notify } from '../../lib/notify'
import { tr, ui } from '../market/marketUi'
import { pct, usd } from './format'
import {
  activeFilters,
  defaultFilters,
  filterLeaders,
  leaderDays,
  parseFilters,
  sortLeaders,
  type LeaderFilters,
  type LeaderSortKey,
} from './leaderFilters'

const PAGE_SIZE = 30
const MAX_PAGE = 50
const STORE_KEY = 'copytrade.discover.v1'
const sortKeys: LeaderSortKey[] = [
  'binance',
  'roi',
  'pnl',
  'mdd',
  'winRate',
  'sharpRatio',
  'copierPnl',
  'aum',
  'copiers',
  'days',
]

interface View {
  timeRange: string
  sort: string
  /** pages fetched per scan */
  scan: number
  filters: LeaderFilters
  sortKey: LeaderSortKey
  desc: boolean
}

const defaultView: View = {
  timeRange: '30D',
  sort: 'COPIER_PNL',
  scan: 3,
  filters: defaultFilters,
  sortKey: 'binance',
  desc: true,
}

function loadView(): View {
  try {
    const raw = JSON.parse(localStorage.getItem(STORE_KEY) || 'null')
    if (!raw || typeof raw !== 'object') return defaultView
    return {
      timeRange: ['7D', '30D', '90D'].includes(raw.timeRange)
        ? raw.timeRange
        : defaultView.timeRange,
      sort: ['COPIER_PNL', 'ROI', 'PNL'].includes(raw.sort)
        ? raw.sort
        : defaultView.sort,
      scan: [1, 3, 5, 10].includes(raw.scan) ? raw.scan : defaultView.scan,
      filters: parseFilters(raw.filters),
      sortKey: sortKeys.includes(raw.sortKey) ? raw.sortKey : 'binance',
      desc: raw.desc !== false,
    }
  } catch {
    return defaultView
  }
}

function saveView(v: View) {
  try {
    localStorage.setItem(STORE_KEY, JSON.stringify(v))
  } catch {
    // storage full or blocked: the view just is not remembered
  }
}

// Threshold choices; null = any. Percentages as Binance reports them.
const ROI_MIN = [0, 20, 50, 100, 300]
const PNL_MIN = [0, 1000, 10000, 100000]
const MDD_MAX = [5, 10, 20, 30, 50]
const WIN_MIN = [40, 50, 60, 70, 80]
const SHARPE_MIN = [0.5, 1, 2, 3]
const COPIER_PNL_MIN = [0, 1000, 10000, 100000]
const AUM_MIN = [10000, 100000, 1000000]
const DAYS_MIN = [30, 90, 180, 365]
const COPIERS_MIN = [10, 50, 100, 300]

function Sparkline({ points }: { points: Leader['chartItems'] }) {
  const values = (points || []).map((p) => Number(p.value) || 0)
  if (values.length < 2) return <span className={ui.muted}>--</span>
  const lo = Math.min(...values)
  const hi = Math.max(...values)
  const span = hi - lo || 1
  const d = values
    .map(
      (v, i) =>
        `${(i / (values.length - 1)) * 80},${22 - ((v - lo) / span) * 20}`
    )
    .join(' ')
  const up = values[values.length - 1] >= values[0]
  return (
    <svg width="80" height="24" aria-hidden>
      <polyline
        points={d}
        fill="none"
        strokeWidth="1.5"
        stroke={
          up
            ? 'var(--color-nofx-success, #2e8b57)'
            : 'var(--color-nofx-danger, #d6433a)'
        }
      />
    </svg>
  )
}

/** A select over number thresholds where '' means any. */
function NumberSelect({
  label,
  value,
  options,
  format,
  onChange,
  anyLabel,
}: {
  label: string
  value: number | null
  options: number[]
  format: (v: number) => string
  onChange: (v: number | null) => void
  anyLabel: string
}) {
  return (
    <label>
      <span className={ui.label}>{label}</span>
      <select
        className={`${ui.inputInline} ${value !== null ? 'border-nofx-gold' : ''}`}
        value={value === null ? '' : String(value)}
        onChange={(e) =>
          onChange(e.target.value === '' ? null : Number(e.target.value))
        }
      >
        <option value="">{anyLabel}</option>
        {options.map((v) => (
          <option key={v} value={v}>
            {format(v)}
          </option>
        ))}
      </select>
    </label>
  )
}

export function DiscoverTab({
  language,
  followed,
  onFollowed,
}: {
  language: string
  followed: Set<string>
  onFollowed: () => void
}) {
  const [view, setView] = useState<View>(loadView)
  const { timeRange, sort, scan, filters, sortKey, desc } = view
  const [nameInput, setNameInput] = useState('')
  const [nickname, setNickname] = useState('')
  const [list, setList] = useState<Leader[]>([])
  const [total, setTotal] = useState(0)
  /** last page fetched */
  const [pages, setPages] = useState(0)
  const [ended, setEnded] = useState(false)
  const [loadedAt, setLoadedAt] = useState(0)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')
  const [idInput, setIdInput] = useState('')
  // Bumped per scan so an older scan stops writing when a newer one starts.
  const run = useRef(0)

  const update = (patch: Partial<View>) =>
    setView((v) => {
      const next = { ...v, ...patch }
      saveView(next)
      return next
    })
  const setFilter = <K extends keyof LeaderFilters>(
    k: K,
    v: LeaderFilters[K]
  ) => update({ filters: { ...filters, [k]: v } })

  // Search by nickname once typing pauses.
  useEffect(() => {
    const t = setTimeout(() => setNickname(nameInput.trim()), 500)
    return () => clearTimeout(t)
  }, [nameInput])

  /** Fetches `count` pages from `first`, appending unless first is 1. */
  const scanPages = useCallback(
    async (first: number, count: number) => {
      const id = ++run.current
      setLoading(true)
      setError('')
      let acc: Leader[] = []
      try {
        for (let p = first; p < first + count && p <= MAX_PAGE; p++) {
          const page = await api.getLeaderboard(timeRange, sort, p, nickname)
          if (id !== run.current) return
          const rows = page.list || []
          acc = acc.concat(rows)
          const fresh = acc
          setList((prev) => {
            const base = first === 1 ? [] : prev
            const seen = new Set(base.map((r) => r.leadPortfolioId))
            return base.concat(
              fresh.filter((r) => !seen.has(r.leadPortfolioId))
            )
          })
          setTotal(page.total || 0)
          setPages(p)
          setLoadedAt(Date.now())
          const last = rows.length < PAGE_SIZE || p >= MAX_PAGE
          setEnded(last)
          if (last) break
        }
      } catch (e) {
        if (id === run.current)
          setError(e instanceof Error ? e.message : String(e))
      } finally {
        if (id === run.current) setLoading(false)
      }
    },
    [timeRange, sort, nickname]
  )

  // Cached pages come back at once, so a rescan from page 1 is cheap.
  useEffect(() => {
    void scanPages(1, scan)
  }, [scanPages, scan])
  useEffect(
    () => () => {
      run.current++
    },
    []
  )

  const rows = useMemo(
    () =>
      sortLeaders(
        filterLeaders(list, filters, followed, loadedAt),
        sortKey,
        desc,
        loadedAt
      ),
    [list, filters, followed, sortKey, desc, loadedAt]
  )
  const nActive = activeFilters(filters)

  const follow = async (id: string) => {
    setBusy(id)
    try {
      const f = await api.follow(id)
      notify.success(
        tr(
          language,
          `已关注 ${f.nickname}，正在拉取最近 30 天的成交`,
          `Following ${f.nickname}; fetching the last 30 days of trades`
        )
      )
      setIdInput('')
      onFollowed()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy('')
    }
  }

  const any = tr(language, '不限', 'Any')
  const money = (v: number) => (v ? `≥ ${usd(v)}` : '≥ 0')
  const header = (key: LeaderSortKey, label: string) => (
    <th className="font-normal">
      <button
        type="button"
        className={`inline-flex items-center gap-0.5 hover:text-nofx-text ${sortKey === key ? 'text-nofx-gold' : ''}`}
        onClick={() =>
          update(
            sortKey === key
              ? desc
                ? { desc: false }
                : { sortKey: 'binance', desc: true }
              : { sortKey: key, desc: key !== 'mdd' }
          )
        }
        title={tr(
          language,
          '点击排序：降序 → 升序 → 恢复币安顺序',
          'Click to sort: descending → ascending → Binance order'
        )}
      >
        {label}
        {sortKey === key ? (
          desc ? (
            <ArrowDown className="h-3 w-3" />
          ) : (
            <ArrowUp className="h-3 w-3" />
          )
        ) : null}
      </button>
    </th>
  )

  return (
    <div className="space-y-3">
      <section className={ui.panel}>
        <h2 className={ui.title}>
          {tr(language, '按 ID 或链接关注', 'Follow by ID or link')}
        </h2>
        <form
          className="flex flex-wrap gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            if (idInput.trim()) void follow(idInput.trim())
          }}
        >
          <input
            className={`${ui.inputInline} min-w-[260px] flex-1`}
            value={idInput}
            onChange={(e) => setIdInput(e.target.value)}
            placeholder={tr(
              language,
              '粘贴带单员主页链接，或填入数字 ID',
              'Paste a lead trader page link or the numeric ID'
            )}
          />
          <button
            type="submit"
            className={`${ui.btnGold} w-auto`}
            disabled={!idInput.trim() || Boolean(busy)}
          >
            {tr(language, '关注', 'Follow')}
          </button>
        </form>
      </section>

      <section className={ui.panel}>
        <div className="mb-2 flex flex-wrap items-end gap-3">
          <label>
            <span className={ui.label}>
              {tr(language, '时间范围', 'Period')}
            </span>
            <select
              className={ui.inputInline}
              value={timeRange}
              onChange={(e) => update({ timeRange: e.target.value })}
            >
              <option value="7D">{tr(language, '7 天', '7 days')}</option>
              <option value="30D">{tr(language, '30 天', '30 days')}</option>
              <option value="90D">{tr(language, '90 天', '90 days')}</option>
            </select>
          </label>
          <label>
            <span className={ui.label}>
              {tr(language, '币安榜单', 'Binance ranking')}
            </span>
            <select
              className={ui.inputInline}
              value={sort}
              onChange={(e) => update({ sort: e.target.value })}
            >
              <option value="COPIER_PNL">
                {tr(language, '跟单者收益', 'Copier PnL')}
              </option>
              <option value="ROI">{tr(language, '收益率', 'ROI')}</option>
              <option value="PNL">{tr(language, '盈利额', 'PnL')}</option>
            </select>
          </label>
          <label>
            <span className={ui.label}>{tr(language, '每次扫描', 'Scan')}</span>
            <select
              className={ui.inputInline}
              value={scan}
              onChange={(e) => update({ scan: Number(e.target.value) })}
            >
              {[1, 3, 5, 10].map((n) => (
                <option key={n} value={n}>
                  {tr(
                    language,
                    `${n} 页（${n * PAGE_SIZE} 人）`,
                    `${n} page${n > 1 ? 's' : ''} (${n * PAGE_SIZE})`
                  )}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span className={ui.label}>
              {tr(language, '搜索昵称', 'Nickname')}
            </span>
            <input
              className={`${ui.inputInline} w-36`}
              value={nameInput}
              onChange={(e) => setNameInput(e.target.value)}
              placeholder={tr(language, '昵称关键字', 'Keyword')}
              maxLength={50}
            />
          </label>
          <button
            type="button"
            className={`${ui.btnGhost} mb-1 ml-auto inline-flex items-center gap-1`}
            onClick={() => void scanPages(1, scan)}
            disabled={loading}
          >
            <RefreshCw className={`h-3 w-3 ${loading ? 'animate-spin' : ''}`} />
            {tr(language, '刷新', 'Refresh')}
          </button>
        </div>

        <div className="mb-2 flex flex-wrap items-end gap-3 rounded-lg bg-nofx-bg-deeper p-2">
          <NumberSelect
            label={tr(language, '收益率不低于', 'ROI ≥')}
            value={filters.minRoi}
            options={ROI_MIN}
            format={(v) => `${v}%`}
            onChange={(v) => setFilter('minRoi', v)}
            anyLabel={any}
          />
          <NumberSelect
            label={tr(language, '盈利不低于', 'PnL ≥')}
            value={filters.minPnl}
            options={PNL_MIN}
            format={money}
            onChange={(v) => setFilter('minPnl', v)}
            anyLabel={any}
          />
          <NumberSelect
            label={tr(language, '最大回撤不超过', 'Max drawdown ≤')}
            value={filters.maxMdd}
            options={MDD_MAX}
            format={(v) => `${v}%`}
            onChange={(v) => setFilter('maxMdd', v)}
            anyLabel={any}
          />
          <NumberSelect
            label={tr(language, '胜率不低于', 'Win rate ≥')}
            value={filters.minWin}
            options={WIN_MIN}
            format={(v) => `${v}%`}
            onChange={(v) => setFilter('minWin', v)}
            anyLabel={any}
          />
          <NumberSelect
            label={tr(language, '夏普不低于', 'Sharpe ≥')}
            value={filters.minSharpe}
            options={SHARPE_MIN}
            format={(v) => String(v)}
            onChange={(v) => setFilter('minSharpe', v)}
            anyLabel={any}
          />
          <NumberSelect
            label={tr(language, '跟单者收益不低于', 'Copier PnL ≥')}
            value={filters.minCopierPnl}
            options={COPIER_PNL_MIN}
            format={money}
            onChange={(v) => setFilter('minCopierPnl', v)}
            anyLabel={any}
          />
          <NumberSelect
            label={tr(language, '管理资金不低于', 'AUM ≥')}
            value={filters.minAum}
            options={AUM_MIN}
            format={money}
            onChange={(v) => setFilter('minAum', v)}
            anyLabel={any}
          />
          <NumberSelect
            label={tr(language, '带单天数不少于', 'Leading ≥ days')}
            value={filters.minDays}
            options={DAYS_MIN}
            format={(v) => tr(language, `${v} 天`, `${v} days`)}
            onChange={(v) => setFilter('minDays', v)}
            anyLabel={any}
          />
          <NumberSelect
            label={tr(language, '跟单人数不少于', 'Copiers ≥')}
            value={filters.minCopiers}
            options={COPIERS_MIN}
            format={(v) => tr(language, `${v} 人`, String(v))}
            onChange={(v) => setFilter('minCopiers', v)}
            anyLabel={any}
          />
          <label>
            <span className={ui.label}>
              {tr(language, '交易品种', 'Market')}
            </span>
            <select
              className={`${ui.inputInline} ${filters.market !== 'all' ? 'border-nofx-gold' : ''}`}
              value={filters.market}
              onChange={(e) =>
                setFilter('market', e.target.value as LeaderFilters['market'])
              }
            >
              <option value="all">{any}</option>
              <option value="crypto">
                {tr(language, '只做加密币', 'Crypto only')}
              </option>
              <option value="stocks">
                {tr(language, '含股票合约', 'Trades stocks')}
              </option>
            </select>
          </label>
          <label>
            <span className={ui.label}>
              {tr(language, '下单方式', 'Orders')}
            </span>
            <select
              className={`${ui.inputInline} ${filters.mode !== 'all' ? 'border-nofx-gold' : ''}`}
              value={filters.mode}
              onChange={(e) =>
                setFilter('mode', e.target.value as LeaderFilters['mode'])
              }
            >
              <option value="all">{any}</option>
              <option value="api">
                {tr(language, 'API 程序下单', 'API (bot)')}
              </option>
              <option value="manual">
                {tr(language, '手动下单', 'Manual')}
              </option>
            </select>
          </label>
          <label>
            <span className={ui.label}>
              {tr(language, '关注状态', 'Followed')}
            </span>
            <select
              className={`${ui.inputInline} ${filters.followState !== 'all' ? 'border-nofx-gold' : ''}`}
              value={filters.followState}
              onChange={(e) =>
                setFilter(
                  'followState',
                  e.target.value as LeaderFilters['followState']
                )
              }
            >
              <option value="all">{any}</option>
              <option value="unfollowed">
                {tr(language, '未关注', 'Not followed')}
              </option>
              <option value="followed">
                {tr(language, '已关注', 'Followed')}
              </option>
            </select>
          </label>
          <label className="flex items-center gap-1.5 pb-2 text-sm text-nofx-text">
            <input
              type="checkbox"
              checked={filters.openSlots}
              onChange={(e) => setFilter('openSlots', e.target.checked)}
            />
            {tr(language, '还有跟单名额', 'Has open copy slots')}
          </label>
          {nActive ? (
            <button
              type="button"
              className={`${ui.btnGhost} mb-1 inline-flex items-center gap-1`}
              onClick={() => update({ filters: defaultFilters })}
            >
              <RotateCcw className="h-3 w-3" />
              {tr(
                language,
                `清除 ${nActive} 个筛选`,
                `Clear ${nActive} filters`
              )}
            </button>
          ) : null}
        </div>
        <p className={`${ui.muted} mb-2`}>
          {tr(
            language,
            `已扫描前 ${pages} 页 ${list.length} 人（币安共 ${total} 位带单员），符合筛选 ${rows.length} 人。筛选和点表头排序都在已扫描的人里进行，想找得更全就多扫几页。每页约 4 秒，榜单缓存 10 分钟。`,
            `Scanned ${pages} page(s), ${list.length} traders (of ${total} on Binance); ${rows.length} match. Filters and column sorting apply to scanned traders, so scan more pages for a wider search. About 4 s per page; the ranking is cached for 10 minutes.`
          )}
        </p>
        {error ? (
          <p role="alert" className="py-3 text-center text-sm text-nofx-danger">
            <NoticeText text={error} />
          </p>
        ) : null}
        <div className="overflow-x-auto">
          <table className="w-full min-w-[1000px] text-sm">
            <thead className="text-left text-xs text-nofx-text-muted">
              <tr>
                <th className="py-2 font-normal">
                  {tr(language, '带单员', 'Trader')}
                </th>
                {header('roi', tr(language, '收益率', 'ROI'))}
                {header('pnl', tr(language, '盈利', 'PnL'))}
                {header('mdd', tr(language, '最大回撤', 'MDD'))}
                {header('winRate', tr(language, '胜率', 'Win'))}
                {header('sharpRatio', tr(language, '夏普', 'Sharpe'))}
                {header('copierPnl', tr(language, '跟单者收益', 'Copier PnL'))}
                {header('aum', tr(language, '管理资金', 'AUM'))}
                {header('copiers', tr(language, '跟单人数', 'Copiers'))}
                {header('days', tr(language, '带单天数', 'Days'))}
                <th className="font-normal">
                  {tr(language, '收益曲线', 'Curve')}
                </th>
                <th />
              </tr>
            </thead>
            <tbody className="text-nofx-text">
              {rows.map((r) => {
                const days = leaderDays(r, loadedAt)
                return (
                  <tr
                    key={r.leadPortfolioId}
                    className={`border-t ${ui.border}`}
                  >
                    <td className="py-2">
                      <div className="flex items-center gap-2">
                        <img
                          src={r.avatarUrl}
                          alt=""
                          className="h-7 w-7 rounded-full"
                          loading="lazy"
                          referrerPolicy="no-referrer"
                        />
                        <div>
                          <div className="font-medium">{r.nickname}</div>
                          <div className="flex gap-1 text-[10px] text-nofx-text-muted">
                            {r.apiKeyTag ? (
                              <span className="rounded bg-nofx-bg-deeper px-1">
                                API
                              </span>
                            ) : null}
                            {r.tradFiTag ? (
                              <span className="rounded bg-nofx-bg-deeper px-1">
                                {tr(language, '含股票合约', 'Stocks')}
                              </span>
                            ) : null}
                          </div>
                        </div>
                      </div>
                    </td>
                    <td
                      className={
                        r.roi >= 0 ? 'text-nofx-success' : 'text-nofx-danger'
                      }
                    >
                      {pct(r.roi)}
                    </td>
                    <td>{usd(r.pnl)}</td>
                    <td className={r.mdd > 30 ? 'text-nofx-danger' : ''}>
                      {r.mdd.toFixed(1)}%
                    </td>
                    <td>{r.winRate.toFixed(0)}%</td>
                    <td>{r.sharpRatio ? r.sharpRatio.toFixed(2) : '--'}</td>
                    <td>{usd(r.copierPnl)}</td>
                    <td>{usd(r.aum)}</td>
                    <td className="tabular-nums">
                      {r.currentCopyCount}/{r.maxCopyCount}
                    </td>
                    <td className="tabular-nums">
                      {days === null ? '--' : Math.floor(days)}
                    </td>
                    <td>
                      <Sparkline points={r.chartItems} />
                    </td>
                    <td className="text-right">
                      {followed.has(r.leadPortfolioId) ? (
                        <span className={ui.muted}>
                          {tr(language, '已关注', 'Following')}
                        </span>
                      ) : (
                        <button
                          type="button"
                          className={`${ui.btnGhost} inline-flex items-center gap-1`}
                          disabled={Boolean(busy)}
                          onClick={() => void follow(r.leadPortfolioId)}
                        >
                          {busy === r.leadPortfolioId ? (
                            <Loader2 className="h-3 w-3 animate-spin" />
                          ) : (
                            <Plus className="h-3 w-3" />
                          )}
                          {tr(language, '关注', 'Follow')}
                        </button>
                      )}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
        {!rows.length && !loading && list.length ? (
          <p className={`py-6 text-center ${ui.muted}`}>
            {tr(
              language,
              '已扫描的人里没有符合这些条件的，放宽筛选或再扫几页。',
              'No scanned trader matches; loosen the filters or scan more pages.'
            )}
          </p>
        ) : null}
        <div className="mt-3 flex items-center justify-center gap-3">
          {loading ? (
            <span className={`${ui.muted} inline-flex items-center gap-1`}>
              <Loader2 className="h-3 w-3 animate-spin" />
              {tr(
                language,
                `正在读取第 ${pages + 1} 页…`,
                `Loading page ${pages + 1}…`
              )}
            </span>
          ) : !ended ? (
            <button
              type="button"
              className={ui.btnGhost}
              onClick={() => void scanPages(pages + 1, scan)}
            >
              {tr(
                language,
                `再扫 ${scan} 页（${scan * PAGE_SIZE} 人）`,
                `Scan ${scan} more page${scan > 1 ? 's' : ''}`
              )}
            </button>
          ) : (
            <span className={ui.muted}>
              {tr(language, '已经到底了', 'No more traders')}
            </span>
          )}
        </div>
      </section>
    </div>
  )
}
