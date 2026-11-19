import { API_BASE, httpClient } from './helpers'

// Copy-trade monitor: Binance futures lead traders the user follows, read
// from binance.com's copy-trading pages by the backend.

export type TradeSide = 'long' | 'short'
export type TradeAction = 'open' | 'add' | 'reduce' | 'close'

export interface CopyTradeStatus {
  client: {
    paused: boolean
    paused_until?: string
    last_error?: string
    last_limited_at?: string
    last_ok_at?: string
    requests_per_min: number
    /** Masked proxy; empty = server IP. */
    proxy: string
    /** endpoints whose latest answer looked malformed: "endpoint: detail" */
    format_errors?: string[]
  }
  follows: number
  max_follows: number
  hot: number
  failing: number
  oldest_update?: string
  /** Data readiness only; the strategy must separately enable copy-trade context. */
  ai_ready?: number
  ai_total?: number
  positions_fresh?: number
  fills_fresh?: number
  profiles_fresh?: number
}

export interface Leader {
  leadPortfolioId: string
  nickname: string
  avatarUrl: string
  roi: number
  pnl: number
  mdd: number
  winRate: number
  sharpRatio: number
  aum: number
  copierPnl: number
  currentCopyCount: number
  maxCopyCount: number
  apiKeyTag: string
  tradFiTag: string
  startTime: number
  chartItems: { value: number; dateTime: number }[] | null
}

export interface LeaderPage {
  total: number
  list: Leader[]
}

export interface FollowedTrader {
  portfolio_id: string
  nickname: string
  avatar_url: string
  status: string
  position_show: boolean
  aum: number
  copy_count: number
  max_copy_count: number
  roi: number
  pnl: number
  mdd: number
  win_rate: number
  last_trade_time: number
  /** private portfolio: Binance hides its trades, so no data ever shows */
  private?: boolean
  /** the same trader's public portfolio, when private */
  public_id?: string
  /** auto = picked by auto-follow (may be dropped); manual = never dropped */
  source?: 'auto' | 'manual' | 'board'
  /** ms when auto-follow picked the trader */
  picked_at?: number
  followed_at: string
}

export interface TraderPosition {
  symbol: string
  side: TradeSide
  qty: number
  entry_price: number
  /** qty × entry price */
  entry_value: number
  /** ms; 0 = opened before monitoring started */
  opened_at: number
  /** ms of the latest open or add; 0 = not seen */
  last_add_at: number
  source: 'public' | 'inferred'
  /** Binance's for public positions, from the latest mark price otherwise */
  mark_price?: number
  leverage?: number
  margin?: number
  unrealized_pnl?: number
  /** unrealized result in %, of margin when pct_of_margin, else of entry */
  return_pct: number
  pct_of_margin: boolean
  /** a mark price was available */
  priced: boolean
  /** Current displayed loss only; historical entry does not set this flag. */
  trapped: boolean
  trapped_threshold_pct?: number
  /** Reference movement from the first observed fill, separate from current PnL. */
  historical_entry_warning?: boolean
  first_entry_return_pct?: number
  first_entry_pct_of_margin?: boolean
  first_entry_threshold_pct?: number
  first_entry?: number
  /** inferred position whose loss reached the trader's balance */
  liquidated: boolean
  /** opened or added to within 24 hours (either kind of activity) */
  fresh: boolean
  /** the position was first opened within 24 hours */
  opened_recently?: boolean
  /** an already open position was added to within 24 hours */
  added_recently?: boolean
  /** a stock or commodity contract, which a crypto-only strategy skips */
  tradfi?: boolean
}

export interface TradeEvent {
  time: number
  symbol: string
  side: TradeSide
  action: TradeAction
  qty: number
  price: number
  value: number
  realized_pnl: number
  fee: number
  position_qty: number
  untracked?: boolean
  /** a stock or commodity contract (see TraderPosition.tradfi) */
  tradfi?: boolean
}

