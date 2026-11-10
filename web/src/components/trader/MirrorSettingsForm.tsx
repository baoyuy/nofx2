import type { MirrorForm } from './mirrorFormat'
import { tr, ui } from '../market/marketUi'

const choice = (active: boolean) =>
  `flex-1 rounded-lg border px-3 py-1.5 text-sm ${active ? 'border-nofx-gold bg-nofx-gold/10 font-semibold text-nofx-text' : 'border-[rgba(25,36,52,0.14)] text-nofx-text-muted'}`

/** Sizing, leverage, stops and caps of a copy link. */
export function MirrorSettingsForm({
  language,
  value,
  onChange,
}: {
  language: string
  value: MirrorForm
  onChange: (v: MirrorForm) => void
}) {
  const set = (patch: Partial<MirrorForm>) => onChange({ ...value, ...patch })
  const field = (
    k: keyof MirrorForm,
    label: string,
    hint: string,
    step = 'any'
  ) => (
    <label className="block">
      <span className={ui.label}>{label}</span>
      <input
        type="number"
        min={0}
        step={step}
        value={value[k] as string}
        onChange={(e) => set({ [k]: e.target.value })}
        className={ui.input}
        aria-label={label}
      />
      <span className={`${ui.muted} mt-0.5 block`}>{hint}</span>
    </label>
  )
  return (
    <div className="grid gap-3">
      <div>
        <span className={ui.label}>{tr(language, '下单金额', 'Size')}</span>
        <div className="flex gap-2">
          <button
            type="button"
            className={choice(value.size_mode === 'fixed')}
            onClick={() => set({ size_mode: 'fixed' })}
          >
            {tr(language, '固定金额', 'Fixed margin')}
          </button>
          <button
            type="button"
            className={choice(value.size_mode === 'ratio')}
            onClick={() => set({ size_mode: 'ratio' })}
          >
            {tr(language, '按比例', 'By ratio')}
          </button>
        </div>
      </div>
      {value.size_mode === 'fixed'
        ? field(
            'fixed_margin',
            tr(language, '每单保证金 (USDT)', 'Margin per open (USDT)'),
            tr(
              language,
              'AI 每开一单，跟单账户用这么多保证金开。加仓、减仓按 AI 的比例跟。',
              'Each AI open is copied with this much margin; adds and reduces follow the AI in proportion.'
            )
          )
        : field(
            'ratio',
            tr(language, '比例倍数', 'Ratio'),
            tr(
              language,
              'AI 这单用了它总资金的几成，跟单账户就用自己总资金的几成，再乘这个倍数。1 = 一样，0.5 = 减半。',
              'The follower puts up the same share of its equity as the AI, times this. 1 = same, 0.5 = half.'
            )
          )}
      <div>
        <span className={ui.label}>{tr(language, '杠杆', 'Leverage')}</span>
        <div className="flex gap-2">
          <button
            type="button"
            className={choice(value.leverage_mode === 'follow')}
            onClick={() => set({ leverage_mode: 'follow' })}
          >
            {tr(language, '跟随 AI', "AI's")}
          </button>
          <button
            type="button"
            className={choice(value.leverage_mode === 'fixed')}
            onClick={() => set({ leverage_mode: 'fixed' })}
          >
            {tr(language, '固定', 'Fixed')}
          </button>
        </div>
      </div>
      {value.leverage_mode === 'fixed'
        ? field(
            'leverage',
            tr(language, '杠杆倍数', 'Leverage'),
            tr(language, '1 到 125', '1 to 125'),
            '1'
          )
        : field(
            'max_leverage',
            tr(language, '杠杆上限', 'Max leverage'),
            tr(
              language,
              'AI 用的杠杆超过这个就按这个开；留空不限。',
              "Caps the AI's leverage; empty = no cap."
            ),
            '1'
          )}
      <label className="flex items-start gap-2 text-sm text-nofx-text">
        <input
          type="checkbox"
          className="mt-1"
          checked={value.follow_protect}
          onChange={(e) => set({ follow_protect: e.target.checked })}
        />
        <span>
          {tr(
            language,
            '跟随 AI 的止盈止损',
            "Copy the AI's stop loss and take profit"
          )}
          <span className={`${ui.muted} block`}>
            {tr(
              language,
              '不勾也会跟着 AI 平仓：AI 平仓、止损止盈触发或被强平时，跟单账户按同样比例平仓。',
              'Either way the copy closes when the AI closes, its stop fires or it is liquidated.'
            )}
          </span>
        </span>
      </label>
      <div className="grid grid-cols-2 gap-3">
        {field(
          'max_margin',
          tr(language, '单笔保证金上限 (USDT)', 'Max margin a position (USDT)'),
          tr(language, '留空不限', 'Empty = no cap')
        )}
        {field(
          'max_loss',
          tr(language, '总亏损上限 (USDT)', 'Loss cap (USDT)'),
          tr(
            language,
            '跟单账户比开始时亏了这么多，就平掉跟单仓位并停止；留空不限。',
            'Closes the copies and stops once the account lost this much since the start; empty = no cap.'
          )
        )}
      </div>
    </div>
  )
}
