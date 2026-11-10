import { StrictMode } from 'react'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { FrontierSetup, feeWalletState } from './FrontierSetup'
const mocks = vi.hoisted(() => ({
  api: vi.fn(),
  create: vi.fn(),
  update: vi.fn(),
}))
vi.mock('../../lib/planning', () => ({ planningAPI: mocks.api }))
vi.mock('../../lib/api', () => ({
  api: {
    createExchangeEncrypted: mocks.create,
    updateExchangeConfigsEncrypted: mocks.update,
    getExchangeConfigs: async () => [],
    getSupportedModels: async () => [{ id: 'claw402', provider: 'claw402' }],
  },
}))
vi.mock('../common/HyperliquidWalletConnect', () => ({
  HyperliquidWalletConnect: ({
    onSaved,
  }: {
    onSaved: (id?: string) => void
  }) => (
    <button onClick={() => onSaved('saved-hl')}>
      Mock authorized connection saved
    </button>
  ),
}))
vi.mock('../trader/ExchangeConfigModal', () => ({
  ExchangeConfigModal: ({
    initialExchangeType,
    onSave,
    editingExchangeId,
  }: {
    initialExchangeType: string
    editingExchangeId: string | null
    onSave: (...args: unknown[]) => Promise<void>
  }) => (
    <button
      onClick={() =>
        void onSave(
          editingExchangeId,
          initialExchangeType,
          'Account two',
          'test-key',
          'test-secret',
          'test-passphrase',
          false
        )
      }
    >
      Save mock account
    </button>
  ),
}))
const address = '0x1111111111111111111111111111111111111111'
const missing = {
  found: false,
  enabled: false,
  chain: 'base',
  asset: 'USDC',
  balance_status: 'unknown',
}
const refresh = vi.fn().mockResolvedValue(undefined),
  open = vi.fn(),
  state = vi.fn()
beforeEach(() => {
  mocks.api.mockReset()
  refresh.mockClear()
  open.mockClear()
  state.mockClear()
  mocks.api.mockResolvedValue(missing)
})
afterEach(cleanup)
function show(step: 'ai' | 'exchange' | null = 'ai', direct = false) {
  return render(
    <StrictMode>
      <MemoryRouter>
        <FrontierSetup
          models={
            direct
              ? [
                  {
                    id: 'direct',
                    name: 'My AI',
                    provider: 'deepseek',
                    enabled: true,
                    has_api_key: true,
                  },
                ]
              : []
          }
          modelID={direct ? 'direct' : ''}
          accounts={[]}
          accountID=""
          venue="hyperliquid"
          loaded
          open={step}
          onOpen={open}
          onRefresh={refresh}
          onWalletState={state}
        />
      </MemoryRouter>
    </StrictMode>
  )
}
it('offers only a bring-your-own-key AI setup without a paid wallet', async () => {
  show()
  await waitFor(() => expect(mocks.api).toHaveBeenCalled())
  expect(
    screen.queryByRole('button', { name: /AI wallet|AI payment address/ })
  ).toBeNull()
  expect(screen.queryByText(/USDC on Base|Pay as you go/)).toBeNull()
  expect(
    screen.getByRole('button', { name: 'Connect my own API key →' })
  ).toBeVisible()
  expect(mocks.api.mock.calls.every((call) => call.length === 1)).toBe(true)
  expect(refresh).not.toHaveBeenCalled()
})
it('lets an existing direct AI provider continue without a fee-wallet deposit', async () => {
  show('ai', true)
  expect(screen.getByText('Your AI is configured')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: 'Continue with this AI' }))
  expect(open).toHaveBeenCalledWith(null)
  expect(mocks.api.mock.calls.every((call) => call.length === 1)).toBe(true)
})
it('refreshes a saved exchange in place without starting trading or the model', async () => {
  show('exchange', true)
  fireEvent.click(
    screen.getByRole('button', { name: 'Mock authorized connection saved' })
  )
  await waitFor(() =>
    expect(refresh).toHaveBeenCalledWith(undefined, 'hyperliquid', 'saved-hl')
  )
  expect(open).not.toHaveBeenCalled()
  expect(mocks.create).not.toHaveBeenCalled()
})
it('does not treat an unknown or malformed balance as funded', () => {
  for (const balance of [undefined, '', 'NaN', '-1'])
    expect(
      feeWalletState({
        ...missing,
        found: true,
        enabled: true,
        balance_usdc: balance,
        balance_status: 'ok',
      })
    ).toBe('unknown')
  expect(
    feeWalletState({
      ...missing,
      found: true,
      enabled: true,
      balance_usdc: '0.000001',
      balance_status: 'ok',
    })
  ).toBe('ready')
})

