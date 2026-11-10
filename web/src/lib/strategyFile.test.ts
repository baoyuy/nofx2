import { describe, expect, it } from 'vitest'
import { parseStrategyFile, stripJsonc, strategyToJsonc } from './strategyFile'
import type { FreeForm } from './strategyForm'

const form: FreeForm = {
  name: '趋势 "测试"',
  description: 'a // not a comment',
  coinSource: 'static',
  coinsInput: 'BTCUSDT, ETHUSDT',
  timeframe: '15m',
  extraTimeframes: ['1h', '4h'],
  bars: 30,
  leverage: 3,
  altLeverage: 2,
  btcEthPositionRatio: 5,
  altPositionRatio: 2,
  maxPositions: 3,
  marginUsage: 0.5,
  minPositionSize: 12,
  minConfidence: 75,
  minRiskReward: 2,
  customPrompt: '只做多\n不在周末开仓',
  rules: {
    role_definition: '# 角色\n谨慎',
    trading_frequency: '',
    entry_standards: 'URL http://x.y/z',
    decision_process: '',
  },
  indicators: {
    enable_ema: true,
    enable_macd: true,
    enable_rsi: false,
    enable_atr: true,
    enable_boll: false,
    enable_volume: true,
  },
  enableOI: true,
  enableFunding: false,
}

describe('strategy file', () => {
  it('round-trips every editor value through a commented file', () => {
    const text = strategyToJsonc(form, 'zh')
    expect(text).toContain('// ')
    expect(text).toContain('代码强制')
    const { patch, warnings } = parseStrategyFile(text, [], 'zh')
    expect(warnings).toEqual([])
    expect(patch).toMatchObject({
      name: form.name,
      description: form.description,
      coinsInput: 'BTCUSDT, ETHUSDT',
      timeframe: '15m',
      extraTimeframes: ['1h', '4h'],
      leverage: 3,
      altLeverage: 2,
      altPositionRatio: 2,
      marginUsage: 0.5,
      customPrompt: form.customPrompt,
      rules: form.rules,
      indicators: form.indicators,
      enableOI: true,
      enableFunding: false,
    })
  })

  it('keeps // and commas inside strings while stripping real comments', () => {
    const text = '{ "a": "x // y, }", /* c */ "b": [1, 2,], // tail\n }'
    expect(JSON.parse(stripJsonc(text))).toEqual({ a: 'x // y, }', b: [1, 2] })
  })

  it('clamps values an AI pushed out of range and reports them', () => {
    const edited = strategyToJsonc(form, 'en')
      .replace('"btc_eth_leverage": 3', '"btc_eth_leverage": 50')
      .replace('"bars": 30', '"bars": 200')
      .replace('"timeframe": "15m"', '"timeframe": "7m"')
    const { patch, warnings } = parseStrategyFile(edited, [], 'en')
    expect(patch.leverage).toBe(20)
    expect(patch.bars).toBe(30)
    expect(patch.timeframe).toBeUndefined()
    expect(warnings).toHaveLength(3)
  })

  it('falls back to the coin list when the pool is from another account', () => {
    const pooled = strategyToJsonc(
      {
        ...form,
        coinSource: 'pool',
        coinPoolID: 'other',
        poolCoins: ['SOLUSDT'],
      },
      'zh'
    )
    const elsewhere = parseStrategyFile(pooled, [], 'zh')
    expect(elsewhere.patch).toMatchObject({
      coinSource: 'static',
      coinsInput: 'SOLUSDT',
    })
    expect(elsewhere.warnings).toHaveLength(1)
    const here = parseStrategyFile(
      pooled,
      [{ id: 'other', coins: [{ symbol: 'XRPUSDT' }] }],
      'zh'
    )
    expect(here.patch).toMatchObject({
      coinSource: 'pool',
      coinPoolID: 'other',
      poolCoins: ['XRPUSDT'],
    })
  })

  it('round-trips the live Bollinger horn source and its interval', () => {
    const text = strategyToJsonc(
      { ...form, coinSource: 'horn', hornInterval: '4h' },
      'zh'
    )
    const { patch, warnings } = parseStrategyFile(text, [], 'zh')
    expect(warnings).toEqual([])
    expect(patch).toMatchObject({ coinSource: 'horn', hornInterval: '4h' })
  })

  it('rejects files that are not NOFX strategies', () => {
    expect(() => parseStrategyFile('{"a":1}', [], 'en')).toThrow(
      'Not a NOFX strategy file'
    )
    expect(() => parseStrategyFile('not json', [], 'en')).toThrow('not valid')
  })
})
