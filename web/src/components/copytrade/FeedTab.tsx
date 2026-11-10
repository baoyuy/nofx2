import { NoticeText } from '../NoticeText'
import { useEffect, useState } from 'react'
import useSWR from 'swr'
import { LOCAL_SNAPSHOT_OPTIONS } from '../../lib/liveSnapshot'
import { api } from '../../lib/api'
import type {
  FeedQuery,
  TradeAction,
  TraderSnapshot,
} from '../../lib/api/copyTrade'
import { tr, ui } from '../market/marketUi'
import {
  actionClass,
  actionLabel,
  base,
  clock,
  price,
  signedUsd,
  usd,
} from './format'

export function FeedTab({
  language,
  snaps,
  onSymbol,
}: {
  language: string
  snaps: TraderSnapshot[]
  onSymbol: (symbol: string) => void
}) {
  const [trader, setTrader] = useState('')
  const [symbol, setSymbol] = useState('')
  const [action, setAction] = useState<TradeAction | ''>('')
  const [minValue, setMinValue] = useState(0)
  const [hideHighFreq, setHideHighFreq] = useState(true)
  const highFreq = snaps.filter((s) => s.high_freq)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(20)
  // Changing a filter starts again from the newest page.
  const filter =
    <T,>(set: (v: T) => void) =>
    (v: T) => {
      set(v)
      setPage(1)
    }

  const query: FeedQuery = {
    portfolio_id: trader || undefined,
    symbol: symbol.trim()
      ? `${symbol.trim().toUpperCase().replace(/USDT$/, '')}USDT`
      : undefined,
    action,
    min_value: minValue || undefined,
    hide_high_freq: hideHighFreq ? '1' : undefined,
    page,
    page_size: pageSize,
  }
  const { data, error: requestError } = useSWR(
    ['copytrade/feed', query],
    () => api.getFeed(query),
    LOCAL_SNAPSHOT_OPTIONS
  )
  const items = data?.items || []
  const total = data?.total || 0
  const error = requestError instanceof Error ? requestError.message : String(requestError || '')
  const pages = Math.max(1, Math.ceil(total / pageSize))
  useEffect(() => {
    if (data) {
      const last = Math.max(1, Math.ceil(data.total / pageSize))
      if (page > last) setPage(last)
    }
  }, [data, page, pageSize])

  const pager = (where: string) =>
    total > pageSize ? (
      <div
        className={`${where} flex flex-wrap items-center justify-center gap-2 text-sm`}
      >
        <button
          type="button"
          className={ui.btnGhost}
          disabled={page <= 1}
          onClick={() => setPage(1)}
        >
          {tr(language, '最新', 'Newest')}
        </button>
        <button
          type="button"
          className={ui.btnGhost}
          disabled={page <= 1}
          onClick={() => setPage((p) => p - 1)}
        >
          {tr(language, '上一页', 'Previous')}
        </button>
        <span className={ui.muted}>
          {tr(
            language,
            `第 ${page} / ${pages} 页 · 共 ${total} 条`,
            `Page ${page} / ${pages} · ${total} actions`
          )}
        </span>
        <button
          type="button"
          className={ui.btnGhost}
          disabled={page >= pages}
          onClick={() => setPage((p) => p + 1)}
        >
          {tr(language, '下一页', 'Next')}
        </button>
        <button
          type="button"
          className={ui.btnGhost}
          disabled={page >= pages}
          onClick={() => setPage(pages)}
        >
          {tr(language, '最早', 'Oldest')}
        </button>
        <select
          className={`${ui.inputInline} py-0.5 text-xs`}
          value={pageSize}
          onChange={(e) => filter(setPageSize)(Number(e.target.value))}
          aria-label={tr(language, '每页条数', 'Per page')}
        >
          {[20, 50, 100].map((n) => (
            <option key={n} value={n}>
              {tr(language, `每页 ${n} 条`, `${n} per page`)}
            </option>
          ))}
        </select>
      </div>
    ) : null

  return (
    <section className={ui.panel}>
      <div className="mb-3 flex flex-wrap items-end gap-3">
        <label>
          <span className={ui.label}>{tr(language, '带单员', 'Trader')}</span>
          <select
            className={`${ui.inputInline}`}
            value={trader}
            onChange={(e) => filter(setTrader)(e.target.value)}
          >
            <option value="">{tr(language, '全部', 'All')}</option>
            {snaps.map((s) => (
              <option key={s.trader.portfolio_id} value={s.trader.portfolio_id}>
                {s.trader.nickname}
              </option>
            ))}
          </select>
        </label>
        <label>
          <span className={ui.label}>{tr(language, '币种', 'Coin')}</span>
          <input
            className={`${ui.inputInline} w-28`}
            value={symbol}
            onChange={(e) => filter(setSymbol)(e.target.value)}
            placeholder="BTC"
          />
        </label>
        <label>
          <span className={ui.label}>{tr(language, '动作', 'Action')}</span>
          <select
            className={`${ui.inputInline}`}
            value={action}
            onChange={(e) =>
              filter(setAction)(e.target.value as TradeAction | '')
            }
          >
            <option value="">{tr(language, '全部', 'All')}</option>
            <option value="open">{tr(language, '开仓', 'Open')}</option>
            <option value="add">{tr(language, '加仓', 'Add')}</option>
            <option value="reduce">{tr(language, '减仓', 'Reduce')}</option>
            <option value="close">{tr(language, '平仓', 'Close')}</option>
          </select>
        </label>
        <label>
          <span className={ui.label}>
            {tr(language, '金额不低于', 'Min value')}
          </span>
          <select
            className={`${ui.inputInline}`}
            value={minValue}
            onChange={(e) => filter(setMinValue)(Number(e.target.value))}
          >
            {[0, 100, 1000, 10000, 100000].map((v) => (
              <option key={v} value={v}>
                {v ? usd(v) : tr(language, '不限', 'Any')}
              </option>
            ))}
          </select>
        </label>
        {highFreq.length ? (
          <label
            className="flex items-center gap-1.5 pb-2 text-sm text-nofx-text"
            title={highFreq
              .map(
                (s) =>
                  `${s.trader.nickname}：${tr(language, `近 24 小时 ${s.orders_per_day} 单`, `${s.orders_per_day} orders in 24h`)}`
              )
              .join('\n')}
          >
            <input
              type="checkbox"
              checked={hideHighFreq}
              onChange={(e) => filter(setHideHighFreq)(e.target.checked)}
            />
            {tr(
              language,
              `隐藏高频交易员（${highFreq.length} 人，每天 48 单以上）`,
              `Hide high-frequency traders (${highFreq.length}, 48+ orders a day)`
            )}
          </label>
        ) : null}
      </div>
      {pager('mb-3')}
      {error ? (
        <p role="alert" className="py-4 text-sm text-nofx-danger">
          <NoticeText text={error} />
        </p>
      ) : null}
      {items.length ? (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[720px] text-sm">
            <thead className="text-left text-xs text-nofx-text-muted">
              <tr>
                <th className="py-1.5 font-normal">
                  {tr(language, '时间', 'Time')}
                </th>
                <th className="font-normal">
                  {tr(language, '带单员', 'Trader')}
                </th>
                <th className="font-normal">
                  {tr(language, '动作', 'Action')}
                </th>
                <th className="font-normal">{tr(language, '币种', 'Coin')}</th>
                <th className="font-normal">{tr(language, '金额', 'Value')}</th>
                <th className="font-normal">{tr(language, '价格', 'Price')}</th>
                <th className="font-normal">
                  {tr(language, '已实现盈亏', 'Realized')}
                </th>
              </tr>
            </thead>
            <tbody className="text-nofx-text">
              {items.map((e, i) => (
                <tr
                  key={`${e.portfolio_id}-${e.time}-${i}`}
                  className={`border-t ${ui.border}`}
                >
                  <td className="py-1.5 tabular-nums text-nofx-text-muted">
                    {clock(e.time)}
                  </td>
                  <td>{e.nickname}</td>
                  <td
                    className={`font-medium ${actionClass(e.action, e.side)}`}
                  >
                    {actionLabel(language, e.action, e.side)}
                    {e.untracked ? (
                      <span
                        className="ml-1 text-[10px] font-normal text-nofx-text-muted"
                        title={tr(
                          language,
                          '这个仓位在开始监控前就有了',
                          'Opened before monitoring started'
                        )}
                      >
                        {tr(language, '（早于监控）', '(pre-monitoring)')}
                      </span>
                    ) : null}
                  </td>
                  <td>
                    <button
                      type="button"
                      className="hover:underline"
                      onClick={() => onSymbol(e.symbol)}
                    >
                      {base(e.symbol)}
                    </button>
                  </td>
                  <td className="tabular-nums">{usd(e.value)}</td>
                  <td className="tabular-nums">{price(e.price)}</td>
                  <td
                    className={`tabular-nums ${e.realized_pnl > 0 ? 'text-nofx-success' : e.realized_pnl < 0 ? 'text-nofx-danger' : ''}`}
                  >
                    {e.realized_pnl ? signedUsd(e.realized_pnl) : ''}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className={`py-8 text-center ${ui.muted}`}>
          {tr(
            language,
            '还没有动态。关注带单员后，他们的开仓、加仓、减仓、平仓会显示在这里。',
            'No actions yet. Opens, adds, reductions and closes of followed traders show here.'
          )}
        </p>
      )}
      {pager('mt-3')}
    </section>
  )
}
