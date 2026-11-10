import type {
  Strategy,
  StrategyConfig,
} from '../../types'
import { API_BASE, httpClient } from './helpers'

export const strategyApi = {
  async getStrategies(): Promise<Strategy[]> {
    const result = await httpClient.get<{ strategies: Strategy[] }>(`${API_BASE}/strategies`)
    if (!result.success) throw new Error('Failed to fetch strategy list')
    const strategies = result.data?.strategies
    return Array.isArray(strategies) ? strategies : []
  },

  async getStrategy(strategyId: string): Promise<Strategy> {
    const result = await httpClient.get<Strategy>(`${API_BASE}/strategies/${strategyId}`)
    if (!result.success) throw new Error('Failed to fetch strategy')
    return result.data!
  },

  async getActiveStrategy(): Promise<Strategy> {
    const result = await httpClient.get<Strategy>(`${API_BASE}/strategies/active`)
    if (!result.success) throw new Error('Failed to fetch active strategy')
    return result.data!
  },

  async getDefaultStrategyConfig(): Promise<StrategyConfig> {
    const result = await httpClient.get<StrategyConfig>(`${API_BASE}/strategies/default-config`)
    if (!result.success) throw new Error('Failed to fetch default strategy config')
    return result.data!
  },

  async createStrategy(data: {
    name: string
    description: string
    config: StrategyConfig
  }): Promise<Strategy> {
    const result = await httpClient.post<Strategy>(`${API_BASE}/strategies`, data)
    if (!result.success) throw new Error('Failed to create strategy')
    return result.data!
  },

  async updateStrategy(
    strategyId: string,
    data: {
      name?: string
      description?: string
      config?: StrategyConfig
    }
  ): Promise<Strategy> {
    const result = await httpClient.put<Strategy>(`${API_BASE}/strategies/${strategyId}`, data)
    if (!result.success) throw new Error('Failed to update strategy')
    return result.data!
  },

  async deleteStrategy(strategyId: string): Promise<void> {
    const result = await httpClient.delete(`${API_BASE}/strategies/${strategyId}`)
    if (!result.success) throw new Error('Failed to delete strategy')
  },

  async activateStrategy(strategyId: string): Promise<Strategy> {
    const result = await httpClient.post<Strategy>(`${API_BASE}/strategies/${strategyId}/activate`)
    if (!result.success) throw new Error('Failed to activate strategy')
    return result.data!
  },

  async duplicateStrategy(strategyId: string, name?: string): Promise<{ id: string }> {
    const result = await httpClient.post<{ id: string }>(
      `${API_BASE}/strategies/${strategyId}/duplicate`,
      name ? { name } : {}
    )
    if (!result.success) throw new Error('Failed to duplicate strategy')
    return result.data!
  },

  // Builds the prompts from live market data without trading. With
  // run_real_ai the chosen model also analyzes once (uses its tokens).
  async testRunStrategy(data: {
    config: StrategyConfig
    ai_model_id?: string
    run_real_ai?: boolean
  }): Promise<StrategyTestRun> {
    const result = await httpClient.request<StrategyTestRun>(
      `${API_BASE}/strategies/test-run`,
      {
        method: 'POST',
        data: { prompt_variant: 'balanced', ...data },
        timeout: 180000,
      }
    )
    if (!result.success || !result.data) {
      throw new Error(result.message || 'Strategy test run failed')
    }
    return result.data
  },
}

export interface StrategyTestRun {
  system_prompt: string
  user_prompt: string
  candidate_count: number
  candidates: { symbol: string }[]
  ai_response?: string
  ai_error?: string
  note?: string
}
