import { useState, useEffect } from 'react'
import { useSearchParams } from 'react-router-dom'
import { toast } from '../lib/toast'
import {
  User,
  Cpu,
  Building2,
  Eye,
  EyeOff,
  ChevronRight,
  Plus,
  Pencil,
  Globe,
} from 'lucide-react'
import { useAuth } from '../contexts/AuthContext'
import { useLanguage } from '../contexts/LanguageContext'
import { api } from '../lib/api'
import { ExchangeConfigModal } from '../components/trader/ExchangeConfigModal'
import { ModelConfigModal } from '../components/trader/ModelConfigModal'
import { ProxySettings } from '../components/copytrade/ProxySettings'
import type { Exchange, AIModel, ExchangeAccountState } from '../types'

type Tab = 'account' | 'models' | 'exchanges' | 'data'

function configBadge(label: string, active: boolean) {
  return (
    <span
      className={`text-[11px] px-2 py-0.5 rounded-full ${
        active
          ? 'bg-nofx-success/10 text-nofx-success'
          : 'bg-nofx-bg-deeper text-nofx-text-muted'
      }`}
    >
      {label}
    </span>
  )
}

export function SettingsPage() {
  const { user } = useAuth()
  const { language } = useLanguage()
  // Local bilingual helper: zh shows Chinese, en (and id fallback) keeps the
  // original English copy verbatim.
  const tt = (en: string, zh: string) => (language === 'zh' ? zh : en)
  const [searchParams] = useSearchParams()
  const [activeTab, setActiveTab] = useState<Tab>(() => {
    const tab = searchParams.get('tab')
    return tab === 'models' || tab === 'exchanges' || tab === 'data'
      ? tab
      : 'account'
  })

  // Account state
  const [newPassword, setNewPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [changingPassword, setChangingPassword] = useState(false)

  // AI Models state
  const [configuredModels, setConfiguredModels] = useState<AIModel[]>([])
  const [supportedModels, setSupportedModels] = useState<AIModel[]>([])
  const [showModelModal, setShowModelModal] = useState(false)
  const [editingModel, setEditingModel] = useState<string | null>(null)

  // Exchanges state
  const [exchanges, setExchanges] = useState<Exchange[]>([])
  const [exchangeStates, setExchangeStates] = useState<
    Record<string, ExchangeAccountState>
  >({})
  const [exchangeStatesLoading, setExchangeStatesLoading] = useState(false)
  const [showExchangeModal, setShowExchangeModal] = useState(false)
  const [editingExchange, setEditingExchange] = useState<string | null>(null)

  const refreshModelConfigs = async () => {
    const [configs, supported] = await Promise.all([
      api.getModelConfigs(),
      api.getSupportedModels(),
    ])
    setConfiguredModels(configs)
    setSupportedModels(supported)
  }

  const refreshExchangeConfigs = async () => {
    const [refreshed, accountStateResponse] = await Promise.all([
      api.getExchangeConfigs(),
      api.getExchangeAccountState().catch(() => ({ states: {} })),
    ])
    setExchanges(refreshed)
    setExchangeStates(accountStateResponse.states || {})
  }

  const refreshExchangeStates = async () => {
    setExchangeStatesLoading(true)
    try {
      const response = await api.getExchangeAccountState()
      setExchangeStates(response.states || {})
    } catch {
      toast.error(tt('Failed to load exchange balances', '加载交易所余额失败'))
    } finally {
      setExchangeStatesLoading(false)
    }
  }

  // Fetch data when tabs are visited
  useEffect(() => {
    if (activeTab === 'models') {
      refreshModelConfigs().catch(() =>
        toast.error(tt('Failed to load AI models', '加载 AI 模型失败'))
      )
    }
    if (activeTab === 'exchanges') {
      refreshExchangeConfigs().catch(() =>
        toast.error(tt('Failed to load exchanges', '加载交易所失败'))
      )
    }
  }, [activeTab])

  const handleChangePassword = async (e: React.FormEvent) => {
    e.preventDefault()
    if (newPassword.length < 8) {
      toast.error(tt('Password must be at least 8 characters', '密码至少需要 8 个字符'))
      return
    }
    setChangingPassword(true)
    try {
      const res = await fetch('/api/user/password', {
        method: 'PUT',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${localStorage.getItem('auth_token') || ''}`,
        },
        body: JSON.stringify({ new_password: newPassword }),
      })
      if (!res.ok) {
        const data = await res.json().catch(() => ({}))
        throw new Error(data.error || tt('Failed to update password', '更新密码失败'))
      }
      toast.success(tt('Password updated successfully', '密码更新成功'))
      setNewPassword('')
    } catch (err) {
      toast.error(
        err instanceof Error
          ? err.message
          : tt('Failed to update password', '更新密码失败')
      )
    } finally {
      setChangingPassword(false)
    }
  }

  const handleSaveModel = async (
    modelId: string,
    apiKey: string,
    customApiUrl?: string,
    customModelName?: string
  ) => {
    try {
      const existingModel = configuredModels.find((m) => m.id === modelId)
      const modelTemplate = supportedModels.find((m) => m.id === modelId)
      const modelToUpdate = existingModel || modelTemplate
      if (!modelToUpdate) {
        toast.error(tt('Model not found', '未找到模型'))
        return
      }

      let updatedModels: AIModel[]
      if (existingModel) {
        updatedModels = configuredModels.map((m) =>
          m.id === modelId
            ? {
                ...m,
                apiKey,
                customApiUrl: customApiUrl || '',
                customModelName: customModelName || '',
                enabled: true,
              }
            : m
        )
      } else {
        updatedModels = [
          ...configuredModels,
          {
            ...modelToUpdate,
            apiKey,
            customApiUrl: customApiUrl || '',
            customModelName: customModelName || '',
            enabled: true,
          },
        ]
      }

      const request = {
        models: Object.fromEntries(
          updatedModels.map((m) => [
            m.provider,
            {
              enabled: m.enabled,
              api_key: m.apiKey || '',
              custom_api_url: m.customApiUrl || '',
              custom_model_name: m.customModelName || '',
            },
          ])
        ),
      }
      await api.updateModelConfigs(request)
      toast.success(tt('Model config saved', '模型配置已保存'))
      await refreshModelConfigs()
      setShowModelModal(false)
      setEditingModel(null)
    } catch {
      toast.error(tt('Failed to save model config', '保存模型配置失败'))
    }
  }

  const handleDeleteModel = async (modelId: string) => {
    try {
      const updatedModels = configuredModels.map((m) =>
        m.id === modelId
          ? {
              ...m,
              apiKey: '',
              customApiUrl: '',
              customModelName: '',
              enabled: false,
            }
          : m
      )
      const request = {
        models: Object.fromEntries(
          updatedModels.map((m) => [
            m.provider,
            {
              enabled: m.enabled,
              api_key: m.apiKey || '',
              custom_api_url: m.customApiUrl || '',
              custom_model_name: m.customModelName || '',
            },
          ])
        ),
      }
      await api.updateModelConfigs(request)
      await refreshModelConfigs()
      setShowModelModal(false)
      setEditingModel(null)
      toast.success(tt('Model config removed', '模型配置已移除'))
    } catch {
      toast.error(tt('Failed to remove model config', '移除模型配置失败'))
    }
  }

  const handleSaveExchange = async (
    exchangeId: string | null,
    exchangeType: string,
    accountName: string,
    apiKey: string,
    secretKey?: string,
    passphrase?: string,
    testnet?: boolean,
    hyperliquidWalletAddr?: string,
    asterUser?: string,
    asterSigner?: string,
    asterPrivateKey?: string,
    lighterWalletAddr?: string,
    lighterPrivateKey?: string,
    lighterApiKeyPrivateKey?: string,
    lighterApiKeyIndex?: number
  ) => {
    try {
      if (exchangeType === 'hyperliquid') {
        toast.error(
          language === 'zh'
            ? 'Hyperliquid 必须通过钱包授权连接，不能手动填写密钥。'
            : 'Hyperliquid must be connected through wallet authorization, not manual keys.'
        )
        return
      }
      if (exchangeId) {
        const request = {
          exchanges: {
            [exchangeId]: {
              enabled: true,
              api_key: apiKey || '',
              secret_key: secretKey || '',
              passphrase: passphrase || '',
              testnet: testnet || false,
              hyperliquid_wallet_addr: hyperliquidWalletAddr || '',
              hyperliquid_unified_account: exchangeType === 'hyperliquid',
              aster_user: asterUser || '',
              aster_signer: asterSigner || '',
              aster_private_key: asterPrivateKey || '',
              lighter_wallet_addr: lighterWalletAddr || '',
              lighter_private_key: lighterPrivateKey || '',
              lighter_api_key_private_key: lighterApiKeyPrivateKey || '',
              lighter_api_key_index: lighterApiKeyIndex || 0,
            },
          },
        }
        await api.updateExchangeConfigsEncrypted(request)
        toast.success(tt('Exchange config updated', '交易所配置已更新'))
      } else {
        const createRequest = {
          exchange_type: exchangeType,
          account_name: accountName,
          enabled: true,
          api_key: apiKey || '',
          secret_key: secretKey || '',
          passphrase: passphrase || '',
          testnet: testnet || false,
          hyperliquid_wallet_addr: hyperliquidWalletAddr || '',
          hyperliquid_unified_account: exchangeType === 'hyperliquid',
          aster_user: asterUser || '',
          aster_signer: asterSigner || '',
          aster_private_key: asterPrivateKey || '',
          lighter_wallet_addr: lighterWalletAddr || '',
          lighter_private_key: lighterPrivateKey || '',
          lighter_api_key_private_key: lighterApiKeyPrivateKey || '',
          lighter_api_key_index: lighterApiKeyIndex || 0,
        }
        await api.createExchangeEncrypted(createRequest)
        toast.success(tt('Exchange account created', '交易所账户已创建'))
      }
      await refreshExchangeConfigs()
      setShowExchangeModal(false)
      setEditingExchange(null)
    } catch {
      toast.error(tt('Failed to save exchange config', '保存交易所配置失败'))
    }
  }

  const handleDeleteExchange = async (exchangeId: string) => {
    try {
      await api.deleteExchange(exchangeId)
      toast.success(tt('Exchange account deleted', '交易所账户已删除'))
      await refreshExchangeConfigs()
      setShowExchangeModal(false)
      setEditingExchange(null)
    } catch {
      toast.error(tt('Failed to delete exchange account', '删除交易所账户失败'))
    }
  }

  const tabs: { key: Tab; label: string; icon: React.ReactNode }[] = [
    { key: 'account', label: tt('Account', '账户'), icon: <User size={16} /> },
    { key: 'models', label: tt('AI Models', 'AI 模型'), icon: <Cpu size={16} /> },
    {
      key: 'exchanges',
      label: tt('Exchanges', '交易所'),
      icon: <Building2 size={16} />,
    },
    {
      key: 'data',
      label: tt('Data proxy', '数据代理'),
      icon: <Globe size={16} />,
    },
  ]

  return (
    <div
      className="nofx-settings min-h-screen pt-20 pb-12 px-4"
      style={{ background: '#f6f8fc' }}
    >
      <div className="max-w-2xl mx-auto">
        <h1 className="text-xl font-bold text-nofx-text mb-6">
          {tt('Settings', '设置')}
        </h1>

        {/* Tabs */}
        <div className="flex gap-1 mb-6 bg-nofx-bg-lighter border border-[rgba(25,36,52,0.14)] rounded-xl p-1">
          {tabs.map((tab) => (
            <button
              key={tab.key}
              onClick={() => setActiveTab(tab.key)}
              className={`flex-1 flex items-center justify-center gap-2 px-3 py-2 rounded-lg text-sm font-medium transition-all
                ${
                  activeTab === tab.key
                    ? 'bg-nofx-gold text-nofx-bg'
                    : 'text-nofx-text-muted hover:text-nofx-text'
                }`}
            >
              {tab.icon}
              <span className="hidden sm:inline">{tab.label}</span>
            </button>
          ))}
        </div>

        {/* Tab Content */}
        <div className="bg-nofx-bg-lighter backdrop-blur-xl border border-[rgba(25,36,52,0.14)] rounded-2xl p-6">
          {/* Account Tab */}
          {activeTab === 'account' && (
            <div className="space-y-6">
              <div>
                <p className="text-xs text-nofx-text-muted mb-1">
                  {tt('Email', '邮箱')}
                </p>
                <p className="text-sm text-nofx-text font-medium">
                  {user?.email}
                </p>
              </div>

              <div className="border-t border-[rgba(25,36,52,0.14)] pt-6">
                <h3 className="text-sm font-semibold text-nofx-text mb-4">
                  {tt('Change Password', '修改密码')}
                </h3>
                <form onSubmit={handleChangePassword} className="space-y-4">
                  <div>
                    <label className="block text-xs font-medium text-nofx-text-muted mb-2">
                      {tt('New Password', '新密码')}
                    </label>
                    <div className="relative">
                      <input
                        type={showPassword ? 'text' : 'password'}
                        value={newPassword}
                        onChange={(e) => setNewPassword(e.target.value)}
                        className="w-full bg-nofx-bg-deeper border border-[rgba(25,36,52,0.14)] rounded-xl px-4 py-3 pr-11 text-sm text-nofx-text placeholder-nofx-text-muted focus:outline-hidden focus:border-nofx-gold/60 focus:ring-1 focus:ring-nofx-gold/30 transition-all"
                        placeholder={tt('At least 8 characters', '至少 8 个字符')}
                        required
                      />
                      <button
                        type="button"
                        onClick={() => setShowPassword(!showPassword)}
                        className="absolute right-3.5 top-1/2 -translate-y-1/2 text-nofx-text-muted hover:text-nofx-text transition-colors"
                      >
                        {showPassword ? (
                          <EyeOff size={16} />
                        ) : (
                          <Eye size={16} />
                        )}
                      </button>
                    </div>
                  </div>
                  <button
                    type="submit"
                    disabled={changingPassword || newPassword.length < 8}
                    className="w-full bg-nofx-gold hover:bg-nofx-gold-highlight active:scale-[0.98] text-nofx-bg font-semibold py-3 rounded-xl text-sm transition-all disabled:opacity-50 disabled:cursor-not-allowed"
                  >
                    {changingPassword
                      ? tt('Updating...', '更新中...')
                      : tt('Update Password', '更新密码')}
                  </button>
                </form>
              </div>
            </div>
          )}

          {/* AI Models Tab */}
          {activeTab === 'models' && (
            <div className="space-y-4">
              <div className="flex items-center justify-between">
                <p className="text-sm text-nofx-text-muted">
                  {language === 'zh'
                    ? `已配置 ${configuredModels.length} 个模型`
                    : `${configuredModels.length} model${configuredModels.length !== 1 ? 's' : ''} configured`}
                </p>
                <button
                  onClick={() => {
                    setEditingModel(null)
                    setShowModelModal(true)
                  }}
                  className="flex items-center gap-1.5 text-xs font-medium bg-nofx-gold/10 hover:bg-nofx-gold/20 text-nofx-gold px-3 py-1.5 rounded-lg transition-colors"
                >
                  <Plus size={14} />
                  {tt('Add Model', '添加模型')}
                </button>
              </div>

              {configuredModels.length === 0 ? (
                <div className="text-center py-8 text-nofx-text-muted text-sm">
                  {tt('No AI models configured yet', '尚未配置 AI 模型')}
                </div>
              ) : (
                <div className="space-y-2">
                  {configuredModels.map((model) => (
                    <button
                      key={model.id}
                      onClick={() => {
                        setEditingModel(model.id)
                        setShowModelModal(true)
                      }}
                      className="w-full flex items-center justify-between px-4 py-3 rounded-xl bg-nofx-bg-deeper hover:bg-nofx-bg-deeper border border-[rgba(25,36,52,0.14)] transition-colors group"
                    >
                      <div className="flex items-center gap-3">
                        <div className="w-8 h-8 rounded-lg bg-nofx-bg-deeper flex items-center justify-center">
                          <Cpu size={14} className="text-nofx-text" />
                        </div>
                        <div className="text-left">
                          <p className="text-sm font-medium text-nofx-text">
                            {model.name}
                          </p>
                          <div className="flex flex-wrap items-center gap-1.5 mt-1">
                            <p className="text-xs text-nofx-text-muted">
                              {model.provider}
                            </p>
                            {configBadge(
                              tt('API Key', 'API 密钥'),
                              !!model.has_api_key
                            )}
                            {model.customModelName
                              ? configBadge(tt('Custom Model', '自定义模型'), true)
                              : null}
                            {model.customApiUrl
                              ? configBadge('Base URL', true)
                              : null}
                          </div>
                        </div>
                      </div>
                      <div className="flex items-center gap-2">
                        <span
                          className={`text-xs px-2 py-0.5 rounded-full ${model.enabled ? 'bg-nofx-success/10 text-nofx-success' : 'bg-nofx-bg-deeper text-nofx-text-muted'}`}
                        >
                          {model.enabled
                            ? tt('Active', '已启用')
                            : tt('Inactive', '未启用')}
                        </span>
                        <Pencil
                          size={14}
                          className="text-nofx-text-muted group-hover:text-nofx-text-muted transition-colors"
                        />
                      </div>
                    </button>
                  ))}
                </div>
              )}
            </div>
          )}

          {/* Exchanges Tab */}
          {activeTab === 'exchanges' && (
            <div className="space-y-4">
              <div className="flex items-center justify-between">
                <p className="text-sm text-nofx-text-muted">
                  {language === 'zh'
                    ? `已连接 ${exchanges.length} 个账户`
                    : `${exchanges.length} account${exchanges.length !== 1 ? 's' : ''} connected`}
                </p>
                <div className="flex items-center gap-2">
                  <button
                    onClick={refreshExchangeStates}
                    disabled={exchangeStatesLoading}
                    className="text-xs font-medium bg-nofx-bg-deeper hover:bg-nofx-bg-deeper disabled:opacity-60 text-nofx-text px-3 py-1.5 rounded-lg transition-colors"
                  >
                    {exchangeStatesLoading
                      ? tt('Refreshing…', '刷新中…')
                      : tt('Refresh Balances', '刷新余额')}
                  </button>
                  <button
                    onClick={() => {
                      setEditingExchange(null)
                      setShowExchangeModal(true)
                    }}
                    className="flex items-center gap-1.5 text-xs font-medium bg-nofx-gold/10 hover:bg-nofx-gold/20 text-nofx-gold px-3 py-1.5 rounded-lg transition-colors"
                  >
                    <Plus size={14} />
                    {tt('Add Exchange', '添加交易所')}
                  </button>
                </div>
              </div>

              {exchanges.length === 0 ? (
                <div className="text-center py-8 text-nofx-text-muted text-sm">
                  {tt(
                    'No exchange accounts connected yet',
                    '尚未连接交易所账户'
                  )}
                </div>
              ) : (
                <div className="space-y-2">
                  {exchanges.map((exchange) => {
                    const accountState = exchangeStates[exchange.id]
                    return (
                      <button
                        key={exchange.id}
                        onClick={() => {
                          setEditingExchange(exchange.id)
                          setShowExchangeModal(true)
                        }}
                        className="w-full flex items-center justify-between px-4 py-3 rounded-xl bg-nofx-bg-deeper hover:bg-nofx-bg-deeper border border-[rgba(25,36,52,0.14)] transition-colors group"
                      >
                        <div className="flex items-center gap-3">
                          <div className="w-8 h-8 rounded-lg bg-nofx-bg-deeper flex items-center justify-center">
                            <Building2 size={14} className="text-nofx-text" />
                          </div>
                          <div className="text-left">
                            <p className="text-sm font-medium text-nofx-text">
                              {exchange.account_name || exchange.name}
                            </p>
                            <div className="flex flex-wrap items-center gap-1.5 mt-1">
                              <p className="text-xs text-nofx-text-muted capitalize">
                                {exchange.exchange_type || exchange.type}
                              </p>
                              {configBadge(
                                tt('API Key', 'API 密钥'),
                                !!exchange.has_api_key
                              )}
                              {configBadge(
                                tt('Secret', '密钥'),
                                !!exchange.has_secret_key
                              )}
                              {exchange.has_passphrase
                                ? configBadge(tt('Passphrase', '口令'), true)
                                : null}
                              {exchange.hyperliquidWalletAddr
                                ? configBadge(tt('Wallet', '钱包'), true)
                                : null}
                              {exchange.has_aster_private_key
                                ? configBadge(tt('Aster Key', 'Aster 密钥'), true)
                                : null}
                              {exchange.has_lighter_private_key ||
                              exchange.has_lighter_api_key_private_key
                                ? configBadge(
                                    tt('Lighter Key', 'Lighter 密钥'),
                                    true
                                  )
                                : null}
                            </div>
                            {accountState && (
                              <div className="flex flex-wrap items-center gap-2 mt-2 text-xs">
                                {accountState.status === 'ok' ? (
                                  <>
                                    <span className="rounded-full bg-nofx-success/10 px-2 py-0.5 font-mono text-nofx-success">
                                      {tt('Balance', '余额')}{' '}
                                      {accountState.display_balance ||
                                        `${accountState.total_equity?.toFixed(2) ?? '--'} ${accountState.asset || ''}`}
                                    </span>
                                    {typeof accountState.available_balance ===
                                      'number' && (
                                      <span className="text-nofx-text-muted">
                                        {tt('Available', '可用')}{' '}
                                        {accountState.available_balance.toFixed(
                                          2
                                        )}{' '}
                                        {accountState.asset || ''}
                                      </span>
                                    )}
                                  </>
                                ) : (
                                  <span className="rounded-full bg-nofx-gold/10 px-2 py-0.5 text-nofx-gold">
                                    {tt('Balance unavailable:', '余额不可用：')}
                                    {' '}
                                    {accountState.error_message ||
                                      accountState.status}
                                  </span>
                                )}
                              </div>
                            )}
                          </div>
                        </div>
                        <ChevronRight
                          size={14}
                          className="text-nofx-text-muted group-hover:text-nofx-text-muted transition-colors"
                        />
                      </button>
                    )
                  })}
                </div>
              )}
            </div>
          )}

          {/* Data source Tab */}
          {activeTab === 'data' && <ProxySettings tt={tt} />}
        </div>
      </div>

      {/* AI Model Modal */}
      {showModelModal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-xs px-4">
          <ModelConfigModal
            allModels={supportedModels}
            configuredModels={configuredModels}
            editingModelId={editingModel}
            onSave={handleSaveModel}
            onDelete={handleDeleteModel}
            onClose={() => {
              setShowModelModal(false)
              setEditingModel(null)
            }}
            language={language}
          />
        </div>
      )}

      {/* Exchange Modal */}
      {showExchangeModal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-xs px-4">
          <ExchangeConfigModal
            allExchanges={exchanges}
            editingExchangeId={editingExchange}
            onSave={handleSaveExchange}
            onDelete={handleDeleteExchange}
            onClose={() => {
              setShowExchangeModal(false)
              setEditingExchange(null)
            }}
            language={language}
          />
        </div>
      )}
    </div>
  )
}
