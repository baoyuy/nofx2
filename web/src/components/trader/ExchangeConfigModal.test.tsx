import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import type { Exchange } from '../../types'
import { ExchangeConfigModal } from './ExchangeConfigModal'

vi.mock('../../contexts/AuthContext', () => ({
  useAuth: () => ({ user: { id: 'alice' } }),
}))
vi.mock('../../lib/api', () => ({
  api: { getServerIP: async () => ({ public_ip: '127.0.0.1' }) },
}))
vi.mock('../common/HyperliquidWalletConnect', () => ({
  HyperliquidWalletConnect: () => null,
}))
vi.mock('../common/HyperliquidFundsPanel', () => ({
  HyperliquidFundsPanel: () => null,
}))
vi.mock('../common/WebCryptoEnvironmentCheck', () => ({
  WebCryptoEnvironmentCheck: () => null,
}))
vi.mock('../modals/TwoStageKeyModal', () => ({ TwoStageKeyModal: () => null }))
afterEach(cleanup)

it.each(['binance', 'okx', 'aster', 'lighter', 'hyperliquid'])(
  'renames a saved %s account without re-entering write-only credentials',
  async (exchangeType) => {
    const onSave = vi.fn().mockResolvedValue(undefined)
    const exchange: Exchange = {
      id: 'ex',
      exchange_type: exchangeType,
      account_name: 'Original',
      name: 'Account',
      type: 'cex',
      enabled: true,
      has_api_key: true,
      has_secret_key: true,
      has_passphrase: true,
      has_aster_private_key: true,
      asterUser: 'user',
      asterSigner: 'signer',
      has_lighter_api_key_private_key: true,
      lighterWalletAddr: 'wallet',
    }
    const { container } = render(
      <ExchangeConfigModal
        allExchanges={[exchange]}
        editingExchangeId="ex"
        language="en"
        onSave={onSave}
        onClose={() => {}}
        onDelete={async () => {}}
      />
    )
    fireEvent.change(await screen.findByDisplayValue('Original'), {
      target: { value: ' Renamed ' },
    })
    for (const input of container.querySelectorAll('input[type="password"]')) {
      expect(input).not.toBeRequired()
      expect(input).toHaveValue('')
    }
    fireEvent.click(screen.getByRole('button', { name: /Save Config/i }))
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
    expect(onSave.mock.calls[0].slice(0, 5)).toEqual([
      'ex',
      exchangeType,
      'Renamed',
      '',
      '',
    ])
  }
)

it('does not allow an explicitly blank account name', async () => {
  const onSave = vi.fn()
  render(
    <ExchangeConfigModal
      allExchanges={[
        {
          id: 'ex',
          exchange_type: 'binance_paper',
          account_name: 'Original',
          name: 'Paper',
          type: 'cex',
          enabled: true,
        },
      ]}
      editingExchangeId="ex"
      language="en"
      onSave={onSave}
      onClose={() => {}}
      onDelete={async () => {}}
    />
  )
  fireEvent.change(await screen.findByDisplayValue('Original'), {
    target: { value: '   ' },
  })
  expect(screen.getByRole('button', { name: /Save Config/i })).toBeDisabled()
  expect(onSave).not.toHaveBeenCalled()
})