export interface TraderSnapshot {
  trader: FollowedTrader
  positions: TraderPosition[] | null
  events: TradeEvent[] | null
  positions_source: 'public' | 'inferred'
  updated_at: string
  error?: string
  positions_updated_at?: string
  fills_updated_at?: string
  profile_updated_at?: string
  history_complete?: boolean
  positions_error?: string
  fills_error?: string
  profile_error?: string
  hot: boolean
  score: TraderScore
  /** actions in the last 24 hours */
  orders_per_day: number
  /** 48+ actions a day: bots and grid traders, weighted down in confidence */
  high_freq: boolean
  /** Holding-time style and whether they average into losses. */
  style: TraderStyle
  /** auto-follow drop rules broken (codes, see ruleLabel) */
  review?: string[]
  /** picked by auto-follow within 7 days; votes count half */
  probation?: boolean
  accuracy?: TraderAccuracy
}

export interface AutoConfig {
  enabled: boolean
  target: number
}

export interface AutoState {
  config: AutoConfig
  last_run: string
  error?: string
  added: number
  dropped: number
  trimmed: number
  checked: number
  candidates: number
  running: boolean
}

export interface AutoLog {
  id: number
  time: number
  action: 'follow' | 'drop' | 'trim' | 'skip'
  portfolio_id: string
  nickname: string
  /** rule codes, comma separated */
  reason: string
}

export interface AutoExclusion {
  portfolio_id: string
  nickname: string
  reason: string
  /** ms; 0 = for good */
  until: number
  created_at: number
}

export interface AutoFollowInfo {
  state: AutoState
  log: AutoLog[] | null
  exclusions: AutoExclusion[] | null
  auto_follows: number
  manual_follows: number
  min_target: number
  max_target: number
}

export interface BoardSyncState {
  config: { enabled: boolean }
  last_run: string
  error?: string
  scanned: number
  public: number
  private: number
  following: number
  added: number
  dropped: number
  kept: number
  running: boolean
}

export interface BoardMember {
  portfolio_id: string
  nickname: string
  avatar_url: string
  /** PUBLIC | PRIVATE | SIGNAL as the board reported it */
  portfolio_type: string
  rank: number
  followed: boolean
  observe: boolean
  missed: number
  last_seen_at: number
  last_trade_time: number
  roi30: number
  pnl30: number
  mdd30: number
  win_rate30: number
  /** JSON array of {symbol, ratio} for an observe-only private trader */
  coin_share?: string
  coin_share_at?: number
}

export interface BoardInfo {
  state: BoardSyncState
  members: BoardMember[] | null
  following: number
  observe: number
  board_size: number
  max_follows: number
}

/** One line of the simulated-following report. */
export interface PaperRow {
  table: string
  label: string
  trips: number
  traders: number
  win: number
  avg: number
  med: number
  total: number
  beat: number
  avg_excess: number
  max_open: number
  max_dd: number
  roi: number
  avg_delay_min: number
}

export interface PaperRoster {
  id: string
  started_at: number
  members: string
  note: string
  created_at: string
}

export interface PaperInfo {
  roster: PaperRoster | null
  rosters: PaperRoster[] | null
  fills: number
  rows: PaperRow[] | null
  members?: number
}

export interface CoinScore {
  bias: 'long' | 'short' | 'neutral'
  bull_score?: number
  bear_score?: number
  edge?: number
  smoothed_edge?: number
  conviction?: number
  horizon: 'short' | 'swing' | ''
  tf_alignment?: Record<string, number>
  long_case?: string[]
  short_case?: string[]
  changed?: boolean
  change_reason?: string
  market_context?: string
  /** legacy/derived for display */
  score: number
  confidence: number
  reasons: string[]
  risks: string[]
  invalidates: string
  sources?: string[]
}

export interface CoinScoreResult {
  symbol: string
  interval: string
  model_id: string
  model_name?: string
  analyzed_at: number
  price: number
  analysis?: CoinScore
  raw?: string
  parse_error?: string
}

export interface CoinScoreBrief {
  symbol: string
  bias: 'long' | 'short' | 'neutral'
  horizon: string
  bull_score: number
  bear_score: number
  edge: number
  smoothed_edge: number
  conviction: number
  score: number
  confidence: number
  analyzed_at: number
  model_id: string
}

export interface CoinScoreSettings {
  enabled: boolean
  interval_minutes: number
  min_interval: number
  running: boolean
  done: number
  total: number
  last_run: number
  last_count: number
  last_error?: string
  model: string
  coin_count: number
}

export interface CoinScoreModelInfo {
  models: { id: string; name: string; provider: string }[]
  selected: string
  default: string
}

