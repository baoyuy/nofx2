import { useEffect, useState } from 'react'
import useSWR from 'swr'
import { Play, Save, Loader2 } from 'lucide-react'
import { api } from '../../lib/api'
import type {
  CoinPoolInfo,
  OutcomeStat,
  CoinScoreModelInfo,
  CoinScoreSettings as Settings,
} from '../../lib/api/copyTrade'
import { notify } from '../../lib/notify'
import { tr, ui } from '../market/marketUi'
import { NoticeText } from '../NoticeText'
import { ago } from './format'

// Settings for the per-coin AI reading: run every coin on demand, or on a timer.
export function CoinScoreSettings({ language }: { language: string }) {
  const { data, mutate } = useSWR<Settings>(
    'copytrade/coin-score/settings',
    () => api.getCoinScoreSettings(),
    { refreshInterval: 8000 }
  )
  const { data: modelInfo, mutate: mutateModels } = useSWR<CoinScoreModelInfo>(
    'copytrade/coin-score/models',
    () => api.getCoinScoreModels(),
    { revalidateOnFocus: false }
  )
  const { data: pool, mutate: mutatePool } = useSWR<CoinPoolInfo>(
    'copytrade/coin-pool',
    () => api.getCoinPool(),
    { refreshInterval: 15000 }
  )
  const { data: outcomes } = useSWR<{ rows: OutcomeStat[] }>(
    'copytrade/coin-score/outcomes',
    () => api.getCoinScoreOutcomes(),
    { refreshInterval: 120000 }
  )
  const [enabled, setEnabled] = useState(false)
  const [poolAuto, setPoolAuto] = useState(false)
  const [topN, setTopN] = useState('0')
  const [conc, setConc] = useState('4')
  const [minVol, setMinVol] = useState('0')
  const [poolRefresh, setPoolRefresh] = useState('5')
  const [poolTTL, setPoolTTL] = useState('12')
  const [interval, setIntervalMin] = useState('60')
  const [saving, setSaving] = useState(false)
  const [dirty, setDirty] = useState(false)
  const [modelID, setModelID] = useState('')
  useEffect(() => {
    if (modelInfo && !modelID)
      setModelID(modelInfo.selected || modelInfo.default || '')
  }, [modelInfo, modelID])
  const changeModel = async (id: string) => {
    setModelID(id)
    try {
      await api.setCoinScoreModel(id)
      await mutateModels()
      await mutate()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : String(e))
    }
  }

  useEffect(() => {
    if (data && !dirty) {
      setEnabled(data.enabled)
      setPoolAuto(Boolean(data.pool_auto))
      setTopN(String(data.top_n ?? 0))
      setConc(String(data.concurrency ?? 4))
      setMinVol(String(data.min_quote_volume ?? 0))
      setPoolRefresh(String(data.pool_refresh_minutes ?? 5))
      setPoolTTL(String(data.pool_ttl_minutes ?? 12))
      setIntervalMin(String(data.interval_minutes))
    }
  }, [data, dirty])

  if (!data) return null
  const n = Number(interval)
  const valid = Number.isInteger(n) && n >= data.min_interval && n <= 1440
  const running = data.running
  const progress = data.total ? Math.round((data.done / data.total) * 100) : 0
  const ran = data.last_run > 0

  const save = async (nextEnabled: boolean) => {
    if (!valid) return
    setSaving(true)
    try {
      await api.setCoinScoreSettings({ enabled: nextEnabled, interval_minutes: n, pool_auto: poolAuto, top_n: Number(topN) || 0, concurrency: Number(conc) || 1, min_quote_volume: Number(minVol) || 0, pool_refresh_minutes: Number(poolRefresh) || 5, pool_ttl_minutes: Number(poolTTL) || 12 })
      setDirty(false)
      await mutate()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  const runNow = async () => {
    try {
      await api.runCoinScoreAll()
      await mutate()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <div className="space-y-4">
      <p className={ui.muted}>
        {tr(
          language,
          '对「带单监控 → 按币种」页上的每个币跑一次 AI 研判（方向 + 0–100 分 + 依据/风险）。只读，不影响选币和下单。',
          'Runs the AI reading for every coin on the copy-trade by-coin page: direction + 0–100 score + reasons/risks. Read-only; it does not pick or place orders.'
        )}
      </p>
      <p className={`${ui.muted} text-xs`}>
        {tr(
          language,
          '定时按「每个币距上次分析的间隔」轮询刷新（最旧的优先），不是整批跑完再等；若币多/间隔短排不下，请加大并发或间隔。',
          'Refreshes each coin when it is at least the interval old (oldest first), not one big batch then wait; if too many coins or the interval is too short, raise concurrency or the interval.'
        )}
      </p>

      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-medium text-nofx-text">
          {tr(language, '默认分析模型', 'Default analysis model')}
        </span>
        {modelInfo && modelInfo.models.length ? (
          <select
            value={modelID}
            onChange={(e) => void changeModel(e.target.value)}
            className={ui.inputInline}
            aria-label={tr(language, '默认分析模型', 'Default analysis model')}
          >
            {modelInfo.models.map((m) => (
              <option key={m.id} value={m.id}>
                {m.name} ({m.provider})
              </option>
            ))}
          </select>
        ) : (
          <span className="text-xs text-nofx-gold">
            {tr(language, '没有已启用的模型', 'No enabled model')}
          </span>
        )}
        <span className={`${ui.muted} text-xs`}>
          {tr(language, '用于单币研判和全部分析的默认模型', 'Used for single-coin and analyze-all')}
        </span>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-medium text-nofx-text">
          {tr(language, '定时全部分析', 'Scheduled analyze-all')}
        </span>
        <span
          className={`rounded px-1.5 text-[11px] ${enabled ? 'bg-nofx-success/10 text-nofx-success' : 'bg-nofx-bg-deeper text-nofx-text-muted'}`}
        >
          {enabled
            ? tr(language, '已开启', 'On')
            : tr(language, '已关闭', 'Off')}
        </span>
        <label className={`${ui.muted} flex items-center gap-1`}>
          {tr(language, '间隔（分钟）', 'Every (min)')}
          <input
            type="number"
            min={data.min_interval}
            max={1440}
            value={interval}
            onChange={(e) => {
              setIntervalMin(e.target.value)
              setDirty(true)
            }}
            className={`${ui.inputInline} w-20`}
            aria-label={tr(language, '定时间隔（分钟）', 'Interval minutes')}
          />
        </label>
        <label className={`${ui.muted} flex items-center gap-1`}>
          {tr(language, '每轮最多币', 'Max coins/run')}
          <input
            type="number"
            min={0}
            value={topN}
            onChange={(e) => {
              setTopN(e.target.value)
              setDirty(true)
            }}
            className={`${ui.inputInline} w-16`}
            title={tr(language, '0 表示全部；定时分析可用它控制花费', '0 = all; caps cost for scheduled runs')}
          />
        </label>
        <label className={`${ui.muted} flex items-center gap-1`}>
          {tr(language, '并发', 'Concurrency')}
          <input
            type="number"
            min={1}
            value={conc}
            onChange={(e) => {
              setConc(e.target.value)
              setDirty(true)
            }}
            className={`${ui.inputInline} w-16`}
            title={tr(language, '同时调用模型的币数量，越大越快但越容易被限流/花更多钱', 'How many coins are analyzed at once; higher is faster but may hit rate limits / cost more')}
          />
        </label>
        <label className={`${ui.muted} flex items-center gap-1`}>
          {tr(language, '成交额下限(USDT)', 'Min 24h vol (USDT)')}
          <input
            type="number"
            min={0}
            value={minVol}
            onChange={(e) => {
              setMinVol(e.target.value)
              setDirty(true)
            }}
            className={`${ui.inputInline} w-28`}
            title={tr(language, '24h 成交额低于此值的币不做 AI 分析（防插针），0=不过滤', 'Coins with 24h quote volume below this are skipped by AI scoring (avoid wicks); 0 = no filter')}
          />
        </label>
        <label className={`${ui.muted} flex items-center gap-1`}>
          {tr(language, '币池刷新(分钟)', 'Pool refresh (min)')}
          <input
            type="number"
            min={1}
            value={poolRefresh}
            onChange={(e) => {
              setPoolRefresh(e.target.value)
              setDirty(true)
            }}
            className={`${ui.inputInline} w-16`}
            title={tr(language, '每多少分钟自动重建一次币池，默认5', 'Rebuild the coin pool every N minutes, default 5')}
          />
        </label>
        <label className={`${ui.muted} flex items-center gap-1`}>
          {tr(language, '币池有效期(分钟)', 'Pool TTL (min)')}
          <input
            type="number"
            min={1}
            value={poolTTL}
            onChange={(e) => {
              setPoolTTL(e.target.value)
              setDirty(true)
            }}
            className={`${ui.inputInline} w-16`}
            title={tr(language, '币池里的币超过这个时间未刷新即失效(不交易)，默认12', 'Picks older than this are treated as expired (never traded), default 12')}
          />
        </label>
        <button
          type="button"
          className={ui.btnGhost}
          disabled={saving || !valid}
          onClick={() => void save(enabled)}
        >
          <Save className="mr-1 inline h-3 w-3" />
          {tr(language, '保存', 'Save')}
        </button>
        <button
          type="button"
          className={ui.btnGhost}
          disabled={saving || !valid}
          onClick={() => void save(!enabled)}
        >
          {enabled ? tr(language, '关闭定时', 'Turn off') : tr(language, '开启定时', 'Turn on')}
        </button>
      </div>
      {!valid ? (
        <p className="text-xs text-nofx-danger">
          {tr(
            language,
            `间隔要在 ${data.min_interval}–1440 分钟之间`,
            `Interval must be ${data.min_interval}–1440 minutes`
          )}
        </p>
      ) : null}

      <div className="flex flex-wrap items-center gap-2 border-t border-[rgba(25,36,52,0.08)] pt-3">
        <button
          type="button"
          className={ui.btnGhost}
          disabled={running}
          onClick={() => void runNow()}
        >
          {running ? (
            <Loader2 className="mr-1 inline h-3 w-3 animate-spin" />
          ) : (
            <Play className="mr-1 inline h-3 w-3" />
          )}
          {running
            ? tr(language, `刷新中 ${data.done}/${data.total}`, `Refreshing ${data.done}/${data.total}`)
            : tr(language, '立即全部分析一次', 'Analyze all now')}
        </button>
        <span className={`${ui.muted} tabular-nums`}>
          {tr(language, '模型', 'Model')}: {data.model || '—'} ·{' '}
          {tr(language, '币数', 'coins')}: {data.coin_count}
        </span>
      </div>

      {running ? (
        <div>
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-nofx-bg-deeper">
            <div
              className="h-full rounded-full bg-nofx-gold transition-all"
              style={{ width: `${progress}%` }}
            />
          </div>
          <p className={`${ui.muted} mt-1 text-xs tabular-nums`}>
            {data.done}/{data.total}
          </p>
        </div>
      ) : ran ? (
        <p className={`${ui.muted} text-xs`}>
          {tr(
            language,
            `上次扫描 ${ago(language, data.last_run)}，本轮派发 ${data.last_count} 个币（按各自到期时间轮询）`,
            `Last scan ${ago(language, data.last_run)}, dispatched ${data.last_count} coins (rolling per-coin)`
          )}
        </p>
      ) : (
        <p className={`${ui.muted} text-xs`}>
          {tr(language, '还没运行过', 'Not run yet')}
        </p>
      )}
      {data.last_error ? (
        <p role="alert" className="text-xs text-nofx-danger">
          <NoticeText text={data.last_error} />
        </p>
      ) : null}

      <div className="flex flex-wrap items-center gap-2 border-t border-[rgba(25,36,52,0.08)] pt-3">
        <label className="flex items-center gap-1.5 text-sm text-nofx-text">
          <input
            type="checkbox"
            checked={poolAuto}
            onChange={async (e) => {
              const next = e.target.checked
              setPoolAuto(next)
              try {
                await api.setCoinScoreSettings({
                  enabled,
                  interval_minutes: valid ? n : data.interval_minutes,
                  pool_auto: next,
                  top_n: Number(topN) || 0,
                  concurrency: Number(conc) || 1,
                  min_quote_volume: Number(minVol) || 0,
                  pool_refresh_minutes: Number(poolRefresh) || 5,
                  pool_ttl_minutes: Number(poolTTL) || 12,
                })
                await mutate()
              } catch (err) {
                setPoolAuto(!next)
                notify.error(err instanceof Error ? err.message : String(err))
              }
            }}
          />
          {tr(language, '分析后自动生成币池', 'Auto-build coin pool after analysis')}
        </label>
        <button
          type="button"
          className={ui.btnGhost}
          onClick={async () => {
            try {
              await api.generateCoinPool()
              await mutatePool()
            } catch (e) {
              notify.error(e instanceof Error ? e.message : String(e))
            }
          }}
        >
          {tr(language, '立即生成币池', 'Build pool now')}
        </button>
        <span className={`${ui.muted} text-xs tabular-nums`}>
          {tr(language, `当前币池 ${pool?.count ?? 0} 个`, `pool: ${pool?.count ?? 0} coins`)}
        </span>
      </div>

      <div className="border-t border-[rgba(25,36,52,0.08)] pt-3">
        <div className="mb-1 text-sm font-medium text-nofx-text">
          {tr(language, '前向验证（高分是否真的更准 / 空头是否赚钱）', 'Forward check (do high scores and shorts really work)')}
        </div>
        {outcomes && outcomes.rows.some((r) => r.samples > 0) ? (
          <div className="overflow-x-auto">
            <table className="w-full text-xs tabular-nums">
              <thead>
                <tr className={ui.muted}>
                  <th className="py-1 pr-2 text-left">{tr(language, '周期', 'H')}</th>
                  <th className="py-1 pr-2 text-left">{tr(language, '方向', 'Side')}</th>
                  <th className="py-1 pr-2 text-left">{tr(language, '分数档', 'Band')}</th>
                  <th className="py-1 pr-2 text-right">{tr(language, '样本', 'N')}</th>
                  <th className="py-1 pr-2 text-right">{tr(language, '胜率', 'Hit')}</th>
                  <th className="py-1 pr-2 text-right">{tr(language, '跑赢均值', 'Beat mkt')}</th>
                  <th className="py-1 text-right">{tr(language, '平均超额', 'Excess')}</th>
                </tr>
              </thead>
              <tbody>
                {outcomes.rows
                  .filter((r) => r.samples > 0)
                  .map((r, i) => (
                    <tr key={i} className="border-t border-[rgba(25,36,52,0.06)]">
                      <td className="py-1 pr-2">{r.horizon}</td>
                      <td className="py-1 pr-2">{r.side}</td>
                      <td className="py-1 pr-2">{r.band}</td>
                      <td className="py-1 pr-2 text-right">{r.samples}</td>
                      <td className="py-1 pr-2 text-right">{r.hit_rate.toFixed(1)}%</td>
                      <td className="py-1 pr-2 text-right">{r.beat_market.toFixed(1)}%</td>
                      <td className="py-1 text-right">{r.avg_excess >= 0 ? '+' : ''}{r.avg_excess.toFixed(2)}%</td>
                    </tr>
                  ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className={`${ui.muted} text-xs`}>
            {tr(language, '还没有到期样本（分析后 4/24/72 小时才会回填）', 'No matured samples yet (filled at 4/24/72h after analysis)')}
          </p>
        )}
      </div>

      <p className="text-xs text-nofx-gold">
        {tr(
          language,
          '费用提示：全部分析会给每个币各调用一次模型，币越多、间隔越短，token 花费越高。间隔建议 ≥30 分钟。',
          'Cost note: analyze-all calls the model once per coin. More coins and a shorter interval mean more tokens. An interval of 30+ minutes is recommended.'
        )}
      </p>
    </div>
  )
}
