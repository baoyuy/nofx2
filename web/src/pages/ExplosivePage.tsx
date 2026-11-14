import { useEffect, useMemo, useState } from 'react'
import useSWR from 'swr'
import { Loader2, RefreshCw } from 'lucide-react'
import { api } from '../lib/api'
import { explosiveApi } from '../lib/api/explosive'
import type { MarketKline } from '../lib/api/market'
import {
  BinanceChart,
  BN,
  type MainIndicator,
  type SubIndicator,
} from '../components/market/BinanceChart'
import { tr, ui } from '../components/market/marketUi'
import { useLanguage } from '../contexts/LanguageContext'
import { priceDecimals } from '../utils/marketIndicators'
import { AccuracyPanel } from '../components/explosive/AccuracyPanel'
import { stateColor, stateLabel, tagLabel } from '../components/explosive/labels'

const signed = (v: number) => `${v >= 0 ? '+' : ''}${v.toFixed(2)}%`

function compact(v: number): string {
  if (v >= 1e9) return `${(v / 1e9).toFixed(1)}B`
  if (v >= 1e6) return `${(v / 1e6).toFixed(0)}M`
  if (v >= 1e3) return `${(v / 1e3).toFixed(0)}K`
  return v.toFixed(0)
}

function PotentialBar({ value }: { value: number }) {
  // 40% or more remaining is a strong prediction; scale the bar to that.
  const width = Math.min(100, (value / 40) * 100)
  const color = value >= 20 ? BN.up : value >= 8 ? '#b45309' : '#94a3b8'
  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 w-14 overflow-hidden rounded bg-[rgba(25,36,52,0.08)]">
        <div className="h-full" style={{ width: `${width}%`, background: color }} />
      </div>
      <span className="w-14 text-right text-xs font-semibold tabular-nums" style={{ color }}>
        +{value.toFixed(1)}%
      </span>
    </div>
  )
}

function Part({ label, value }: { label: string; value: number }) {
  return (
    <div>
      <div className={ui.muted}>{label}</div>
      <div className="flex items-center gap-1">
        <div className="h-1 w-12 overflow-hidden rounded bg-[rgba(25,36,52,0.08)]">
          <div className="h-full" style={{ width: `${Math.round(value * 100)}%`, background: BN.up }} />
        </div>
        <span className="tabular-nums text-nofx-text">{Math.round(value * 100)}</span>
      </div>
    </div>
  )
}