export interface FeedItem extends TradeEvent {
  portfolio_id: string
  nickname: string
  avatar_url: string
}

/** A trader's identity and 30-day results. */
export interface TraderBrief {
  nickname: string
  avatar_url: string
  roi_30d: number
  pnl_30d: number
  win_rate_30d: number
  score: number
}

export interface SideSummary {
  traders: number
  value: number
  avg_entry: number
  /** holders deep in loss, among traders */
  trapped: number
  /** holders who opened within 24 hours, among traders */
  fresh: number
  /** holders who added to an existing position within 24 hours, among traders */
  added: number
  /** fresh holders whose call counts: not trapped, not high-frequency, not averaging down */
  openers?: number
}

/** A coin's direction among followed traders. */
export interface Confidence {
  direction: TradeSide | 'neutral'
  /** 0-100 */
  score: number
  /** |long − short| / total weight */
  agreement: number
  long_share: number
  /** penalty for few traders, 0-1 */
  count_factor: number
  /** mean trader score of the holders, 0-1 */
  quality: number
  /** leading side's health, 0-1; below 1 when it sits on losses */
  health: number
  traders: number
  /** independent voices behind the leading side, (Σw)²/Σw² */
  effective?: number
}

/** How far a lead trader's record can be trusted, 0-100, and its parts (0-1). */
export interface TraderScore {
  score: number
  quality: number
  drawdown: number
  win_rate: number
  tenure: number
  scale: number
  reliability: number
}

/** How a lead trader trades, measured from 30 days of fills. */
export interface TraderStyle {
  kind: 'scalp' | 'swing' | 'long' | 'martingale' | 'few_samples' | 'unknown'
  trades: number
  median_hold_hours: number
  adds_per_trade: number
  adverse_add_share: number
  loss_exit_share: number
}

/** How often a trader's own opens and adds moved their way within 24h. */
export interface TraderAccuracy {
  calls: number
  hits: number
  rate: number
  avg_return: number
}

export interface HitRate {
  /**
   * What the rows are split by: confidence (who holds, bands high/mid/low),
   * openers (those signals by fresh openers 0/1/2/3+), shadow (the
   * fresh-call score, same bands), calls (fresh-call signals by independent
   * callers 1/2/3+), rank (the cross-coin score, same bands). Missing on old
   * servers = confidence.
   */
  group?: 'confidence' | 'openers' | 'shadow' | 'calls' | 'rank'
  band: 'high' | 'mid' | 'low' | 'all' | '0' | '1' | '2' | '3+'
  horizon: '4h' | '24h' | '72h'
  /** called side; 'all' sums both */
  direction?: 'all' | 'long' | 'short'
  samples: number
  hits: number
  rate: number
  avg_return: number
  /** Against BTC over the same hours: calls with a benchmark, share that
   * beat it, mean excess. */
  excess_samples?: number
  excess_rate?: number
  avg_excess?: number
}

/** The fresh-call reading of a coin: who opened or added lately. */
export interface Shadow {
  direction: 'long' | 'short' | 'neutral'
  score: number
  long_calls: number
  short_calls: number
  long_exits: number
  short_exits: number
  burst: number
  entry_gap_pct: number
  flow: number
  /** independent calls behind the leading side, (Σw)²/Σw² */
  effective?: number
  /** share of the leading side's calls that opened rather than added, 0-1 */
  open_share?: number
}

/**
 * The cross-coin score: which coin is worth trading now. Fresh calls outrank
 * stale holdings, independent voices outrank account counts, and crowded, thin
 * or chased coins are discounted. Comparable across coins, unlike confidence.
 */
export interface Rank {
  direction: 'long' | 'short' | 'neutral'
  /** 0-100 */
  score: number
  /** 0-100, before the discounts */
  strength: number
  /** effective independent callers */
  independence: number
  /** 1 = room, floor 0.7 when crowded */
  crowding: number
  /** 1 = deep, 0.4 thin, 0.7 unknown */
  liquidity: number
  /** 1 = at the callers' average entry, 0.4 fully chased */
  entry: number
  /** estimated round-trip cost, % */
  cost_pct: number
  /** what the direction rests on */
  basis: 'fresh' | 'holding' | ''
  /** fresh callers on the leading side */
  callers: number
  /** accounts holding the leading side */
  holders: number
}

