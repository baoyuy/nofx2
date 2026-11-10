import { useMemo } from 'react'
import type { DecisionRecord } from '../../types'
import { formatRequestError } from '../../lib/requestErrors'
import { useLanguage } from '../../contexts/LanguageContext'

/**
 * ExecutionLog renders the AI trading agent's real decisions and order results
 * as a high-density, newest-first terminal stream — Bloomberg-style log on the
 * cream paper theme. Each cycle is a clearly-delimited block: a header bar
 * (cycle no · time · action count), one badge line per AI action, and one
 * indented sub-line per execution-log entry, color-coded by
 * success / throttle / risk.
 *
 * Real data only — renders each DecisionRecord without changing stored history;
 * verbose throttle and transport errors are summarized without inventing results.
 */

const C_AMBER = '#c8860b' // throttle / block warnings

// Strip dex prefix + quote suffix to the bare base ticker.
function baseSymbol(raw: string): string {
  return raw
    .toUpperCase()
    .replace(/^XYZ:/, '')
    .replace(/(USDT|USDC|USD)$/, '')
}

// Include the local date: old sessions must not appear to be today's trades.
function fmtTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '--:--:--'
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

type Side = 'long' | 'short' | 'flat'

// Trade side of an action token — drives badge color.
function actionSide(action: string): Side {
  const a = action.toLowerCase()
  if (a === 'open_long' || a === 'close_short') return 'long'
  if (a === 'open_short' || a === 'close_long') return 'short'
  return 'flat'
}

function sideColor(side: Side): string {
  if (side === 'long') return 'var(--tm-up)'
  if (side === 'short') return 'var(--tm-dn)'
  return 'var(--tm-muted)'
}

type LogTone = 'ok' | 'warn' | 'risk' | 'info'

// Classify an execution-log line into a tone for color-coding.
function logTone(line: string): LogTone {
  const s = line.toLowerCase()
  if (s.includes('succeed') || s.includes('success') || line.includes('✓')) return 'ok'
  if (s.includes('throttle') || s.includes('re-entry') || s.includes('cooldown') || s.includes('blocked'))
    return 'warn'
  if (
    s.includes('risk') ||
    s.includes('fail') ||
    s.includes('reject') ||
    s.includes('error') ||
    s.includes('denied') ||
    line.includes('✗') ||
    line.includes('❌')
  )
    return 'risk'
  return 'info'
}

const TONE_COLOR: Record<LogTone, string> = {
  ok: 'var(--tm-up)',
  warn: C_AMBER,
  risk: 'var(--tm-dn)',
  info: 'var(--tm-ink-2)',
}
const TONE_GLYPH: Record<LogTone, string> = { ok: '✓', warn: '⚠', risk: '❌', info: '·' }

// Go duration ("1h30m0s", "29m0s") → "1 小时 30 分" / "29 分".
function zhDuration(d: string): string {
  const m = d.match(/^(?:(\d+)h)?(?:(\d+)m)?(?:([\d.]+)s)?$/)
  if (!m) return d
  const [, h, min, s] = m
  const parts: string[] = []
  if (h && h !== '0') parts.push(`${h} 小时`)
  if (min && min !== '0') parts.push(`${min} 分`)
  if (!parts.length && s) parts.push(`${Math.round(Number(s))} 秒`)
  return parts.join(' ') || '0 分'
}

const SIDE_ZH: Record<string, string> = { long: '多单', short: '空单' }

