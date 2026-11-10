import { NoticeText } from '../components/NoticeText'
import { frontierAccounts, defaultMarket } from '../lib/frontierAccounts'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { startVisiblePolling } from '../lib/visiblePolling'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import { getAuthHeaders, handleJSONResponse } from '../lib/api/helpers'
import { type AIModel, type Exchange } from '../types'
import { ROUTES } from '../router/paths'
import { randomUUID } from '../lib/uuid'
import {
  type Plan,
  type LiveRun,
  type PlanningState,
  type Constraints,
  type Drawing,
  type Idea,
  type PlanningChart,
  type ChartContext,
  defaultLimits,
  expired,
  loadPlanningChart,
  planningAPI,
  streamPlanning,
} from '../lib/planning'
import { formatRequestError } from '../lib/requestErrors'
import { preservePlanningPlans } from '../lib/planningPresentation'
import { useLanguage } from '../contexts/LanguageContext'
import { PlanningDataPanel } from '../components/planning/PlanningDataPanel'
import { FrontierMarkets } from '../components/planning/FrontierMarkets'
import { ExecutionSchedule } from '../components/planning/ExecutionSchedule'
import {
  PlanningActivity,
  recordActivity,
  type ActivityItem,
} from '../components/planning/PlanningActivity'
import {
  PlanStructures,
  type PlanCheck,
} from '../components/planning/PlanStructures'
import { FrontierOrderBlocker } from '../components/planning/FrontierOrderBlocker'
import { PlanFunding } from '../components/planning/PlanFunding'
import { FrontierAccount } from '../components/planning/FrontierAccount'
import {
  FrontierSetup,
  type FeeWalletState,
} from '../components/planning/FrontierSetup'
import {
  accountLabel,
  preferredAccount as preferredTradingAccount,
  rememberAccount,
} from '../lib/frontierAccounts'
import { nativeMarket } from '../lib/frontierAccount'
import {
  batchPlans,
  comparablePlans,
  readBatch,
  saveBatch,
  workspaceKey,
  type PlanBatch,
  type AccountObservation,
} from '../lib/frontierWorkspace'
import './TradingPlanningPage.css'
const format = (n: string | number) =>
  Number(n).toLocaleString('en-US', { maximumFractionDigits: 8 })
