import { useState } from 'react'
import useSWR from 'swr'
import { RefreshCw } from 'lucide-react'
import { api } from '../../lib/api'
import type { CoinPoolInfo } from '../../lib/api/copyTrade'
import { tr, ui } from '../market/marketUi'
import { ago, base } from './format'

// The meta-AI coin pool: a small list of coins (each with a side) that the
// strategy layer can trade. Visible on the by-coin page.
export function CoinPoolPanel({ language }: { language: string }) {
  const { data, mutate } = useSWR<CoinPoolInfo>(
    'copytrade/coin-pool',
    () => api.getCoinPool(),
    { refreshInterval: 15000 }
  )
  const coins = data?.coins || []
  const now = Date.now()
  const [msg, setMsg] = useState('')
  const build = async () => {
    setMsg('')
    try {
      const p = await api.generateCoinPool()
      await mutate()
      if (!p || !p.count) {
        setMsg(
          tr(
            language,
            '暂无可用候选：请先跑一次 AI 分析（有评分）后再生成。',
            'No candidates yet: run an AI analysis first, then build.'
          )
        )
      }
    } catch (e) {
      setMsg(
        tr(
          language,
          '生成失败或暂无可用评分，请先分析后再试。',
          'Build failed or no scores yet; run an analysis first.'
        )
      )
      void e
    }
  }
  return (
    <section className={`${ui.panel} mb-3`}>
      <div className="mb-1 flex flex-wrap items-center gap-2">
        <span className="text-sm font-semibold text-nofx-text">
          {tr(language, '总 AI 币池（≤10，多空）', 'AI coin pool (≤10, long/short)')}
        </span>
        <span className={`${ui.muted} text-xs tabular-nums`}>
          {tr(language, `${coins.length} 个`, `${coins.length} coins`)}
        </span>
        {data?.updated_at ? (
          <span className={`${ui.muted} text-xs`}>
            · {tr(language, '更新于', 'updated')} {ago(language, data.updated_at)}
          </span>
        ) : null}
        {data?.history && data.history.length > 0 ? (
          <span className={`${ui.muted} text-xs tabular-nums`}>
            · {tr(language, '本次', 'this round')}{' '}
            <span className="text-nofx-success">+{data.history[0].added}</span>{' '}
            <span className="text-nofx-danger">-{data.history[0].removed}</span>
          </span>
        ) : null}
        <button
          type="button"
          className={`${ui.btnGhost} ml-auto`}
          onClick={() => void build()}
        >
          <RefreshCw className="mr-1 inline h-3 w-3" />
          {tr(language, '重新生成', 'Rebuild')}
        </button>
      </div>
      {coins.length ? (
        <div className="flex flex-wrap gap-2">
          {coins.map((c) => {
            const short = c.direction === 'short'
            const neutral = c.direction === 'neutral'
            // An expired pick is ignored by the engine (store/coin_pool.go),
            // so it must not render like a live one. It used to clamp to
            // "· 0m" in full colour, reading as a position being traded.
            const expired = !!c.expiry_at && c.expiry_at <= now
            const cls = expired
              ? 'text-nofx-text-muted border-[rgba(25,36,52,0.2)] line-through opacity-60'
              : neutral
                ? 'text-nofx-text-muted border-[rgba(25,36,52,0.2)]'
                : short
                  ? 'text-nofx-danger border-nofx-danger/40'
                  : 'text-nofx-success border-nofx-success/40'
            const expMin = c.expiry_at
              ? Math.max(0, Math.round((c.expiry_at - now) / 60000))
              : null
            return (
              <span
                key={c.symbol}
                className={`rounded border px-2 py-1 text-xs tabular-nums ${cls}`}
                title={
                  expired
                    ? tr(
                        language,
                        '已过期，策略不会再交易这个选币',
                        'Expired; the strategy no longer trades this pick'
                      ) + (c.note ? ` · ${c.note}` : '')
                    : c.note || ''
                }
              >
                {base(c.symbol)}{' '}
                {neutral
                  ? tr(language, '中', 'N')
                  : short
                    ? tr(language, '空', 'S')
                    : tr(language, '多', 'L')}
                {c.score != null ? ` ${Math.round(c.score)}` : ''}
                {expired
                  ? ` · ${tr(language, '已过期', 'expired')}`
                  : expMin != null
                    ? ` · ${tr(language, `${expMin}m`, `${expMin}m`)}`
                    : ''}
              </span>
            )
          })}
        </div>
      ) : data?.id ? (
        <p className={`${ui.muted} text-xs`}>
          {tr(
            language,
            '本轮没有达到入池条件的币。币池保留（ID 不变），使用它的策略本轮不开新仓，已有持仓照常管理。',
            'No coin qualifies this round. The pool is kept (same ID); strategies using it open nothing new, existing positions are still managed.'
          )}
        </p>
      ) : (
        <p className={`${ui.muted} text-xs`}>
          {tr(
            language,
            '还没有生成。后台会按设置的间隔自动刷新；也可到「设置 → 带单 AI」点「立即生成币池」，或开启「分析后自动生成币池」。',
            'None yet. It auto-refreshes on the configured interval; or go to Settings → Copy-trade AI and click "Build pool now".'
          )}
        </p>
      )}
      {data?.id ? (
        <p className={`${ui.muted} mt-1 text-xs`}>
          {tr(
            language,
            '交易方式：在策略编辑里把币源选为「我的币池 → 带单AI币池」，交易员使用该策略后，看板会显示策略加载的这些币。',
            'To trade it: in the strategy editor set the coin source to "My coin pool → 带单AI币池"; a trader using that strategy shows these coins on the dashboard.'
          )}
        </p>
      ) : null}
      {data?.history && data.history.length > 0 ? (
        <details className="mt-2">
          <summary className={`${ui.muted} cursor-pointer text-xs`}>
            {tr(language, '变化历史', 'Change history')}
          </summary>
          <ul className="mt-1 space-y-0.5 text-xs tabular-nums">
            {data.history.slice(0, 10).map((h) => (
              <li key={h.id} className={ui.muted}>
                {ago(language, h.at)} · <span className="text-nofx-success">+{h.added}</span>{' '}
                <span className="text-nofx-danger">-{h.removed}</span>
              </li>
            ))}
          </ul>
        </details>
      ) : null}
      {msg ? <p className="mt-1 text-xs text-nofx-gold">{msg}</p> : null}
    </section>
  )
}
