import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../../lib/api'
import type { SymbolSummary } from '../../lib/api/copyTrade'
import {
  COPY_TRADE_LIMITS,
  DEFAULT_COPY_TRADE,
  type CopyDirection,
  type CopyFollow,
  type CopyTradeSettings,
} from '../../lib/strategyForm'
import { ROUTES } from '../../router/paths'
import { Field, NumberInput, text } from './StrategyEditorParts'

/**
 * Coins the copy_trade source would pick now, as the backend does: the
 * leading side matches the direction, enough traders, confidence high
 * enough, highest confidence first. (The backend also drops traders not
 * refreshed for 30 minutes.)
 */
export function pickCopyTradeCoins(
  coins: SymbolSummary[],
  s: CopyTradeSettings
): SymbolSummary[] {
  return coins
    .filter((c) => {
      const dir = c.confidence?.direction
      return (
        dir !== undefined &&
        dir !== 'neutral' &&
        c.confidence.score >= s.minConfidence &&
        (s.direction === 'both' || dir === s.direction) &&
        c.long.traders + c.short.traders >= s.minTraders &&
        (!s.minOpeners ||
          ((dir === 'short' ? c.short : c.long).openers ?? 0) >= s.minOpeners)
      )
    })
    .sort((a, b) => b.confidence.score - a.confidence.score)
    .slice(0, s.limit)
}

const choice = (active: boolean) =>
  `flex-1 rounded-lg border px-3 py-2 text-sm font-semibold ${active ? 'border-nofx-gold bg-nofx-gold/10 text-nofx-text' : 'border-[rgba(25,36,52,0.14)] text-nofx-text-muted'}`

/** Program-enforced link between the AI's opens and the lead traders' lean. */
function FollowChoice({
  language,
  value,
  onChange,
}: {
  language: string
  value: CopyFollow
  onChange: (v: CopyFollow) => void
}) {
  const options: [CopyFollow, string, string][] = [
    [
      'follow',
      text(language, '跟随', 'Follow'),
      text(
        language,
        '只能顺着带单员的偏向开仓',
        "Open only on the lead traders' side"
      ),
    ],
    [
      'free',
      text(language, '自由', 'Free'),
      text(language, 'AI 自己决定方向', 'The AI picks the side'),
    ],
    [
      'fade',
      text(language, '反向', 'Fade'),
      text(language, '只做和带单员相反的方向', 'Open only against their side'),
    ],
  ]
  return (
    <Field
      language={language}
      enforced="code"
      label={text(language, '开仓方向', 'Open direction')}
      hint={text(
        language,
        '由程序强制执行：不符合的开仓会被拒绝，平仓不受限制。带单员在这个币上没有明显偏向时，「跟随」和「反向」都不会开仓。',
        'Enforced by the program: other opens are rejected; closing is never blocked. With no clear lean on a coin, Follow and Fade do not open it.'
      )}
    >
      <div className="flex gap-2" role="radiogroup">
        {options.map(([v, label, hint]) => (
          <button
            key={v}
            type="button"
            role="radio"
            aria-checked={value === v}
            onClick={() => onChange(v)}
            className={`${choice(value === v)} text-left`}
          >
            <span className="block">{label}</span>
            <span className="block text-xs font-normal text-nofx-text-muted">
              {hint}
            </span>
          </button>
        ))}
      </div>
    </Field>
  )
}