// Translate a throttle reason (trader/auto_trader_throttle.go) to Chinese.
function zhReason(reason: string): string {
  let m = reason.match(
    /has only been held for (\S+) with price PnL (-?[\d.]+)%; min AI-managed hold is (\S+) unless price loss <= (-?[\d.]+)% or price profit >= (-?[\d.]+)%(?:; wait about (\S+))?/
  )
  if (m) {
    const [, held, pnl, min, loss, profit, wait] = m
    return `持仓仅 ${zhDuration(held)}，价格盈亏 ${pnl}%。持仓不满 ${zhDuration(min)} 时，AI 只有在亏损达 ${loss}% 或盈利达 ${profit}% 时才能平仓${wait ? `，约 ${zhDuration(wait)} 后可平` : ''}`
  }
  m = reason.match(
    /has been held for (\S+) with price PnL (-?[\d.]+)%; it is still inside the noise band (-?[\d.]+)% to (-?[\d.]+)%, so wait about (\S+)/
  )
  if (m) {
    const [, held, pnl, lo, hi, wait] = m
    return `已持仓 ${zhDuration(held)}，价格盈亏 ${pnl}%，仍在 ${lo}% ~ ${hi}% 的波动区间内，约 ${zhDuration(wait)} 后才允许小幅盈亏平仓`
  }
  m = reason.match(/was closed (\S+) ago; wait (\S+) before re-entry/)
  if (m) return `${zhDuration(m[1])}前刚平仓，需再等 ${zhDuration(m[2])} 才能重新开仓`
  m = reason.match(/already has an open (long|short) position/i)
  if (m) return `已有${SIDE_ZH[m[1].toLowerCase()]}持仓，需先处理现有持仓再开反向仓`
  return reason.replace(/^trade throttle:\s*/, '')
}

// Translate one backend execution-log line (trader/auto_trader_loop.go) to
// Chinese. Unknown lines are returned unchanged.
function zhLog(s: string): string {
  let m = s.match(/^AI call duration: (\d+) ms$/)
  if (m) return `AI 响应耗时 ${(Number(m[1]) / 1000).toFixed(1)} 秒`
  m = s.match(/^(\d+) of (\d+) trade actions failed or were blocked$/)
  if (m) return `${m[2]} 笔交易操作中有 ${m[1]} 笔失败或被拦截`
  m = s.match(/^(?:[^\sA-Z0-9]+\s*)?([A-Z0-9:_-]+) (\w+) (succeeded|blocked|failed)(?:: ([\s\S]*))?$/)
  if (m) {
    const [, sym, action, result, detail] = m
    const head = `${baseSymbol(sym)} ${ACTION_ZH[action.toLowerCase()] ?? action}`
    if (result === 'succeeded') return `${head} · 执行成功`
    if (result === 'blocked') return `${head} · 被拦截：${zhReason(detail ?? '')}`
    return `${head} · 执行失败：${formatRequestError(detail ?? '', 'zh')}`
  }
  if (s === 'No candidate coins available, cycle skipped') return '没有可用的候选币，跳过本轮'
  m = s.match(/^Risk control paused, remaining (\d+) minutes$/)
  if (m) return `风控暂停交易中，还剩 ${m[1]} 分钟`
  m = s.match(/^Trading blocked: ([\s\S]*)$/)
  if (m) return `交易被禁止：${m[1]}`
  m = s.match(/^Failed to build trading context: ([\s\S]*)$/)
  if (m) return `构建交易上下文失败：${formatRequestError(m[1], 'zh')}`
  m = s.match(/^Failed to get AI decision: ([\s\S]*)$/)
  if (m) return `获取 AI 决策失败：${formatRequestError(m[1], 'zh')}`
  m = s.match(/^AI fee wallet out of funds: balance \$([\d.]+) USDC, next call needs ~\$([\d.]+)/)
  if (m) return `AI 费用钱包余额不足：余额 $${m[1]} USDC，下次调用约需 $${m[2]}，请充值 Base USDC 钱包`
  m = s.match(/^Grid cycle completed with (\d+) decisions$/)
  if (m) return `网格周期完成，共 ${m[1]} 个决策`
  return formatRequestError(s, 'zh')
}

/**
 * Tidy a verbose execution-log string to its gist without fabricating data.
 * Strips a leading "└"/glyph the backend may already include (we render our
 * own), the symbol (already shown), and collapses the wordy throttle phrasing
 * to "throttle · closed 28m ago, wait 2m". Falls back to the original line.
 */
function cleanLog(raw: string, isZh = false): string {
  let s = raw.replace(/^[\s└>•·]*[✓✗⚠❌]?\s*/, '').trim()
  if (isZh) return zhLog(s)

  const throttle = s.match(/closed\s+([0-9smhd.]+)\s+ago;\s*wait\s+([0-9smhd.]+)/i)
  if (throttle) {
    const ago = throttle[1].replace(/(\d)0s$/, '$1').replace(/0s$/, '')
    const wait = throttle[2].replace(/(\d)0s$/, '$1').replace(/0s$/, '')
    return `throttle · closed ${ago} ago, wait ${wait}`
  }
  // drop a redundant leading "SYMBOL action" prefix when present
  s = s.replace(/^[A-Z0-9:_-]{2,12}\s+(open_long|open_short|close_long|close_short)\s+/i, '')
  return formatRequestError(s)
}

