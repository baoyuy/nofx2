import { useEffect, useState } from 'react'
import { Loader2 } from 'lucide-react'
import { api } from '../../lib/api'
import { notify } from '../../lib/notify'
import { NoticeText } from '../NoticeText'
import { localizeNotice } from '../../i18n/notices'

// Proxy for the copy-trade monitor's Binance requests. Empty = the server's
// own IP. The saved URL is stored encrypted and shown with the password masked.
export function ProxySettings({
  tt,
}: {
  tt: (en: string, zh: string) => string
}) {
  const [current, setCurrent] = useState('')
  const [input, setInput] = useState('')
  const [busy, setBusy] = useState<'' | 'save' | 'test' | 'clear'>('')
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(
    null
  )

  useEffect(() => {
    api
      .getCopyTradeProxy()
      .then((r) => setCurrent(r.proxy))
      .catch(() => {})
  }, [])

  const test = async () => {
    setBusy('test')
    setResult(null)
    try {
      const r = await api.testCopyTradeProxy(input.trim())
      setResult(
        r.ok
          ? {
              ok: true,
              text: tt(
                `Connected to Binance in ${r.latency_ms} ms (${r.traders} lead traders).`,
                `连接币安成功，用时 ${r.latency_ms} 毫秒（共 ${r.traders} 位带单员）。`
              ),
            }
          : {
              ok: false,
              text: tt(`Failed: ${r.error}`, `连接失败：${localizeNotice(r.error || 'Test failed', 'zh')}`),
            }
      )
    } catch (e) {
      setResult({ ok: false, text: e instanceof Error ? e.message : String(e) })
    } finally {
      setBusy('')
    }
  }

  const save = async (value: string) => {
    setBusy(value ? 'save' : 'clear')
    try {
      const r = await api.setCopyTradeProxy(value)
      setCurrent(r.proxy)
      setInput('')
      setResult(null)
      notify.success(
        value
          ? tt('Proxy saved', '代理已保存')
          : tt(
              'Proxy removed; using the server IP',
              '已清除代理，改用服务器 IP'
            )
      )
    } catch (e) {
      notify.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy('')
    }
  }

  const btn =
    'rounded-xl border border-[rgba(25,36,52,0.14)] px-4 py-2.5 text-sm text-nofx-text hover:border-nofx-gold disabled:opacity-50'
  return (
    <div className="space-y-4">
      <div>
        <h3 className="mb-1 text-sm font-semibold text-nofx-text">
          {tt('Copy-trade data proxy', '带单监控数据代理')}
        </h3>
        <p className="text-xs text-nofx-text-muted">
          {tt(
            'The copy-trade monitor reads binance.com pages. Route it through another IP so a Binance rate limit never touches the IP your traders use. Leave empty to use the server IP. Trading and market data do not use this proxy.',
            '带单监控需要读取币安网页数据。走另一个 IP，即使被币安限流，也不会影响交易员使用的服务器 IP。不填就用服务器 IP。交易和行情数据不走这个代理。'
          )}
        </p>
      </div>
      <div className="rounded-xl bg-nofx-bg-deeper px-4 py-3 text-sm">
        <span className="text-nofx-text-muted">
          {tt('Current: ', '当前：')}
        </span>
        <span className="font-mono text-nofx-text">
          {current || tt('server IP (no proxy)', '服务器 IP（未设置代理）')}
        </span>
      </div>
      <div>
        <label className="mb-2 block text-xs font-medium text-nofx-text-muted">
          {tt('New proxy', '新代理地址')}
        </label>
        <input
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder="http://user:password@1.2.3.4:8080"
          autoComplete="off"
          spellCheck={false}
          className="w-full rounded-xl border border-[rgba(25,36,52,0.14)] bg-nofx-bg-deeper px-4 py-3 font-mono text-sm text-nofx-text outline-hidden focus:border-nofx-gold/60"
        />
        <p className="mt-1 text-xs text-nofx-text-muted">
          {tt(
            'Supports http://, https://, socks5:// and socks5h://. Username and password are optional and stored encrypted.',
            '支持 http://、https://、socks5://、socks5h://。用户名和密码可选，会加密保存。'
          )}
        </p>
      </div>
      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          className={btn}
          disabled={Boolean(busy)}
          onClick={() => void test()}
        >
          {busy === 'test' ? (
            <Loader2 className="mr-1 inline h-4 w-4 animate-spin" />
          ) : null}
          {input.trim()
            ? tt('Test this proxy', '测试这个代理')
            : tt('Test the server IP', '测试服务器 IP')}
        </button>
        <button
          type="button"
          className="rounded-xl bg-nofx-gold px-4 py-2.5 text-sm font-semibold text-white hover:bg-nofx-gold-highlight disabled:opacity-50"
          disabled={Boolean(busy) || !input.trim()}
          onClick={() => void save(input.trim())}
        >
          {tt('Save', '保存')}
        </button>
        {current ? (
          <button
            type="button"
            className={btn}
            disabled={Boolean(busy)}
            onClick={() => void save('')}
          >
            {tt('Remove proxy', '清除代理')}
          </button>
        ) : null}
      </div>
      {result ? (
        <p
          role="status"
          className={`text-sm ${result.ok ? 'text-nofx-success' : 'text-nofx-danger'}`}
        >
          <NoticeText text={result.text} />
        </p>
      ) : null}
    </div>
  )
}