it('does not mark saved credentials as verified until the live account check succeeds', async () => {
  let resolveAccount!: (value: unknown) => void
  const accountRead = new Promise((resolve) => {
    resolveAccount = resolve
  })
  mocks.api.mockImplementation((path: string) =>
    path.startsWith('account?') ? accountRead : Promise.resolve(missing)
  )
  const complete = vi.fn()
  render(
    <FrontierSetup
      models={[]}
      modelID=""
      accounts={[
        {
          id: 'binance',
          name: 'My Binance',
          exchange_type: 'binance',
          enabled: true,
        },
      ]}
      accountID="binance"
      venue="binance"
      loaded
      open="exchange"
      onOpen={open}
      onRefresh={refresh}
      onWalletState={state}
      onComplete={complete}
    />
  )
  expect(
    screen.getByRole('button', { name: 'Return to my plan' })
  ).toBeDisabled()
  resolveAccount({
    equity: '500',
    available: '250',
    position: '0',
    open_orders: 0,
  })
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Return to my plan' })
    ).toBeEnabled()
  )
  expect(screen.getByText(/Available margin 250.00 USDT/)).toBeVisible()
  expect(
    screen.getByRole('button', {
      name: /Trading account My Binance Account equity 500.00 USDT Available to trade 250.00 USDT/,
    })
  ).toBeVisible()
  expect(complete).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: 'Return to my plan' }))
  expect(complete).toHaveBeenCalledWith('exchange')
  expect(
    mocks.api.mock.calls.every(
      ([path]) => path.startsWith('account?') || path.startsWith('setup-wallet')
    )
  ).toBe(true)
})
it('keeps invalid exchange credentials actionable instead of showing a ready account', async () => {
  mocks.api.mockImplementation(async (path: string) => {
    if (path.startsWith('account?'))
      throw new Error('Futures trading permission is disabled')
    return missing
  })
  render(
    <FrontierSetup
      models={[]}
      modelID=""
      accounts={[
        {
          id: 'binance',
          name: 'My Binance',
          exchange_type: 'binance',
          enabled: true,
        },
      ]}
      accountID="binance"
      venue="binance"
      loaded
      open="exchange"
      onOpen={open}
      onRefresh={refresh}
      onWalletState={state}
    />
  )
  expect(
    await screen.findByText('Futures trading permission is disabled')
  ).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Return to my plan' })
  ).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Verify account' })).toBeEnabled()
})

it('does not offer an empty provider picker when the deployment supports Claw402 only', async () => {
  show()
  expect(
    await screen.findByRole('button', { name: 'Connect my own API key →' })
  ).toBeDisabled()
})

it.each(['okx', 'bitget'])(
  'connects %s with an encrypted passphrase and selects the saved account',
  async (venue) => {
    mocks.create.mockResolvedValue({ id: 'saved-account' })
    render(
      <FrontierSetup
        models={[]}
        modelID=""
        accounts={[]}
        accountID=""
        venue={venue}
        loaded
        open="exchange"
        onOpen={open}
        onRefresh={refresh}
        onWalletState={state}
      />
    )
    fireEvent.click(
      screen.getByRole('button', {
        name: `Connect ${venue === 'okx' ? 'OKX' : 'Bitget'}`,
        exact: true,
      })
    )
    fireEvent.click(screen.getByRole('button', { name: 'Save mock account' }))
    await waitFor(() =>
      expect(refresh).toHaveBeenCalledWith(undefined, venue, 'saved-account')
    )
    expect(mocks.create).toHaveBeenCalledWith(
      expect.objectContaining({
        exchange_type: venue,
        api_key: 'test-key',
        secret_key: 'test-secret',
        passphrase: 'test-passphrase',
        testnet: false,
      })
    )
  }
)

it('forwards a renamed existing account through Frontier encrypted updates', async () => {
  mocks.api.mockResolvedValue(missing)
  render(
    <FrontierSetup
      models={[]}
      modelID=""
      accounts={[
        {
          id: 'existing',
          name: 'Old account',
          exchange_type: 'okx',
          enabled: true,
        },
      ]}
      accountID="existing"
      venue="okx"
      loaded
      open="exchange"
      onOpen={open}
      onRefresh={refresh}
      onWalletState={state}
    />
  )
  fireEvent.click(
    screen.getByRole('button', { name: 'Update OKX connection', exact: true })
  )
  fireEvent.click(screen.getByRole('button', { name: 'Save mock account' }))
  await waitFor(() =>
    expect(mocks.update).toHaveBeenCalledWith({
      exchanges: {
        existing: expect.objectContaining({
          account_name: 'Account two',
          passphrase: 'test-passphrase',
        }),
      },
    })
  )
})
