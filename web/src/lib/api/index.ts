import { traderApi } from './traders'
import { strategyApi } from './strategies'
import { configApi } from './config'
import { dataApi } from './data'
import { telegramApi } from './telegram'
import { walletApi } from './wallet'
import { marketApi } from './market'
import { coinPoolApi } from './coinPool'
import { copyTradeApi } from './copyTrade'
import { mirrorApi } from './mirror'

export const api = {
  ...traderApi,
  ...strategyApi,
  ...configApi,
  ...dataApi,
  ...telegramApi,
  ...walletApi,
  ...marketApi,
  ...coinPoolApi,
  ...copyTradeApi,
  ...mirrorApi,
}
