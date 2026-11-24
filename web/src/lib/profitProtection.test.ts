import { describe, expect, it } from 'vitest'
import { normalizeProfitProtection } from './strategyForm'

describe('profit protection risk units', () => {
  it('retains R settings and validates activation ranges', () => {
    const config = normalizeProfitProtection({enabled:true, mode:'r',breakeven_activation_r:1.5,trailing_activation_r:3})
    expect(config.mode).toBe('r')
    expect(config.breakeven_activation_r).toBe(1.5)
    expect(config.trailing_activation_r).toBe(3)
    expect(normalizeProfitProtection({mode:'r',breakeven_activation_r:0}).breakeven_activation_r).toBe(1)
  })
})
