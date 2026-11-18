import { useState } from 'react'
import useSWR from 'swr'
import { Play, RefreshCw } from 'lucide-react'
import { api } from '../../lib/api'
import type { PaperInfo, PaperRow } from '../../lib/api/copyTrade'
import { confirmToast, notify } from '../../lib/notify'
import { tr, ui } from '../market/marketUi'
import { NoticeText } from '../NoticeText'

// PaperTab shows what copying the board would have returned, without trading.

const TABLE_LABEL: Record<string, { zh: string; en: string }> = {
  all: { zh: '全部', en: 'All' },
  style: { zh: '按风格', en: 'By style' },
  side: { zh: '按多空', en: 'By side' },
  agree: { zh: '按同向人数', en: 'By agreement' },
  delay: { zh: '按延迟', en: 'By delay' },
  price: { zh: '按成交价基准', en: 'By price basis' },
}

const STYLE_LABEL: Record<string, { zh: string; en: string }> = {
  scalp: { zh: '短线', en: 'Scalp' },
  swing: { zh: '波段', en: 'Swing' },
  long: { zh: '长线', en: 'Long' },
  martingale: { zh: '摊平型', en: 'Martingale' },
  high_freq: { zh: '高频', en: 'High freq' },
  few_samples: { zh: '样本不足', en: 'Few samples' },
  unknown: { zh: '未知', en: 'Unknown' },
}

const pct = (v: number) => `${(v * 100).toFixed(1)}%`
const signed = (v: number) => `${v >= 0 ? '+' : ''}${(v * 100).toFixed(2)}%`
const num = (v: number) => v.toFixed(2)

function label(language: string, table: string, raw: string): string {
  if (table === 'style' && STYLE_LABEL[raw]) {
    return tr(language, STYLE_LABEL[raw].zh, STYLE_LABEL[raw].en)
  }
  if (table === 'side') {
    return raw === 'long' ? tr(language, '做多', 'Long') : tr(language, '做空', 'Short')
  }
  return raw
}