interface ExecutionLogProps {
  decisions?: DecisionRecord[]
  height?: number
}

export function ExecutionLog({ decisions, height = 440 }: ExecutionLogProps) {
  const { language } = useLanguage()
  const isZh = language === 'zh'
  // Newest cycle first.
  const cycles = useMemo(() => {
    const list = decisions ?? []
    return [...list].sort((a, b) => b.cycle_number - a.cycle_number)
  }, [decisions])

  return (
    <div style={{ fontFamily: 'var(--tm-mono)' }}>
      {/* header */}
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, marginBottom: 2 }}>
        <span className="tm-px" style={{ fontSize: 11 }}>{isZh ? '执行日志' : 'Execution log'}</span>
        <span
          className="tm-sc"
          style={{ marginLeft: 'auto', color: cycles.length ? 'var(--tm-up)' : 'var(--tm-muted)' }}
        >
          {cycles.length ? (isZh ? `${cycles.length} 轮` : `${cycles.length} cyc`) : '—'}
        </span>
      </div>
      <div className="tm-sc" style={{ fontSize: 9, marginBottom: 5 }}>
        {isZh ? '执行日志 · 每轮 AI 决策与成交' : 'Execution log · AI decisions & fills per cycle'}
      </div>

      {/* legend */}
      <div
        className="tm-sc"
        style={{ display: 'flex', flexWrap: 'wrap', gap: 12, marginBottom: 6, fontSize: 9 }}
      >
        <Legend glyph="✓" c="var(--tm-up)" label={isZh ? '正常' : 'ok'} />
        <Legend glyph="⚠" c={C_AMBER} label={isZh ? '限流' : 'throttle'} />
        <Legend glyph="❌" c="var(--tm-dn)" label={isZh ? '风险' : 'risk'} />
      </div>

      <div className="tm-hair" style={{ marginBottom: 0 }} />

      {!cycles.length ? (
        <div className="tm-sc" style={{ padding: '16px 0' }}>{isZh ? '暂无执行事件。' : 'No execution events yet.'}</div>
      ) : (
        <div
          style={{
            height,
            overflowY: 'auto',
            fontSize: 10,
            lineHeight: 1.5,
            fontVariantNumeric: 'tabular-nums',
          }}
        >
          {cycles.map((c) => (
            <Cycle key={`${c.cycle_number}-${c.timestamp}`} record={c} />
          ))}
        </div>
      )}
    </div>
  )
}

function Legend({ glyph, c, label }: { glyph: string; c: string; label: string }) {
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
      <span style={{ color: c, fontSize: 10 }}>{glyph}</span>
      <span>{label}</span>
    </span>
  )
}

interface CycleProps {
  record: DecisionRecord
}

