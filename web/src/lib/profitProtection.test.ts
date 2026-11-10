import { describe, expect, it } from 'vitest'
import { configFromForm, formFromStrategy, type FreeForm, normalizeProfitProtection } from './strategyForm'
import type { Strategy } from '../types'

const form: FreeForm = {
 name:'test', description:'', coinSource:'static', coinsInput:'BTCUSDT', timeframe:'15m', bars:30, leverage:3, maxPositions:3, marginUsage:0.5, minConfidence:75, minRiskReward:2, customPrompt:'',
 indicators:{enable_ema:true,enable_macd:true,enable_rsi:true,enable_atr:true,enable_boll:false,enable_volume:true},
}

describe('simulated profit protection strategy config',()=>{
 it('defaults disabled and clamps price percentages',()=>{
  expect(normalizeProfitProtection({enabled:true,breakeven_activation_pct:0,breakeven_lock_pct:200,trailing_activation_pct:-1,trailing_giveback_pct:200})).toEqual({enabled:true,breakeven_activation_pct:2,breakeven_lock_pct:2,trailing_activation_pct:0.1,trailing_giveback_pct:99})
  const saved=configFromForm(null,form,'en',null)
  expect(saved.ai_config!.risk_control.profit_protection?.enabled).toBe(false)
 })
 it('round trips enabled settings while retaining unconfigured coin sources',()=>{
  const base=configFromForm(null,form,'en',null)
  base.ai_config!.coin_source={...base.ai_config!.coin_source,source_type:'hyper_all',use_hyper_all:true}
  const protection={enabled:true,breakeven_activation_pct:3,breakeven_lock_pct:0.4,trailing_activation_pct:7,trailing_giveback_pct:35}
  base.ai_config!.risk_control.profit_protection=protection
  const loaded=formFromStrategy({id:'s',name:'saved',config:base} as Strategy)
  const saved=configFromForm(base,{...loaded,profitProtection:protection},'en',null)
  expect(saved.ai_config!.risk_control.profit_protection).toEqual(protection)
  expect(saved.ai_config!.coin_source).toEqual(base.ai_config!.coin_source)
 })
})