export function CopyTradeSourcePanel({
  language,
  settings,
  onChange,
  follow,
  onFollow,
}: {
  language: string
  settings?: CopyTradeSettings
  onChange: (s: CopyTradeSettings) => void
  follow: CopyFollow
  onFollow: (v: CopyFollow) => void
}) {
  const navigate = useNavigate()
  const s = { ...DEFAULT_COPY_TRADE, ...(settings || {}) }
  const set = (patch: Partial<CopyTradeSettings>) =>
    onChange({ ...s, ...patch })
  const [coins, setCoins] = useState<SymbolSummary[] | null>(null)
  const [coverage, setCoverage] = useState<{
    fresh: number
    total: number
  } | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    let alive = true
    api
      .getCopyTradeSymbols(true)
      .then((r) => {
        if (!alive) return
        setCoins(r.symbols || [])
        setCoverage({
          fresh: r.fresh_traders ?? 0,
          total: r.total_traders ?? 0,
        })
      })
      .catch((e) => alive && setError(String(e?.message || e)))
    return () => {
      alive = false
    }
  }, [])
  const picked = coins ? pickCopyTradeCoins(coins, s) : []

  return (
    <>
      <p className="text-xs text-nofx-text-muted">
        {text(
          language,
          '每轮决策前，从「带单」页关注的带单员当前持仓里选币：按多空方向置信度从高到低，交给 AI。AI 同时会看到每个币上带单员的多空人数、均价、被套和新开情况，但仍然按 K 线和指标自己判断，不会照抄带单员。持仓中的币掉出名单后，AI 仍会继续管理直到平仓。',
          'Before each cycle, coins are picked from what the lead traders you follow on the Copy trade page hold, highest direction confidence first. The AI also sees, per coin, how many hold long or short, their average entry, and who is trapped or fresh, but still decides from candles and indicators instead of copying them. Held coins that drop off are still managed until closed.'
        )}
      </p>
      <FollowChoice language={language} value={follow} onChange={onFollow} />
      <div className="grid gap-4 md:grid-cols-2">
        <Field
          language={language}
          label={text(language, '带单员偏向', 'Lead traders lean')}
          hint={text(
            language,
            '只选带单员偏多或偏空的币。AI 开仓方向仍由它自己决定。',
            'Only coins the lead traders lean long or short on. The AI still picks its own side.'
          )}
        >
          <div className="flex gap-2" role="radiogroup">
            {(
              [
                ['both', text(language, '都要', 'Both')],
                ['long', text(language, '偏多', 'Long')],
                ['short', text(language, '偏空', 'Short')],
              ] as [CopyDirection, string][]
            ).map(([v, label]) => (
              <button
                key={v}
                type="button"
                role="radio"
                aria-checked={s.direction === v}
                onClick={() => set({ direction: v })}
                className={choice(s.direction === v)}
              >
                {label}
              </button>
            ))}
          </div>
        </Field>
        <Field
          language={language}
          label={text(language, '置信度不低于', 'Min confidence')}
          hint={text(
            language,
            '带单页「按币种」里的置信度，0–100。越高越少但越一致。',
            'The confidence on the Copy trade coin view, 0-100. Higher means fewer but more agreed coins.'
          )}
        >
          <NumberInput
            value={s.minConfidence}
            range={COPY_TRADE_LIMITS.minConfidence}
            onChange={(v) => set({ minConfidence: v })}
          />
        </Field>
        <Field
          language={language}
          label={text(language, '至少几个带单员持有', 'Min lead traders')}
          hint={text(
            language,
            '只有一两个人持有的币，说明不了什么。',
            'A coin held by one or two traders says little.'
          )}
        >
          <NumberInput
            value={s.minTraders}
            range={COPY_TRADE_LIMITS.minTraders}
            suffix={text(language, '人', '')}
            onChange={(v) => set({ minTraders: v })}
          />
        </Field>
        <Field
          language={language}
          label={text(language, '最多选几个币', 'Max coins')}
          hint={text(
            language,
            '币越多，每轮花的 token 越多。',
            'More coins cost more tokens per cycle.'
          )}
        >
          <NumberInput
            value={s.limit}
            range={COPY_TRADE_LIMITS.limit}
            onChange={(v) => set({ limit: v })}
          />
        </Field>
      </div>
      <CopyTradeGates
        language={language}
        settings={s}
        onChange={onChange}
        follow={follow}
      />
      <div className="flex flex-wrap items-center gap-2" aria-live="polite">
        <span className="text-xs text-nofx-text-muted">
          {text(language, '当前会交易：', 'Would trade now:')}
        </span>
        {error ? (
          <span className="text-xs text-nofx-danger">
            {text(
              language,
              '读取带单数据失败，请稍后再试。',
              'Could not read copy-trade data; try again later.'
            )}
          </span>
        ) : !coins ? (
          <span className="text-xs text-nofx-text-muted">
            {text(language, '正在读取带单数据…', 'Reading copy-trade data…')}
          </span>
        ) : picked.length === 0 ? (
          <span className="text-xs text-nofx-text-muted">
            {coins.length
              ? text(
                  language,
                  '现在没有符合条件的币，本轮只会管理已有持仓。可以放宽置信度或人数。',
                  'No coin passes now; the cycle only manages open positions. Try a lower confidence or fewer traders.'
                )
              : text(
                  language,
                  '关注的带单员现在都没有持仓。先去「带单」页关注一些带单员。',
                  'Followed lead traders hold nothing now. Follow some on the Copy trade page first.'
                )}
          </span>
        ) : (
          picked.map((c) => (
            <span
              key={c.symbol}
              className="rounded-md bg-nofx-gold/10 px-2 py-1 text-xs font-medium text-nofx-gold"
              title={text(
                language,
                `做多 ${c.long.traders} 人、做空 ${c.short.traders} 人`,
                `${c.long.traders} long, ${c.short.traders} short`
              )}
            >
              {c.symbol.replace(/USDT$/, '')}
              <span
                className={`ml-1 font-normal ${c.confidence.direction === 'long' ? 'text-nofx-success' : 'text-nofx-danger'}`}
              >
                {c.confidence.direction === 'long'
                  ? text(language, '偏多', 'long')
                  : text(language, '偏空', 'short')}{' '}
                {c.confidence.score.toFixed(0)}
              </span>
            </span>
          ))
        )}
        <button
          type="button"
          onClick={() => navigate(ROUTES.copyTrade)}
          className="text-xs text-nofx-gold underline"
        >
          {text(language, '去带单页查看', 'Open the Copy trade page')}
        </button>
        {coverage && coverage.total > 0 && coverage.fresh < coverage.total ? (
          <span className="text-xs text-nofx-text-muted">
            {text(
              language,
              `只有 ${coverage.fresh}/${coverage.total} 个带单员的数据是新鲜的（30 分钟内），实际可选币可能更少。`,
              `Only ${coverage.fresh} of ${coverage.total} followed traders have fresh data (last 30 minutes); the coins actually traded may be fewer.`
            )}
          </span>
        ) : null}
      </div>
    </>
  )
}

