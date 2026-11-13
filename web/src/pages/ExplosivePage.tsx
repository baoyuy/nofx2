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

function StrengthBar({ strength }: { strength: number }) {
  const color = strength >= 55 ? BN.up : strength >= 35 ? '#b45309' : '#94a3b8'
  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 w-14 overflow-hidden rounded bg-[rgba(25,36,52,0.08)]">
        <div className="h-full" style={{ width: `${Math.min(100, strength)}%`, background: color }} />
      </div>
      <span className="w-7 text-right text-xs font-semibold tabular-nums" style={{ color }}>
        {strength.toFixed(0)}
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
        <p className={`${ui.muted} leading-relaxed`}>
          {tr(
            language,
            '按"速度 / 量能 / 持仓量 / 结构 / 相对强度"算爆发力，榜单按爆发力排序——正在爆的币不会被藏起来。资金费率过热、追高、OI 流出只作为风险标注，并给出扣风险后的"健康分"。价格和 24h 涨幅来自实时行情，每秒更新；K 线、持仓量和评分每分钟在后台重算，页面不会等待。',
            'Strength combines speed, participation, open interest, structure and relative strength, and the board is ordered by it: a coin in the middle of a move is never buried. Hot funding, chasing and open-interest drain are shown as risk and rolled into a separate health score. Prices and 24h change stream in every second; candles, open interest and scores recompute in the background once a minute, so the page never waits.'
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
      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_420px]">
        <section className={`${ui.panel} min-w-0 overflow-x-auto`}>
          {rows.length ? (
            <table className="w-full min-w-[860px] text-sm">
              <thead className="text-left text-xs text-nofx-text-muted">
                <tr>
                  <th className="py-1.5 font-normal">#</th>
                  <th className="font-normal">{tr(language, '币种', 'Coin')}</th>
                  <th className="font-normal">{tr(language, '爆发力', 'Strength')}</th>
                  <th className="text-right font-normal">{tr(language, '健康分', 'Health')}</th>
                  <th className="font-normal">{tr(language, '状态', 'State')}</th>
                  <th className="text-right font-normal">24h</th>
                  <th className="text-right font-normal">5m</th>
                  <th className="text-right font-normal">{tr(language, '量比', 'Vol×')}</th>
                  <th className="text-right font-normal">OI 1h</th>
                  <th className="text-right font-normal">{tr(language, '费率', 'Funding')}</th>
                  <th className="text-right font-normal">{tr(language, '成交额', 'Turnover')}</th>
                  <th className="text-right font-normal">
                    {tr(language, '距高点', 'Off high')}
                  </th>
                  <th className="font-normal">{tr(language, '触发', 'Signals')}</th>
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
                    <td className="py-1.5 text-nofx-text-muted tabular-nums">{i + 1}</td>
                    <td className="font-medium">{r.symbol.replace('USDT', '')}</td>
                    <td>
                      <StrengthBar strength={r.strength} />
                    </td>
                    <td
                      className="text-right tabular-nums"
                      title={tr(
                        language,
                        `风险系数 ${r.risk.toFixed(2)}：费率、追高、OI 流出会打折`,
                        `Risk factor ${r.risk.toFixed(2)}: funding, chasing and open-interest drain discount it`
                      )}
                      style={{ color: r.risk < 0.8 ? BN.down : undefined }}
                    >
                      {r.score.toFixed(0)}
                    </td>
                    <td>
                      <span
                        className="rounded px-1.5 py-0.5 text-xs font-semibold"
                        style={{ color: stateColor[r.state], background: 'rgba(25,36,52,0.06)' }}
                      >
                        {stateLabel(language, r.state)}
                      </span>
                    </td>
                    <td className="text-right tabular-nums" style={{ color: r.change_24h >= 0 ? BN.up : BN.down }}>
                      {signed(r.change_24h)}
                    </td>
                    <td className="text-right tabular-nums" style={{ color: r.change_5m >= 0 ? BN.up : BN.down }}>
                      {signed(r.change_5m)}
                    </td>
                    <td className="text-right tabular-nums">{r.vol_ratio.toFixed(1)}</td>
                    <td className="text-right tabular-nums" style={{ color: r.oi_change_1h >= 0 ? BN.up : BN.down }}>
                      {signed(r.oi_change_1h * 100)}
                    </td>
                    <td className="text-right tabular-nums">{(r.funding * 100).toFixed(3)}%</td>
                    <td className="text-right tabular-nums">{compact(r.volume_24h)}</td>
                    <td className="text-right tabular-nums">{r.distance_high.toFixed(1)}%</td>
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

        <aside className="space-y-3">
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
              <h3 className={`${ui.muted} mb-2`}>
                {tr(language, '爆发力构成', 'Strength parts')}
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
