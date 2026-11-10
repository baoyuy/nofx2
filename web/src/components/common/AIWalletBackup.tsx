import { NoticeText } from '../NoticeText'
import { useCallback, useEffect, useRef, useState } from 'react'
import { Copy, Download, Eye, EyeOff, KeyRound } from 'lucide-react'
import { getAuthHeaders } from '../../lib/api/helpers'
import { useLanguage } from '../../contexts/LanguageContext'
import type { Language } from '../../i18n/translations'
import './AIWalletBackup.css'

const TEXT = {
  zh: {
    errorExport: '无法导出此钱包。请检查登录状态并重新打开钱包设置。',
    copyFailed: '复制失败。请选择密钥并手动复制。',
    downloadFailed: '下载失败。请改为将密钥复制到你的密码管理器。',
    backupTitle: '私钥备份',
    intro:
      '备份此钱包以保留对资金的控制权。任何持有密钥的人都可以花费这些资金。切勿分享给他人。',
    walletPrivateKey: '钱包私钥',
    copied: '私钥已复制',
    copyKey: '复制私钥',
    downloadBackup: '下载备份',
    hideKey: '隐藏私钥',
    note: '下载的文件未加密。密钥会在 60 秒后、或当你离开此标签页时在此处隐藏。',
    loading: '正在加载私钥…',
    showKey: '显示私钥',
  },
  en: {
    errorExport:
      'Could not export this wallet. Check your login and reopen wallet settings.',
    copyFailed: 'Copy failed. Select the key and copy it manually.',
    downloadFailed:
      'Download failed. Copy the key to your password manager instead.',
    backupTitle: 'Private key backup',
    intro:
      'Back up this wallet to keep control of your funds. Anyone with the key can spend them. Never share it.',
    walletPrivateKey: 'Wallet private key',
    copied: 'Private key copied',
    copyKey: 'Copy private key',
    downloadBackup: 'Download backup',
    hideKey: 'Hide private key',
    note: 'The downloaded file is not encrypted. The key hides here after 60 seconds or when you leave this tab.',
    loading: 'Loading private key…',
    showKey: 'Show private key',
  },
}

// This component is also rendered in tests without a LanguageProvider;
// fall back to English there instead of throwing.
function useBackupLanguage(): Language {
  try {
    return useLanguage().language
  } catch {
    return 'en'
  }
}

export function downloadWalletBackup(
  address: string,
  privateKey: string,
  language: Language = 'en'
) {
  const text = (
    language === 'zh'
      ? [
          'NOFX AI 钱包备份 — 请私密保管',
          '网络：Base',
          `地址：${address}`,
          `私钥：${privateKey}`,
          '',
          '此文件未加密。任何持有此私钥的人都可以花费钱包中的资金。',
          '请将其存放在安全的密码管理器或离线备份中。切勿分享给任何客服。',
        ]
      : [
          'NOFX AI wallet backup — keep private',
          'Network: Base',
          `Address: ${address}`,
          `Private key: ${privateKey}`,
          '',
          'This file is not encrypted. Anyone with this private key can spend the wallet funds.',
          'Store it in a secure password manager or offline backup. Never share it with support.',
        ]
  ).join('\n')
  const url = URL.createObjectURL(
    new Blob([text], { type: 'text/plain;charset=utf-8' })
  )
  try {
    const link = document.createElement('a')
    link.href = url
    link.download = `NOFX-AI-wallet-${address.slice(0, 8)}.txt`
    link.click()
  } finally {
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
}

export function AIWalletBackup({
  modelID,
  address,
  privateKey,
}: {
  modelID?: string
  address: string
  privateKey?: string
}) {
  const language = useBackupLanguage()
  const tx = TEXT[language === 'zh' ? 'zh' : 'en']
  const [secret, setSecret] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const pending = useRef<AbortController | null>(null)
  const hideTimer = useRef<ReturnType<typeof setTimeout>>()
  const hide = useCallback(() => {
    pending.current?.abort()
    pending.current = null
    clearTimeout(hideTimer.current)
    setSecret('')
    setCopied(false)
    setBusy(false)
  }, [])

  useEffect(() => {
    hide()
    setError('')
    const onHidden = () => {
      if (document.visibilityState === 'hidden') hide()
    }
    document.addEventListener('visibilitychange', onHidden)
    return () => {
      pending.current?.abort()
      clearTimeout(hideTimer.current)
      document.removeEventListener('visibilitychange', onHidden)
    }
  }, [modelID, address, privateKey, hide])

  async function reveal() {
    if (pending.current) return
    const controller = new AbortController()
    pending.current = controller
    const timeout = setTimeout(() => controller.abort(), 15000)
    setBusy(true)
    setError('')
    try {
      let key = privateKey
      if (!key) {
        const response = await fetch('/api/wallet/export', {
          method: 'POST',
          headers: getAuthHeaders(),
          cache: 'no-store',
          signal: controller.signal,
          body: JSON.stringify({ model_id: modelID, address }),
        })
        if (!response.ok) throw new Error('Export unavailable')
        const data = await response.json()
        if (data.address?.toLowerCase() !== address.toLowerCase())
          throw new Error('Wallet changed')
        key = data.private_key
      }
      if (controller.signal.aborted) return
      if (!key || !/^0x[0-9a-fA-F]{64}$/.test(key))
        throw new Error('Invalid backup')
      setSecret(key)
      hideTimer.current = setTimeout(hide, 60000)
    } catch {
      if (pending.current === controller)
        setError(tx.errorExport)
    } finally {
      clearTimeout(timeout)
      if (pending.current === controller) {
        pending.current = null
        setBusy(false)
      }
    }
  }

  return (
    <section className="ai-wallet-backup" aria-label={tx.backupTitle}>
      <div className="ai-wallet-backup-heading">
        <KeyRound size={18} />
        <strong>{tx.backupTitle}</strong>
      </div>
      <p>{tx.intro}</p>
      {secret ? (
        <>
          <label>
            {tx.walletPrivateKey}
            <input
              aria-label={tx.walletPrivateKey}
              readOnly
              value={secret}
              autoComplete="off"
              spellCheck={false}
            />
          </label>
          <div className="ai-wallet-backup-actions">
            <button
              type="button"
              onClick={async () => {
                try {
                  await navigator.clipboard.writeText(secret)
                  setCopied(true)
                  setError('')
                } catch {
                  setError(tx.copyFailed)
                }
              }}
            >
              <Copy size={15} />
              {copied ? tx.copied : tx.copyKey}
            </button>
            <button
              type="button"
              onClick={() => {
                try {
                  downloadWalletBackup(address, secret, language)
                } catch {
                  setError(tx.downloadFailed)
                }
              }}
            >
              <Download size={15} />
              {tx.downloadBackup}
            </button>
            <button type="button" onClick={hide}>
              <EyeOff size={15} />
              {tx.hideKey}
            </button>
          </div>
          <small>{tx.note}</small>
        </>
      ) : (
        <button
          type="button"
          disabled={busy || (!modelID && !privateKey) || !address}
          onClick={() => void reveal()}
        >
          <Eye size={16} />
          {busy ? tx.loading : tx.showKey}
        </button>
      )}
      {error && <p role="alert"><NoticeText text={error} language={language} /></p>}
    </section>
  )
}
