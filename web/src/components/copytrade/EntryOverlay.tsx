import { useState } from 'react'
import { X } from 'lucide-react'
import type { FeedItem, TraderBrief } from '../../lib/api/copyTrade'
import type { ChartProjector } from '../market/BinanceChart'
import { tr } from '../market/marketUi'
import type { EntryMark, ExitMark } from './chartMarks'
import { positionResult } from './chartMarks'
import { HealthTag, ScoreBadge } from './ConfidenceUi'
import {
  actionLabel,
  clock,
  pct,
  price,
  sideLabel,
  signedUsd,
  usd,
} from './format'

const AVATAR = 22
const UP = '#2e8b57'
const DOWN = '#d6433a'

function Avatar({
  url,
  name,
  side,
  dashed,
  closed,
  trapped,
}: {
  url?: string
  name: string
  side: 'long' | 'short'
  dashed?: boolean
  /** the position is no longer held: faded with a grey border */
  closed?: boolean
  /** held at a deep loss: a small amber dot */
  trapped?: boolean
}) {
  const [broken, setBroken] = useState(false)
  const color = closed ? '#9aa3b2' : side === 'long' ? UP : DOWN
  // Binance's blank default picture tells traders apart worse than a letter.
  const usable = url && !/default-avatar/i.test(url)
  return (
    <span className="relative block">
      <span
        className="relative flex items-center justify-center overflow-hidden rounded-full bg-white text-[10px] font-semibold shadow"
        style={{
          width: AVATAR,
          height: AVATAR,
          border: `2px ${dashed ? 'dashed' : 'solid'} ${color}`,
          color,
          opacity: closed ? 0.45 : 1,
        }}
      >
        {/* The letter shows until (or if never) the picture loads. */}
        {(name || '?').slice(0, 1)}
        {usable && !broken ? (
          <img
            src={url}
            alt=""
            className="absolute inset-0 h-full w-full object-cover"
            referrerPolicy="no-referrer"
            onError={() => setBroken(true)}
          />
        ) : null}
      </span>
      {trapped && !closed ? (
        <span className="absolute -right-0.5 -top-0.5 h-2 w-2 rounded-full border border-white bg-nofx-gold" />
      ) : null}
    </span>
  )
}

