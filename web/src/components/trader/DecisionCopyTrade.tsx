import { useState } from 'react'
import type { DecisionCopyTrade } from '../../types/trading'

const UP = '#2E8B57'
const DOWN = '#D6433A'
const MUTED = '#707e92'

function price(v: number): string {
  if (!v) return '-'
  if (v >= 100) return v.toFixed(2)
  if (v >= 1) return v.toFixed(4)
  return v.toPrecision(4)
}

/**
 * What followed lead traders did on each coin the AI saw in this cycle.
 * Click a coin for the exact block the AI read.
 */
export function DecisionCopyTradePanel({
  items,
  language,
}: {
  items?: DecisionCopyTrade[]
  language: string
}) {
  const [open, setOpen] = useState('')
  if (!items?.length) return null
  const zh = language === 'zh'
  const shown = items.find((i) => i.symbol === open)
  return (
    <div
      className="mb-4 rounded-lg p-3"
      style={{ background: '#f6f8fb', border: '1px solid rgba(25,36,52,0.14)' }}
    >
      <div className="mb-2 flex items-center justify-between">
        <span className="text-sm font-semibold" style={{ color: '#192434' }}>
          👥 {zh ? 'AI 看到的带单参考' : 'Lead-trader context the AI saw'}
        </span>
        <span className="text-xs" style={{ color: MUTED }}>
          {zh
            ? '带单原文在下方「用户提示词」里'
            : 'The text the AI read is in the user prompt below'}
        </span>
      </div>
      <div className="flex flex-wrap gap-2">
        {items.map((c) => {
          const color =
            c.direction === 'long' ? UP : c.direction === 'short' ? DOWN : MUTED
          const dir =
            c.direction === 'long'
              ? zh
                ? '偏多'
                : 'long'
              : c.direction === 'short'
                ? zh
                  ? '偏空'
                  : 'short'
                : zh
                  ? '分歧'
                  : 'split'
          const avg =
            c.direction === 'short' ? c.short_avg_entry : c.long_avg_entry
          return (
            <button
              key={c.symbol}
              type="button"
              onClick={() => setOpen(open === c.symbol ? '' : c.symbol)}
              className="rounded-md px-2.5 py-1.5 text-left text-xs"
              style={{
                background: open === c.symbol ? 'rgba(41,91,232,0.1)' : '#fff',
                border: `1px solid ${open === c.symbol ? '#295be8' : 'rgba(25,36,52,0.14)'}`,
              }}
            >
              <span className="font-semibold" style={{ color: '#192434' }}>
                {c.symbol.replace(/^xyz:/i, '').replace(/USDT$/, '')}
              </span>{' '}
              <span className="font-semibold" style={{ color }}>
                {dir} {c.confidence.toFixed(0)}
              </span>
              <span style={{ color: MUTED }}>
                {' · '}
                <span style={{ color: UP }}>
                  {c.long_traders}
                  {zh ? '多' : 'L'}
                </span>
                /
                <span style={{ color: DOWN }}>
                  {c.short_traders}
                  {zh ? '空' : 'S'}
                </span>
                {avg ? ` · ${zh ? '均价' : 'avg'} ${price(avg)}` : ''}
                {c.trapped
                  ? ` · ${c.trapped}${zh ? '人被套' : ' trapped'}`
                  : ''}
                {c.fresh ? ` · ${c.fresh}${zh ? '人新开' : ' fresh'}` : ''}
              </span>
            </button>
          )
        })}
      </div>
      {shown ? (
        <div
          className="mt-2 rounded-md p-2 text-xs"
          style={{ background: '#edf1f7', color: MUTED }}
        >
          {zh
            ? `${shown.symbol}：完整带单原文在下方「用户提示词」中，可复制或下载。`
            : `${shown.symbol}: the full copy-trade text is in the user prompt below, where you can copy or download it.`}
        </div>
      ) : null}
    </div>
  )
}
