import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { SWRConfig } from 'swr'
import { DirectionShortlistPanel } from './DirectionShortlistPanel'
const mock = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../../lib/api', () => ({ api: { getDirectionShortlist: mock.get } }))
afterEach(cleanup)
const evidence = { samples: 20, hits: 15, hit_rate: .75, lower_95: .53, mean_return: .01 }
function mount(onSelect = vi.fn()) {
 render(<SWRConfig value={{provider: () => new Map(), shouldRetryOnError:false}}><DirectionShortlistPanel language="en" onSelect={onSelect}/></SWRConfig>)
 return onSelect
}
describe('direction research shortlist', () => {
 it('keeps empty slots and selects a coin without navigation', async () => {
 mock.get.mockResolvedValue({as_of:Date.now(),min_volume:1e8,candidates:53,long:[],short:[{symbol:'BTCUSDT',ai_history:evidence,trader_history:evidence,calls:3,invalidation_price:110}],excluded:{insufficient_evidence:52}})
 const select=mount()
 expect(await screen.findByText('Short candidates (1/10)')).toBeVisible()
 expect(screen.getByText(/No coin has sufficient evidence/)).toBeVisible()
 fireEvent.click(screen.getByRole('button',{name:'BTC'}))
 expect(select).toHaveBeenCalledWith('BTCUSDT')
 expect(screen.getByText(/75.0% \/ 75.0%/)).toBeVisible()
 })
 it('does not show expired selections as current',async()=>{
 mock.get.mockResolvedValue({as_of:Date.now()-180000,min_volume:1e8,candidates:53,long:[],short:[],excluded:{}})
 mount()
 expect(await screen.findByRole('alert')).toHaveTextContent('expired')
 expect(screen.queryByText('Short candidates (0/10)')).toBeNull()
 })
})
