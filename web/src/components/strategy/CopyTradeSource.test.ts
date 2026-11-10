import { describe, expect, it } from 'vitest'
import type { SymbolSummary } from '../../lib/api/copyTrade'
import {
  DEFAULT_COPY_TRADE,
  configFromForm,
  formFromStrategy,
} from '../../lib/strategyForm'
import type { Strategy } from '../../types'
import { pickCopyTradeCoins } from './CopyTradeSource'

const side = (traders: number) => ({
  traders,
  value: 0,
  avg_entry: 0,
  trapped: 0,
  fresh: 0,
})
const coin = (
  symbol: string,
  dir: 'long' | 'short' | 'neutral',
  score: number,
  long: number,
  short: number
) =>
  ({
    symbol,
    long: side(long),
    short: side(short),
    liquidated: 0,
    high_freq: 0,
    confidence: {
      direction: dir,
      score,
      agreement: 1,
      long_share: 1,
      count_factor: 1,
      quality: 1,
      health: 1,
      traders: long + short,
    },
  }) as SymbolSummary

const coins = [
  coin('ETHUSDT', 'short', 62, 0, 4),
  coin('SNDKUSDT', 'long', 55, 6, 0),
  coin('XRPUSDT', 'long', 70, 1, 0),
  coin('BCHUSDT', 'long', 30, 3, 0),
  coin('BZUSDT', 'neutral', 0, 1, 1),
]

describe('copy-trade source', () => {
  it('picks like the backend', () => {
    const ids = (s: Partial<typeof DEFAULT_COPY_TRADE>) =>
      pickCopyTradeCoins(coins, { ...DEFAULT_COPY_TRADE, ...s }).map(
        (c) => c.symbol
      )
    expect(ids({})).toEqual(['ETHUSDT', 'SNDKUSDT'])
    expect(ids({ direction: 'long', minTraders: 1 })).toEqual([
      'XRPUSDT',
      'SNDKUSDT',
    ])
    expect(ids({ limit: 1 })).toEqual(['ETHUSDT'])
    expect(ids({ minConfidence: 60 })).toEqual(['ETHUSDT'])
    // Fresh openers on the leading side: none in the sample coins.
    expect(ids({ minOpeners: 1 })).toEqual([])
    const withCalls = coins.map((c) =>
      c.symbol === 'SNDKUSDT' ? { ...c, long: { ...c.long, openers: 2 } } : c
    )
    expect(
      pickCopyTradeCoins(withCalls, {
        ...DEFAULT_COPY_TRADE,
        minOpeners: 2,
      }).map((c) => c.symbol)
    ).toEqual(['SNDKUSDT'])
  })

  it('round-trips through the strategy config', () => {
    const base = formFromStrategy({
      name: 'x',
      config: null,
    } as unknown as Strategy)
    const cfg = configFromForm(
      null,
      {
        ...base,
        coinSource: 'copy',
        copyTrade: {
          ...DEFAULT_COPY_TRADE,
          minConfidence: 55,
          direction: 'short',
          limit: 3,
          minTraders: 4,
          minOpeners: 2,
          minHitRate: 55,
          minSamples: 40,
          minVolumeM: 20,
          trendFilter: true,
        },
      },
      'zh',
      null
    )
    const src = cfg.ai_config!.coin_source
    expect(src).toMatchObject({
      source_type: 'copy_trade',
      copy_trade_min_confidence: 55,
      copy_trade_direction: 'short',
      copy_trade_limit: 3,
      copy_trade_min_traders: 4,
      copy_trade_context: true,
      copy_trade_min_openers: 2,
      copy_trade_min_hit_rate: 55,
      copy_trade_min_samples: 40,
      copy_trade_min_volume_millions: 20,
      copy_trade_trend_filter: true,
      static_coins: [],
    })
    const back = formFromStrategy({
      name: 'x',
      config: cfg,
    } as unknown as Strategy)
    expect(back.coinSource).toBe('copy')
    expect(back.copyTrade).toEqual({
      minConfidence: 55,
      direction: 'short',
      limit: 3,
      minTraders: 4,
      contextMinConfidence: 40,
      minOpeners: 2,
      minHitRate: 55,
      minSamples: 40,
      minVolumeM: 20,
      trendFilter: true,
    })
    // Gates turned off are sent as 0 / false: the backend merges a save
    // into the stored config, so leaving them out kept the old values.
    const off = configFromForm(
      null,
      { ...base, coinSource: 'copy', copyTrade: DEFAULT_COPY_TRADE },
      'zh',
      null
    ).ai_config!.coin_source
    expect(off.copy_trade_min_openers).toBe(0)
    expect(off.copy_trade_min_hit_rate).toBe(0)
    expect(off.copy_trade_min_volume_millions).toBe(0)
    expect(off.copy_trade_trend_filter).toBe(false)

    const staticCfg = configFromForm(
      null,
      { ...base, coinSource: 'static', copyTradeContext: true },
      'zh',
      null
    )
    expect(staticCfg.ai_config!.coin_source.source_type).toBe('static')
    expect(staticCfg.ai_config!.coin_source.copy_trade_context).toBe(true)
    expect(
      formFromStrategy({ name: 'x', config: staticCfg } as unknown as Strategy)
        .copyTradeContext
    ).toBe(true)
  })

  it('keeps the direction rule and stop distance', () => {
    const base = formFromStrategy({
      name: 'x',
      config: null,
    } as unknown as Strategy)
    // Defaults: follow for the copy source, free for context only.
    const copy = configFromForm(
      null,
      { ...base, coinSource: 'copy' },
      'zh',
      null
    )
    expect(copy.ai_config!.coin_source.copy_trade_follow).toBe('follow')
    const ctx = configFromForm(
      null,
      { ...base, coinSource: 'static', copyTradeContext: true },
      'zh',
      null
    )
    expect(ctx.ai_config!.coin_source.copy_trade_follow).toBe('free')
    const plain = configFromForm(
      null,
      { ...base, coinSource: 'static' },
      'zh',
      null
    )
    expect(plain.ai_config!.coin_source.copy_trade_follow).toBeUndefined()

    const fade = configFromForm(
      null,
      { ...base, coinSource: 'copy', copyTradeFollow: 'fade', minStopATR: 1.5 },
      'zh',
      null
    )
    expect(fade.ai_config!.risk_control.min_stop_atr).toBe(1.5)
    const back = formFromStrategy({
      name: 'x',
      config: fade,
    } as unknown as Strategy)
    expect(back.copyTradeFollow).toBe('fade')
    expect(back.minStopATR).toBe(1.5)
  })

  it('gates follow and fade on the context confidence', () => {
    const base = formFromStrategy({
      name: 'x',
      config: null,
    } as unknown as Strategy)
    const ctx = configFromForm(
      null,
      {
        ...base,
        coinSource: 'static',
        copyTradeContext: true,
        copyTradeFollow: 'follow',
        copyTrade: {
          ...DEFAULT_COPY_TRADE,
          contextMinConfidence: 55,
        },
      },
      'zh',
      null
    )
    expect(ctx.ai_config!.coin_source.copy_trade_context_min_confidence).toBe(
      55
    )
    // The copy_trade source keeps its own selector threshold.
    const copy = configFromForm(
      null,
      { ...base, coinSource: 'copy' },
      'zh',
      null
    )
    expect(
      copy.ai_config!.coin_source.copy_trade_context_min_confidence
    ).toBeUndefined()
    const back = formFromStrategy({
      name: 'x',
      config: ctx,
    } as unknown as Strategy)
    expect(back.copyTrade?.contextMinConfidence).toBe(55)
  })
})