export interface SymbolSummary {
  symbol: string
  shadow?: Shadow
  long: SideSummary
  short: SideSummary
  confidence: Confidence
  /** traders likely liquidated here; not in long, short or confidence */
  liquidated: number
  /** high-frequency holders, weighted down */
  high_freq: number
  /** 24h traded value in USDT; 0 = unknown */
  volume_24h?: number
  /** the cross-coin score (see Rank); absent on old servers */
  rank?: Rank
}

export interface Holder extends TraderPosition, TraderBrief {
  portfolio_id: string
  history_complete?: boolean
  high_freq: boolean
  /** scalp | swing | long | martingale | few_samples | unknown */
  style?: string
  accuracy?: TraderAccuracy
}
export interface FeedQuery {
  portfolio_id?: string
  symbol?: string
  action?: TradeAction | ''
  min_value?: number
  /** '1' leaves out high-frequency traders unless portfolio_id picks one */
  hide_high_freq?: '1'
  page?: number
  page_size?: number
}

function unwrap<T>(
  result: { success: boolean; data?: T; message?: string },
  fallback: string
): T {
  if (!result.success || result.data === undefined) {
    throw new Error(result.message || fallback)
  }
  return result.data
}

const get = async <T>(path: string, params?: Record<string, unknown>) =>
  unwrap(
    await httpClient.request<T>(`${API_BASE}${path}`, {
      params,
      silent: true,
      timeout: 60000,
    }),
    'Request failed'
  )

