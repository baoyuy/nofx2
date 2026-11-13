import { useState } from 'react'
import useSWR from 'swr'
import { explosiveApi, type BandStat } from '../../lib/api/explosive'
import { tr, ui } from '../market/marketUi'
import { tagLabel } from './labels'

const pct = (v: number) => `${(v * 100).toFixed(0)}%`

const signedPct = (v: number) => `${v >= 0 ? '+' : ''}${(v * 100).toFixed(2)}%`

const MIN_SAMPLES = 30

function BandTable({
  language,
  rows,
  first,
}: {
  language: string
  rows: BandStat[]
  first?: string
}) {
  return (
    <table className="w-full text-xs">
      <thead className="text-left text-nofx-text-muted">
        <tr>
          <th className="py-1 font-normal">{first || tr(language, '档位', 'Band')}</th>
          <th className="text-right font-normal">{tr(language, '样本', 'Samples')}</th>
          <th className="text-right font-normal">1h</th>
          <th className="text-right font-normal">4h</th>
          <th className="text-right font-normal">24h</th>
          <th className="text-right font-normal">
            {tr(language, '4h 跑赢 BTC', '4h vs BTC')}
          </th>
          <th className="text-right font-normal">
            {tr(language, '4h 超额', '4h excess')}
          </th>
          <th className="text-right font-normal">
            {tr(language, '回撤', 'Drawdown')}
          </th>
          <th className="text-right font-normal">
            {tr(language, '持续率', 'Sustained')}
          </th>
        </tr>
      </thead>
      <tbody className="text-nofx-text">
        {rows.map((r) => {
          const thin = r.samples < MIN_SAMPLES
          return (
            <tr key={r.band} className="border-t border-[rgba(25,36,52,0.14)]">
              <td className="py-1 font-medium">{r.band}</td>
              <td className="text-right tabular-nums">{r.samples}</td>
              <td className="text-right tabular-nums">{thin ? '—' : pct(r.up_1h)}</td>
              <td className="text-right tabular-nums">{thin ? '—' : pct(r.up_4h)}</td>
              <td className="text-right tabular-nums">{thin ? '—' : pct(r.up_24h)}</td>
              <td className="text-right tabular-nums">{thin ? '—' : pct(r.beat_4h)}</td>
              <td className="text-right tabular-nums">{thin ? '—' : signedPct(r.avg_excess_4h)}</td>
              <td className="text-right tabular-nums">{thin ? '—' : signedPct(r.avg_dd_4h)}</td>
              <td className="text-right font-semibold tabular-nums">
                {thin ? '—' : pct(r.sustained)}
              </td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}

export function AccuracyPanel({ language }: { language: string }) {
  const [days, setDays] = useState(30)
  const { data, error } = useSWR(
    ['explosive/accuracy', days],
    () => explosiveApi.accuracy(days),
    { refreshInterval: 60000 }
  )

  return (
    <section className={`${ui.panel} space-y-3`}>
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="text-base font-semibold text-nofx-text">
          {tr(language, '准确率', 'Accuracy')}
        </h2>
        <select
          className={ui.input}
          value={days}
          onChange={(e) => setDays(Number(e.target.value))}
          aria-label={tr(language, '统计周期', 'Period')}
        >
          {[7, 30, 90].map((d) => (
            <option key={d} value={d}>
              {tr(language, `近 ${d} 天`, `${d} days`)}
            </option>
          ))}
        </select>
        {data ? (
          <span className={ui.muted}>
            {tr(
              language,
              `样本 ${data.total} 条，每 15 分钟记录一次`,
              `${data.total} samples, recorded every 15 minutes`
            )}
          </span>
        ) : null}
      </div>

      <p className={`${ui.muted} leading-relaxed`}>
        {tr(
          language,
          '命中定义：4h 后价格上涨，并且期间最大回撤不超过 3%。「4h 跑赢 BTC」是同期相对 BTC 的超额。样本少于 30 条的档位不做判断，显示为 —。',
          'A hit means the price was higher after 4h without a drawdown deeper than 3%. "4h vs BTC" is the excess over BTC over the same hours. Bands with fewer than 30 samples are shown as — rather than judged.'
        )}
      </p>

      {error ? (
        <p className="text-sm text-nofx-danger">
          {tr(language, '加载失败：', 'Could not load: ')}
          {error instanceof Error ? error.message : String(error)}
        </p>
      ) : null}

      {data && data.total > 0 ? (
        <>
          <div>
            <h3 className={`${ui.muted} mb-1`}>
              {tr(language, '按爆发力分档', 'By strength band')}
            </h3>
            <BandTable language={language} rows={data.bands} />
          </div>
          <div>
            <h3 className={`${ui.muted} mb-1`}>
              {tr(language, '前 5 名 vs 其余', 'Top 5 vs the rest')}
            </h3>
            <BandTable language={language} rows={data.top} first={tr(language, '分组', 'Group')} />
          </div>
          {data.tags.length ? (
            <div>
              <h3 className={`${ui.muted} mb-1`}>
                {tr(language, '按触发标签', 'By signal tag')}
              </h3>
              <table className="w-full text-xs">
                <thead className="text-left text-nofx-text-muted">
                  <tr>
                    <th className="py-1 font-normal">{tr(language, '标签', 'Tag')}</th>
                    <th className="text-right font-normal">{tr(language, '样本', 'Samples')}</th>
                    <th className="text-right font-normal">{tr(language, '4h 上涨', '4h up')}</th>
                    <th className="text-right font-normal">{tr(language, '持续率', 'Sustained')}</th>
                  </tr>
                </thead>
                <tbody className="text-nofx-text">
                  {data.tags.map((t) => (
                    <tr key={t.tag} className="border-t border-[rgba(25,36,52,0.14)]">
                      <td className="py-1">{tagLabel(language, t.tag)}</td>
                      <td className="text-right tabular-nums">{t.samples}</td>
                      <td className="text-right tabular-nums">
                        {t.samples < MIN_SAMPLES ? '—' : pct(t.up_4h)}
                      </td>
                      <td className="text-right tabular-nums">
                        {t.samples < MIN_SAMPLES ? '—' : pct(t.sustained)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : null}
        </>
      ) : (
        <p className={`${ui.muted} py-6 text-center`}>
          {tr(
            language,
            '还没有可评估的样本。榜单每 15 分钟记录一次，1 小时后开始产生 1h 结果，4 小时后才有 4h 结果。',
            'No evaluable samples yet. The board is recorded every 15 minutes; 1h results appear after an hour, 4h results after four.'
          )}
        </p>
      )}
    </section>
  )
}
