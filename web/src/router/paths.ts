export type Page =
  | 'planning'
  | 'market'
  | 'coin-pool'
  | 'explosive'
  | 'copy-trade'
  | 'competition'
  | 'traders'
  | 'trader'
  | 'program'
  | 'events'
  | 'strategy'
  | 'strategy-market'
  | 'data'
  | 'faq'
  | 'login'
  | 'register'

export const ROUTES = {
  planning: '/frontier',
  home: '/',
  login: '/login',
  register: '/register',
  setup: '/setup',
  welcome: '/welcome',
  faq: '/faq',
  resetPassword: '/reset-password',
  settings: '/settings',
  data: '/data',
  competition: '/competition',
  traders: '/traders',
  dashboard: '/dashboard',
  program: '/program',
  events: '/events',
  strategy: '/strategy',
  strategyMarket: '/strategy-market',
  market: '/market',
  coinPool: '/coin-pool',
  explosive: '/explosive',
  copyTrade: '/copy-trade',
} as const

export const PAGE_PATHS: Record<Page, string> = {
  planning: ROUTES.planning,
  market: ROUTES.market,
  'coin-pool': ROUTES.coinPool,
  explosive: ROUTES.explosive,
  'copy-trade': ROUTES.copyTrade,
  competition: ROUTES.competition,
  traders: ROUTES.traders,
  trader: ROUTES.dashboard,
  program: ROUTES.program,
  events: ROUTES.events,
  strategy: ROUTES.strategy,
  'strategy-market': ROUTES.strategyMarket,
  data: ROUTES.data,
  faq: ROUTES.faq,
  login: ROUTES.login,
  register: ROUTES.register,
}

export const LEGACY_HASH_ROUTES: Record<string, string> = {
  competition: ROUTES.competition,
  traders: ROUTES.traders,
  trader: ROUTES.dashboard,
  details: ROUTES.dashboard,
  strategy: ROUTES.strategy,
  'strategy-market': ROUTES.strategyMarket,
  data: ROUTES.data,
}

export function getCurrentPageForPath(pathname: string): Page | undefined {
  switch (pathname) {
    case ROUTES.planning:
    case '/planning':
    case '/planning/':
      return 'planning'
    case ROUTES.welcome:
    case ROUTES.traders:
      return 'traders'
    case ROUTES.dashboard:
      return 'trader'
    case ROUTES.program:
      return 'program'
    case ROUTES.events:
      return 'events'
    case ROUTES.market:
      return 'market'
    case ROUTES.coinPool:
      return 'coin-pool'
    case ROUTES.copyTrade:
      return 'copy-trade'
    case ROUTES.strategy:
      return 'strategy'
    case ROUTES.strategyMarket:
      return 'strategy-market'
    case ROUTES.data:
      return 'data'
    case ROUTES.faq:
      return 'faq'
    case ROUTES.login:
      return 'login'
    case ROUTES.register:
      return 'register'
    case ROUTES.competition:
      return 'competition'
    default:
      return undefined
  }
}

export function buildDashboardPath(traderSlug?: string): string {
  if (!traderSlug) {
    return ROUTES.dashboard
  }

  return `${ROUTES.dashboard}?trader=${encodeURIComponent(traderSlug)}`
}