function Card({
  mark,
  events,
  traders,
  lastPrice,
  language,
  onClose,
}: {
  mark: EntryMark
  events: FeedItem[]
  traders: Record<string, TraderBrief>
  lastPrice: number
  language: string
  onClose: () => void
}) {
  const t = traders[mark.portfolioId] || mark.holder
  const e = mark.event
  const h = mark.holder
  const r = positionResult(mark, events, lastPrice)
  const tone = (v: number) =>
    v > 0 ? 'text-nofx-success' : v < 0 ? 'text-nofx-danger' : 'text-nofx-text'
  return (
    <div className="w-[250px] rounded-lg border border-[rgba(25,36,52,0.14)] bg-white p-3 text-xs text-nofx-text shadow-lg">
      <div className="mb-2 flex items-center gap-2">
        <Avatar url={t?.avatar_url} name={t?.nickname || ''} side={mark.side} />
        <a
          href={`https://www.binance.com/zh-CN/copy-trading/lead-details/${mark.portfolioId}`}
          target="_blank"
          rel="noreferrer"
          className="min-w-0 flex-1 truncate font-semibold hover:underline"
        >
          {t?.nickname || mark.portfolioId}
        </a>
        <button
          type="button"
          onClick={onClose}
          aria-label={tr(language, '关闭', 'Close')}
          className="text-nofx-text-muted hover:text-nofx-text"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      </div>
      {e ? (
        <div className="mb-1.5">
          <span
            className={`font-semibold ${mark.side === 'long' ? 'text-nofx-success' : 'text-nofx-danger'}`}
          >
            {actionLabel(language, e.action, e.side)}
          </span>{' '}
          {usd(e.value)} @{' '}
          <span className="font-semibold">{price(e.price)}</span>
          <div className="text-nofx-text-muted">{clock(e.time)}</div>
        </div>
      ) : (
        <div className="mb-1.5">
          <span
            className={`font-semibold ${mark.side === 'long' ? 'text-nofx-success' : 'text-nofx-danger'}`}
          >
            {tr(
              language,
              `持有${sideLabel(language, mark.side)}单`,
              `Holding ${sideLabel(language, mark.side)}`
            )}
          </span>{' '}
          @ <span className="font-semibold">{price(mark.price)}</span>
          <div className="text-nofx-text-muted">
            {h?.opened_at
              ? tr(
                  language,
                  `${clock(h.opened_at)} 入场，早于图上的 K 线`,
                  `Opened ${clock(h.opened_at)}, before the first candle`
                )
              : tr(
                  language,
                  '开始监控前就已持有，入场时间未知',
                  'Held before monitoring started; entry time unknown'
                )}
          </div>
        </div>
      )}
      <div className="space-y-0.5 border-t border-[rgba(25,36,52,0.08)] pt-1.5">
        {r.state === 'held' && h ? (
          <>
            <div>
              {tr(language, '当前持仓', 'Position')} {usd(h.entry_value)} @{' '}
              {price(h.entry_price)}
              {h.leverage ? ` · ${h.leverage}x` : ''}
            </div>
            {h.trapped || h.fresh || h.liquidated ? (
              <div>
                <HealthTag h={h} language={language} />
              </div>
            ) : null}
            <div>
              {tr(language, '浮盈亏', 'Unrealized')}{' '}
              <span className={`font-semibold ${tone(r.pnl)}`}>
                {signedUsd(r.pnl)}
              </span>
              {r.pct !== null ? (
                <span className={tone(r.pnl)}>
                  {' '}
                  ({pct(r.pct)}
                  {r.pctOfMargin ? tr(language, ' 保证金', ' of margin') : ''})
                </span>
              ) : null}
              {h.source !== 'public' ? (
                <span className="text-nofx-text-muted">
                  {tr(language, ' · 按标记价估算', ' · est. from mark price')}
                </span>
              ) : null}
            </div>
          </>
        ) : r.state === 'closed' ? (
          <div>
            {tr(language, '已平仓，盈亏', 'Closed, realized')}{' '}
            <span className={`font-semibold ${tone(r.pnl)}`}>
              {signedUsd(r.pnl)}
            </span>
          </div>
        ) : (
          <div className="text-nofx-text-muted">
            {tr(language, '这笔仓位已不在持仓里', 'No longer held')}
            {r.pnl ? (
              <>
                {tr(language, '，已实现', ', realized ')}{' '}
                <span className={tone(r.pnl)}>{signedUsd(r.pnl)}</span>
              </>
            ) : null}
          </div>
        )}
        {t ? (
          <div className="text-nofx-text-muted">
            {t.score !== undefined ? (
              <>
                <ScoreBadge score={t.score} language={language} />{' '}
              </>
            ) : null}
            {tr(language, '近 30 天', '30 days')}{' '}
            <span className={tone(t.roi_30d)}>{pct(t.roi_30d)}</span> ·{' '}
            {tr(language, '盈利', 'PnL')}{' '}
            <span className={tone(t.pnl_30d)}>{signedUsd(t.pnl_30d)}</span> ·{' '}
            {tr(language, '胜率', 'Win')} {t.win_rate_30d.toFixed(0)}%
          </div>
        ) : null}
      </div>
    </div>
  )
}

const CARD_W = 250
const CARD_H = 175

// Keeps the card off the entry line: below it (or above when there is no
// room), and left of the avatar near the right edge.
function cardPosition(x: number, lineY: number, p: ChartProjector) {
  const right = x + AVATAR
  const left =
    right + CARD_W <= p.width - 4 ? right : Math.max(4, x - AVATAR - CARD_W)
  const below = lineY + 14
  const top =
    below + CARD_H <= p.height - 4 ? below : Math.max(4, lineY - 14 - CARD_H)
  return { left, top }
}

