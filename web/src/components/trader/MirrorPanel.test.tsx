import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { SWRConfig } from 'swr'
import { MirrorPanel } from './MirrorPanel'

const link = {
  id: '96fd',
  leader_id: 'ai',
  exchange_id: 'e56e',
  size_mode: 'fixed',
  fixed_margin: 200,
  ratio: 1,
  leverage_mode: 'fixed',
  leverage: 20,
  max_leverage: 10,
  follow_protect: true,
  max_margin: 0,
  max_loss: 0,
  running: true,
  start_equity: 559.2752705,
  stop_reason: '',
  created_at: '2026-11-07T03:46:04.025708103Z',
  account_name: '主',
  paper: false,
  active: true,
  status: {
    equity: 559.2752705,
    available: 559.2752705,
    pnl: 0,
    updated_at: '2026-11-07T11:48:14.415581541+08:00',
    positions: [],
  },
  positions: [
    {
      symbol: 'XRPUSDT',
      side: 'short',
      leader_qty: 3864.3,
      follower_qty: 0,
      skip: 'before_start',
      opened_at: 1794023164704,
    },
  ],
}

const fresh = {
  ...link,
  status: {
    equity: 0,
    available: 0,
    pnl: 0,
    updated_at: '0001-01-01T00:00:00Z',
    positions: null,
  },
}
const mocks = vi.hoisted(() => ({ links: vi.fn() }))
vi.mock('../../lib/api', () => ({ api: { getMirrorLinks: mocks.links } }))
afterEach(cleanup)

it('renders a running real link', async () => {
  mocks.links.mockResolvedValue([link])
  render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <MirrorPanel language="zh" traderId="ai" />
    </SWRConfig>
  )
  await act(async () => {
    await new Promise((r) => setTimeout(r, 10))
  })
  expect(screen.getByText('主')).toBeVisible()
})

it('renders a link before its first tick', async () => {
  mocks.links.mockResolvedValue([fresh])
  render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <MirrorPanel language="zh" traderId="ai" />
    </SWRConfig>
  )
  await act(async () => {
    await new Promise((r) => setTimeout(r, 10))
  })
  expect(screen.getByText('主')).toBeVisible()
})
