import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import type { Strategy, TraderConfigData } from '../../types'
import { TraderConfigModal } from './TraderConfigModal'
import { t } from '../../i18n/translations'

vi.mock('../../contexts/LanguageContext', () => ({
  useLanguage: () => ({ language: 'en' }),
}))
vi.mock('../../lib/httpClient', () => ({
  httpClient: {
    get: async () => ({
      success: true,
      data: { strategies: [] as Strategy[] },
    }),
  },
}))
vi.mock('../ui/select', () => ({
  NofxSelect: ({
    value,
    options,
    onChange,
  }: {
    value: string
    options: { value: string; label: string }[]
    onChange: (value: string) => void
  }) => (
    <select value={value} onChange={(e) => onChange(e.target.value)}>
      {options.map((option) => (
        <option key={option.value} value={option.value}>
          {option.label}
        </option>
      ))}
    </select>
  ),
}))
afterEach(cleanup)

it('omits hidden legacy settings and submits an explicit empty strategy when detached', async () => {
  const onSave = vi.fn().mockResolvedValue(undefined)
  const traderData = {
    trader_id: 'bot',
    trader_name: 'Existing',
    ai_model: 'model',
    exchange_id: 'exchange',
    strategy_id: '',
    is_cross_margin: true,
    show_in_competition: false,
    scan_interval_minutes: 15,
    custom_prompt: 'Keep me',
    trading_symbols: 'BTCUSDT',
    override_base_prompt: true,
  } as TraderConfigData
  render(
    <TraderConfigModal
      isOpen
      isEditMode
      traderData={traderData}
      availableModels={[]}
      availableExchanges={[]}
      onClose={() => {}}
      onSave={onSave}
    />
  )
  expect(screen.getByRole('status')).toHaveTextContent('no default is applied')
  fireEvent.click(
    screen.getByRole('button', { name: t('editTrader', 'en'), exact: true })
  )
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
  const payload = onSave.mock.calls[0][0]
  expect(payload.strategy_id).toBe('')
  expect(payload).not.toHaveProperty('custom_prompt')
  expect(payload).not.toHaveProperty('trading_symbols')
  expect(payload).not.toHaveProperty('override_base_prompt')
})