function Rows({
  language,
  table,
  rows,
}: {
  language: string
  table: string
  rows: PaperRow[]
}) {
  const t = TABLE_LABEL[table] || { zh: table, en: table }
  return (
    <div className="mb-4">
      <div className="mb-1 text-sm font-semibold text-nofx-text">
        {tr(language, t.zh, t.en)}
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-xs tabular-nums">
          <thead>
            <tr className={ui.muted}>
              <th className="py-1 pr-2 text-left font-medium">#</th>
              <th className="py-1 pr-2 text-right font-medium">
                {tr(language, '仓位', 'Trips')}
              </th>
              <th className="py-1 pr-2 text-right font-medium">
                {tr(language, '人数', 'Traders')}
              </th>
              <th className="py-1 pr-2 text-right font-medium">
                {tr(language, '盈利比例', 'Win')}
              </th>
              <th className="py-1 pr-2 text-right font-medium">
                {tr(language, '每仓均值', 'Avg')}
              </th>
              <th className="py-1 pr-2 text-right font-medium">
                {tr(language, '中位数', 'Med')}
              </th>
              <th className="py-1 pr-2 text-right font-medium">
                {tr(language, '合计(份)', 'Total')}
              </th>
              <th className="py-1 pr-2 text-right font-medium">
                {tr(language, '跑赢BTC', 'Beat BTC')}
              </th>
              <th className="py-1 pr-2 text-right font-medium">
                {tr(language, '平均超额', 'Excess')}
              </th>
              <th className="py-1 pr-2 text-right font-medium">
                {tr(language, '延迟(min)', 'Delay')}
              </th>
              <th className="py-1 text-right font-medium">
                {tr(language, '最多同时', 'Max open')}
              </th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.label} className="border-t border-[rgba(25,36,52,0.06)]">
                <td className="py-1 pr-2 text-left text-nofx-text">
                  {label(language, table, r.label)}
                </td>
                <td className="py-1 pr-2 text-right">{r.trips}</td>
                <td className="py-1 pr-2 text-right">{r.traders}</td>
                <td className="py-1 pr-2 text-right">{pct(r.win)}</td>
                <td
                  className={`py-1 pr-2 text-right ${r.avg >= 0 ? 'text-nofx-success' : 'text-nofx-danger'}`}
                >
                  {signed(r.avg)}
                </td>
                <td
                  className={`py-1 pr-2 text-right ${r.med >= 0 ? 'text-nofx-success' : 'text-nofx-danger'}`}
                >
                  {signed(r.med)}
                </td>
                <td className="py-1 pr-2 text-right">{num(r.total)}</td>
                <td className="py-1 pr-2 text-right">
                  {r.beat > 0 ? pct(r.beat) : '—'}
                </td>
                <td className="py-1 pr-2 text-right">
                  {r.beat > 0 ? signed(r.avg_excess) : '—'}
                </td>
                <td className="py-1 pr-2 text-right">{num(r.avg_delay_min)}</td>
                <td className="py-1 text-right">{r.max_open}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

export function PaperTab({ language }: { language: string }) {
  const { data, mutate } = useSWR<PaperInfo>('copytrade/paper', api.getPaper, {
    refreshInterval: 30000,
  })
  const [busy, setBusy] = useState(false)
  if (!data) return null
  const rows = data.rows || []
  const roster = data.roster

  const start = async () => {
    const ok = await confirmToast(
      tr(
        language,
        '开始新的模拟跟单记录？当前名单会固定为现在关注的榜单账户，之后榜单变化也继续记这批人。已有的旧记录不会删除，但页面只显示最新一份。',
        'Start a new simulated-copy record? The roster is frozen to the currently followed board accounts and kept even as the board changes. Old records are kept but only the latest is shown.'
      ),
      {
        title: tr(language, '开始记录', 'Start recording'),
        okText: tr(language, '开始', 'Start'),
        cancelText: tr(language, '取消', 'Cancel'),
      }
    )
    if (!ok) return
    setBusy(true)
    try {
      await api.startPaper()
      await mutate()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const tables = ['all', 'style', 'side', 'agree', 'delay', 'price'].filter((k) =>
    rows.some((r) => r.table === k)
  )

  return (
    <div className={`${ui.panel} mb-3`}>
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-semibold text-nofx-text">
          {tr(language, '模拟跟单（只记账，不下单）', 'Simulated copy (record only)')}
        </span>
        {roster ? (
          <span className={`${ui.muted} tabular-nums`}>
            {tr(
              language,
              `名单 ${data.members ?? 0} 人 · 已记 ${data.fills} 笔 · 开始于 ${new Date(roster.started_at).toLocaleString()}`,
              `${data.members ?? 0} traders · ${data.fills} fills · since ${new Date(roster.started_at).toLocaleString()}`
            )}
          </span>
        ) : (
          <span className={ui.muted}>
            {tr(language, '尚未开始记录', 'Not recording yet')}
          </span>
        )}
        <button
          type="button"
          className={`${ui.btnGhost} ml-auto`}
          disabled={busy}
          onClick={() => void start()}
        >
          {roster ? (
            <RefreshCw className="mr-1 inline h-3 w-3" />
          ) : (
            <Play className="mr-1 inline h-3 w-3" />
          )}
          {roster
            ? tr(language, '重新开始一份', 'Start a new record')
            : tr(language, '开始记录', 'Start recording')}
        </button>
      </div>
      <p className={`${ui.muted} mt-1 text-xs`}>
        {tr(
          language,
          '名单在开始时固定，之后榜单变化也继续记这批人，用来去掉幸存者偏差。价格用我们实际看到那一刻的标记价，延迟单独记录；口径和回测一致：按比例跟每个动作、仓位按最大时金额算一份本金、每笔扣 0.05%。',
          'The roster is fixed at the start and kept as the board changes, to remove survivor bias. Prices use the mark price when we noticed each action, with the delay recorded. Same as the backtest: copy every action proportionally, size by the position at its largest, 0.05% fee per fill.'
        )}
      </p>

      {!roster ? (
        <p className={`${ui.muted} mt-3 text-sm`}>
          {tr(
            language,
            '还没有开始记录。点上面的「开始记录」，把当前榜单关注的人固定下来，往后记录“如果跟了会怎样”。',
            'No record yet. Click "Start recording" above to freeze the current board follows and record what copying them would have returned.'
          )}
        </p>
      ) : rows.length === 0 ? (
        <p className={`${ui.muted} mt-3 text-sm`}>
          {tr(
            language,
            '已开始记录，但还没有记到任何动作。等关注的人有开平仓后，这里会出现结果。',
            'Recording has started but no action has been captured yet. Results appear once followed traders open or close positions.'
          )}
        </p>
      ) : (
        <div className="mt-3">
          {tables.map((k) => (
            <Rows
              key={k}
              language={language}
              table={k}
              rows={rows.filter((r) => r.table === k)}
            />
          ))}
        </div>
      )}
      {data.roster === null && data.rosters && data.rosters.length > 0 ? (
        <p role="alert" className="mt-1 text-xs text-nofx-gold">
          <NoticeText
            text={tr(
              language,
              '有历史记录但没有生效的名单，点「开始记录」新建一份。',
              'There are past records but no active roster; start a new record.'
            )}
          />
        </p>
      ) : null}
    </div>
  )
}