/** Program-enforced signal-quality gates; every one is off at 0. */
export function CopyTradeGates({
  language,
  settings,
  onChange,
  follow,
}: {
  language: string
  settings?: CopyTradeSettings
  onChange: (s: CopyTradeSettings) => void
  follow: CopyFollow
}) {
  const s = { ...DEFAULT_COPY_TRADE, ...(settings || {}) }
  const set = (patch: Partial<CopyTradeSettings>) =>
    onChange({ ...s, ...patch })
  const directed = follow !== 'free'
  return (
    <div className="rounded-lg border border-[rgba(25,36,52,0.14)] px-3 py-2">
      <div className="text-sm font-semibold text-nofx-text">
        {text(language, '信号质量门槛', 'Signal quality gates')}
      </div>
      <p className="mb-2 text-xs text-nofx-text-muted">
        {text(
          language,
          '由程序强制，填 0 表示关闭。「新开仓人数」「历史命中率」「顺 4 小时趋势」只在跟随或反向时生效；「成交额」总是生效。',
          'Enforced by the program; 0 turns one off. Fresh calls, hit rate and the 4h trend apply under follow or fade; the volume floor always applies.'
        )}
      </p>
      <div className="grid gap-4 md:grid-cols-2">
        <Field
          language={language}
          label={text(
            language,
            '24 小时内新开仓的带单员至少',
            'Fresh calls at least'
          )}
          hint={text(
            language,
            '偏向那一边、24 小时内新开或加仓、没被套、不是高频也不是摊平型的人数。只是一直拿着的老仓不算判断。',
            'Traders on the leading side who opened or added in 24h, not trapped, high-frequency or averaging down. Old positions held on are not a call.'
          )}
        >
          <NumberInput
            value={s.minOpeners}
            range={COPY_TRADE_LIMITS.minOpeners}
            suffix={text(language, '人', '')}
            onChange={(v) => set({ minOpeners: v })}
          />
        </Field>
        <Field
          language={language}
          label={text(language, '历史命中率至少', 'Hit rate at least')}
          hint={text(
            language,
            '同一置信度区间、同一方向的历史信号，24 小时后方向对的比例。反向模式看的是错的比例。样本不够时不开仓。',
            'How often past signals of the same confidence band and side were right 24h later (fade: wrong). No open until there are enough samples.'
          )}
        >
          <NumberInput
            value={s.minHitRate}
            range={COPY_TRADE_LIMITS.minHitRate}
            suffix="%"
            onChange={(v) => set({ minHitRate: v })}
          />
        </Field>
        {s.minHitRate > 0 ? (
          <Field
            language={language}
            label={text(language, '命中率至少基于', 'Over at least')}
            hint={text(
              language,
              '少于这么多次历史信号时不开仓。',
              'No open with fewer scored signals than this.'
            )}
          >
            <NumberInput
              value={s.minSamples}
              range={COPY_TRADE_LIMITS.minSamples}
              suffix={text(language, '次', 'signals')}
              onChange={(v) => set({ minSamples: v })}
            />
          </Field>
        ) : null}
        <Field
          language={language}
          label={text(language, '24 小时成交额至少', '24h volume at least')}
          hint={text(
            language,
            '成交额太小的币滑点大、容易被操纵，不开仓。',
            'Thin coins slip and get pushed around; no open below this.'
          )}
        >
          <NumberInput
            value={s.minVolumeM}
            range={COPY_TRADE_LIMITS.minVolumeM}
            suffix={text(language, '百万 U', 'M USDT')}
            onChange={(v) => set({ minVolumeM: v })}
          />
        </Field>
      </div>
      <label className="mt-2 flex items-center gap-2 text-sm text-nofx-text">
        <input
          type="checkbox"
          checked={s.trendFilter}
          onChange={(e) => set({ trendFilter: e.target.checked })}
        />
        {text(
          language,
          '不逆 4 小时趋势开仓（EMA20 在 EMA50 下方不做多，上方不做空）',
          'No open against the 4h trend (no long while EMA20 < EMA50, no short while above)'
        )}
      </label>
      {!directed && (s.minOpeners || s.minHitRate || s.trendFilter) ? (
        <p className="mt-1 text-xs text-nofx-gold">
          {text(
            language,
            '当前是「自由」模式，新开仓人数、命中率和趋势门槛不会生效。',
            'In free mode the fresh-call, hit-rate and trend gates do not apply.'
          )}
        </p>
      ) : null}
    </div>
  )
}

