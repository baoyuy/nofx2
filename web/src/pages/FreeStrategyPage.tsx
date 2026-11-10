import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  AlertTriangle,
  Check,
  Copy,
  Download,
  Loader2,
  Plus,
  Save,
  Trash2,
  Upload,
} from 'lucide-react'
import { useAuth } from '../contexts/AuthContext'
import { useLanguage } from '../contexts/LanguageContext'
import { DeepVoidBackground } from '../components/common/DeepVoidBackground'
import { api } from '../lib/api'
import { confirmToast, notify } from '../lib/notify'
import type { AIModel, Strategy, StrategyConfig, TraderInfo } from '../types'
import type { CoinPool, HornBoard } from '../lib/api/coinPool'
import { hornOnBoard } from '../lib/api/coinPool'
import { ROUTES } from '../router/paths'
import {
  DEFAULT_COINS,
  DEFAULT_COPY_TRADE,
  DEFAULT_HORN_INTERVAL,
  DEFAULT_MIN_OI_VALUE,
  LIMITS,
  hornIntervals,
  SAFE_DEFAULTS,
  configFromForm,
  defaultRules,
  formFromStrategy,
  indicatorOptions,
  parseCoins,
  timeframeOptions,
  usesPaidSource,
  type FreeForm,
} from '../lib/strategyForm'
import { parseStrategyFile, strategyToJsonc } from '../lib/strategyFile'
import { ProfitProtectionPanel } from '../components/strategy/ProfitProtectionPanel'
import {
  CopyTradeContextToggle,
  CopyTradeGates,
  CopyTradeSourcePanel,
} from '../components/strategy/CopyTradeSource'
import {
  Field,
  NumberInput,
  RiskSummary,
  RuleEditor,
  Section,
  TestRun,
  inputClass,
  text,
} from '../components/strategy/StrategyEditorParts'

// The market page's one-click auto trading builds strategies with the same
// form model.
export { configFromForm, parseCoins, usesPaidSource, type FreeForm }

// Free strategy editor: Binance USDT-M perpetuals, candles and local
// indicators only. Never uses paid Claw402 / Vergex / NofxOS data.

const sections = [
  { id: 'basics', zh: '基本信息与币种', en: 'Basics & coins' },
  { id: 'data', zh: 'AI 看什么数据', en: 'What the AI sees' },
  { id: 'risk', zh: '风险控制', en: 'Risk control' },
  { id: 'rules', zh: 'AI 交易规则', en: 'AI trading rules' },
  { id: 'test', zh: '预览与试运行', en: 'Preview & test run' },
] as const