function Cycle({ record }: CycleProps) {
  const { language } = useLanguage()
  const isZh = language === 'zh'
  const time = fmtTime(record.timestamp)
  const actions = record.decisions ?? []
  const logs = record.execution_log ?? []
  const count = actions.length
  const trades = actions.filter((a) => a.action !== 'hold' && a.action !== 'wait')
  const failed = trades.filter((a) => !a.success).length
  // Also correct the display of older records whose aggregate flag was wrong.
  const fault = !record.success || failed > 0

  return (
    <div style={{ borderBottom: '1px solid var(--tm-hair)', padding: '5px 0' }}>
      {/* cycle header bar */}
      <div
        className="tm-sc"
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 7,
          fontSize: 9,
          padding: '2px 6px',
          marginBottom: actions.length || logs.length || record.error_message ? 4 : 0,
          background: 'rgba(25,36,52,0.045)',
          borderLeft: `2px solid ${fault ? 'var(--tm-dn)' : 'var(--tm-hair)'}`,
          color: 'var(--tm-ink-2)',
        }}
      >
        <span style={{ color: 'var(--tm-ink)', fontWeight: 700 }}>{isZh ? `周期 ${record.cycle_number}` : `CYCLE ${record.cycle_number}`}</span>
        <span style={{ color: 'var(--tm-muted)' }}>·</span>
        <span style={{ color: 'var(--tm-muted)' }} title={isZh ? '本地时间' : 'Local time'}>{time}</span>
        <span style={{ marginLeft: 'auto', color: 'var(--tm-muted)' }}>
          {count === 0 ? (isZh ? '无操作' : 'no action') : isZh ? `${count} 个操作` : `${count} action${count > 1 ? 's' : ''}`}
        </span>
        {fault ? (
          <span style={{ color: 'var(--tm-dn)', fontWeight: 700 }}>
            {isZh
              ? `故障${failed > 0 ? ` · ${failed}/${trades.length} 笔交易失败/被拦截` : ''}`
              : `FAULT${failed > 0 ? ` · ${failed}/${trades.length} trades failed/blocked` : ''}`}
          </span>
        ) : null}
      </div>

      {/* AI action lines */}
      {actions.map((a, i) => {
        const side = actionSide(a.action)
        const aTime = fmtTime(a.timestamp)
        return (
          <div
            key={`act-${i}`}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 8,
              padding: '1px 6px',
              color: 'var(--tm-ink-2)',
            }}
          >
            <span style={{ color: 'var(--tm-muted)', flex: '0 0 auto', minWidth: 52 }}>
              {aTime !== '--:--:--' ? aTime : time}
            </span>
            <ActionBadge action={a.action} side={side} />
            <span style={{ color: 'var(--tm-ink)', fontWeight: 600, flex: '0 0 auto', minWidth: 48 }}>
              {baseSymbol(a.symbol)}
            </span>
            {a.confidence != null ? (
              <span style={{ color: 'var(--tm-muted)', flex: '0 0 auto' }}>
                {isZh ? '置信度 ' : 'conf'}<span style={{ color: 'var(--tm-ink-2)' }}>{Math.round(a.confidence)}</span>
              </span>
            ) : null}
          </div>
        )
      })}

      {/* execution-log result sub-lines */}
      {logs.map((line, i) => (
        <SubLine key={`log-${i}`} tone={logTone(line)} text={cleanLog(line, isZh)} />
      ))}

      {/* fault message */}
      {record.error_message ? (
        <SubLine tone="risk" text={cleanLog(record.error_message, isZh)} />
      ) : null}
    </div>
  )
}

const ACTION_ZH: Record<string, string> = {
  open_long: '开仓做多',
  open_short: '开仓做空',
  close_long: '平多',
  close_short: '平空',
  hold: '持有',
  wait: '等待',
}

function ActionBadge({ action, side }: { action: string; side: Side }) {
  const { language } = useLanguage()
  const isZh = language === 'zh'
  const c = sideColor(side)
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        flex: '0 0 auto',
        padding: '0 5px',
        height: 14,
        fontSize: 9,
        letterSpacing: '0.04em',
        color: c,
        border: `1px solid ${c}`,
        background:
          side === 'long'
            ? 'rgba(46,139,87,0.08)'
            : side === 'short'
              ? 'rgba(214,67,58,0.08)'
              : 'transparent',
        borderRadius: 2,
        whiteSpace: 'nowrap',
      }}
    >
      {isZh ? (ACTION_ZH[action.toLowerCase()] ?? action.toLowerCase()) : action.toLowerCase()}
    </span>
  )
}

function SubLine({ tone, text }: { tone: LogTone; text: string }) {
  const color = TONE_COLOR[tone]
  const glyph = TONE_GLYPH[tone]
  return (
    <div
      style={{
        display: 'flex',
        gap: 6,
        padding: '0 6px 0 10px',
        marginLeft: 6,
        borderLeft: '1px solid var(--tm-hair)',
        color,
      }}
    >
      <span style={{ flex: '0 0 auto', width: 10, textAlign: 'center' }}>{glyph}</span>
      <span style={{ minWidth: 0, overflowWrap: 'anywhere' }}>{text}</span>
    </div>
  )
}

export default ExecutionLog