/** Opt-in for every other coin source. */
export function CopyTradeContextToggle({
  language,
  checked,
  onChange,
  follow,
  onFollow,
  minConfidence,
  onMinConfidence,
}: {
  language: string
  checked: boolean
  onChange: (v: boolean) => void
  follow: CopyFollow
  onFollow: (v: CopyFollow) => void
  minConfidence: number
  onMinConfidence: (v: number) => void
}) {
  return (
    <>
      <label className="flex items-start gap-2 rounded-lg border border-[rgba(25,36,52,0.14)] px-3 py-2">
        <input
          type="checkbox"
          className="mt-0.5"
          checked={checked}
          onChange={(e) => onChange(e.target.checked)}
        />
        <span>
          <span className="block text-sm font-semibold text-nofx-text">
            {text(language, '附带带单参考', 'Include lead-trader context')}
          </span>
          <span className="block text-xs text-nofx-text-muted">
            {text(
              language,
              '每个候选币和持仓币，AI 都会看到关注的带单员在这个币上的多空、均价、被套和最近动作，作为参考信号。没人持有的币不显示。',
              'For every candidate and held coin, the AI also sees what your followed lead traders hold there: sides, average entry, trapped or fresh, recent actions. Coins nobody holds show nothing.'
            )}
          </span>
        </span>
      </label>
      {checked ? (
        <FollowChoice language={language} value={follow} onChange={onFollow} />
      ) : null}
      {checked && follow !== 'free' ? (
        <Field
          language={language}
          label={text(
            language,
            '带单员置信度不低于',
            'Lead-trader confidence at least'
          )}
          hint={text(
            language,
            '跟随和反向由程序强制方向：带单员在这个币上的置信度低于这个值就不开仓。',
            'Follow and fade make the program enforce the side: with less confidence than this on a coin, no open is allowed.'
          )}
        >
          <NumberInput
            value={minConfidence}
            range={COPY_TRADE_LIMITS.contextMinConfidence}
            onChange={onMinConfidence}
          />
        </Field>
      ) : null}
    </>
  )
}