export function FreeStrategyPage() {
  const { token } = useAuth()
  const { language } = useLanguage()
  const navigate = useNavigate()
  const lang = (language === 'zh' ? 'zh' : 'en') as 'zh' | 'en'
  const fileInput = useRef<HTMLInputElement>(null)

  const [strategies, setStrategies] = useState<Strategy[]>([])
  const [selected, setSelected] = useState<Strategy | null>(null)
  const [form, setForm] = useState<FreeForm | null>(null)
  const [defaultConfig, setDefaultConfig] = useState<StrategyConfig | null>(
    null
  )
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [hasChanges, setHasChanges] = useState(false)
  const [pools, setPools] = useState<CoinPool[]>([])
  const [models, setModels] = useState<AIModel[]>([])
  const [traders, setTraders] = useState<TraderInfo[]>([])

  const parsedCoins = useMemo(
    () => (form ? parseCoins(form.coinsInput) : []),
    [form]
  )
  const usingPool = form?.coinSource === 'pool'
  const usingHorn = form?.coinSource === 'horn'
  const preservingSource = form?.coinSource === 'preserved'
  const hornInterval = form?.hornInterval || DEFAULT_HORN_INTERVAL
  const selectedPool = pools.find((p) => p.id === form?.coinPoolID)
  // Preview of what the live horn source would trade right now.
  const [hornBoard, setHornBoard] = useState<HornBoard | null>(null)
  const [hornError, setHornError] = useState('')
  useEffect(() => {
    if (!usingHorn || !token) return
    let alive = true
    setHornBoard(null)
    setHornError('')
    api
      .screenBollinger(hornInterval)
      .then((b) => alive && setHornBoard(b))
      .catch((e) => alive && setHornError(String(e?.message || e)))
    return () => {
      alive = false
    }
  }, [usingHorn, hornInterval, token])
  const hornMatches = (hornBoard?.rows || [])
    .filter((r) => r.match && !r.error)
    .slice(0, LIMITS.maxCoins)
  const rulesDefault = useMemo(
    () => defaultRules(defaultConfig),
    [defaultConfig]
  )

  const selectStrategy = useCallback((strategy: Strategy | null) => {
    setSelected(strategy)
    setForm(strategy ? formFromStrategy(strategy) : null)
    setHasChanges(false)
  }, [])

  const loadStrategies = useCallback(
    async (preferredId?: string) => {
      if (!token) {
        setLoading(false)
        return
      }
      setLoading(true)
      try {
        const [list, defaults] = await Promise.all([
          api.getStrategies(),
          api.getDefaultStrategyConfig().catch(() => null),
        ])
        setStrategies(list)
        setDefaultConfig(defaults)
        const next =
          (preferredId ? list.find((item) => item.id === preferredId) : null) ||
          list.find((item) => item.is_active) ||
          list[0] ||
          null
        selectStrategy(next)
      } catch (err) {
        notify.error(
          err instanceof Error
            ? err.message
            : text(language, '加载策略失败', 'Failed to load strategies')
        )
      } finally {
        setLoading(false)
      }
    },
    [token, language, selectStrategy]
  )

  useEffect(() => {
    void loadStrategies()
  }, [loadStrategies])

  useEffect(() => {
    if (!token) return
    api
      .getCoinPools()
      .then(setPools)
      .catch(() => setPools([]))
    api
      .getModelConfigs()
      .then(setModels)
      .catch(() => setModels([]))
    api
      .getTraders(true)
      .then(setTraders)
      .catch(() => setTraders([]))
  }, [token])

  const patchForm = (patch: Partial<FreeForm>) => {
    setForm((prev) => (prev ? { ...prev, ...patch } : prev))
    setHasChanges(true)
  }

  const changeCoinSource = async (value: FreeForm['coinSource']) => {
    if (form?.coinSource === value) return
    if (preservingSource) {
      const ok = await confirmToast(
        text(
          language,
          '切换选币来源会替换原动态源及其参数，并改变交易行为。确认迁移？',
          'Switching coin sources replaces the original dynamic source and its settings, changing trading behavior. Migrate?'
        ),
        { title: text(language, '确认迁移选币源', 'Confirm source migration') }
      )
      if (!ok) return
    }
    patchForm({ coinSource: value, copyTradeFollow: undefined })
  }

  const buildConfig = () =>
    configFromForm(selected?.config || null, form!, lang, defaultConfig)

  // A test run must not depend on unsaved pool selection checks, so a pool
  // is previewed through its current coins.
  const buildTestConfig = () => {
    const config = buildConfig()
    const source = config.ai_config!.coin_source
    if (source.source_type === 'coin_pool' && selectedPool) {
      source.static_coins = selectedPool.coins
        .slice(0, LIMITS.maxCoins)
        .map((c) => c.symbol)
    }
    return config
  }

  const validate = () => {
    if (!form) return false
    if (usingPool && !form.coinPoolID) {
      notify.error(text(language, '请选择一个币池', 'Choose a coin pool'))
      return false
    }
    if (
      !usingPool &&
      !usingHorn &&
      !preservingSource &&
      form.coinSource !== 'copy' &&
      parsedCoins.length === 0
    ) {
      notify.error(
        text(
          language,
          '请至少填写一个币种，例如 BTCUSDT',
          'Add at least one coin, e.g. BTCUSDT'
        )
      )
      return false
    }
    return true
  }
  const saveStrategy = async (activateAfter = false) => {
    if (!selected || !form || !validate()) return
    setSaving(true)
    try {
      await api.updateStrategy(selected.id, {
        name: form.name.trim() || selected.name,
        description: form.description,
        config: buildConfig(),
      })
      if (activateAfter) await api.activateStrategy(selected.id)
      notify.success(
        activateAfter
          ? text(
              language,
              '策略已保存并设为使用中',
              'Strategy saved and activated'
            )
          : text(language, '策略已保存', 'Strategy saved')
      )
      await loadStrategies(selected.id)
    } catch (err) {
      notify.error(
        err instanceof Error
          ? err.message
          : text(language, '保存策略失败', 'Failed to save strategy')
      )
    } finally {
      setSaving(false)
    }
  }

  const createStrategy = async () => {
    setSaving(true)
    try {
      const newForm: FreeForm = {
        name: text(language, '新的策略', 'New strategy'),
        description: '',
        coinSource: 'static',
        coinsInput: DEFAULT_COINS,
        timeframe: '15m',
        extraTimeframes: ['1h'],
        bars: 30,
        ...SAFE_DEFAULTS,
        altLeverage: 2,
        btcEthPositionRatio: 3,
        altPositionRatio: 1.5,
        minPositionSize: 12,
        minOIValue: DEFAULT_MIN_OI_VALUE,
        customPrompt: '',
        rules: rulesDefault,
        indicators: {
          enable_ema: true,
          enable_macd: true,
          enable_rsi: true,
          enable_atr: true,
          enable_boll: false,
          enable_volume: true,
        },
        enableOI: false,
        enableFunding: false,
      }
      const created = await api.createStrategy({
        name: newForm.name,
        description: newForm.description,
        config: configFromForm(defaultConfig, newForm, lang, defaultConfig),
      })
      notify.success(text(language, '已创建新策略', 'Strategy created'))
      await loadStrategies(created?.id)
    } catch (err) {
      notify.error(
        err instanceof Error
          ? err.message
          : text(language, '创建策略失败', 'Failed to create strategy')
      )
    } finally {
      setSaving(false)
    }
  }

  const duplicateStrategy = async () => {
    if (!selected) return
    if (hasChanges) {
      const ok = await confirmToast(
        text(
          language,
          '复制的是已保存的版本，未保存的更改不会带过去。继续吗？',
          'The saved version is copied; unsaved changes are not. Continue?'
        ),
        {
          title: text(language, '复制策略', 'Duplicate'),
          okText: text(language, '复制', 'Duplicate'),
          cancelText: text(language, '取消', 'Cancel'),
        }
      )
      if (!ok) return
    }
    try {
      const copy = await api.duplicateStrategy(
        selected.id,
        `${selected.name}${text(language, '（副本）', ' (copy)')}`
      )
      notify.success(text(language, '已复制策略', 'Strategy duplicated'))
      await loadStrategies(copy?.id)
    } catch (err) {
      notify.error(
        err instanceof Error
          ? err.message
          : text(language, '复制失败', 'Failed to duplicate')
      )
    }
  }

  const deleteStrategy = async () => {
    if (!selected || selected.is_active) return
    const ok = await confirmToast(
      text(
        language,
        `确定删除策略「${selected.name}」？删除后无法恢复。`,
        `Delete "${selected.name}"? This cannot be undone.`
      ),
      {
        title: text(language, '确认删除', 'Confirm delete'),
        okText: text(language, '删除', 'Delete'),
        cancelText: text(language, '取消', 'Cancel'),
      }
    )
    if (!ok) return
    try {
      await api.deleteStrategy(selected.id)
      notify.success(text(language, '策略已删除', 'Strategy deleted'))
      await loadStrategies()
    } catch (err) {
      notify.error(
        err instanceof Error
          ? err.message
          : text(language, '删除策略失败', 'Failed to delete strategy')
      )
    }
  }

  const exportStrategy = () => {
    if (!form) return
    const blob = new Blob([strategyToJsonc(form, lang)], {
      type: 'application/json',
    })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${(form.name || 'strategy').replace(/[\\/:*?"<>|]+/g, '_')}.nofx-strategy.jsonc`
    a.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }

  const importStrategy = async (file: File) => {
    if (!form) return
    if (file.size > 256 * 1024) {
      notify.error(text(language, '文件太大', 'File is too large'))
      return
    }
    try {
      const { patch, warnings } = parseStrategyFile(
        await file.text(),
        pools,
        lang
      )
      patchForm(patch)
      for (const w of warnings) notify.warning(w)
      notify.success(
        text(
          language,
          '已载入到编辑器，检查无误后点「保存」才会生效',
          'Loaded into the editor. Review it, then click Save to apply'
        )
      )
    } catch (err) {
      notify.error(err instanceof Error ? err.message : String(err))
    }
  }

  if (loading) {
    return (
      <div className="flex min-h-[70vh] items-center justify-center">
        <Loader2 className="h-7 w-7 animate-spin text-nofx-gold" />
      </div>
    )
  }

  const paid = usesPaidSource(selected?.config)
  const usedBy = (id: string) => traders.filter((t) => t.strategy_id === id)
  const toolbarButton =
    'inline-flex items-center gap-1.5 rounded-lg border border-[rgba(25,36,52,0.14)] px-3 py-1.5 text-sm text-nofx-text hover:border-nofx-gold disabled:opacity-50'
  return (
    <DeepVoidBackground className="nofx-strategy min-h-[calc(100vh-72px)] bg-nofx-bg">
      <div className="border-b border-[rgba(25,36,52,0.14)] px-5 py-4">
        <div className="mx-auto flex max-w-7xl flex-wrap items-center justify-between gap-3">
          <div>
            <h1 className="text-xl font-semibold text-nofx-text">
              {text(language, '策略', 'Strategies')}
            </h1>
            <p className="mt-1 text-sm text-nofx-text-muted">
              {text(
                language,
                '交易币安 U 本位永续合约。只用 K 线、本地指标和免费的合约数据，不调用任何付费数据。每项设置旁都标明了作用，以及是程序强制还是由 AI 遵守。',
                'Trades Binance USDT-M perpetuals using candles, local indicators and free futures data only. Each setting says what it does and whether the program enforces it or the AI follows it.'
              )}
            </p>
          </div>
          <button
            type="button"
            onClick={createStrategy}
            disabled={saving}
            className="inline-flex items-center gap-2 rounded-lg bg-nofx-gold px-4 py-2 text-sm font-semibold text-white hover:bg-nofx-gold-highlight disabled:opacity-50"
          >
            <Plus className="h-4 w-4" />
            {text(language, '新建策略', 'New strategy')}
          </button>
        </div>
      </div>

      <div className="mx-auto grid max-w-7xl grid-cols-1 gap-4 p-5 md:grid-cols-[240px_1fr]">
        <aside className="space-y-4 md:sticky md:top-20 md:self-start">
          <div className="space-y-2">
            <div className="px-1 text-xs font-medium uppercase tracking-wide text-nofx-text-muted">
              {text(language, '我的策略', 'My strategies')}
            </div>
            {strategies.length === 0 ? (
              <div className="rounded-lg border border-[rgba(25,36,52,0.14)] p-3 text-sm text-nofx-text-muted">
                {text(
                  language,
                  '还没有策略，点右上角「新建策略」。',
                  'No strategies yet. Click "New strategy".'
                )}
              </div>
            ) : null}
            {strategies.map((strategy) => {
              const users = usedBy(strategy.id)
              const running = users.filter((t) => t.is_running).length
              return (
                <button
                  key={strategy.id}
                  type="button"
                  aria-pressed={selected?.id === strategy.id}
                  onClick={async () => {
                    if (hasChanges && selected?.id !== strategy.id) {
                      const ok = await confirmToast(
                        text(
                          language,
                          '当前策略有未保存的更改，切换后会丢失。继续吗？',
                          'Unsaved changes will be lost. Switch anyway?'
                        ),
                        {
                          title: text(language, '切换策略', 'Switch strategy'),
                          okText: text(language, '切换', 'Switch'),
                          cancelText: text(language, '取消', 'Cancel'),
                        }
                      )
                      if (!ok) return
                    }
                    selectStrategy(strategy)
                  }}
                  className={`w-full rounded-lg border px-3 py-3 text-left transition ${
                    selected?.id === strategy.id
                      ? 'border-nofx-gold bg-nofx-gold/10'
                      : 'border-[rgba(25,36,52,0.14)] bg-nofx-bg-lighter hover:border-[rgba(25,36,52,0.24)]'
                  }`}
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className="line-clamp-2 text-sm font-medium text-nofx-text">
                      {strategy.name}
                    </span>
                    {strategy.is_active ? (
                      <span className="shrink-0 rounded-sm bg-nofx-success/15 px-1.5 py-0.5 text-[10px] text-nofx-success">
                        {text(language, '使用中', 'Active')}
                      </span>
                    ) : null}
                  </div>
                  <div className="mt-1 text-xs text-nofx-text-muted">
                    {users.length
                      ? text(
                          language,
                          `${users.length} 个交易员在用 · ${running} 个运行中`,
                          `${users.length} trader(s) · ${running} running`
                        )
                      : text(
                          language,
                          '没有交易员使用',
                          'Not used by a trader'
                        )}
                  </div>
                  {usesPaidSource(strategy.config) ? (
                    <div className="mt-1 text-xs text-nofx-danger">
                      {text(
                        language,
                        '付费数据源，需要改',
                        'Paid source, needs update'
                      )}
                    </div>
                  ) : null}
                </button>
              )
            })}
          </div>
          {selected ? (
            <nav
              className="hidden space-y-1 md:block"
              aria-label={text(language, '设置分区', 'Sections')}
            >
              <div className="px-1 text-xs font-medium uppercase tracking-wide text-nofx-text-muted">
                {text(language, '设置', 'Settings')}
              </div>
              {sections.map((s) => (
                <a
                  key={s.id}
                  href={`#${s.id}`}
                  className="block rounded-md px-2 py-1 text-sm text-nofx-text-muted hover:bg-nofx-gold/10 hover:text-nofx-text"
                >
                  {text(language, s.zh, s.en)}
                </a>
              ))}
            </nav>
          ) : null}
        </aside>

        {selected && form ? (
          <main className="min-w-0 space-y-4">
            <div className="sticky top-[72px] z-10 flex flex-wrap items-center gap-2 rounded-lg border border-[rgba(25,36,52,0.14)] bg-nofx-bg-lighter/95 p-2 backdrop-blur">
              <button
                type="button"
                onClick={() => saveStrategy(false)}
                disabled={saving || (!hasChanges && !paid)}
                className="inline-flex items-center gap-1.5 rounded-lg bg-nofx-gold px-3 py-1.5 text-sm font-semibold text-white disabled:opacity-50"
              >
                {saving ? (
                  <Loader2 className="h-4 w-4 animate-spin" />
                ) : (
                  <Save className="h-4 w-4" />
                )}
                {text(language, '保存', 'Save')}
              </button>
              {!selected.is_active ? (
                <button
                  type="button"
                  onClick={() => saveStrategy(true)}
                  disabled={saving}
                  className="inline-flex items-center gap-1.5 rounded-lg bg-nofx-success px-3 py-1.5 text-sm font-semibold text-white disabled:opacity-50"
                >
                  <Check className="h-4 w-4" />
                  {text(language, '保存并设为使用中', 'Save and use')}
                </button>
              ) : null}
              {hasChanges ? (
                <span className="text-xs text-nofx-gold">
                  {text(language, '有未保存的更改', 'Unsaved changes')}
                </span>
              ) : null}
              <span className="ml-auto" />
              <button
                type="button"
                onClick={exportStrategy}
                className={toolbarButton}
                title={text(
                  language,
                  '导出带注释的 .jsonc 文件，可本地保存或交给 AI 优化',
                  'Export a commented .jsonc file to keep or give to an AI for tuning'
                )}
              >
                <Download className="h-4 w-4" />
                {text(language, '导出', 'Export')}
              </button>
              <button
                type="button"
                onClick={() => fileInput.current?.click()}
                className={toolbarButton}
                title={text(
                  language,
                  '载入 .jsonc / .json 策略文件到编辑器',
                  'Load a .jsonc / .json strategy file into the editor'
                )}
              >
                <Upload className="h-4 w-4" />
                {text(language, '导入', 'Import')}
              </button>
              <input
                ref={fileInput}
                type="file"
                accept=".jsonc,.json,.txt,application/json"
                className="hidden"
                onChange={(e) => {
                  const file = e.target.files?.[0]
                  e.target.value = ''
                  if (file) void importStrategy(file)
                }}
              />
              <button
                type="button"
                onClick={duplicateStrategy}
                className={toolbarButton}
              >
                <Copy className="h-4 w-4" />
                {text(language, '复制', 'Duplicate')}
              </button>
              {!selected.is_active ? (
                <button
                  type="button"
                  onClick={deleteStrategy}
                  className={`${toolbarButton} text-nofx-danger`}
                >
                  <Trash2 className="h-4 w-4" />
                  {text(language, '删除', 'Delete')}
                </button>
              ) : null}
            </div>

            {paid ? (
              <div
                role="alert"
                className="flex gap-2 rounded-lg border border-nofx-danger/30 bg-nofx-danger/10 p-3 text-sm text-nofx-danger"
              >
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
                <span>
                  {text(
                    language,
                    preservingSource
                      ? '此策略使用付费动态源；保存将保留该源和参数，可能继续产生费用。切换来源需明确确认。'
                      : '这个策略原来用的是付费数据源（Claw402 / Vergex / AI500）。点「保存」后会改成下面的币种，之后不再产生费用。',
                    preservingSource
                      ? 'This strategy uses a paid dynamic source. Saving preserves it and its settings; charges may continue. Switching sources requires confirmation.'
                      : 'This strategy used a paid source (Claw402 / Vergex / AI500). Saving switches it to the coins below, with no further charges.'
                  )}
                </span>
              </div>
            ) : null}
            {usedBy(selected.id).some((t) => t.is_running) ? (
              <p className="rounded-lg border border-nofx-gold/30 bg-nofx-gold/10 p-3 text-xs text-nofx-text">
                {text(
                  language,
                  '有交易员正在用这个策略运行。保存后，它们会在下一个周期自动使用新设置。',
                  'A trader is running with this strategy. After saving, it uses the new settings from its next cycle.'
                )}
              </p>
            ) : null}
            <Section
              id="basics"
              title={text(language, '基本信息与币种', 'Basics & coins')}
            >
              <div className="grid gap-4 md:grid-cols-2">
                <Field
                  language={language}
                  label={text(language, '策略名称', 'Strategy name')}
                >
                  <input
                    value={form.name}
                    maxLength={100}
                    onChange={(e) => patchForm({ name: e.target.value })}
                    className={inputClass}
                  />
                </Field>
                <Field
                  language={language}
                  label={text(language, '备注（可选）', 'Note (optional)')}
                >
                  <input
                    value={form.description}
                    maxLength={500}
                    onChange={(e) => patchForm({ description: e.target.value })}
                    className={inputClass}
                  />
                </Field>
              </div>
              <div
                className="flex flex-col gap-2 sm:flex-row"
                role="radiogroup"
                aria-label={text(language, '币种来源', 'Coin source')}
              >
                {(
                  [
                    [
                      'static',
                      text(language, '固定币种', 'Fixed coins'),
                      text(
                        language,
                        '自己填写，每轮都分析这些币',
                        'You list them; analyzed every cycle'
                      ),
                    ],
                    [
                      'pool',
                      text(language, '我的币池', 'My coin pool'),
                      text(
                        language,
                        '每轮读取「币池」页保存的币池',
                        'Read a saved pool each cycle'
                      ),
                    ],
                    [
                      'horn',
                      text(
                        language,
                        '布林喇叭口（实时）',
                        'Bollinger horn (live)'
                      ),
                      text(
                        language,
                        `每轮重新筛选，取前 ${LIMITS.maxCoins} 个`,
                        `Re-screened every cycle, top ${LIMITS.maxCoins}`
                      ),
                    ],
                    [
                      'copy',
                      text(language, '带单币种', "Lead traders' coins"),
                      text(
                        language,
                        '关注的带单员正在做的币',
                        'What your followed lead traders hold'
                      ),
                    ],
                  ] as const
                ).map(([value, label, hint]) => (
                  <button
                    key={value}
                    type="button"
                    role="radio"
                    aria-checked={(form.coinSource || 'static') === value}
                    onClick={() => void changeCoinSource(value)}
                    className={`flex-1 rounded-lg border px-3 py-2 text-left ${(form.coinSource || 'static') === value ? 'border-nofx-gold bg-nofx-gold/10' : 'border-[rgba(25,36,52,0.14)]'}`}
                  >
                    <span className="block text-sm font-semibold text-nofx-text">
                      {label}
                    </span>
                    <span className="block text-xs text-nofx-text-muted">
                      {hint}
                    </span>
                  </button>
                ))}
              </div>
              {preservingSource ? (
                <p role="status" className="text-sm text-nofx-text-muted">
                  {text(
                    language,
                    `保留原选币源 ${form.preservedCoinSource?.source_type} 及全部参数。本编辑器暂不支持编辑它；修改其他字段不会改变选币源。切换来源需要确认。`,
                    `Preserving original source ${form.preservedCoinSource?.source_type} and all its settings. This editor cannot edit it; other changes keep the source unchanged. Switching requires confirmation.`
                  )}
                </p>
              ) : form.coinSource === 'copy' ? (
                <CopyTradeSourcePanel
                  language={language}
                  settings={form.copyTrade}
                  onChange={(copyTrade) => patchForm({ copyTrade })}
                  follow={form.copyTradeFollow || 'follow'}
                  onFollow={(copyTradeFollow) => patchForm({ copyTradeFollow })}
                />
              ) : usingHorn ? (
                <>
                  <Field
                    language={language}
                    label={text(language, '布林线周期', 'Bollinger interval')}
                    hint={text(
                      language,
                      `每轮决策前，从币安涨幅前 50 的合约里重新筛选布林喇叭口（收口后开口），按得分取前 ${LIMITS.maxCoins} 个交给 AI。形态不符的币不会凑数，所以有时少于 ${LIMITS.maxCoins} 个。持仓中的币掉出名单后，AI 仍会继续管理直到平仓。`,
                      `Before each cycle, the top 50 Binance gainers are re-screened for the Bollinger horn (squeeze then expansion) and the top ${LIMITS.maxCoins} by score go to the AI. Non-matching coins are never padded in, so there can be fewer. Held coins that drop off are still managed by the AI until closed.`
                    )}
                  >
                    <div
                      className="flex gap-2"
                      role="radiogroup"
                      aria-label={text(
                        language,
                        '布林线周期',
                        'Bollinger interval'
                      )}
                    >
                      {hornIntervals.map((iv) => (
                        <button
                          key={iv}
                          type="button"
                          role="radio"
                          aria-checked={hornInterval === iv}
                          onClick={() => patchForm({ hornInterval: iv })}
                          className={`flex-1 rounded-lg border px-3 py-2 text-sm font-semibold ${hornInterval === iv ? 'border-nofx-gold bg-nofx-gold/10 text-nofx-text' : 'border-[rgba(25,36,52,0.14)] text-nofx-text-muted'}`}
                        >
                          {iv}
                        </button>
                      ))}
                    </div>
                  </Field>
                  <div
                    className="flex flex-wrap items-center gap-2"
                    aria-live="polite"
                  >
                    <span className="text-xs text-nofx-text-muted">
                      {text(language, '当前会交易：', 'Would trade now:')}
                    </span>
                    {hornError ? (
                      <span className="text-xs text-nofx-danger">
                        {text(
                          language,
                          '筛选失败，请稍后再试。',
                          'Screening failed; try again later.'
                        )}
                      </span>
                    ) : !hornBoard ? (
                      <span className="text-xs text-nofx-text-muted">
                        {text(
                          language,
                          '正在筛选币安合约…',
                          'Screening Binance futures…'
                        )}
                      </span>
                    ) : hornMatches.length === 0 ? (
                      <span className="text-xs text-nofx-text-muted">
                        {text(
                          language,
                          '现在没有符合喇叭口形态的币，本轮只会管理已有持仓。',
                          'No coin shows the horn right now; the cycle only manages open positions.'
                        )}
                      </span>
                    ) : (
                      hornMatches.map((r) => {
                        const onBoard = hornOnBoard(r, language === 'zh')
                        return (
                          <span
                            key={r.symbol}
                            className="rounded-md bg-nofx-gold/10 px-2 py-1 text-xs font-medium text-nofx-gold"
                            title={text(
                              language,
                              `得分 ${r.score.toFixed(0)}`,
                              `score ${r.score.toFixed(0)}`
                            )}
                          >
                            {r.symbol}
                            {onBoard.label ? (
                              <span
                                className={
                                  onBoard.isNew
                                    ? 'ml-1 font-semibold text-nofx-success'
                                    : 'ml-1 font-normal text-nofx-text-muted'
                                }
                              >
                                {onBoard.label}
                              </span>
                            ) : null}
                          </span>
                        )
                      })
                    )}
                    <button
                      type="button"
                      onClick={() => navigate(ROUTES.coinPool)}
                      className="text-xs text-nofx-gold underline"
                    >
                      {text(
                        language,
                        '在币池页查看完整筛选',
                        'See the full screen on the Coin pool page'
                      )}
                    </button>
                  </div>
                </>
              ) : usingPool ? (
                <>
                  <Field
                    language={language}
                    label={text(language, '币池', 'Coin pool')}
                    hint={text(
                      language,
                      `每轮从币池读取最新币种，按评分取前 ${LIMITS.maxCoins} 个。在「币池」页更新后，下一轮自动生效。`,
                      `Each cycle reads the pool fresh and trades its top ${LIMITS.maxCoins} by score. Pool updates apply from the next cycle.`
                    )}
                  >
                    <select
                      value={form.coinPoolID || ''}
                      onChange={(e) => {
                        const pool = pools.find((p) => p.id === e.target.value)
                        patchForm({
                          coinPoolID: e.target.value,
                          poolCoins: (pool?.coins || []).map((c) => c.symbol),
                        })
                      }}
                      className={inputClass}
                    >
                      <option value="">
                        {text(language, '选择一个币池…', 'Choose a coin pool…')}
                      </option>
                      {pools.map((p) => (
                        <option key={p.id} value={p.id}>
                          {p.name} · {p.coins.length}{' '}
                          {text(language, '个币', 'coins')}
                        </option>
                      ))}
                    </select>
                  </Field>
                  <div
                    className="flex flex-wrap items-center gap-2"
                    aria-live="polite"
                  >
                    {selectedPool ? (
                      selectedPool.coins
                        .slice(0, LIMITS.maxCoins)
                        .map((coin) => (
                          <span
                            key={coin.symbol}
                            className="rounded-md bg-nofx-gold/10 px-2 py-1 text-xs font-medium text-nofx-gold"
                          >
                            {coin.symbol}
                          </span>
                        ))
                    ) : form.coinPoolID ? (
                      <span className="text-xs text-nofx-danger">
                        {text(
                          language,
                          '这个币池已被删除，策略会使用保存时的币种。',
                          'This pool was deleted; the strategy uses the coins saved with it.'
                        )}
                      </span>
                    ) : null}
                    <button
                      type="button"
                      onClick={() => navigate(ROUTES.coinPool)}
                      className="text-xs text-nofx-gold underline"
                    >
                      {pools.length
                        ? text(language, '管理币池', 'Manage coin pools')
                        : text(
                            language,
                            '还没有币池，去币池页筛选',
                            'No pools yet: open the Coin pool page'
                          )}
                    </button>
                  </div>
                </>
              ) : (
                <>
                  <Field
                    language={language}
                    label={text(
                      language,
                      `交易币种（最多 ${LIMITS.maxCoins} 个）`,
                      `Coins to trade (max ${LIMITS.maxCoins})`
                    )}
                    hint={text(
                      language,
                      '币安 U 本位永续合约代码，用逗号或空格分隔。只写 BTC 会自动补成 BTCUSDT。币越多，每轮花的 token 越多。',
                      'Binance USDT-M perpetual symbols, separated by commas or spaces. "BTC" becomes BTCUSDT. More coins cost more tokens per cycle.'
                    )}
                  >
                    <input
                      value={form.coinsInput}
                      onChange={(e) =>
                        patchForm({ coinsInput: e.target.value })
                      }
                      placeholder="BTCUSDT, ETHUSDT, SOLUSDT"
                      className={inputClass}
                    />
                  </Field>
                  <div className="flex flex-wrap gap-2" aria-live="polite">
                    {parsedCoins.length === 0 ? (
                      <span className="text-xs text-nofx-danger">
                        {text(
                          language,
                          '还没有识别到有效币种',
                          'No valid coins recognized yet'
                        )}
                      </span>
                    ) : (
                      parsedCoins.map((coin) => (
                        <span
                          key={coin}
                          className="rounded-md bg-nofx-gold/10 px-2 py-1 text-xs font-medium text-nofx-gold"
                        >
                          {coin}
                        </span>
                      ))
                    )}
                  </div>
                </>
              )}
              {!preservingSource && form.coinSource !== 'copy' ? (
                <CopyTradeContextToggle
                  language={language}
                  checked={Boolean(form.copyTradeContext)}
                  onChange={(copyTradeContext) =>
                    patchForm({ copyTradeContext })
                  }
                  follow={form.copyTradeFollow || 'free'}
                  onFollow={(copyTradeFollow) => patchForm({ copyTradeFollow })}
                  minConfidence={
                    form.copyTrade?.contextMinConfidence ??
                    DEFAULT_COPY_TRADE.contextMinConfidence
                  }
                  onMinConfidence={(contextMinConfidence) =>
                    patchForm({
                      copyTrade: {
                        ...(form.copyTrade || DEFAULT_COPY_TRADE),
                        contextMinConfidence,
                      },
                    })
                  }
                />
              ) : null}
              {!preservingSource &&
              form.coinSource !== 'copy' &&
              form.copyTradeContext ? (
                <CopyTradeGates
                  language={language}
                  settings={form.copyTrade}
                  onChange={(copyTrade) => patchForm({ copyTrade })}
                  follow={form.copyTradeFollow || 'free'}
                />
              ) : null}
            </Section>

            <Section
              id="data"
              title={text(language, 'AI 看什么数据', 'What the AI sees')}
              intro={text(
                language,
                '每个周期，程序按这里的设置取行情、算好指标，再交给 AI。看的数据越多，判断依据越多，但每轮花的 token 也越多。',
                'Each cycle the program fetches market data and computes indicators with these settings, then gives them to the AI. More data gives more evidence but costs more tokens per cycle.'
              )}
            >
              <div className="grid gap-4 md:grid-cols-2">
                <Field
                  language={language}
                  label={text(language, '主 K 线周期', 'Primary timeframe')}
                  hint={text(
                    language,
                    'AI 主要依据这个周期判断。周期越短，交易越频繁、噪音越多；新手建议 15m 或 1h。',
                    'The AI decides mainly on this one. Shorter means more trades and more noise; 15m or 1h is a good start.'
                  )}
                >
                  <select
                    value={form.timeframe}
                    onChange={(e) =>
                      patchForm({
                        timeframe: e.target.value,
                        extraTimeframes: (form.extraTimeframes || []).filter(
                          (tf) => tf !== e.target.value
                        ),
                      })
                    }
                    className={inputClass}
                  >
                    {timeframeOptions.map((tf) => (
                      <option key={tf} value={tf}>
                        {tf}
                      </option>
                    ))}
                  </select>
                </Field>
                <Field
                  language={language}
                  label={text(
                    language,
                    '每个周期的 K 线根数',
                    'Candles per timeframe'
                  )}
                  hint={text(
                    language,
                    `${LIMITS.bars.min}-${LIMITS.bars.max} 根。越多看得越远，但 token 越多。`,
                    `${LIMITS.bars.min}-${LIMITS.bars.max}. More shows further back but costs more tokens.`
                  )}
                >
                  <NumberInput
                    value={form.bars}
                    range={LIMITS.bars}
                    onChange={(bars) => patchForm({ bars })}
                    suffix={text(language, '根', 'bars')}
                  />
                </Field>
              </div>
              <Field
                language={language}
                label={text(
                  language,
                  '额外参考周期（多周期共振）',
                  'Extra timeframes (multi-timeframe)'
                )}
                hint={text(
                  language,
                  `最多 ${LIMITS.maxExtraTimeframes} 个。通常选比主周期更长的，例如主周期 15m 再看 1h、4h，用大周期确认方向。`,
                  `Up to ${LIMITS.maxExtraTimeframes}. Usually longer than the primary, e.g. 15m plus 1h and 4h, to confirm direction.`
                )}
              >
                <div className="flex flex-wrap gap-2">
                  {timeframeOptions
                    .filter((tf) => tf !== form.timeframe)
                    .map((tf) => {
                      const on = (form.extraTimeframes || []).includes(tf)
                      const full =
                        !on &&
                        (form.extraTimeframes || []).length >=
                          LIMITS.maxExtraTimeframes
                      return (
                        <button
                          key={tf}
                          type="button"
                          aria-pressed={on}
                          disabled={full}
                          onClick={(e) => {
                            e.preventDefault()
                            const next = on
                              ? (form.extraTimeframes || []).filter(
                                  (x) => x !== tf
                                )
                              : [...(form.extraTimeframes || []), tf]
                            patchForm({
                              extraTimeframes: timeframeOptions.filter((x) =>
                                next.includes(x)
                              ),
                            })
                          }}
                          className={`rounded-md border px-2.5 py-1 text-xs ${on ? 'border-nofx-gold bg-nofx-gold/10 font-semibold text-nofx-gold' : 'border-[rgba(25,36,52,0.14)] text-nofx-text'} disabled:opacity-40`}
                        >
                          {tf}
                        </button>
                      )
                    })}
                </div>
              </Field>
              <fieldset>
                <legend className="mb-2 text-sm font-medium text-nofx-text">
                  {text(
                    language,
                    '技术指标（本地用 K 线计算，免费）',
                    'Indicators (computed locally from candles, free)'
                  )}
                </legend>
                <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
                  {indicatorOptions.map((item) => (
                    <label
                      key={item.key}
                      className="flex items-start gap-2 rounded-md border border-[rgba(25,36,52,0.08)] p-2 text-sm text-nofx-text"
                    >
                      <input
                        type="checkbox"
                        className="mt-0.5"
                        checked={form.indicators[item.key]}
                        onChange={(e) =>
                          patchForm({
                            indicators: {
                              ...form.indicators,
                              [item.key]: e.target.checked,
                            },
                          })
                        }
                      />
                      <span>
                        {text(language, item.zh, item.en)}
                        <span className="block text-xs text-nofx-text-muted">
                          {text(language, item.hintZh, item.hintEn)}
                        </span>
                      </span>
                    </label>
                  ))}
                </div>
              </fieldset>
              <fieldset>
                <legend className="mb-2 text-sm font-medium text-nofx-text">
                  {text(
                    language,
                    '合约数据（币安公开数据，免费）',
                    'Futures data (free Binance public data)'
                  )}
                </legend>
                <div className="grid gap-2 sm:grid-cols-2">
                  {(
                    [
                      [
                        'enableOI',
                        text(language, '持仓量（OI）', 'Open interest'),
                        text(
                          language,
                          '持仓增减反映资金进出，配合价格判断是新开仓还是平仓',
                          'Rising or falling OI shows money moving in or out'
                        ),
                      ],
                      [
                        'enableFunding',
                        text(language, '资金费率', 'Funding rate'),
                        text(
                          language,
                          '过高说明多头拥挤，过低说明空头拥挤',
                          'Very high means crowded longs; very low crowded shorts'
                        ),
                      ],
                    ] as const
                  ).map(([key, label, hint]) => (
                    <label
                      key={key}
                      className="flex items-start gap-2 rounded-md border border-[rgba(25,36,52,0.08)] p-2 text-sm text-nofx-text"
                    >
                      <input
                        type="checkbox"
                        className="mt-0.5"
                        checked={Boolean(form[key])}
                        onChange={(e) => patchForm({ [key]: e.target.checked })}
                      />
                      <span>
                        {label}
                        <span className="block text-xs text-nofx-text-muted">
                          {hint}
                        </span>
                      </span>
                    </label>
                  ))}
                </div>
              </fieldset>
            </Section>
            <Section
              id="risk"
              title={text(language, '风险控制', 'Risk control')}
              intro={text(
                language,
                '「代码强制」的限制由程序执行，AI 无法突破；「AI 参考」的写进提示词，由 AI 遵守。新手建议：杠杆 3 倍以下、保证金使用率 50% 以下，先用模拟盘试跑。',
                '"Code enforced" limits are applied by the program; "AI guided" ones are written into the prompt. Beginners: leverage 3x or less, margin usage 50% or less, try the paper account first.'
              )}
            >
              <div className="grid gap-4 md:grid-cols-2">
                <Field
                  language={language}
                  enforced="ai"
                  label={text(
                    language,
                    'BTC / ETH 最大杠杆',
                    'Max BTC / ETH leverage'
                  )}
                  hint={text(
                    language,
                    `${LIMITS.leverage.min}-${LIMITS.leverage.max} 倍。10 倍时价格反向约 10% 就会爆仓。`,
                    `${LIMITS.leverage.min}-${LIMITS.leverage.max}x. At 10x a ~10% adverse move liquidates.`
                  )}
                >
                  <NumberInput
                    value={form.leverage}
                    range={LIMITS.leverage}
                    onChange={(leverage) => patchForm({ leverage })}
                    suffix="x"
                  />
                </Field>
                <Field
                  language={language}
                  enforced="ai"
                  label={text(
                    language,
                    '山寨币最大杠杆',
                    'Max altcoin leverage'
                  )}
                  hint={text(
                    language,
                    '山寨币波动大，通常应低于 BTC/ETH。',
                    'Altcoins swing more; usually lower than BTC/ETH.'
                  )}
                >
                  <NumberInput
                    value={form.altLeverage ?? form.leverage}
                    range={LIMITS.leverage}
                    onChange={(altLeverage) => patchForm({ altLeverage })}
                    suffix="x"
                  />
                </Field>
                <Field
                  language={language}
                  enforced="code"
                  label={text(
                    language,
                    'BTC / ETH 单笔仓位上限',
                    'Max BTC / ETH position'
                  )}
                  hint={text(
                    language,
                    `仓位价值最多为账户权益的几倍，${LIMITS.positionRatio.min}-${LIMITS.positionRatio.max}。`,
                    `Max position value as a multiple of equity, ${LIMITS.positionRatio.min}-${LIMITS.positionRatio.max}.`
                  )}
                >
                  <NumberInput
                    value={form.btcEthPositionRatio ?? 5}
                    range={LIMITS.positionRatio}
                    step={0.5}
                    onChange={(btcEthPositionRatio) =>
                      patchForm({ btcEthPositionRatio })
                    }
                    suffix={text(language, '倍权益', '× equity')}
                  />
                </Field>
                <Field
                  language={language}
                  enforced="code"
                  label={text(
                    language,
                    '山寨币单笔仓位上限',
                    'Max altcoin position'
                  )}
                  hint={text(
                    language,
                    '同上，通常应小于 BTC/ETH。',
                    'Same as above; usually smaller than BTC/ETH.'
                  )}
                >
                  <NumberInput
                    value={form.altPositionRatio ?? 5}
                    range={LIMITS.positionRatio}
                    step={0.5}
                    onChange={(altPositionRatio) =>
                      patchForm({ altPositionRatio })
                    }
                    suffix={text(language, '倍权益', '× equity')}
                  />
                </Field>
                <Field
                  language={language}
                  enforced="code"
                  label={text(language, '最多同时持仓', 'Max open positions')}
                  hint={text(
                    language,
                    `${LIMITS.maxPositions.min}-${LIMITS.maxPositions.max} 个。`,
                    `${LIMITS.maxPositions.min}-${LIMITS.maxPositions.max}.`
                  )}
                >
                  <NumberInput
                    value={form.maxPositions}
                    range={LIMITS.maxPositions}
                    onChange={(maxPositions) => patchForm({ maxPositions })}
                    suffix={text(language, '个', '')}
                  />
                </Field>
                <Field
                  language={language}
                  enforced="code"
                  label={text(language, '最多使用保证金', 'Max margin usage')}
                  hint={text(
                    language,
                    '所有仓位合计最多占用账户的百分之多少。',
                    'Max share of the account used as margin across all positions.'
                  )}
                >
                  <NumberInput
                    value={Math.round(form.marginUsage * 100)}
                    range={{
                      min: LIMITS.marginUsage.min * 100,
                      max: LIMITS.marginUsage.max * 100,
                    }}
                    step={5}
                    onChange={(v) => patchForm({ marginUsage: v / 100 })}
                    suffix="%"
                  />
                </Field>
                <Field
                  language={language}
                  enforced="code"
                  label={text(
                    language,
                    '单笔最小开仓金额',
                    'Min position size'
                  )}
                  hint={text(
                    language,
                    `${LIMITS.minPositionSize.min}-${LIMITS.minPositionSize.max} USDT。太小会低于交易所的最小下单额。`,
                    `${LIMITS.minPositionSize.min}-${LIMITS.minPositionSize.max} USDT. Too small falls below the exchange minimum.`
                  )}
                >
                  <NumberInput
                    value={form.minPositionSize ?? 12}
                    range={LIMITS.minPositionSize}
                    onChange={(minPositionSize) =>
                      patchForm({ minPositionSize })
                    }
                    suffix="USDT"
                  />
                </Field>
                <Field
                  language={language}
                  enforced="code"
                  label={text(
                    language,
                    '候选币最低持仓量',
                    'Min candidate open interest'
                  )}
                  hint={text(
                    language,
                    `${LIMITS.minOIValue.min}-${LIMITS.minOIValue.max} 百万美元，默认 ${DEFAULT_MIN_OI_VALUE}。持仓量（OI）低于这个值的候选币不交给 AI 分析，0 = 不过滤。小币流动性差、滑点大，调低前请留意。`,
                    `${LIMITS.minOIValue.min}-${LIMITS.minOIValue.max} million USD, default ${DEFAULT_MIN_OI_VALUE}. Candidates whose open interest is below this are not sent to the AI; 0 = no filter. Small coins are illiquid with more slippage.`
                  )}
                >
                  <NumberInput
                    value={form.minOIValue ?? DEFAULT_MIN_OI_VALUE}
                    range={LIMITS.minOIValue}
                    step={0.5}
                    onChange={(minOIValue) => patchForm({ minOIValue })}
                    suffix="M USD"
                  />
                </Field>
                <Field
                  language={language}
                  enforced="ai"
                  label={text(language, 'AI 最低把握度', 'Min AI confidence')}
                  hint={text(
                    language,
                    `${LIMITS.confidence.min}-${LIMITS.confidence.max}。越高开仓越少、越挑剔。`,
                    `${LIMITS.confidence.min}-${LIMITS.confidence.max}. Higher means fewer, pickier trades.`
                  )}
                >
                  <NumberInput
                    value={form.minConfidence}
                    range={LIMITS.confidence}
                    onChange={(minConfidence) => patchForm({ minConfidence })}
                    suffix="%"
                  />
                </Field>
                <Field
                  language={language}
                  enforced="ai"
                  label={text(language, '最低盈亏比', 'Min reward / risk')}
                  hint={text(
                    language,
                    '止盈距离 ÷ 止损距离至少多少。2 表示赚 2 份才愿意冒 1 份的风险。',
                    'Target distance ÷ stop distance. 2 means risking 1 to make at least 2.'
                  )}
                >
                  <NumberInput
                    value={form.minRiskReward}
                    range={LIMITS.riskReward}
                    step={0.5}
                    onChange={(minRiskReward) => patchForm({ minRiskReward })}
                    suffix=": 1"
                  />
                </Field>
                <Field
                  language={language}
                  enforced="code"
                  label={text(language, '止损最小距离', 'Min stop distance')}
                  hint={text(
                    language,
                    '止损离现价至少几倍 ATR（主 K 线周期的平均波幅）。止损太近的开仓会被程序拒绝，避免被正常波动扫掉。0 表示不限制，1.5 比较常用。',
                    'Stop at least this many ATR (average range of the primary candles) from the price. Closer stops are rejected so normal swings do not stop you out. 0 = off; 1.5 is common.'
                  )}
                >
                  <NumberInput
                    value={form.minStopATR || 0}
                    range={LIMITS.minStopATR}
                    step={0.5}
                    onChange={(minStopATR) => patchForm({ minStopATR })}
                    suffix="ATR"
                  />
                </Field>
              </div>
              <RiskSummary form={form} language={language} />
              <ProfitProtectionPanel
                value={form.profitProtection}
                onChange={(profitProtection) => patchForm({ profitProtection })}
                language={language}
              />
            </Section>

            <Section
              id="rules"
              title={text(language, 'AI 交易规则', 'AI trading rules')}
              intro={text(
                language,
                '这些文字会原样写进 AI 的提示词，可以用中文。留空的部分会使用程序内置的默认规则。风险控制里的数值会自动加进提示词，这里不用重复写。',
                'This text goes into the AI prompt as written, in any language. Empty sections use the built-in defaults. Risk-control values are added automatically, so you need not repeat them here.'
              )}
            >
              <RuleEditor
                form={form}
                defaults={rulesDefault}
                language={language}
                onChange={(rules, customPrompt) =>
                  patchForm(
                    customPrompt === undefined
                      ? { rules }
                      : { rules, customPrompt }
                  )
                }
              />
            </Section>

            <Section
              id="test"
              title={text(language, '预览与试运行', 'Preview & test run')}
            >
              <TestRun
                buildConfig={buildTestConfig}
                models={models}
                language={language}
              />
            </Section>

            <div className="rounded-lg border border-[rgba(25,36,52,0.14)] p-4 text-sm text-nofx-text-muted">
              <div className="mb-2 font-medium text-nofx-text">
                {text(
                  language,
                  '下一步：让它开始交易',
                  'Next: start trading with it'
                )}
              </div>
              <ol className="list-decimal space-y-1 pl-5">
                <li>
                  {text(
                    language,
                    '在「配置 → 连接」里添加 AI 模型和币安账户（或模拟盘）',
                    'Add an AI model and a Binance (or paper) account under Configuration → Connections'
                  )}
                </li>
                <li>
                  {text(
                    language,
                    '在「配置 → Autopilot」里点「创建 Autopilot」，选择这个策略和账户',
                    'In Configuration → Autopilot, click "Create Autopilot" and pick this strategy and account'
                  )}
                </li>
              </ol>
              <button
                type="button"
                onClick={() => navigate(ROUTES.traders)}
                className="mt-3 rounded-lg border border-[rgba(25,36,52,0.24)] px-3 py-2 text-sm text-nofx-text hover:border-nofx-gold"
              >
                {text(language, '去配置页面', 'Go to Configuration')}
              </button>
            </div>
          </main>
        ) : null}
      </div>
    </DeepVoidBackground>
  )
}