const states: Record<string, string> = {
  draft: 'Awaiting approval',
  approved: 'Approved',
  running: 'Running',
  position_open: 'Position open',
  stopping: 'Stop further entries',
  needs_attention: 'Needs reconciliation',
  completed: 'Completed',
  cancelled: 'Cancelled',
  expired: 'Expired',
}
const orderStates: Record<string, string> = {
  scheduled: 'Scheduled',
  submitting: 'Submitting',
  UNKNOWN: 'Unconfirmed receipt',
  NEW: 'Open',
  PARTIALLY_FILLED: 'Partially filled',
  FILLED: 'Filled',
  CANCELED: 'Cancelled',
  REJECTED: 'Rejected',
  EXPIRED: 'Expired',
  TRIGGERED: 'Triggered · verify fills',
}
const statesZh: Record<string, string> = {
  draft: '待审批',
  approved: '已批准',
  running: '运行中',
  position_open: '持仓中',
  stopping: '停止继续入场',
  needs_attention: '需要对账',
  completed: '已完成',
  cancelled: '已取消',
  expired: '已过期',
}
const orderStatesZh: Record<string, string> = {
  scheduled: '已排期',
  submitting: '提交中',
  UNKNOWN: '回执未确认',
  NEW: '未成交',
  PARTIALLY_FILLED: '部分成交',
  FILLED: '已成交',
  CANCELED: '已取消',
  REJECTED: '已拒绝',
  EXPIRED: '已过期',
  TRIGGERED: '已触发 · 请核对成交',
}
interface ChatMessage {
  role: 'user' | 'assistant' | 'error'
  content: string
}
export function TradingPlanningPage() {
  const { language } = useLanguage()
  const isZh = language === 'zh'
  const planStates = isZh ? statesZh : states
  const orderStatesL = isZh ? orderStatesZh : orderStates
  const [observation, setObservation] = useState<AccountObservation | null>(
    null
  )
  const pendingIntent = useRef<{
    venue: string
    symbol: string
    text?: string
    compare?: boolean
    plan?: Plan
  } | null>(null)
  const [setupOpen, setSetupOpen] = useState<'ai' | 'exchange' | null>(null)
  const [walletState, setWalletState] = useState<FeeWalletState>('checking')
  const [configsLoaded, setConfigsLoaded] = useState(false)
  const [accounts, setAccounts] = useState<Exchange[]>([]),
    [models, setModels] = useState<AIModel[]>([]),
    [exchangeID, setExchangeID] = useState(''),
    [modelID, setModelID] = useState('')
  const [venue, setVenue] = useState('binance')
  // Markets on the free trend board (legacy "ai500" group, long-only).
  const [ai500Symbols, setAI500Symbols] = useState<string[]>([])
  const requestedVenue = useRef('binance')
  const marketChosen = useRef(false)
  const [symbol, setSymbol] = useState('BTCUSDT'),
    [symbolInput, setSymbolInput] = useState('BTCUSDT'),
    [limits, setLimits] = useState<Constraints>(defaultLimits)
  const marketGroup =
    venue === 'binance' && ai500Symbols.includes(symbol) ? 'ai500' : undefined
  const entryPolicy = marketGroup ? 'ai500_trend_long' : undefined
  const [data, setData] = useState<PlanningState>({
      records: {},
      accounts: {},
      runs: [],
      mode: 'live',
      now: '',
    }),
    [selected, setSelected] = useState(''),
    [compare, setCompare] = useState<string[]>([])
  const [messages, setMessages] = useState<ChatMessage[]>([]),
    [instruction, setInstruction] = useState(''),
    [busy, setBusy] = useState(false),
    [planningBusy, setPlanningBusy] = useState(false),
    [status, setStatus] = useState(''),
    [error, setError] = useState(''),
    [orderNotice, setOrderNotice] = useState(''),
    [price, setPrice] = useState('—'),
    [interval, setInterval] = useState('1h')
  const [tradeCheckError, setTradeCheckError] = useState<{
    runID: string
    message: string
  } | null>(null)
  const [reviewReplacement, setReviewReplacement] = useState<LiveRun | null>(
    null
  )
  const [trace, setTrace] = useState<ActivityItem[]>([]),
    [review, setReview] = useState<Plan | null>(null),
    [leverage, setLeverage] = useState(5),
    [extrasOpen, setExtrasOpen] = useState(false),
    [builder, setBuilder] = useState(false),
    [builderIdea, setBuilderIdea] = useState<Idea>({
      name: isZh ? '自定义计划' : 'Custom plan',
      side: 'long',
      entry: '',
      stop: '',
      target: '',
      rationale:
        isZh
          ? '用户自定义的条件价格，需通过数学校验；尚未批准，也未执行。'
          : 'User-defined conditional prices, subject to mathematical validation; neither approved nor executed.',
      execution: { kind: 'limit' },
    }),
    [levels, setLevels] = useState('')
  const [pluginWindow, setPluginWindow] = useState('7d')
  const [preferences, setPreferences] = useState(''),
    [history, setHistory] = useState<
      {
        id: string
        symbol: string
        exchange_id: string
        model_id: string
        messages: string
      }[]
    >([]),
    [tools, setTools] = useState<unknown[]>([])
  const chart = useRef<PlanningChart | null>(null),
    abort = useRef<AbortController | null>(null),
    session = useRef<string>(randomUUID()),
    chatBox = useRef<HTMLDivElement | null>(null),
    chatFollow = useRef(true),
    active = useRef(true)
  const account = accounts.find((a) => a.id === exchangeID)
  const records = useMemo(
    () =>
      Object.values(data.records)
        .filter(
          (r) =>
            data.accounts[r.plan.id] === exchangeID &&
            r.plan.snapshot.symbol === symbol &&
            r.plan.snapshot.venue === venue
        )
        .sort(
          (a, b) =>
            Date.parse(b.plan.created) - Date.parse(a.plan.created) ||
            ['market', 'limit', 'ladder', 'twap'].indexOf(
              a.plan.idea.execution.kind
            ) -
              ['market', 'limit', 'ladder', 'twap'].indexOf(
                b.plan.idea.execution.kind
              )
        ),
    [data, exchangeID, symbol, venue]
  )
  const [checks, setChecks] = useState<PlanCheck[]>([])
  const [turnPlanIDs, setTurnPlanIDs] = useState<string[]>([])
  const batchKey = workspaceKey(exchangeID, venue, symbol)
  const [batch, setBatch] = useState<{ key: string; value: PlanBatch | null }>(
    () => ({ key: batchKey, value: readBatch(batchKey) })
  )
  const currentBatch =
    batch.key === batchKey ? batch.value : readBatch(batchKey)
  function rememberBatch(value: PlanBatch, key = batchKey) {
    saveBatch(key, value)
    setBatch({ key, value })
  }
  function showSection(section: 'chart' | 'plans' | 'assistant' | 'account') {
    requestAnimationFrame(() =>
      document
        .getElementById('frontier-' + section)
        ?.scrollIntoView({ behavior: 'smooth', block: 'start' })
    )
  }
  function requestReview(plan: Plan) {
    if (!exchangeID) {
      pendingIntent.current = { venue, symbol, plan }
      setSetupOpen('exchange')
    } else void reviewWithAccount(plan)
  }
  const openSetup = useCallback((step: 'ai' | 'exchange' | null) => {
    if (!step) pendingIntent.current = null
    setSetupOpen(step)
  }, [])
  function completeSetup(step: 'ai' | 'exchange') {
    const intent = pendingIntent.current
    pendingIntent.current = null
    if (!intent) return
    if (intent.venue !== venue || intent.symbol !== symbol) {
      setOrderNotice(
        isZh
          ? '连接已保存。请选择当前市场的一个计划以继续。'
          : 'Connection saved. Choose a plan for the current market to continue.'
      )
      return
    }
    if (step === 'ai' && intent.text) void send(intent.text, intent.compare)
    if (step === 'exchange' && intent.plan && exchangeID)
      void reviewWithAccount(intent.plan)
  }
  function resetTurn() {
    setChecks([])
    setTurnPlanIDs([])
    setTrace([])
  }
  const selectedRun = data.runs.find(
    (r) => r.plan_id === selected && r.exchange_id === exchangeID
  )
  const current = useMemo(
    () =>
      data.records[selected] ||
      (selectedRun
        ? { plan: selectedRun.plan, state: selectedRun.state }
        : undefined),
    [data.records, selected, selectedRun?.plan, selectedRun?.state]
  )
  const run = selectedRun
  const clock = Date.parse(data.now) || Date.now()
  const blockingRun = data.runs.find(
    (r) =>
      r.exchange_id === exchangeID &&
      [
        'approved',
        'starting',
        'running',
        'position_open',
        'stopping',
        'needs_attention',
      ].includes(r.state) &&
      !(r.state === 'approved' && expired(r.plan, clock))
  )
  useEffect(() => {
    setOrderNotice('')
    setTradeCheckError(null)
  }, [exchangeID])
  const reload = useCallback(async () => {
    const next = await planningAPI<PlanningState>('state')
    if (active.current)
      setData((old) =>
        JSON.stringify({ ...old, now: '' }) ===
        JSON.stringify({ ...next, now: '' })
          ? { ...old, now: next.now }
          : preservePlanningPlans(old, next)
      )
  }, [])
  const refreshHistory = useCallback(async () => {
    const rows = await planningAPI<typeof history>('conversations')
    if (active.current) setHistory(rows)
  }, [])
  useEffect(() => {
    active.current = true
    Promise.all([api.getExchangeConfigs(), api.getModelConfigs()])
      .then(([ex, ai]) => {
        if (!active.current) return
        const supported = frontierAccounts(ex)
        const enabled = ai.filter((m) => m.enabled && m.has_api_key)
        setAccounts(supported)
        setModels(enabled)
        const preferred =
          supported.find(
            (a) =>
              a.id ===
              preferredTradingAccount(supported, requestedVenue.current)
          ) ||
          (!marketChosen.current &&
          supported.length === 1 &&
          supported[0].exchange_type !== requestedVenue.current
            ? supported[0]
            : undefined)
        setExchangeID(preferred?.id || '')
        if (
          preferred &&
          !marketChosen.current &&
          preferred.exchange_type !== requestedVenue.current
        ) {
          requestedVenue.current = preferred.exchange_type
          setVenue(preferred.exchange_type)
          const native = defaultMarket(preferred.exchange_type || '')
          setSymbol(native)
          setSymbolInput(native)
        }
        setModelID(enabled[0]?.id || '')
        setConfigsLoaded(true)
      })
      .catch((e) => setError(String(e.message)))
    let initialPoll = true
    const stopPolling = startVisiblePolling(async () => {
      try {
        await reload()
      } catch (e) {
        if (initialPoll && active.current) setError((e as Error).message)
      } finally {
        initialPoll = false
      }
    }, 4000)
    refreshHistory().catch(() => {})
    planningAPI<{ text: string }>('preferences')
      .then((p) => setPreferences(p.text))
      .catch(() => {})
    return () => {
      active.current = false
      stopPolling()
      abort.current?.abort()
      chart.current?.dispose()
      chart.current = null
    }
  }, [reload, refreshHistory])
  useEffect(() => {
    let cancelled = false
    let chartFailure = ''
    const reportChartFailure = (message: string) => {
      if (cancelled) return
      chartFailure = message
      setError(message)
    }
    loadPlanningChart()
      .then(() => {
        if (cancelled) return
        chart.current?.dispose()
        chart.current = new window.WorkspaceChart({
          venue,
          symbol,
          onStatus: () => {},
          onPrice: (c: { close: string }) => !cancelled && setPrice(c.close),
          onContext: (c: ChartContext) => !cancelled && setInterval(c.interval),
          onError: reportChartFailure,
          onAvailability: (available: boolean) => {
            if (cancelled) return
            if (available && chartFailure)
              setError((message) => (message === chartFailure ? '' : message))
          },
        })
        chart.current
          .setContext(venue, symbol, '60')
          .catch((e) => reportChartFailure(e.message))
      })
      .catch((e) => reportChartFailure(e.message))
    return () => {
      cancelled = true
      chart.current?.dispose()
      chart.current = null
    }
  }, [venue, symbol])
  useEffect(() => {
    if (!chart.current) return
    const list = compare
      .map((id) => data.records[id]?.plan)
      .filter(
        (p): p is Plan =>
          !!p && p.snapshot.symbol === symbol && p.snapshot.venue === venue
      )
    chart.current
      .plot(
        current?.plan || null,
        run?.state || current?.state || 'draft',
        list.length >= 2 ? list : []
      )
      .catch((e) => setError(e.message))
  }, [current, compare, data.records, symbol, venue, run?.state])
  useEffect(() => {
    const box = chatBox.current
    if (box && chatFollow.current)
      box.scrollTop = messages.length || trace.length ? box.scrollHeight : 0
  }, [messages, trace])
  async function refreshSetup(
    preferredModel?: string,
    preferredVenue?: string,
    preferredAccount?: string
  ) {
    const [ex, ai] = await Promise.all([
      api.getExchangeConfigs(),
      api.getModelConfigs(),
    ])
    if (!active.current) return
    const supported = frontierAccounts(ex)
    const enabled = ai.filter((m) => m.enabled && m.has_api_key)
    setAccounts(supported)
    setModels(enabled)
    setModelID((old) =>
      preferredModel && enabled.some((m) => m.id === preferredModel)
        ? preferredModel
        : enabled.some((m) => m.id === old)
          ? old
          : enabled[0]?.id || ''
    )
    const nextVenue = preferredVenue || venue
    const matching = supported.filter((a) => a.exchange_type === nextVenue)
    const nextAccountID = matching.some((a) => a.id === preferredAccount)
      ? preferredAccount!
      : matching.some((a) => a.id === exchangeID)
        ? exchangeID
        : preferredTradingAccount(supported, nextVenue)
    setExchangeID(nextAccountID)
    rememberAccount(nextVenue, nextAccountID)
    if (nextAccountID !== exchangeID) {
      setReview(null)
      setBuilder(false)
      setObservation(null)
      setLimits(defaultLimits)
      setError('')
      setOrderNotice('')
      setSelected('')
      setCompare([])
      resetTurn()
    }
    if (preferredVenue && preferredVenue !== venue) {
      requestedVenue.current = preferredVenue
      setVenue(preferredVenue)
      const native = defaultMarket(preferredVenue)
      setSymbol(native)
      setSymbolInput(native)
      setSelected('')
      setCompare([])
    }
    setConfigsLoaded(true)
  }
  function openMarket(next: string) {
    marketChosen.current = true
    requestedVenue.current = venue
    setPrice('—')
    setSymbol(next)
    setSymbolInput(next)
    setSelected('')
    resetTurn()
    setCompare([])
    setMessages([])
    setTrace([])
    setError('')
    setOrderNotice('')
    session.current = randomUUID()
  }
  async function choose(p: Plan) {
    setSelected(p.id)
    setCompare([])
    setError('')
    showSection('chart')
    await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()))
    await chart.current?.plot(p, data.records[p.id]?.state || 'draft', [])
    await chart.current?.focusPlan()
  }
  function changeAccount(
    id: string,
    nextVenue = accounts.find((a) => a.id === id)?.exchange_type || venue
  ) {
    marketChosen.current = true
    abort.current?.abort()
    pendingIntent.current = null
    setExchangeID(id)
    if (configsLoaded) rememberAccount(nextVenue, id)
    setLimits(defaultLimits)
    setObservation(null)
    setReview(null)
    setBuilder(false)
    setError('')
    setOrderNotice('')
    requestedVenue.current = nextVenue
    setVenue(nextVenue)
    if (nextVenue !== venue) {
      const native = defaultMarket(nextVenue)
      setSymbol(native)
      setSymbolInput(native)
      setPrice('—')
    }
    setSelected('')
    resetTurn()
    setCompare([])
    setMessages([])
    session.current = randomUUID()
  }
  function viewTrade(trade: LiveRun) {
    marketChosen.current = true
    requestedVenue.current = trade.plan.snapshot.venue
    setVenue(trade.plan.snapshot.venue)
    setExchangeID(trade.exchange_id)
    setSymbol(
      nativeMarket(trade.plan.snapshot.symbol, trade.plan.snapshot.venue)
    )
    setSymbolInput(
      nativeMarket(trade.plan.snapshot.symbol, trade.plan.snapshot.venue)
    )
    setSelected(trade.plan_id)
    setCompare([])
    resetTurn()
    setMessages([])
  }
  function manualEntry(side: 'long' | 'short') {
    setBuilderIdea({
      name: isZh
        ? `手动 ${symbol} ${side === 'long' ? '做多' : '做空'}`
        : `Manual ${symbol} ${side}`,
      rationale: isZh
        ? '用户创建的手动限价入场'
        : 'User-created manual limit entry',
      side,
      entry: price === '—' ? '' : price,
      stop: '',
      target: '',
      execution: { kind: 'limit' },
    })
    setBuilder(true)
  }
  async function send(text = instruction, compareStructures = false) {
    if (busy || !text.trim()) return
    if (
      !modelID ||
      (models.find((m) => m.id === modelID)?.provider === 'claw402' &&
        walletState !== 'ready')
    ) {
      setInstruction(text)
      pendingIntent.current = {
        venue,
        symbol,
        text,
        compare: compareStructures,
      }
      setSetupOpen('ai')
      return
    }
    const context = chart.current?.context()
    if (!context || context.venue !== venue || context.symbol !== symbol) {
      setError(
        isZh
          ? '图表仍在切换市场，请等它就绪后再试。'
          : 'The chart is still switching markets. Try again once it is ready.'
      )
      return
    }
    // A locked trade is immutable, but it must not lock the whole workspace.
    const separateDraft = Boolean(current && current.state !== 'draft')
    const revisionID =
      !compareStructures && current?.state === 'draft' ? selected : undefined
    const historyMessages = separateDraft
      ? []
      : messages.filter((m) => m.role !== 'error').slice(-16)
    if (separateDraft) {
      session.current = randomUUID()
      setSelected('')
      setCompare([])
      setMessages([])
    }
    setBusy(true)
    setPlanningBusy(true)
    const generation: PlanBatch = {
      id: randomUUID(),
      ids: [],
      created: new Date().toISOString(),
    }
    setError('')
    setOrderNotice('')
    setInstruction('')
    resetTurn()
    chatFollow.current = true
    setStatus(isZh ? '正在读取当前市场上下文…' : 'Reading current market context…')
    setMessages((m) => [
      ...(separateDraft ? [] : m),
      { role: 'user', content: text },
    ])
    const controller = new AbortController()
    abort.current = controller
    let streamStarted = false
    let waiting = false
    let preferredPlan: Plan | undefined
    try {
      await streamPlanning(
        {
          exchange_id: exchangeID,
          model_id: modelID,
          symbol,
          venue,
          interval: context.interval,
          visible_from: context.visible_from,
          visible_to: context.visible_to,
          message: text,
          market_group: marketGroup,
          plan_id: revisionID,
          execution_kind: 'auto',
          compare_structures: compareStructures,
          plugin_window: pluginWindow,
          session_id: session.current,
          turn_id: generation.id,
          history: historyMessages,
          language: isZh ? 'zh' : 'en',
          constraints: { ...limits, leverage, entry_policy: entryPolicy },
          auto_size: Boolean(exchangeID),
        },
        controller.signal,
        (event) => {
          streamStarted = true
          setTrace((items) => recordActivity(items, event))
          if (event.type === 'planning_sizing')
            setOrderNotice(String(event.data.message))
          if (event.type === 'assistant')
            setMessages((m) => [
              ...m,
              { role: 'assistant', content: String(event.data.message) },
            ])
          if (event.type === 'error') {
            const message = formatRequestError(
              String(event.data.message),
              isZh ? 'zh' : 'en'
            )
            setError(message)
          }
          if (event.type === 'status') setStatus(String(event.data.message))
          if (event.type === 'iteration')
            setStatus(
              isZh
                ? `规划第 ${event.data.round} 轮，共 ${event.data.max_rounds} 轮`
                : `Planning round ${event.data.round} of ${event.data.max_rounds}`
            )
          if (event.type === 'outcome' && Number(event.data.drafts) === 0) {
            setStatus(
              isZh
                ? '规划已完成，但没有通过校验的草案。请查看说明。'
                : 'Planning completed without a validated draft. Review the explanation.'
            )
            if (waiting && event.data.stop_reason === 'completed') {
              rememberBatch({ ...generation, waiting: true }, batchKey)
              setSelected('')
              setCompare([])
            }
          }
          const mergeChecks = (incoming: PlanCheck[]) =>
            setChecks((previous) => {
              const merged = new Map(previous.map((check) => [check.id, check]))
              incoming.forEach((check) => merged.set(check.id, check))
              return [...merged.values()]
            })
          if (event.type === 'candidate_result')
            mergeChecks([event.data as unknown as PlanCheck])
          if (event.type === 'plans') {
            waiting = event.data.waiting === true
            if (Array.isArray(event.data.checks)) {
              mergeChecks(event.data.checks as PlanCheck[])
              const deferred = new Map(
                (generation.deferred || []).map((entry) => [entry.kind, entry])
              )
              for (const check of event.data.checks as PlanCheck[]) {
                if (check.status === 'deferred' && check.reason)
                  deferred.set(check.kind, {
                    kind: check.kind,
                    reason: check.reason,
                  })
                else if (check.status === 'saved' || check.status === 'reused')
                  deferred.delete(check.kind)
              }
              generation.deferred = [...deferred.values()]
            }
            const plans = (event.data.plans as Plan[])
              ?.slice()
              .sort(
                (a, b) =>
                  ['market', 'limit', 'ladder', 'twap'].indexOf(
                    a.idea.execution.kind
                  ) -
                  ['market', 'limit', 'ladder', 'twap'].indexOf(
                    b.idea.execution.kind
                  )
              )
            if (plans?.length) {
              generation.ids = [
                ...new Set([...generation.ids, ...plans.map((p) => p.id)]),
              ]
              rememberBatch({ ...generation }, batchKey)
              setLimits(plans[0].constraints)
              setTurnPlanIDs((ids) => [
                ...new Set([...ids, ...plans.map((plan) => plan.id)]),
              ])
              const rank = (p: Plan) =>
                ['market', 'limit', 'ladder', 'twap'].indexOf(
                  p.idea.execution.kind
                )
              if (!preferredPlan || rank(plans[0]) <= rank(preferredPlan))
                preferredPlan = plans[0]
              setSelected(preferredPlan.id)

              setCompare([])
            } else if (generation.ids.length) {
              rememberBatch({ ...generation }, batchKey)
            }
            reload().catch(() => {})
          }
          if (event.type === 'drawings') {
            const drawings = event.data.drawings as Drawing[]
            chart.current
              ?.draw(drawings, 'ai', context.interval)
              .catch((e) => setError(e.message))
          }
        }
      )
    } catch (e) {
      if (!controller.signal.aborted) {
        if (!streamStarted) setMessages(separateDraft ? [] : messages)
        const message = formatRequestError(e, isZh ? 'zh' : 'en')
        setError(message)
        setTrace((items) =>
          recordActivity(items, { type: 'error', data: { message } })
        )
      }
    } finally {
      if (controller.signal.aborted)
        setTrace((items) =>
          recordActivity(items, { type: 'cancelled', data: {} })
        )
      setBusy(false)
      setPlanningBusy(false)
      setStatus(
        controller.signal.aborted
          ? isZh
            ? '规划已停止'
            : 'Planning stopped'
          : isZh
            ? '规划已完成'
            : 'Planning finished'
      )
      await reload().catch(() => {})
      await refreshHistory().catch(() => {})
    }
  }
  async function reviewWithAccount(plan: Plan) {
    if (busy) return
    setBusy(true)
    setError('')
    setOrderNotice('')
    setTradeCheckError(null)
    setReviewReplacement(null)
    let checkingPreviousTrade = false
    let replacement: LiveRun | null = null
    try {
      const existingPosition =
        observation?.accountID === exchangeID &&
        Math.abs(clock - observation.checkedAt) < 30000
          ? observation.positions.find(
              (p) =>
                nativeMarket(p.symbol, venue) ===
                nativeMarket(blockingRun?.plan.snapshot.symbol || symbol, venue)
            )
          : undefined
      if (blockingRun && existingPosition) {
        showSection('account')
        return
      }
      if (blockingRun?.state === 'approved') {
        replacement = blockingRun
      } else if (blockingRun) {
        checkingPreviousTrade = true
        const checked = await planningAPI<LiveRun>('check-status', {
          id: blockingRun.id,
        })
        await reload()
        if (checked.state === 'approved') {
          replacement = checked
        } else if (
          !['completed', 'cancelled', 'expired'].includes(checked.state)
        ) {
          throw new Error(
            isZh
              ? '上一笔交易仍在进行中。请先查看下方的持仓与订单，再下新单。'
              : 'The previous trade is still active. Review Positions & orders below before placing another.'
          )
        }
        checkingPreviousTrade = false
      }
      const sized = await planningAPI<Plan>('compile', {
        exchange_id: exchangeID,
        venue,
        symbol,
        idea: plan.idea,
        market_group: marketGroup,
        constraints: {
          ...plan.constraints,
          leverage,
          min_reward_risk: limits.min_reward_risk,
          entry_policy: plan.constraints.entry_policy || entryPolicy,
        },
        auto_size: true,
      })
      await reload()
      // A refreshed account size is a new review, never a replacement in an older comparison set.
      setSelected(sized.id)
      setCompare([])
      setLimits(sized.constraints)
      setReviewReplacement(replacement)
      setReview(sized)
    } catch (e) {
      const message = e instanceof Error ? e.message : String(e)
      if (checkingPreviousTrade && blockingRun) {
        setTradeCheckError({ runID: blockingRun.id, message })
        await reload().catch(() => {})
      } else setError(message)
    } finally {
      setBusy(false)
    }
  }
  async function approveAndExecute() {
    if (!review || busy) return
    const locked = review
    setBusy(true)
    setError('')
    setOrderNotice('')
    try {
      const approved = await planningAPI<LiveRun>('approve', {
        plan_id: locked.id,
        hash: locked.hash,
        confirmed: true,
        ...(reviewReplacement
          ? {
              replace_approval_id: reviewReplacement.id,
              replace_approval_hash: reviewReplacement.approval_hash,
            }
          : {}),
      })
      setReview(null)
      await planningAPI('execute', {
        id: approved.id,
        hash: approved.approval_hash,
      })
      await reload()
    } catch (e) {
      setReview(null)
      setError(e instanceof Error ? e.message : String(e))
      await reload().catch(() => {})
    } finally {
      setBusy(false)
    }
  }
  async function checkPreviousTrade(r: LiveRun) {
    setBusy(true)
    setError('')
    setOrderNotice('')
    setTradeCheckError(null)
    try {
      const checked = await planningAPI<LiveRun>('check-status', { id: r.id })
      await reload()
      setOrderNotice(
        checked.state === 'completed'
          ? isZh
            ? '上一笔交易已关闭。你的账户可以下新单了，准备好后请选择一个计划审核。'
            : 'Previous trade is closed. Your account is ready for a new order. Review a plan when you are ready.'
          : ''
      )
    } catch (e) {
      setTradeCheckError({
        runID: r.id,
        message: e instanceof Error ? e.message : String(e),
      })
      await reload().catch(() => {})
    } finally {
      setBusy(false)
    }
  }
  async function runAction(
    action: 'execute' | 'stop' | 'reconcile',
    r: LiveRun
  ) {
    setBusy(true)
    setError('')
    setOrderNotice('')
    try {
      await planningAPI(action, { id: r.id, hash: r.approval_hash })
      await reload()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
  async function saveBuilder() {
    setBusy(true)
    setError('')
    setReviewReplacement(blockingRun?.state === 'approved' ? blockingRun : null)
    try {
      const idea = { ...builderIdea, execution: { ...builderIdea.execution } }
      if (idea.execution.kind === 'ladder') {
        idea.execution.levels = levels
          .split('\n')
          .filter(Boolean)
          .map((line) => {
            const [p, w] = line.trim().split(/\s+/)
            return { price: p, weight: w }
          })
      }
      const p = await planningAPI<Plan>('compile', {
        exchange_id: exchangeID,
        venue,
        symbol,
        idea,
        market_group: marketGroup,
        constraints: { ...limits, leverage, entry_policy: entryPolicy },
        auto_size: Boolean(exchangeID),
      })
      setBuilder(false)
      await reload()
      setSelected(p.id)
      setCompare([])
      setReview(p)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
  function newSession() {
    abort.current?.abort()
    session.current = randomUUID()
    setMessages([])
    setSelected('')
    resetTurn()
    setCompare([])
    setTrace([])
    setError('')
    setOrderNotice('')
  }
  const outsideEntryPolicy = (plan: Plan) =>
    (entryPolicy === 'ai500_trend_long' ||
      plan.constraints.entry_policy === 'ai500_trend_long') &&
    plan.idea.side !== 'long'
  const deckPlans = batchPlans(records, currentBatch).filter(
    (plan) => !outsideEntryPolicy(plan)
  )
  const waitingForEntry = currentBatch?.waiting && !planningBusy
  const entryChecks =
    checks.length || planningBusy
      ? checks
      : (currentBatch?.deferred || []).map((entry) => ({
          id: `deferred_${entry.kind}`,
          kind: entry.kind,
          name: isZh ? '等待入场条件' : 'Wait for entry conditions',
          status: 'deferred',
          reason: entry.reason,
        }))
  const comparisonPlans = comparablePlans(deckPlans, clock)
  const archivedRecords = records.filter(
    (r) => !deckPlans.some((p) => p.id === r.plan.id)
  )
  const renderPlanCard = (r: PlanningState['records'][string]) => (
    <button
      key={r.plan.id}
      className={'planning-card ' + (selected === r.plan.id ? 'selected' : '')}
      onClick={() => choose(r.plan)}
      disabled={r.state === 'draft' && outsideEntryPolicy(r.plan)}
    >
      <span
        className={r.plan.idea.side === 'long' ? 'target-text' : 'stop-text'}
      >
        {compare.includes(r.plan.id)
          ? String.fromCharCode(65 + compare.indexOf(r.plan.id)) + ' · '
          : ''}
        {r.plan.idea.side === 'long'
          ? isZh
            ? '↗ 做多'
            : '↗ Long'
          : isZh
            ? '↘ 做空'
            : '↘ Short'}{' '}
        ·{' '}
        {r.plan.idea.execution.kind === 'market'
          ? isZh
            ? '市价'
            : 'Market'
          : r.plan.idea.execution.kind === 'ladder'
            ? isZh
              ? '价格阶梯'
              : 'Price ladder'
            : r.plan.idea.execution.kind === 'twap'
              ? 'TWAP'
              : isZh
                ? '单笔限价'
                : 'Single limit'}
      </span>
      <strong>{r.plan.idea.name}</strong>
      <span>
        {isZh ? '入场' : 'Entry'} {format(r.plan.idea.entry)}
      </span>
      <div>
        <span>
          {isZh ? '计划亏损' : 'Planned loss'} <b>{format(r.plan.risk)}</b>
        </span>
        <span>
          {isZh ? '净收益/风险' : 'Net reward/risk'}{' '}
          <b>{Number(r.plan.reward_risk).toFixed(2)} : 1</b>
        </span>
      </div>
      <small>
        {r.state === 'draft' && outsideEntryPolicy(r.plan)
          ? isZh
            ? '超出趋势榜仅做多策略范围 · 请生成新计划'
            : 'Outside trend board long-only policy · generate a new plan'
          : expired(r.plan, clock) && r.state === 'draft'
            ? isZh
              ? '已过期 · 重新规划'
              : 'Expired · replan'
            : planStates[r.state] || r.state}
      </small>
    </button>
  )
  return (
    <div className="planning-page">
      <div className="planning-heading">
        <div>
          <h1>Frontier</h1>
          <p>{isZh ? '你的市场，你的下一步。' : 'Your market. Your next move.'}</p>
        </div>
        <nav className="frontier-workflow" aria-label={isZh ? 'Frontier 如何运作' : 'How Frontier works'}>
          <span data-active={!current && !turnPlanIDs.length}>
            <b>1</b> {isZh ? '探索市场' : 'Explore the market'}
          </span>
          <span data-active={!current && turnPlanIDs.length > 0}>
            <b>2</b> {isZh ? '比较计划' : 'Compare plans'}
          </span>
          <span data-active={Boolean(current)}>
            <b>3</b> {isZh ? '审核并交易' : 'Review & trade'}
          </span>
        </nav>
        <a
          className="frontier-account-link"
          href="#frontier-account"
          onClick={(event) => {
            event.preventDefault()
            showSection('account')
          }}
        >
          {isZh ? '持仓与订单 ↓' : 'Positions & orders ↓'}
        </a>
        <Link to={ROUTES.settings}>{isZh ? '设置 ↗' : 'Settings ↗'}</Link>
      </div>
      <div className="planning-toolbar">
        <div className="frontier-toolbar-field">
          <span>{isZh ? '交易所' : 'Exchange'}</span>
          <select
            aria-label={isZh ? '交易场所' : 'Market venue'}
            value={venue}
            disabled={busy}
            onChange={(e) =>
              changeAccount(
                preferredTradingAccount(accounts, e.target.value),
                e.target.value
              )
            }
          >
            <option value="binance">{isZh ? 'Binance · USDT 永续合约' : 'Binance · USDT perpetual'}</option>
            <option value="hyperliquid">{isZh ? 'Hyperliquid · 永续合约' : 'Hyperliquid · perpetual'}</option>
            <option value="okx">{isZh ? 'OKX · USDT 永续合约' : 'OKX · USDT perpetual'}</option>
            <option value="bitget">{isZh ? 'Bitget · USDT 永续合约' : 'Bitget · USDT perpetual'}</option>
          </select>
        </div>
        <div className="frontier-toolbar-field">
          <span>{isZh ? '账户' : 'Account'}</span>
          <select
            aria-label={isZh ? '交易账户' : 'Trading account'}
            value={exchangeID}
            onChange={(e) => {
              if (e.target.value === '__add_account__') {
                setSetupOpen('exchange')
                return
              }
              changeAccount(e.target.value)
            }}
            disabled={busy}
          >
            <option value="">{isZh ? '不使用账户，先探索' : 'Explore without an account'}</option>
            {accounts
              .filter((a) => a.exchange_type === venue)
              .map((a) => (
                <option key={a.id} value={a.id}>
                  {accountLabel(a, accounts)} ·{' '}
                  {a.paper ? (isZh ? '模拟盘' : 'Paper') : a.exchange_type}
                </option>
              ))}
            <option value="__add_account__">{isZh ? '+ 添加另一个账户…' : '+ Add another account…'}</option>
          </select>
        </div>
        <div className="frontier-toolbar-field">
          <span>{isZh ? '市场' : 'Market'}</span>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              openMarket(
                symbolInput.trim().includes(':')
                  ? symbolInput.trim()
                  : symbolInput.trim().toUpperCase()
              )
            }}
          >
            <input
              aria-label={isZh ? '币种' : 'Symbol'}
              value={symbolInput}
              onChange={(e) => setSymbolInput(e.target.value)}
              disabled={busy}
            />
            <button disabled={busy}>{isZh ? '打开市场' : 'Open market'}</button>
          </form>
        </div>
        <span className="planning-live">
          <i />{' '}
          {accounts.find((a) => a.id === exchangeID)?.paper
            ? isZh
              ? '模拟盘交易 · 不动真钱'
              : 'Paper trading · No real funds'
            : isZh
              ? '实盘交易 · 由你掌控'
              : 'Live trading · You’re in control'}
        </span>
      </div>
      <FrontierSetup
        models={models}
        modelID={modelID}
        accounts={accounts}
        accountID={exchangeID}
        venue={venue}
        loaded={configsLoaded}
        open={setupOpen}
        onOpen={openSetup}
        onRefresh={refreshSetup}
        onWalletState={setWalletState}
        onComplete={completeSetup}
      />
      {
        <FrontierMarkets
          venue={venue}
          disabled={busy}
          selected={symbol}
          onSelect={openMarket}
          onAI500Markets={setAI500Symbols}
        />
      }
      {blockingRun && (
        <FrontierOrderBlocker
          run={blockingRun}
          now={clock}
          observation={observation}
          checkError={
            tradeCheckError?.runID === blockingRun.id
              ? tradeCheckError.message
              : undefined
          }
          onAccount={() => showSection('account')}
          busy={busy}
          onCheck={() => void checkPreviousTrade(blockingRun)}
          onView={() => {
            viewTrade(blockingRun)
            showSection('account')
          }}
          onRevoke={() => void runAction('stop', blockingRun)}
        />
      )}
      {orderNotice && !blockingRun && !error && (
        <p className="frontier-order-notice" role="status">
          <NoticeText text={orderNotice} language={language} />
        </p>
      )}
      {error &&
        !(
          blockingRun &&
          (error.includes('already has an approved or unresolved plan') ||
            error.includes('An earlier trade is reserving this account'))
        ) &&
        !trace.some(
          (item) => item.state === 'failed' && item.detail === error
        ) && (
          <div className="planning-error" role="alert">
            {formatRequestError(error, isZh ? 'zh' : 'en')}
            <button onClick={() => setError('')} aria-label={isZh ? '关闭' : 'Dismiss'}>
              ×
            </button>
          </div>
        )}
      <div className="planning-workspace">
        <section className="planning-chart-pane" id="frontier-chart">
          <div className="planning-market">
            <div>
              <b>{symbol}</b>
              <strong>{price === '—' ? price : format(price)}</strong>
              <small>
                <i className="frontier-live-dot" /> {isZh ? '实时市场' : 'Live market'} · {interval}
              </small>
            </div>
            <div>
              <button
                disabled={!current}
                onClick={() => chart.current?.focusPlan()}
              >
                {isZh ? '适配所选计划' : 'Fit selected plan'}
              </button>
              <button
                disabled={busy || comparisonPlans.length < 2}
                onClick={() => {
                  setCompare(comparisonPlans.slice(0, 4).map((p) => p.id))
                }}
              >
                {isZh ? '比较计划' : 'Compare plans'}
              </button>
            </div>
          </div>
          <div className="frontier-sizing">
            <label>
              {isZh ? '杠杆' : 'Leverage'}{' '}
              <select
                aria-label={isZh ? '规划杠杆' : 'Planning leverage'}
                value={leverage}
                disabled={busy}
                onChange={(e) => setLeverage(Number(e.target.value))}
              >
                {[1, 2, 3, 5, 10, 20].map((value) => (
                  <option key={value} value={value}>
                    {value}x
                  </option>
                ))}
              </select>
            </label>
            <span>
              {exchangeID
                ? isZh
                  ? `可用保证金 × ${leverage}x · 已预留执行成本`
                  : `Available margin × ${leverage}x · execution costs reserved`
                : isZh
                  ? '连接账户以自动计算仓位'
                  : 'Connect an account for automatic sizing'}
            </span>
          </div>
          <div className="planning-chart-frame">
            <div className="planning-chart" id="chart-container" />
            {current && compare.length < 2 && (
              <ExecutionSchedule plan={current.plan} />
            )}
          </div>
          <div className="planning-legend">
            {isZh ? '入场' : 'Entry'} <i className="entry" /> {isZh ? '止损' : 'Stop loss'} <i className="stop" /> {isZh ? '止盈' : 'Take profit'} <i className="target" />
            {compare.length > 1 && (
              <button onClick={() => setCompare([])}>
                {isZh ? '退出比较 · 选择一个计划聚焦' : 'Exit comparison · select one plan to focus'}
              </button>
            )}
          </div>
          <section
            className="frontier-plan-deck"
            id="frontier-plans"
            aria-label={isZh ? '入场计划' : 'Entry plans'}
          >
            <div className="frontier-deck-heading">
              <div>
                <h2>
                  {waitingForEntry
                    ? isZh
                      ? '等待入场'
                      : 'Waiting for an entry'
                    : currentBatch
                      ? isZh
                        ? '你的最新计划'
                        : 'Your latest plans'
                      : deckPlans.length
                        ? isZh
                          ? '已保存的想法'
                          : 'Saved idea'
                        : isZh
                          ? '选择你的入场'
                          : 'Choose your entry'}
                </h2>
                <p>
                  {waitingForEntry ? (
                    isZh
                      ? '尚未选择新的入场。请查看助手的说明和需要关注的条件。'
                      : 'No new entry selected. See the assistant’s explanation and conditions to watch.'
                  ) : (
                    <>
                      {currentBatch
                        ? isZh
                          ? `同批生成 · ${new Date(currentBatch.created).toLocaleString('zh-CN', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}。 `
                          : `Generated together · ${new Date(currentBatch.created).toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}. `
                        : isZh
                          ? '较旧的草案在下方的历史记录中。 '
                          : 'Older drafts are in history below. '}
                      {isZh ? '审核会用当前价格和资金更新所选计划。' : 'Review updates the selected plan with current prices and funds.'}
                    </>
                  )}
                </p>
              </div>
              <span>
                {deckPlans.length}{' '}
                {isZh ? '个计划' : deckPlans.length === 1 ? 'plan' : 'plans'}
              </span>
            </div>
            <PlanStructures
              checks={entryChecks}
              showAllTypes={Boolean(currentBatch)}
              plans={deckPlans}
              busy={planningBusy}
              selected={selected}
              onSelect={choose}
              onReview={requestReview}
              reviewDisabled={busy}
              connected={Boolean(exchangeID)}
              now={clock}
            />
            {!deckPlans.length &&
              !planningBusy &&
              !entryChecks.length &&
              !waitingForEntry && (
                <p className="frontier-deck-empty">
                  {isZh ? '在此生成计划，比较市价、限价、阶梯和 TWAP 入场。' : 'Generate plans to compare market, limit, ladder and TWAP entries here.'}
                </p>
              )}
            {archivedRecords.length > 0 && (
              <details className="planning-all-plans">
                <summary>{isZh ? '计划历史' : 'Plan history'} · {archivedRecords.length}</summary>
                <div className="planning-card-list">
                  {archivedRecords.map(renderPlanCard)}
                </div>
              </details>
            )}
          </section>
          {current && (
            <div className="planning-selected">
              <b>
                {current.state === 'draft'
                  ? isZh
                    ? '草案 · 未提交 · '
                    : 'Draft · not submitted · '
                  : isZh
                    ? '交易 · '
                    : 'Trade · '}
                {current.plan.idea.name} ·{' '}
                {current.plan.idea.side === 'long'
                  ? isZh
                    ? '做多'
                    : 'Long'
                  : isZh
                    ? '做空'
                    : 'Short'}
              </b>
              <div className="planning-price-row">
                <span>
                  {current.plan.idea.execution.kind === 'market'
                    ? isZh
                      ? '预计市价入场'
                      : 'Est. market entry'
                    : isZh
                      ? '入场'
                      : 'Entry'}{' '}
                  <strong>{format(current.plan.idea.entry)}</strong>
                </span>
                <span>
                  {isZh ? '止损' : 'Stop loss'}{' '}
                  <strong className="stop-text">
                    {format(current.plan.idea.stop)}
                  </strong>
                </span>
                <span>
                  {isZh ? '目标' : 'Target'}{' '}
                  <strong className="target-text">
                    {format(current.plan.idea.target)}
                  </strong>
                </span>
                <span>
                  {isZh ? '计划亏损' : 'Planned loss'}{' '}
                  <strong>
                    {format(current.plan.risk)}{' '}
                    {current.plan.snapshot.quote_asset}
                  </strong>
                </span>
              </div>
              <PlanFunding plan={current.plan} />
              <details>
                <summary>
                  {current.plan.orders.length || 1}{' '}
                  {isZh ? '个子订单 · 查看价格、数量和释放时间' : 'child orders · Inspect prices, quantities and release times'}
                </summary>
                <table>
                  <thead>
                    <tr>
                      <th>{isZh ? '子订单' : 'Child order'}</th>
                      <th>
                        {current?.plan.idea.execution.kind === 'market'
                          ? isZh
                            ? '执行预估价'
                            : 'Execution estimate'
                          : isZh
                            ? '限价'
                            : 'Limit price'}
                      </th>
                      <th>{isZh ? '数量' : 'Quantity'}</th>
                      <th>{isZh ? '释放时间' : 'Release time'}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {current.plan.orders.map((o) => (
                      <tr key={o.index}>
                        <td>{o.index}</td>
                        <td>{format(o.price)}</td>
                        <td>
                          {format(o.quantity)}{' '}
                          {current.plan.snapshot.base_asset}
                        </td>
                        <td>
                          {o.offset_seconds === 0
                            ? isZh
                              ? '开始时'
                              : 'At start'
                            : o.offset_seconds +
                              (isZh ? ' 秒后' : ' seconds later')}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                <p>{current.plan.idea.rationale}</p>
              </details>
            </div>
          )}
          <details className="frontier-advanced">
            <summary>{isZh ? '图表数据与高级工具' : 'Chart data & advanced tools'}</summary>
            <div>
              <details
                className="planning-tools"
                onToggle={(e) => setExtrasOpen(e.currentTarget.open)}
              >
                <summary>{isZh ? '市场数据与指标' : 'Market data and indicators'}</summary>
                {extrasOpen && (
                  <PlanningDataPanel
                    exchangeID={exchangeID}
                    symbol={symbol}
                    venue={venue}
                    onWindowChange={setPluginWindow}
                  />
                )}
              </details>
              <details className="planning-tools">
                <summary>{isZh ? '风险预算、入场订单与高级工具' : 'Risk budget, entry orders and advanced tools'}</summary>
                <div className="planning-limit-grid">
                  {(
                    Object.keys(defaultLimits) as Exclude<
                      keyof Constraints,
                      | 'leverage'
                      | 'available_margin'
                      | 'sizing_policy'
                      | 'planning_only'
                      | 'entry_policy'
                    >[]
                  )
                    .filter(
                      (key) =>
                        !exchangeID ||
                        !['equity', 'max_risk', 'max_notional'].includes(key)
                    )
                    .map((key) => (
                      <label key={key}>
                        {
                          {
                            equity: isZh ? '净值预算' : 'Equity budget',
                            max_risk: isZh ? '最大计划亏损' : 'Maximum planned loss',
                            max_notional: isZh ? '最大名义价值' : 'Maximum notional',
                            min_reward_risk: isZh
                              ? '最低净收益/风险（0 = 无最低要求）'
                              : 'Minimum net reward/risk (0 = no minimum)',
                            fee_bps: isZh ? '手续费假设 / bps' : 'Fee assumption / bps',
                            slippage_bps: isZh
                              ? '滑点假设 / bps'
                              : 'Slippage assumption / bps',
                          }[key]
                        }
                        <input
                          value={limits[key]}
                          inputMode="decimal"
                          disabled={
                            busy ||
                            (Boolean(exchangeID) &&
                              ['equity', 'max_risk', 'max_notional'].includes(
                                key
                              ))
                          }
                          onChange={(e) =>
                            setLimits({ ...limits, [key]: e.target.value })
                          }
                        />
                      </label>
                    ))}
                </div>
                <p>
                  {isZh ? '实盘仓位计算会在规划和审核前刷新。杠杆应用于当前可用保证金，并预留执行成本。数量会按交易所步进取整。市价订单需要在批准的价格范围内有足够深度。Hyperliquid 不收取 Builder 费用。跳空、保护失败和滑点可能导致亏损超过预估。' : 'Live sizing refreshes before planning and review. Leverage is applied to current available margin, with execution costs reserved. Quantities are rounded to exchange steps. Market orders require enough depth within the approved price bound. No Hyperliquid builder fee is charged. Gaps, failed protection and slippage may cause losses beyond the estimate.'}
                </p>
                <button disabled={busy} onClick={() => setBuilder(true)}>
                  {isZh ? '自定义市价 / 限价 / 阶梯 / TWAP' : 'Custom market / limit / ladder / TWAP'}
                </button>
                <label>
                  {isZh ? '讨论偏好' : 'Discussion preferences'}
                  <textarea
                    value={preferences}
                    onChange={(e) => setPreferences(e.target.value)}
                    maxLength={1600}
                  />
                </label>
                <button
                  onClick={async () => {
                    try {
                      const r = await fetch('/api/planning/preferences', {
                        method: 'PUT',
                        headers: getAuthHeaders(),
                        body: JSON.stringify({ text: preferences }),
                      })
                      await handleJSONResponse(r)
                      setStatus(
                        isZh
                          ? '偏好已保存；硬性风险约束未变'
                          : 'Preferences saved; hard risk constraints are unchanged'
                      )
                    } catch (e) {
                      setError(String(e))
                    }
                  }}
                >
                  {isZh ? '保存偏好' : 'Save preferences'}
                </button>
                <button
                  onClick={async () => {
                    try {
                      setTools(
                        await planningAPI<unknown[]>(
                          'entry-plugins?session_id=' + session.current
                        )
                      )
                    } catch (e) {
                      setError(String(e))
                    }
                  }}
                >
                  {isZh ? '查看入场计算器' : 'Inspect entry calculators'}
                </button>
                {tools.length > 0 && (
                  <pre>{JSON.stringify(tools, null, 2)}</pre>
                )}
              </details>
            </div>
          </details>
        </section>
        <aside className="planning-assistant" id="frontier-assistant">
          <div className="planning-assistant-title">
            <div>
              <span className="frontier-eyebrow">{isZh ? '你的交易副驾驶' : 'YOUR TRADING COPILOT'}</span>
              <b>{isZh ? '交易助手' : 'Trade assistant'}</b>
            </div>
            <button onClick={newSession} disabled={busy}>
              {isZh ? '新想法' : 'New idea'}
            </button>
          </div>
          <details className="frontier-model-settings">
            <summary>
              {models.find((m) => m.id === modelID)?.customModelName ||
                models.find((m) => m.id === modelID)?.name ||
                (isZh ? '选择 AI' : 'Choose AI')}{' '}
              <span>{isZh ? '更换模型' : 'Change model'}</span>
            </summary>
            <select
              aria-label={isZh ? 'NOFX AI 模型' : 'NOFX AI model'}
              value={modelID}
              onChange={(e) => setModelID(e.target.value)}
              disabled={busy}
            >
              {models.length === 0 && (
                <option>{isZh ? '请先在 NOFX 设置中启用 AI' : 'Enable AI in NOFX settings first'}</option>
              )}
              {models.map((m) => (
                <option value={m.id} key={m.id}>
                  {m.name} · {m.customModelName || m.provider}
                </option>
              ))}
            </select>
          </details>
          <div className="frontier-assistant-action">
            <button
              className="planning-primary"
              disabled={busy}
              onClick={() =>
                send(
                  instruction.trim() ||
                    (isZh
                      ? '按以下顺序比较四个计划：本市场的市价入场、单笔限价、加权阶梯和 TWAP。'
                      : 'Compare four plans in this order: market entry, single limit, weighted ladder and TWAP for this market.'),
                  true
                )
              }
            >
              {isZh ? '生成计划' : 'Generate plans'}
            </button>
          </div>
          <a
            className="frontier-plans-link"
            href="#frontier-plans"
            onClick={(event) => {
              event.preventDefault()
              showSection('plans')
            }}
          >
            {waitingForEntry
              ? isZh
                ? '查看入场条件'
                : 'View entry conditions'
              : isZh
                ? '查看入场计划'
                : 'View entry plans'}{' '}
            <span>{waitingForEntry ? '→' : `${deckPlans.length} →`}</span>
          </a>
          {busy && (
            <div className="planning-progress" role="status" aria-live="polite">
              <span className="planning-spinner" />
              {planningBusy
                ? status || (isZh ? '正在准备计划…' : 'Preparing plans…')
                : isZh
                  ? '正在验证账户和订单详情…'
                  : 'Validating account and order details…'}
              {planningBusy && (
                <button onClick={() => abort.current?.abort()}>{isZh ? '停止' : 'Stop'}</button>
              )}
            </div>
          )}
          <div
            className="planning-thread"
            ref={chatBox}
            onScroll={() => {
              const box = chatBox.current
              if (box)
                chatFollow.current =
                  box.scrollHeight - box.scrollTop - box.clientHeight < 80
            }}
          >
            <div className="planning-messages" id="frontier-chat-panel">
              {messages.length === 0 && !current && (
                <div className="frontier-welcome">
                  <span className="frontier-welcome-icon" aria-hidden="true">
                    <img src="/icons/nofx-flow.svg" alt="" />
                  </span>
                  <h2>
                    {isZh ? '你的下一步，' : 'Your next move,'}
                    <br />
                    {isZh ? '清晰呈现。' : 'made clear.'}
                  </h2>
                  <p>
                    {isZh ? '告诉我你在考虑什么。我会为你梳理入场、风险和退出，供你审核。' : 'Tell me what you’re considering. I’ll map out entries, risk and exits for you to review.'}
                  </p>
                  <div className="frontier-welcome-note">
                    <span>{isZh ? '01 探索' : '01 Explore'}</span>
                    <span>{isZh ? '02 比较' : '02 Compare'}</span>
                    <span>{isZh ? '03 审核并交易' : '03 Review & trade'}</span>
                  </div>
                  <div
                    className="frontier-starters"
                    aria-label={isZh ? '交易想法建议' : 'Trade idea suggestions'}
                  >
                    <button
                      onClick={() =>
                        setInstruction(
                          isZh
                            ? '寻找一个风险和退出都清晰的回调入场。'
                            : 'Find a pullback entry with clear risk and exits.'
                        )
                      }
                    >
                      {isZh ? '寻找回调入场' : 'Find a pullback entry'} <span>↗</span>
                    </button>
                    <button
                      onClick={() =>
                        setInstruction(
                          isZh
                            ? '比较本市场的市价、单笔限价、价格阶梯和 TWAP 入场。'
                            : 'Compare market, single-limit, price-ladder and TWAP entries for this market.'
                        )
                      }
                    >
                      {isZh ? '比较入场方式' : 'Compare ways to enter'} <span>↗</span>
                    </button>
                    <button
                      onClick={() =>
                        setInstruction(
                          isZh
                            ? '标出支撑位和阻力位，不要创建订单。'
                            : 'Mark support and resistance, without creating orders.'
                        )
                      }
                    >
                      {isZh ? '探索关键价格位' : 'Explore key price levels'} <span>↗</span>
                    </button>
                  </div>
                </div>
              )}
              {messages.length === 0 && current && (
                <p className="frontier-plan-hint">
                  {isZh ? '这个计划已显示在图表上。让我解释或修改它，准备好后审核订单。' : 'This plan is on the chart. Ask me to explain or revise it, then review the order when you are ready.'}
                </p>
              )}
              {messages
                .filter(
                  (m, i) =>
                    m.role !== 'user' ||
                    i === 0 ||
                    messages[i - 1].role !== 'user' ||
                    messages[i - 1].content !== m.content
                )
                .map((m, i) => (
                  <div key={i} className={'planning-message ' + m.role}>
                    {m.content}
                  </div>
                ))}
            </div>
            <div>
              <PlanningActivity items={trace} busy={planningBusy} />
            </div>
          </div>

          <form
            className="planning-composer"
            onSubmit={(e) => {
              e.preventDefault()
              send()
            }}
          >
            <textarea
              aria-label={isZh ? '交易想法' : 'Trade idea'}
              value={instruction}
              onChange={(e) => setInstruction(e.target.value)}
              placeholder={isZh ? '询问这个市场，或描述一笔交易…' : 'Ask about this market, or describe a trade…'}
              maxLength={8000}
              disabled={busy}
            />
            <div>
              <button
                type="button"
                disabled={busy}
                onClick={() =>
                  send(
                    isZh
                      ? '在当前图表上标出潜在的支撑位和阻力位。解释假设，先不要生成交易计划。'
                      : 'Mark potential support and resistance on the current chart. Explain assumptions without generating trade plans yet.'
                  )
                }
              >
                {isZh ? '支撑 / 阻力' : 'Support / resistance'}
              </button>
              {planningBusy ? (
                <button type="button" onClick={() => abort.current?.abort()}>
                  {isZh ? '停止规划' : 'Stop planning'}
                </button>
              ) : (
                <button type="submit" disabled={busy || !instruction.trim()}>
                  {isZh ? '发送' : 'Send'}
                </button>
              )}
            </div>
          </form>
          <div className="planning-review">
            {current ? (
              <>
                <b>{current.plan.idea.name}</b>
                <p>
                  {planStates[run?.state || current.state] || current.state} ·{' '}
                  {accounts.find((a) => a.id === data.accounts[selected])?.paper
                    ? isZh
                      ? '模拟盘交易'
                      : 'Paper trading'
                    : isZh
                      ? '实盘交易'
                      : 'Live trading'}{' '}
                  · {accounts.find((a) => a.id === data.accounts[selected])?.name}
                </p>
                {run?.error && <p className="stop-text"><NoticeText text={run.error} language={language} /></p>}
                {current.state === 'draft' ? (
                  <button
                    className="planning-primary"
                    disabled={
                      busy ||
                      (!!exchangeID && data.accounts[selected] !== exchangeID)
                    }
                    onClick={() => {
                      if (!exchangeID) requestReview(current.plan)
                      else void reviewWithAccount(current.plan)
                    }}
                  >
                    {!exchangeID
                      ? isZh
                        ? '连接账户以交易'
                        : 'Connect account to trade'
                      : expired(current.plan, clock)
                        ? isZh
                          ? '刷新并审核订单'
                          : 'Refresh & review order'
                        : isZh
                          ? '审核订单'
                          : 'Review order'}
                  </button>
                ) : run?.state === 'approved' ? (
                  <>
                    <button
                      disabled={busy}
                      onClick={() => runAction('execute', run)}
                    >
                      {isZh ? '下已批准的订单' : 'Place approved orders'}
                    </button>
                    <button
                      disabled={busy}
                      onClick={() => runAction('stop', run)}
                    >
                      {isZh ? '撤销批准' : 'Revoke approval'}
                    </button>
                  </>
                ) : (
                  run &&
                  !['completed', 'cancelled', 'expired'].includes(
                    run.state
                  ) && (
                    <button
                      disabled={busy}
                      onClick={() => runAction('stop', run)}
                    >
                      {isZh ? '停止剩余入场' : 'Stop remaining entries'}
                    </button>
                  )
                )}
                <small>
                  {isZh ? 'AI 的修改不等于批准。未知订单不会重新提交。保护失败时请立即检查实盘持仓。' : 'AI Revisions are not approval. Unknown orders are not resubmitted. Check live positions immediately if protection fails.'}
                </small>
              </>
            ) : (
              <p className="frontier-no-orders">
                <span aria-hidden="true">○</span>{' '}
                {isZh ? '计划都是草案。每一笔交易都由你批准。' : 'Plans are drafts. You approve every trade.'}
              </p>
            )}
          </div>
          <details className="planning-history">
            <summary>{isZh ? '规划对话' : 'Planning conversations'} {history.length}</summary>
            {history.map((h) => (
              <button
                key={h.id}
                disabled={busy}
                onClick={() => {
                  session.current = h.id
                  changeAccount(h.exchange_id)
                  session.current = h.id
                  setSymbol(h.symbol)
                  setSymbolInput(h.symbol)
                  setModelID(h.model_id)
                  setMessages(JSON.parse(h.messages))
                  setSelected('')
                  resetTurn()
                }}
              >
                {h.symbol} · {h.id.slice(0, 8)}
              </button>
            ))}
          </details>
        </aside>
      </div>
      <div className="frontier-portfolio" id="frontier-account">
        <FrontierAccount
          account={account}
          accounts={accounts}
          venue={venue}
          symbol={symbol}
          runs={data.runs}
          busy={busy}
          onView={viewTrade}
          onManual={manualEntry}
          onBusy={setBusy}
          onRefresh={reload}
          onObserve={setObservation}
          onCheck={checkPreviousTrade}
        />
        <details className="planning-tools">
          <summary>
            {isZh ? '历史执行' : 'Previous executions'} ·{' '}
            {
              data.runs.filter(
                (r) =>
                  r.exchange_id === exchangeID &&
                  ['completed', 'cancelled', 'expired'].includes(r.state)
              ).length
            }
          </summary>
          {data.runs
            .filter(
              (r) =>
                r.exchange_id === exchangeID &&
                ['completed', 'cancelled', 'expired'].includes(r.state)
            )
            .map((r) => (
              <div className="planning-run" key={r.id}>
                <b>
                  {r.plan.idea.name} · {planStates[r.state] || r.state}
                </b>
                {r.error && <p className="stop-text"><NoticeText text={r.error} language={language} /></p>}
                <table>
                  <thead>
                    <tr>
                      <th>{isZh ? '类型' : 'Type'}</th>
                      <th>{isZh ? '原生订单 ID' : 'Native order ID'}</th>
                      <th>{isZh ? '状态' : 'State'}</th>
                      <th>{isZh ? '已成交数量' : 'Filled quantity'}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {r.orders.map((o) => (
                      <tr key={o.client_id}>
                        <td>{o.role}</td>
                        <td>{o.order_id || (isZh ? '未确认' : 'Unconfirmed')}</td>
                        <td>{orderStatesL[o.status] || o.status}</td>
                        <td>{format(o.filled)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ))}
        </details>{' '}
      </div>
      {review && (
        <div className="planning-modal-backdrop">
          <section
            role="dialog"
            aria-modal="true"
            aria-labelledby="planning-approval-title"
            className="planning-modal"
          >
            <h2 id="planning-approval-title">
              {isZh ? '下单' : 'Place'}{' '}
              {review.idea.side === 'long'
                ? isZh
                  ? '做多'
                  : 'Long'
                : isZh
                  ? '做空'
                  : 'Short'}{' '}
              ·{' '}
              {review.snapshot.symbol}
            </h2>
            <p>
              {account?.name} · {review.snapshot.venue} ·{' '}
              {review.snapshot.symbol} ·{' '}
              {review.idea.side === 'long'
                ? isZh
                  ? '做多'
                  : 'Long'
                : isZh
                  ? '做空'
                  : 'Short'}
            </p>
            <div className="planning-price-row">
              <span>
                {review.idea.execution.kind === 'market'
                  ? isZh
                    ? '预计市价入场'
                    : 'Est. market entry'
                  : isZh
                    ? '入场'
                    : 'Entry'}{' '}
                {format(review.idea.entry)}
              </span>
              <span>{isZh ? '止损' : 'Stop loss'} {format(review.idea.stop)}</span>
              <span>{isZh ? '目标' : 'Target'} {format(review.idea.target)}</span>
            </div>
            <p>
              {isZh ? '总数量' : 'Total quantity'} {format(review.quantity)} ·{' '}
              {isZh ? '计划亏损' : 'Planned loss'}{' '}
              {format(review.risk)} {review.snapshot.quote_asset} ·{' '}
              {review.orders.length || 1}{' '}
              {isZh ? '个子订单' : 'child orders'} ·{' '}
              {review.constraints.leverage || 1}x{isZh ? ' 杠杆' : ' leverage'}
            </p>
            <PlanFunding plan={review} />
            {reviewReplacement && (
              <p role="note">
                {isZh ? (<>确认此订单将替换你先前未提交的{' '}{reviewReplacement.plan.snapshot.symbol} 批准。现有的交易所订单和持仓不会因替换而改变。</>) : (<>Confirming this order replaces your earlier unsubmitted{' '}{reviewReplacement.plan.snapshot.symbol} approval. Existing exchange orders and positions are unchanged by the replacement.</>)}
              </p>
            )}
            <p>
              {isZh ? '数量在此次审核时锁定；余额变化需要重新审核。' : 'Quantity is locked at this review; balance changes require a new review.'}
            </p>
            <details>
              <summary>{isZh ? '执行与保护详情' : 'Execution and protection details'}</summary>
              <p>
                {review.idea.execution.kind === 'market'
                  ? isZh
                    ? '市价入场会在批准后立即发送。Binance、OKX 和 Bitget 使用市价订单；Hyperliquid 使用在批准滑点范围内的 IOC 订单。实际成交可能不同，未成交的 IOC 数量将被取消。'
                    : 'Market entry is sent immediately after approval. Binance, OKX and Bitget use market orders; Hyperliquid uses an IOC order within the approved slippage bound. Actual fill may differ and any unfilled IOC quantity is canceled. '
                  : isZh
                    ? '只会提交已锁定的固定限价组合。'
                    : 'Only the locked, fixed-limit basket will be submitted. '}
                {isZh ? '阶梯入场会立即释放；TWAP 分片按计划执行。部分成交会获得匹配的只减仓保护。保护失败会停止后续入场，并需要人工审核。停止入场不会关闭已成交的持仓。实际亏损可能超过预估。' : 'Ladder entries are released immediately; TWAP slices follow the schedule. Partial fills receive matching reduce-only protection. Protection failures stop further entries and require manual review. Stopping entries does not close filled positions. Actual loss can exceed the estimate.'}
              </p>
            </details>
            <table>
              <thead>
                <tr>
                  <th>
                    {review.idea.execution.kind === 'market'
                      ? isZh
                        ? '执行预估价'
                        : 'Execution estimate'
                      : isZh
                        ? '限价'
                        : 'Limit price'}
                  </th>
                  <th>{isZh ? '数量' : 'Quantity'}</th>
                  <th>{isZh ? '释放时间' : 'Release time'}</th>
                </tr>
              </thead>
              <tbody>
                {review.orders.map((o) => (
                  <tr key={o.index}>
                    <td>{format(o.price)}</td>
                    <td>{format(o.quantity)}</td>
                    <td>{o.offset_seconds} {isZh ? '秒' : 'seconds'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            <details>
              <summary>{isZh ? '锁定的订单参考' : 'Locked order reference'}</summary>
              <code>{review.hash}</code>
            </details>
            <div>
              <button disabled={busy} onClick={() => setReview(null)}>
                {isZh ? '返回审核' : 'Back to review'}
              </button>
              <button
                className="planning-primary"
                disabled={busy || expired(review, clock)}
                onClick={approveAndExecute}
              >
                {isZh ? '确认并下实盘订单' : 'Confirm and place live order'}
              </button>
            </div>
          </section>
        </div>
      )}
      {builder && (
        <div className="planning-modal-backdrop">
          <section role="dialog" aria-modal="true" className="planning-modal">
            <h2>{isZh ? '手动入场' : 'Manual entry'} · {symbol}</h2>
            <p>
              {account?.name} · {venue} · {leverage}x · {isZh ? '根据可用保证金自动计算仓位。提交前请审核。' : 'automatically sized from free margin. Review before submitting.'}
            </p>
            <label>
              {isZh ? '杠杆' : 'Leverage'}
              <select
                aria-label={isZh ? '手动杠杆' : 'Manual leverage'}
                value={leverage}
                onChange={(e) => setLeverage(Number(e.target.value))}
              >
                {[1, 2, 3, 5, 10, 20].map((value) => (
                  <option key={value} value={value}>
                    {value}x
                  </option>
                ))}
              </select>
            </label>
            <select
              aria-label={isZh ? '自定义入场方式' : 'Custom entry method'}
              value={builderIdea.execution.kind}
              onChange={(e) =>
                setBuilderIdea({
                  ...builderIdea,
                  execution: {
                    kind: e.target.value as Idea['execution']['kind'],
                    ...(e.target.value === 'twap'
                      ? { slices: 4, interval_seconds: 30 }
                      : {}),
                  },
                })
              }
            >
              <option value="market">{isZh ? '市价' : 'Market'}</option>
              <option value="limit">{isZh ? '单笔限价' : 'Single limit'}</option>
              <option value="ladder">{isZh ? '价格阶梯' : 'Price ladder'}</option>
              <option value="twap">{isZh ? 'TWAP 固定限价' : 'TWAP fixed limit'}</option>
            </select>
            <select
              aria-label={isZh ? '自定义方向' : 'Custom direction'}
              value={builderIdea.side}
              onChange={(e) =>
                setBuilderIdea({
                  ...builderIdea,
                  side: e.target.value as Idea['side'],
                })
              }
            >
              <option value="long">{isZh ? '做多' : 'Long'}</option>
              <option value="short">{isZh ? '做空' : 'Short'}</option>
            </select>
            {builderIdea.execution.kind === 'market' && (
              <p>
                {isZh ? '创建草案时，入场使用当前市场报价。实际成交可能不同。' : 'Entry uses the current market quote when you create the draft. Actual fill may differ.'}
              </p>
            )}
            {(['entry', 'stop', 'target'] as const)
              .filter(
                (k) => k !== 'entry' || builderIdea.execution.kind !== 'market'
              )
              .map((k) => (
                <label key={k}>
                  {
                    {
                      name: isZh ? '名称' : 'Name',
                      entry: isZh ? '限价' : 'Limit price',
                      stop: isZh ? '止损' : 'Stop loss',
                      target: isZh ? '止盈' : 'Take profit',
                    }[k]
                  }
                  <input
                    value={builderIdea[k]}
                    onChange={(e) =>
                      setBuilderIdea({ ...builderIdea, [k]: e.target.value })
                    }
                  />
                </label>
              ))}
            {builderIdea.execution.kind === 'ladder' && (
              <label>
                {isZh ? '每行输入一个价格和数量权重；权重总和必须为 1' : 'Enter one price and quantity weight per line; weights must sum to 1'}
                <textarea
                  placeholder={'82000 0.3\n81500 0.3\n81000 0.4'}
                  value={levels}
                  onChange={(e) => setLevels(e.target.value)}
                />
              </label>
            )}
            {builderIdea.execution.kind === 'twap' && (
              <>
                <label>
                  {isZh ? '分片数' : 'Slices'}
                  <input
                    value={builderIdea.execution.slices}
                    onChange={(e) =>
                      setBuilderIdea({
                        ...builderIdea,
                        execution: {
                          ...builderIdea.execution,
                          slices: Number(e.target.value),
                        },
                      })
                    }
                  />
                </label>
                <label>
                  {isZh ? '间隔 / 秒' : 'Interval / seconds'}
                  <input
                    value={builderIdea.execution.interval_seconds}
                    onChange={(e) =>
                      setBuilderIdea({
                        ...builderIdea,
                        execution: {
                          ...builderIdea.execution,
                          interval_seconds: Number(e.target.value),
                        },
                      })
                    }
                  />
                </label>
              </>
            )}
            <p>{isZh ? '这只会创建一个未批准的草案，不会发送任何订单。' : 'This creates an unapproved draft only. No orders are sent.'}</p>
            <button disabled={busy} onClick={() => setBuilder(false)}>
              {isZh ? '取消' : 'Cancel'}
            </button>
            <button disabled={busy} onClick={saveBuilder}>
              {isZh ? '审核订单' : 'Review order'}
            </button>
          </section>
        </div>
      )}
    </div>
  )
}