export function ExplosivePage() {
  const { language } = useLanguage()
  const [view, setView] = useState<'board' | 'accuracy'>('board')
  const [selected, setSelected] = useState('')
  const [chartInterval, setChartInterval] = useState('15m')

  const { data, error, mutate, isValidating } = useSWR(
    'market/explosive',
    () => explosiveApi.board(),
    { refreshInterval: 1000, revalidateOnFocus: false, keepPreviousData: true }
  )
  const rows = data?.rows || []
  const blasted = data?.blasted || []
  const radar = data?.radar || []
  const current = selected || rows[0]?.symbol || ''
  const row = useMemo(
    () => rows.find((r) => r.symbol === current) || null,
    [rows, current]
  )

  const { data: candles = [] } = useSWR(
    current ? ['explosive/klines', current, chartInterval] : null,
    () => api.getMarketKlines(current, chartInterval, 300) as Promise<MarketKline[]>,
    { revalidateOnFocus: false }
  )

  useEffect(() => {
    if (selected && !rows.some((r) => r.symbol === selected)) setSelected('')
  }, [rows, selected])

  const main: MainIndicator = 'EMA'
  const subs: SubIndicator[] = ['VOL']
  const last = candles[candles.length - 1]?.close || row?.price || 0
  const decimals = priceDecimals(last)

  const refresh = () => void mutate(() => explosiveApi.board(true), { revalidate: false })

  return (
    <div className="space-y-4">
      <section className={ui.panel}>
        <div className="mb-1 flex flex-wrap items-center gap-3">
          <h1 className="text-lg font-semibold text-nofx-text">
            {tr(language, '爆发榜', 'Explosive board')}
          </h1>
          {data ? (
            <span className={ui.muted}>
              {tr(
                language,
                `扫描 ${data.scanned} 个合约，${data.candidates} 个通过初筛`,
                `${data.scanned} contracts scanned, ${data.candidates} passed the gates`
              )}
              {typeof data.age_seconds === 'number'
                ? tr(
                    language,
                    ` · K 线与持仓量 ${data.age_seconds} 秒前`,
                    ` · candles and open interest ${data.age_seconds}s old`
                  )
                : ''}
              {data.refreshing
                ? tr(language, ' · 后台刷新中', ' · refreshing')
                : ''}
            </span>
          ) : null}
          <div className="ml-auto flex gap-1">
            {(['board', 'accuracy'] as const).map((v) => (
              <button
                key={v}
                type="button"
                aria-pressed={view === v}
                className={`${ui.btnGhost} ${view === v ? 'border-nofx-gold text-nofx-gold' : ''}`}
                onClick={() => setView(v)}
              >
                {v === 'board'
                  ? tr(language, '榜单', 'Board')
                  : tr(language, '准确率', 'Accuracy')}
              </button>
            ))}
          </div>
          <button
            type="button"
            className={`flex items-center gap-1 ${ui.btnGhost}`}
            onClick={refresh}
            disabled={isValidating}
          >
            {isValidating ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <RefreshCw className="h-3.5 w-3.5" />
            )}
            {tr(language, '刷新', 'Refresh')}
          </button>
        </div>
        <p className={`text-xs ${ui.muted}`}>
          {tr(
            language,
            '按"预期最终涨幅"从高到低排：现在只有 +10%、但预计能走到 +30% 的币，会排在已经 +20%、预计只到 +22% 的币前面。今日涨幅按北京时间自然日，滚动 24h 与币安一致。',
            'Ranked by expected final gain: a coin at +10% heading for +30% outranks one at +20% heading for +22%. Day gain uses the Beijing calendar day; the 24h column matches Binance.'
          )}
        </p>
      </section>

      {error ? (
        <p className={`${ui.panel} text-sm text-nofx-danger`}>
          {tr(language, '行情拉取失败：', 'Could not load the board: ')}
          {error instanceof Error ? error.message : String(error)}
        </p>
      ) : null}

      {view === 'accuracy' ? (
        <AccuracyPanel language={language} />
      ) : (
      <div className="grid items-start gap-4 lg:grid-cols-[minmax(0,1fr)_380px]">
        <div className="min-w-0 space-y-4">
        <section className={`${ui.panel} min-w-0 overflow-x-auto`}>
          <div className={`mb-2 text-xs ${ui.muted}`}>
            {tr(
              language,
              '预期最终 = 今日涨幅 + 模型预测的剩余空间，按它排序。模型能力有限，仅作参考，不代表一定会涨。',
              'Expected = day gain + modelled room left; the list is sorted by it. The model is weak, so treat this as reference only.'
            )}
          </div>
          {rows.length ? (
            <table className="w-full min-w-[900px] whitespace-nowrap text-sm">
              <thead className="text-left text-xs text-nofx-text-muted">
                <tr>
                  <th className="px-2 py-1.5 font-normal">#</th>
                  <th className="px-2 font-normal">{tr(language, '币种', 'Coin')}</th>
                  <th className="px-2 pr-8 text-right font-normal">{tr(language, '预期最终', 'Expected')}</th>
                  <th className="px-2 pl-5 border-l border-[rgba(25,36,52,0.10)] font-normal">{tr(language, '状态', 'State')}</th>
                  <th className="px-2 text-right font-normal">{tr(language, '今日涨幅', 'Day')}</th>
                  <th className="px-2 text-right font-normal">{tr(language, '滚动24h', '24h')}</th>
                  <th className="px-2 text-right font-normal">{tr(language, '量能斜率', 'Vol slope')}</th>
                  <th className="px-2 text-right font-normal">OI 1h</th>
                  <th className="px-2 pl-6 border-l border-[rgba(25,36,52,0.10)] font-normal">{tr(language, '触发', 'Signals')}</th>
                </tr>
              </thead>
              <tbody className="text-nofx-text">
                {rows.map((r, i) => (
                  <tr
                    key={r.symbol}
                    onClick={() => setSelected(r.symbol)}
                    className={`cursor-pointer border-t ${ui.border} ${
                      r.symbol === current ? 'bg-nofx-gold/5' : 'hover:bg-nofx-bg-deeper'
                    }`}
                  >
                    <td className="px-2 py-1.5 text-nofx-text-muted tabular-nums">{i + 1}</td>
                    <td className="px-2 font-medium">{r.symbol.replace('USDT', '')}</td>
                    <td className="px-2 pr-8 text-right align-middle">
                      <div className="font-semibold tabular-nums" style={{ color: BN.up }}>
                        +{(r.predicted_final ?? 0).toFixed(1)}%
                      </div>
                      <div className="text-[10px] text-nofx-text-muted">
                        {tr(language, '空间', 'room')} +{(r.predicted_remaining ?? 0).toFixed(1)}%
                      </div>
                    </td>
                    <td className="px-2 pl-5 border-l border-[rgba(25,36,52,0.10)]">
                      <span
                        className="rounded px-1.5 py-0.5 text-xs font-semibold"
                        style={{ color: stateColor[r.state], background: 'rgba(25,36,52,0.06)' }}
                      >
                        {stateLabel(language, r.state)}
                      </span>
                    </td>
                    <td className="px-2 text-right tabular-nums" style={{ color: r.day_gain >= 0 ? BN.up : BN.down }}>
                      {signed(r.day_gain * 100)}
                    </td>
                    <td className="px-2 text-right tabular-nums" style={{ color: r.change_24h >= 0 ? BN.up : BN.down }}>
                      {signed(r.change_24h)}
                    </td>
                    <td className="px-2 text-right tabular-nums" style={{ color: r.volume_slope >= 0 ? BN.up : BN.down }}>
                      {signed(r.volume_slope * 100)}
                    </td>
                    <td
                      className="text-right tabular-nums"
                      title={
                        r.oi_known
                          ? undefined
                          : tr(language, '持仓量数据获取失败，按中性处理', 'Open interest unavailable, scored as neutral')
                      }
                      style={{ color: r.oi_known ? (r.oi_change_1h >= 0 ? BN.up : BN.down) : undefined }}
                    >
                      {r.oi_known ? signed(r.oi_change_1h * 100) : '—'}
                    </td>
                    <td>
                      <span className="flex flex-wrap gap-1">
                        {(r.tags ?? []).slice(0, 3).map((t) => (
                          <span key={t} className="rounded bg-nofx-bg-deeper px-1 text-[10px] text-nofx-text-muted">
                            {tagLabel(language, t)}
                          </span>
                        ))}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <p className={`py-8 text-center ${ui.muted}`}>
              {data?.reason
                ? tr(language, `暂时没有符合条件的币：${data.reason}`, `Nothing qualifies right now: ${data.reason}`)
                : tr(language, '正在扫描…', 'Scanning…')}
            </p>
          )}
        </section>

        <section className={`${ui.panel} min-w-0 overflow-x-auto`}>
          <div className="mb-2 flex flex-wrap items-baseline gap-2">
            <h2 className="text-base font-semibold text-nofx-text">
              {tr(language, '妖币雷达', 'Monster radar')}
            </h2>
            <span className={`text-xs ${ui.muted}`}>
              {tr(
                language,
                '成交量突增 + 高波动 + 持仓量增加，仍在早期。实测约 8% 会在当日涨到 50%+，多数日子可能为空，仅作观察。',
                'Volume spike + volatility + rising OI, still early. About 8% reach +50% the same day; often empty. Watch only.'
              )}
            </span>
          </div>
          {radar.length ? (
            <table className="w-full max-w-[820px] text-sm">
              <thead className="text-left text-xs text-nofx-text-muted">
                <tr>
                  <th className="px-2 py-1.5 font-normal">{tr(language, '币种', 'Coin')}</th>
                  <th className="px-2 text-right font-normal">{tr(language, '量能突增', 'Vol spike')}</th>
                  <th className="px-2 text-right font-normal">ATR</th>
                  <th className="px-2 text-right font-normal">{tr(language, '今日涨幅', 'Day')}</th>
                  <th className="px-2 text-right font-normal">OI 1h</th>
                  <th className="px-2 pr-8 text-right font-normal">{tr(language, '预期最终', 'Expected')}</th>
                </tr>
              </thead>
              <tbody className="text-nofx-text">
                {radar.map((r) => (
                  <tr
                    key={r.symbol}
                    onClick={() => setSelected(r.symbol)}
                    className={`cursor-pointer border-t ${ui.border} hover:bg-nofx-bg-deeper`}
                  >
                    <td className="px-2 py-1.5 font-medium">{r.symbol.replace('USDT', '')}</td>
                    <td className="px-2 text-right font-semibold tabular-nums" style={{ color: (r.vol_spike_1h ?? 0) >= 3 ? BN.up : undefined }}>
                      {(r.vol_spike_1h ?? 0).toFixed(1)}x
                    </td>
                    <td className="px-2 text-right tabular-nums">{(r.atr_pct ?? 0).toFixed(1)}%</td>
                    <td className="px-2 text-right tabular-nums" style={{ color: r.day_gain >= 0 ? BN.up : BN.down }}>
                      {signed(r.day_gain * 100)}
                    </td>
                    <td className="px-2 text-right tabular-nums" style={{ color: r.oi_known ? (r.oi_change_1h >= 0 ? BN.up : BN.down) : undefined }}>
                      {r.oi_known ? signed(r.oi_change_1h * 100) : '—'}
                    </td>
                    <td className="px-2 text-right font-semibold tabular-nums" style={{ color: BN.up }}>
                      +{(r.predicted_final ?? 0).toFixed(1)}%
                    </td>
                    <td className="px-2 text-right tabular-nums">{compact(r.volume_24h)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <p className={`py-4 text-center text-xs ${ui.muted}`}>
              {tr(language, '当前没有点火信号（多数时候如此，属正常）。', 'No ignition signal right now (normal most of the time).')}
            </p>
          )}
        </section>

        {blasted.length ? (
          <section className={`${ui.panel} min-w-0`}>
            <div className="mb-2 flex items-baseline gap-2">
              <h2 className="text-base font-semibold text-nofx-text">
                {tr(language, '已爆发', 'Already blasted')}
              </h2>
              <span className={`text-xs ${ui.muted}`}>
                {tr(language, '今日已涨 50% 以上，不宜再追，仅供参考', 'Up 50%+ today: not an entry, reference only')}
              </span>
            </div>
            <table className="whitespace-nowrap text-sm">
              <thead className="text-left text-xs text-nofx-text-muted">
                <tr>
                  <th className="px-2 py-1.5 font-normal">{tr(language, '币种', 'Coin')}</th>
                  <th className="px-2 text-right font-normal">{tr(language, '今日涨幅', 'Day')}</th>
                  <th className="px-2 text-right font-normal">{tr(language, '滚动24h', '24h')}</th>
                  <th className="px-2 text-right font-normal">OI 1h</th>
                  <th className="px-2 text-right font-normal">{tr(language, '状态', 'State')}</th>
                </tr>
              </thead>
              <tbody className="text-nofx-text">
                {blasted.map((r) => (
                  <tr
                    key={r.symbol}
                    onClick={() => setSelected(r.symbol)}
                    className={`cursor-pointer border-t ${ui.border} hover:bg-nofx-bg-deeper`}
                  >
                    <td className="px-2 py-1.5 font-medium">{r.symbol.replace('USDT', '')}</td>
                    <td className="px-2 text-right tabular-nums" style={{ color: r.day_gain >= 0 ? BN.up : BN.down }}>
                      {signed(r.day_gain * 100)}
                    </td>
                    <td className="px-2 text-right tabular-nums" style={{ color: r.change_24h >= 0 ? BN.up : BN.down }}>
                      {signed(r.change_24h)}
                    </td>
                    <td className="px-2 text-right tabular-nums" style={{ color: r.oi_known ? (r.oi_change_1h >= 0 ? BN.up : BN.down) : undefined }}>
                      {r.oi_known ? signed(r.oi_change_1h * 100) : '—'}
                    </td>
                    <td className="px-2 text-right">
                      <span
                        className="rounded px-1.5 py-0.5 text-xs font-semibold"
                        style={{ color: stateColor[r.state], background: 'rgba(25,36,52,0.06)' }}
                      >
                        {stateLabel(language, r.state)}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </section>
        ) : null}

        </div>

        <aside className="space-y-3 lg:sticky lg:top-4">
          <section className={ui.panel}>
            <div className="mb-2 flex items-baseline gap-2">
              <h2 className="text-base font-semibold text-nofx-text">
                {current ? current.replace('USDT', '') : '--'}
              </h2>
              {row ? (
                <span className="tabular-nums text-nofx-text">{row.price}</span>
              ) : null}
              <div className="ml-auto flex gap-1">
                {['5m', '15m', '1h', '4h'].map((iv) => (
                  <button
                    key={iv}
                    type="button"
                    className={`${ui.btnGhost} ${iv === chartInterval ? 'border-nofx-gold text-nofx-gold' : ''}`}
                    onClick={() => setChartInterval(iv)}
                  >
                    {iv}
                  </button>
                ))}
              </div>
            </div>
            {candles.length ? (
              <BinanceChart
                candles={candles}
                decimals={decimals}
                main={main}
                subs={subs}
                levels={{}}
                resetKey={`${current}:${chartInterval}`}
                height={320}
              />
            ) : (
              <p className={`py-10 text-center ${ui.muted}`}>
                {tr(language, '加载 K 线…', 'Loading candles…')}
              </p>
            )}
          </section>

          {row ? (
            <section className={ui.panel}>
              <div className="mb-2 grid grid-cols-2 gap-x-3 gap-y-1 text-xs">
                <div className="flex justify-between">
                  <span className={ui.muted}>{tr(language, '预期最终', 'Expected')}</span>
                  <span className="font-semibold tabular-nums" style={{ color: BN.up }}>
                    +{(row.predicted_final ?? 0).toFixed(1)}%
                  </span>
                </div>
                <div className="flex justify-between">
                  <span className={ui.muted}>{tr(language, '潜在空间', 'Room left')}</span>
                  <span className="tabular-nums" style={{ color: BN.up }}>
                    +{(row.predicted_remaining ?? 0).toFixed(1)}%
                  </span>
                </div>
                <div className="col-span-2">
                  <PotentialBar value={row.predicted_remaining ?? 0} />
                </div>
                <div className="flex justify-between">
                  <span className={ui.muted}>{tr(language, '今日涨幅', 'Day gain')}</span>
                  <span className="tabular-nums">{signed(row.day_gain * 100)}</span>
                </div>
                <div className="flex justify-between">
                  <span className={ui.muted}>{tr(language, '已运行', 'Running')}</span>
                  <span className="tabular-nums">{row.elapsed_hours.toFixed(1)}h</span>
                </div>
                <div className="flex justify-between">
                  <span className={ui.muted}>{tr(language, '量能斜率', 'Vol slope')}</span>
                  <span className="tabular-nums">{signed(row.volume_slope * 100)}</span>
                </div>
              </div>
              <h3 className={`${ui.muted} mb-2`}>
                {tr(language, '动量构成', 'Momentum parts')}
              </h3>
              <div className="grid grid-cols-5 gap-2 text-xs">
                <Part label={tr(language, '速度', 'Speed')} value={row.speed} />
                <Part label={tr(language, '量能', 'Volume')} value={row.volume} />
                <Part label={tr(language, '持仓量', 'OI')} value={row.open_interest} />
                <Part label={tr(language, '结构', 'Structure')} value={row.structure} />
                <Part label={tr(language, '相对强度', 'Rel. str.')} value={row.rel_strength} />
              </div>
              <dl className="mt-3 grid grid-cols-2 gap-x-3 gap-y-1 text-xs">
                <div className="flex justify-between">
                  <dt className={ui.muted}>{tr(language, 'ATR(5m)', 'ATR(5m)')}</dt>
                  <dd className="tabular-nums">{row.atr_pct.toFixed(2)}%</dd>
                </div>
                <div className="flex justify-between">
                  <dt className={ui.muted}>{tr(language, '距 24h 高点', 'Off 24h high')}</dt>
                  <dd className="tabular-nums">{row.distance_high.toFixed(1)}%</dd>
                </div>
                <div className="flex justify-between">
                  <dt className={ui.muted}>EMA20</dt>
                  <dd className="tabular-nums">{row.ema20.toFixed(2)}</dd>
                </div>
                <div className="flex justify-between">
                  <dt className={ui.muted}>EMA60</dt>
                  <dd className="tabular-nums">{row.ema60.toFixed(2)}</dd>
                </div>
              </dl>
              <p className={`${ui.muted} mt-2`}>
                {tr(
                  language,
                  '分数只说明"现在像不像一个刚开始的爆发"，不构成入场建议；过热和追高的币会被压分。',
                  'The score says how much this looks like a fresh burst, not that it is a buy. Overheated and chased coins are discounted.'
                )}
              </p>
            </section>
          ) : null}
        </aside>
      </div>
      )}
    </div>
  )
}
