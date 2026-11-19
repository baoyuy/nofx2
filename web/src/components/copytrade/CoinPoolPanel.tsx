import useSWR from 'swr'
import { RefreshCw } from 'lucide-react'
import { api } from '../../lib/api'
import type { CoinPoolInfo } from '../../lib/api/copyTrade'
import { notify } from '../../lib/notify'
import { tr, ui } from '../market/marketUi'
import { base } from './format'

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
  const build = async () => {
    try {
      await api.generateCoinPool()
      await mutate()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : String(e))
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
            const cls = neutral
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
                title={c.note || ''}
              >
                {base(c.symbol)}{' '}
                {neutral
                  ? tr(language, '中', 'N')
                  : short
                    ? tr(language, '空', 'S')
                    : tr(language, '多', 'L')}
                {c.score != null ? ` ${Math.round(c.score)}` : ''}
                {expMin != null ? ` · ${tr(language, `${expMin}m`, `${expMin}m`)}` : ''}
              </span>
            )
          })}
        </div>
      ) : (
        <p className={`${ui.muted} text-xs`}>
          {tr(
            language,
            '还没有生成。到「设置 → 带单 AI」点「立即生成币池」，或开启「分析后自动生成币池」。',
            'None yet. Go to Settings → Copy-trade AI and click "Build pool now", or enable auto-build.'
          )}
        </p>
      )}
    </section>
  )
}