export const copyTradeApi = {
  getCopyTradeStatus: () => get<CopyTradeStatus>('/copytrade/status'),

  getLeaderboard: (
    timeRange: string,
    sort: string,
    page: number,
    nickname = ''
  ) =>
    get<LeaderPage>('/copytrade/leaderboard', {
      time_range: timeRange,
      sort,
      page,
      nickname: nickname || undefined,
    }),

  getFollows: () =>
    get<{ follows: TraderSnapshot[]; max_follows: number }>(
      '/copytrade/follows'
    ),

  getFollow: (id: string) =>
    get<TraderSnapshot>(`/copytrade/follows/${encodeURIComponent(id)}`),

  async follow(id: string): Promise<FollowedTrader> {
    const r = await httpClient.request<FollowedTrader>(
      `${API_BASE}/copytrade/follows`,
      { method: 'POST', data: { id }, silent: true, timeout: 70000 }
    )
    return unwrap(r, 'Follow failed')
  },

  async unfollow(id: string): Promise<void> {
    const r = await httpClient.request<{ ok: boolean }>(
      `${API_BASE}/copytrade/follows/${encodeURIComponent(id)}`,
      { method: 'DELETE', silent: true }
    )
    unwrap(r, 'Unfollow failed')
  },

  getFeed: (q: FeedQuery) =>
    get<{ items: FeedItem[]; total: number; page: number; page_size: number }>(
      '/copytrade/feed',
      {
        ...q,
        action: q.action || undefined,
      }
    ),

  // onlyFresh keeps the traders refreshed in the last 30 minutes, as the AI
  // sees them, so the strategy preview matches what will be traded.
  getCopyTradeSymbols: (onlyFresh = false) =>
    get<{
      symbols: SymbolSummary[]
      fresh_traders: number
      total_traders: number
    }>(`/copytrade/symbols${onlyFresh ? '?fresh=1' : ''}`),

  getCopyTradeSymbol: (symbol: string) =>
    get<{
      symbol: string
      summary: SymbolSummary | null
      holders: Holder[] | null
      events: FeedItem[] | null
      traders: Record<string, TraderBrief>
    }>(`/copytrade/symbols/${encodeURIComponent(symbol)}`),

  getHitRate: (days = 30) =>
    get<{ days: number; rows: HitRate[] }>('/copytrade/hit-rate', { days }),

  getBoard: () => get<BoardInfo>('/copytrade/board'),

  async setBoard(enabled: boolean): Promise<BoardSyncState> {
    const r = await httpClient.request<{ state: BoardSyncState }>(
      `${API_BASE}/copytrade/board`,
      { method: 'PUT', data: { enabled }, silent: true }
    )
    return unwrap(r, 'Save failed').state
  },

  async runBoard(): Promise<void> {
    const r = await httpClient.request<{ ok: boolean }>(
      `${API_BASE}/copytrade/board/run`,
      { method: 'POST', silent: true }
    )
    unwrap(r, 'Run failed')
  },

  getCoinScore: (symbol: string) =>
    get<CoinScoreResult | { symbol: string; analysis: null }>(
      '/copytrade/coin-score',
      { symbol }
    ),

  async analyzeCoinScore(
    symbol: string,
    modelId?: string,
    lang?: string
  ): Promise<CoinScoreResult> {
    const r = await httpClient.request<CoinScoreResult>(
      `${API_BASE}/copytrade/coin-score`,
      { method: 'POST', data: { symbol, model_id: modelId, lang }, silent: true, timeout: 45000 }
    )
    return unwrap(r, 'Analysis failed')
  },

  getCoinScores: () =>
    get<{ items: CoinScoreBrief[] }>('/copytrade/coin-scores'),

  getCoinScoreSettings: () =>
    get<CoinScoreSettings>('/copytrade/coin-score/settings'),

  async setCoinScoreSettings(cfg: {
    enabled: boolean
    interval_minutes: number
  }): Promise<void> {
    const r = await httpClient.request<{ enabled: boolean; interval_minutes: number }>(
      `${API_BASE}/copytrade/coin-score/settings`,
      { method: 'PUT', data: cfg, silent: true }
    )
    unwrap(r, 'Save failed')
  },

  async runCoinScoreAll(): Promise<void> {
    const r = await httpClient.request<{ started: boolean }>(
      `${API_BASE}/copytrade/coin-score/run-all`,
      { method: 'POST', silent: true }
    )
    unwrap(r, 'Start failed')
  },

  getCoinScoreModels: () =>
    get<CoinScoreModelInfo>('/copytrade/coin-score/models'),

  async setCoinScoreModel(modelId: string): Promise<string> {
    const r = await httpClient.request<{ selected: string }>(
      `${API_BASE}/copytrade/coin-score/model`,
      { method: 'PUT', data: { model_id: modelId }, silent: true }
    )
    return unwrap(r, 'Save failed').selected
  },

  getPaper: () => get<PaperInfo>('/copytrade/paper'),

  async startPaper(ids?: string[]): Promise<PaperRoster> {
    const r = await httpClient.request<{ roster: PaperRoster }>(
      `${API_BASE}/copytrade/paper/start`,
      { method: 'POST', data: { ids: ids || [] }, silent: true }
    )
    return unwrap(r, 'Start failed').roster
  },

  getAutoFollow: () => get<AutoFollowInfo>('/copytrade/auto'),

  async setAutoFollow(cfg: AutoConfig): Promise<AutoState> {
    const r = await httpClient.request<{ state: AutoState }>(
      `${API_BASE}/copytrade/auto`,
      { method: 'PUT', data: cfg, silent: true }
    )
    return unwrap(r, 'Save failed').state
  },

  async removeAutoExclusion(id: string): Promise<void> {
    const r = await httpClient.request<{ ok: boolean }>(
      `${API_BASE}/copytrade/auto/exclusions/${encodeURIComponent(id)}`,
      { method: 'DELETE', silent: true }
    )
    unwrap(r, 'Remove failed')
  },

  getCopyTradeProxy: () =>
    get<{ proxy: string; configured: boolean }>('/copytrade/proxy'),

  async setCopyTradeProxy(
    proxy: string
  ): Promise<{ proxy: string; configured: boolean }> {
    const r = await httpClient.request<{ proxy: string; configured: boolean }>(
      `${API_BASE}/copytrade/proxy`,
      { method: 'PUT', data: { proxy }, silent: true }
    )
    return unwrap(r, 'Save failed')
  },

  async testCopyTradeProxy(proxy: string): Promise<{
    ok: boolean
    error?: string
    latency_ms: number
    traders?: number
  }> {
    const r = await httpClient.request<{
      ok: boolean
      error?: string
      latency_ms: number
      traders?: number
    }>(`${API_BASE}/copytrade/proxy/test`, {
      method: 'POST',
      data: { proxy },
      silent: true,
      timeout: 40000,
    })
    return unwrap(r, 'Test failed')
  },
}
