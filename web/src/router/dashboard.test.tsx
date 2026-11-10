import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { SWRConfig } from 'swr'
import { AppRoutes } from './AppRoutes'

const mocks = vi.hoisted(() => ({ mounted: vi.fn(), unmounted: vi.fn(), account: vi.fn(), positions: vi.fn() }))
vi.mock('../contexts/AuthContext', () => ({ useAuth: () => ({ user: { id: 'user' }, token: 'token', isLoading: false }) }))
vi.mock('../contexts/LanguageContext', () => ({ useLanguage: () => ({ language: 'en', setLanguage: () => {} }) }))
vi.mock('../hooks/useSystemConfig', () => ({ useSystemConfig: () => ({ config: { initialized: true }, loading: false }) }))
vi.mock('../components/common/HeaderBar', () => ({ default: () => null }))
vi.mock('../components/common/SiteFooter', () => ({ SiteFooter: () => null }))
vi.mock('../components/auth/LoginRequiredOverlay', () => ({ LoginRequiredOverlay: () => null }))
vi.mock('../lib/api', () => ({ api: {
  getTraders: async () => [{ trader_id: 'aaaa', trader_name: 'First' }, { trader_id: 'bbbb', trader_name: 'Second' }],
  getStatus: async () => ({}), getAccount: mocks.account, getPositions: mocks.positions, getLatestDecisions: async () => [],
} }))
vi.mock('../components/terminal/TerminalDashboard', async () => {
  const { useEffect } = await import('react')
  return { TerminalDashboard: ({ selectedTraderId, onTraderSelect }: { selectedTraderId?: string; onTraderSelect: (id: string) => void }) => {
    useEffect(() => { mocks.mounted(); return () => { mocks.unmounted() } }, [])
    return <button onClick={() => onTraderSelect('bbbb')}>Selected: {selectedTraderId}</button>
  } }
})
afterEach(() => { cleanup(); vi.clearAllMocks(); vi.restoreAllMocks() })

it('keeps the terminal mounted on trader query changes without waiting for account/positions', async () => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  mocks.account.mockReturnValue(new Promise(() => {}))
  mocks.positions.mockReturnValue(new Promise(() => {}))
  render(<SWRConfig value={{ provider: () => new Map() }}>
    <MemoryRouter initialEntries={['/dashboard?trader=First-aaaa']}><AppRoutes /></MemoryRouter>
  </SWRConfig>)
  await screen.findByRole('button', { name: 'Selected: aaaa' })
  fireEvent.click(screen.getByRole('button'))
  await screen.findByRole('button', { name: 'Selected: bbbb' })
  await waitFor(() => expect(mocks.account).toHaveBeenCalledWith('bbbb', true))
  // Allow a query-keyed exit/enter animation enough time to remount if it regresses.
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 250)) })
  expect(mocks.mounted).toHaveBeenCalledTimes(1)
  expect(mocks.unmounted).not.toHaveBeenCalled()
  expect(mocks.positions).toHaveBeenCalledWith('bbbb', true)
})
