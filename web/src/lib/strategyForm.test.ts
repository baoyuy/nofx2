import { describe, expect, it } from 'vitest'
import type { Strategy } from '../types'
import { configFromForm, formFromStrategy, type FreeForm } from './strategyForm'

const form: FreeForm = {
  name: 'horn',
  description: '',
  coinSource: 'horn',
  hornInterval: '15m',
  coinsInput: 'BTCUSDT',
  timeframe: '15m',
  extraTimeframes: [],
  bars: 30,
  leverage: 3,
  maxPositions: 3,
  marginUsage: 0.5,
  minConfidence: 75,
  minRiskReward: 2,
  customPrompt: '',
  indicators: {
    enable_ema: true,
    enable_macd: true,
    enable_rsi: true,
    enable_atr: true,
    enable_boll: false,
    enable_volume: true,
  },
  enableOI: true,
  enableFunding: false,
}

describe('strategy form', () => {
  it('saves the live horn source without a fixed coin list and loads it back', () => {
    const config = configFromForm(null, form, 'zh', null)
    const source = config.ai_config!.coin_source
    expect(source.source_type).toBe('bollinger_horn')
    expect(source.horn_interval).toBe('15m')
    expect(source.static_coins).toEqual([])
    expect(source.coin_pool_id).toBe('')

    const loaded = formFromStrategy({
      id: 's',
      name: 'horn',
      config,
    } as Strategy)
    expect(loaded.coinSource).toBe('horn')
    expect(loaded.hornInterval).toBe('15m')
  })

  it('defaults an unknown horn interval to 1h', () => {
    const config = configFromForm(null, form, 'zh', null)
    config.ai_config!.coin_source.horn_interval = '2h'
    expect(
      formFromStrategy({ id: 's', name: 'x', config } as Strategy).hornInterval
    ).toBe('1h')
  })

  it.each(['hyper_all', 'hyper_main', 'hyper_rank'] as const)(
    'preserves the entire %s coin source through load/save and unrelated edits',
    (sourceType) => {
      const config = configFromForm(null, form, 'en', null)
      config.ai_config!.coin_source = {
        ...config.ai_config!.coin_source,
        source_type: sourceType,
        use_hyper_all: sourceType === 'hyper_all',
        use_hyper_main: sourceType === 'hyper_main',
        static_coins: ['HYPEUSDT'],
        excluded_coins: ['BTCUSDT'],
        hyper_rank_limit: 7,
        hyper_main_limit: 6,
        copy_trade_context: true,
      }
      const original = structuredClone(config.ai_config!.coin_source)
      const loaded = formFromStrategy({
        id: 's',
        name: 'dynamic',
        config,
      } as Strategy)
      expect(loaded.coinSource).toBe('preserved')
      const saved = configFromForm(
        config,
        { ...loaded, name: 'renamed', leverage: 4, customPrompt: 'New rules' },
        'en',
        null
      )
      expect(saved.ai_config!.coin_source).toEqual(original)
      expect(config.ai_config!.coin_source).toEqual(original)
      expect(saved.ai_config!.coin_source).not.toBe(
        config.ai_config!.coin_source
      )
      expect(saved.ai_config!.risk_control.btc_eth_max_leverage).toBe(4)
      expect(
        formFromStrategy({
          id: 's',
          name: 'dynamic',
          config: saved,
        } as Strategy).coinSource
      ).toBe('preserved')
    }
  )

  it('only migrates a preserved source when the form explicitly selects a supported source', () => {
    const config = configFromForm(null, form, 'en', null)
    config.ai_config!.coin_source.source_type = 'hyper_all'
    const loaded = formFromStrategy({
      id: 's',
      name: 'dynamic',
      config,
    } as Strategy)
    const saved = configFromForm(
      config,
      { ...loaded, coinSource: 'static', coinsInput: 'ETHUSDT' },
      'en',
      null
    )
    expect(saved.ai_config!.coin_source.source_type).toBe('static')
    expect(saved.ai_config!.coin_source.static_coins).toEqual(['ETHUSDT'])
    expect(saved.ai_config!.coin_source.use_hyper_all).toBe(false)
  })
})
