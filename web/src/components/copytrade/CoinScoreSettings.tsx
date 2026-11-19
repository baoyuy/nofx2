import { useEffect, useState } from 'react'
import useSWR from 'swr'
import { Play, Save, Loader2 } from 'lucide-react'
import { api } from '../../lib/api'
import type {
  CoinPoolInfo,
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
  const [enabled, setEnabled] = useState(false)
  const [poolAuto, setPoolAuto] = useState(false)
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
      await api.setCoinScoreSettings({ enabled: nextEnabled, interval_minutes: n, pool_auto: poolAuto })
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
            ? tr(language, `分析中 ${data.done}/${data.total}`, `Analyzing ${data.done}/${data.total}`)
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
            `上次 ${ago(language, data.last_run)} 完成，成功 ${data.last_count} 个币`,
            `Last run ${ago(language, data.last_run)}, ${data.last_count} coins succeeded`
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
            onChange={(e) => {
              setPoolAuto(e.target.checked)
              setDirty(true)
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