export function EntryOverlay({
  p,
  entries,
  exits,
  events,
  traders,
  lastPrice,
  language,
  openKey,
  onOpen,
}: {
  p: ChartProjector
  entries: EntryMark[]
  exits: ExitMark[]
  events: FeedItem[]
  traders: Record<string, TraderBrief>
  lastPrice: number
  language: string
  openKey: string
  onOpen: (key: string) => void
}) {
  // Several avatars on one candle stack away from the price.
  const stack = new Map<string, number>()
  let pinned = 0
  const placed = entries.flatMap((m) => {
    const y0 = p.y(m.price)
    if (y0 === null) return []
    let x: number | null
    let y = y0
    if (m.kind === 'pinned') {
      x = 4 + AVATAR / 2 + pinned++ * (AVATAR + 2)
    } else {
      x = p.x(m.bar)
      const k = `${m.bar}|${m.side}`
      const n = stack.get(k) || 0
      stack.set(k, n + 1)
      y += (m.side === 'long' ? 1 : -1) * n * (AVATAR - 6)
    }
    if (x === null || x < -AVATAR || x > p.width + AVATAR) return []
    return [{ m, x, y }]
  })
  const open = placed.find((v) => v.m.key === openKey)
  const name = (id: string) => traders[id]?.nickname || ''

  return (
    <>
      {exits.map((x) => {
        const cx = p.x(x.bar)
        const cy = p.y(x.price)
        if (cx === null || cy === null || cx < 0 || cx > p.width) return null
        const e = x.event
        const color = e.realized_pnl >= 0 ? UP : DOWN
        // Closing a long sells (arrow down); closing a short buys (arrow up).
        const down = e.side === 'long'
        return (
          <span
            key={x.key}
            className="pointer-events-auto absolute"
            title={`${name(e.portfolio_id)} ${actionLabel(language, e.action, e.side)} ${usd(e.value)} @ ${price(e.price)}${e.realized_pnl ? ` · ${signedUsd(e.realized_pnl)}` : ''}`}
            style={{
              left: cx - 5,
              top: cy - 5,
              width: 0,
              height: 0,
              borderLeft: '5px solid transparent',
              borderRight: '5px solid transparent',
              [down ? 'borderTop' : 'borderBottom']: `8px solid ${color}`,
            }}
          />
        )
      })}
      {placed.map(({ m, x, y }) => {
        const t = traders[m.portfolioId] || m.holder
        return (
          <button
            key={m.key}
            type="button"
            className="pointer-events-auto absolute rounded-full transition-transform hover:z-10 hover:scale-125"
            style={{
              left: x - AVATAR / 2,
              top: y - AVATAR / 2,
              zIndex: m.key === openKey ? 11 : undefined,
            }}
            title={
              m.kind === 'pinned'
                ? tr(
                    language,
                    `${t?.nickname || ''}：入场不在图上显示的时间内`,
                    `${t?.nickname || ''}: entry is before the chart`
                  )
                : m.holder?.liquidated
                  ? tr(
                      language,
                      `${t?.nickname || ''}：这笔仓位可能已被强平`,
                      `${t?.nickname || ''}: likely liquidated`
                    )
                  : m.closed
                    ? tr(
                        language,
                        `${t?.nickname || ''}：这笔仓位已平`,
                        `${t?.nickname || ''}: position closed`
                      )
                    : m.holder?.trapped
                      ? tr(
                          language,
                          `${t?.nickname || ''}：被套中`,
                          `${t?.nickname || ''}: trapped in loss`
                        )
                      : t?.nickname
            }
            aria-label={t?.nickname || m.portfolioId}
            onClick={(ev) => {
              ev.stopPropagation()
              onOpen(m.key === openKey ? '' : m.key)
            }}
          >
            <Avatar
              url={t?.avatar_url}
              name={t?.nickname || ''}
              side={m.side}
              dashed={m.kind === 'pinned'}
              closed={m.closed}
              trapped={m.holder?.trapped}
            />
          </button>
        )
      })}
      {open
        ? (() => {
            // The chosen entry's price, from its candle to now.
            const py = p.y(open.m.price)
            if (py === null) return null
            const left = open.m.kind === 'pinned' ? 0 : Math.max(0, open.x)
            const color = open.m.side === 'long' ? UP : DOWN
            return (
              <>
                <div
                  className="absolute"
                  style={{
                    left,
                    top: py,
                    width: Math.max(0, p.width - left),
                    borderTop: `1.5px dashed ${color}`,
                  }}
                />
                {Math.abs(open.y - py) > 1 ? (
                  <div
                    className="absolute"
                    style={{
                      left: open.x,
                      top: Math.min(open.y, py),
                      height: Math.abs(open.y - py),
                      borderLeft: `1.5px dashed ${color}`,
                    }}
                  />
                ) : null}
                <span
                  className="absolute rounded px-1 text-[10px] font-semibold text-white"
                  style={{
                    left: Math.min(left + AVATAR, p.width - 110),
                    top: py - 16,
                    background: color,
                  }}
                >
                  {tr(language, '入场', 'Entry')} {price(open.m.price)}
                </span>
              </>
            )
          })()
        : null}
      {open ? (
        <div
          className="pointer-events-auto absolute z-30"
          style={cardPosition(open.x, p.y(open.m.price) ?? open.y, p)}
          onClick={(ev) => ev.stopPropagation()}
        >
          <Card
            mark={open.m}
            events={events}
            traders={traders}
            lastPrice={lastPrice}
            language={language}
            onClose={() => onOpen('')}
          />
        </div>
      ) : null}
    </>
  )
}
