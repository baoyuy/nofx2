import type { ProfitProtectionConfig } from '../../types/strategy'
import { normalizeProfitProtection } from '../../lib/strategyForm'
import { Field, NumberInput, text } from './StrategyEditorParts'

export function ProfitProtectionPanel({ value, onChange, language }: {
  value?: ProfitProtectionConfig
  onChange: (value: ProfitProtectionConfig) => void
  language: string
}) {
  const config = normalizeProfitProtection(value)
  const fields = [
    { key: 'breakeven_activation_pct', zh: '保本激活价格涨幅', en: 'Breakeven activation', min: 0.1, max: 100 },
    { key: 'breakeven_lock_pct', zh: '保本额外锁利缓冲', en: 'Additional lock buffer', min: 0, max: config.breakeven_activation_pct },
    { key: 'trailing_activation_pct', zh: '移动保护激活价格涨幅', en: 'Trailing activation', min: 0.1, max: 100 },
    { key: 'trailing_giveback_pct', zh: '允许回吐峰值利润比例', en: 'Peak-profit giveback', min: 1, max: 99 },
  ] as const
  return (
    <details className="mt-5 rounded-xl border border-white/10 p-4">
      <summary className="cursor-pointer text-sm font-medium">{text(language, '高级风控：模拟账户盈利保护', 'Advanced risk: paper profit protection')}</summary>
      <p className="mt-3 text-xs leading-relaxed text-zinc-400">
        {text(language,
          '仅 Binance 模拟账户（binance_paper），默认关闭；实盘拒绝启用。仅对启用后新开仓生效，不追溯现有仓位。每分钟检查，写入模拟止损且只收紧不放宽；激活后跌回门槛以下仍保护。',
          'Binance paper accounts (binance_paper) only; off by default, rejected for live accounts. Applies only to new opens, never existing positions. Checked every minute; persists a stop that only tightens and stays armed below activation.')}
      </p>
      <label className="my-4 flex items-center gap-2 text-sm">
        <input type="checkbox" checked={config.enabled} onChange={(event) => onChange({ ...config, enabled: event.target.checked })} />
        {text(language, '启用新模拟仓位盈利保护', 'Enable protection for new paper positions')}
      </label>
      <fieldset disabled={!config.enabled} className="grid gap-4 sm:grid-cols-2 disabled:opacity-50">
        {fields.map((field) => (
          <Field key={field.key} language={language} enforced="code" label={text(language, field.zh, field.en)}>
            <NumberInput value={config[field.key]} range={{ min: field.min, max: field.max }} step={0.1} suffix="%" onChange={(n) => onChange(normalizeProfitProtection({ ...config, [field.key]: n }))} />
          </Field>
        ))}
      </fieldset>
      <p className="mt-3 text-xs leading-relaxed text-amber-300/80">
        {text(language,
          '激活与锁利均按开仓成本的价格幅度计（空仓为有利跌幅），不是杠杆 ROI。回吐 40% 指峰值价格利润回吐 40%（峰值 +10% 时底线为 +6%）。锁利 0 表示覆盖模拟手续费及退出滑点后的保本底线；正数是额外价格缓冲，不保证扣费后收益，跳空仍可能亏损。禁用不会撤销已设置的保护止损。原有峰值 >5% / 回吐 40% 的保护始终保留。升级后丢弃无法验证的旧峰值，从首次可信观察重新建立。',
          'Activation/lock use entry-cost price moves (favorable declines for shorts), not leveraged ROI. 40% giveback means a +10% peak has a +6% floor. A zero lock covers simulated fees and exit slippage; a positive lock adds a price buffer, not guaranteed net returns. Gaps can still lose money. Disabling does not cancel existing stops. The original peak >5% / 40% giveback protection remains. On upgrade, unverifiable old peaks are discarded and tracking restarts at the first trusted observation.')}
      </p>
    </details>
  )
}
