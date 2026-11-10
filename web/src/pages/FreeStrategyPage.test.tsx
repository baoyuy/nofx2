import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { Strategy } from '../types'
import { FreeStrategyPage } from './FreeStrategyPage'
import { configFromForm, formFromStrategy } from '../lib/strategyForm'

const mocks = vi.hoisted(() => ({
  list: vi.fn(),
  update: vi.fn(),
  confirm: vi.fn(),
}))
vi.mock('../contexts/AuthContext', () => ({
  useAuth: () => ({ token: 'fixture' }),
}))
vi.mock('../contexts/LanguageContext', () => ({
  useLanguage: () => ({ language: 'en' }),
}))
vi.mock('../lib/api', () => ({
  api: {
    getStrategies: mocks.list,
    updateStrategy: mocks.update,
    getDefaultStrategyConfig: async () => null,
    getCoinPools: async () => [],
    getModelConfigs: async () => [],
    getTraders: async () => [],
  },
}))
vi.mock('../lib/notify', () => ({
  confirmToast: mocks.confirm,
  notify: { success: vi.fn(), error: vi.fn() },
}))
afterEach(cleanup)
beforeEach(() => vi.clearAllMocks())

it('keeps a dynamic source on unrelated saves and requires confirmation before migration', async () => {
  const config = configFromForm(
    null,
    formFromStrategy({ name: 'Dynamic', config: null } as unknown as Strategy),
    'en',
    null
  )
  config.ai_config!.coin_source.source_type = 'hyper_rank'
  config.ai_config!.coin_source.hyper_rank_limit = 7
  config.ai_config!.coin_source.hyper_rank_direction = 'losers'
  const original = structuredClone(config.ai_config!.coin_source)
  mocks.list.mockResolvedValue([
    { id: 's', name: 'Dynamic', description: '', config },
  ])
  mocks.update.mockResolvedValue(undefined)
  render(
    <MemoryRouter>
      <FreeStrategyPage />
    </MemoryRouter>
  )
  expect(
    await screen.findByText(/Preserving original source hyper_rank/)
  ).toBeVisible()
  fireEvent.change(screen.getByDisplayValue('Dynamic'), {
    target: { value: 'Renamed' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save', exact: true }))
  await waitFor(() => expect(mocks.update).toHaveBeenCalledTimes(1))
  expect(mocks.update.mock.calls[0][1].config.ai_config.coin_source).toEqual(
    original
  )
  await screen.findByText(/Preserving original source hyper_rank/)
  const fixed = screen.getByRole('radio', { name: /Fixed coins/ })
  mocks.confirm.mockResolvedValueOnce(false)
  fireEvent.click(fixed)
  await waitFor(() => expect(mocks.confirm).toHaveBeenCalledTimes(1))
  expect(fixed).toHaveAttribute('aria-checked', 'false')
  expect(
    screen.getByText(/Preserving original source hyper_rank/)
  ).toBeVisible()
  mocks.confirm.mockResolvedValueOnce(true)
  fireEvent.click(fixed)
  await waitFor(() => expect(fixed).toHaveAttribute('aria-checked', 'true'))
  fireEvent.click(screen.getByRole('button', { name: 'Save', exact: true }))
  await waitFor(() => expect(mocks.update).toHaveBeenCalledTimes(2))
  expect(
    mocks.update.mock.calls[1][1].config.ai_config.coin_source.source_type
  ).toBe('static')
})
