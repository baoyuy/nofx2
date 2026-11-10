import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Trash2 } from 'lucide-react'
import { useLanguage } from '../contexts/LanguageContext'
import { api } from '../lib/api'
import type { CoinPool, CoinPoolCoin } from '../lib/api/coinPool'
import { confirmToast, notify } from '../lib/notify'
import { ROUTES } from '../router/paths'
import {
  BollingerScreener,
  directionBadge,
} from '../components/coinpool/BollingerScreener'
import { tr, ui } from '../components/market/marketUi'

// Each screener is one sub-page. Add new screeners to this list.
const screeners = [
  {
    id: 'bollinger_squeeze',
    zh: '布林喇叭口',
    en: 'Bollinger horn',
    hintZh: '涨幅榜前 50 · 收窄后张口',
    hintEn: 'Top 50 gainers · squeeze then expansion',
  },
] as const
type ScreenerID = (typeof screeners)[number]['id']

const MAX_STRATEGY_COINS = 10

type Draft = { coins: CoinPoolCoin[]; interval: string; screener: ScreenerID }

export function CoinPoolPage() {
  const { language } = useLanguage()
  const navigate = useNavigate()
  const [active, setActive] = useState<ScreenerID>('bollinger_squeeze')
  const [pools, setPools] = useState<CoinPool[]>([])
  const [draft, setDraft] = useState<Draft | null>(null)
  const [name, setName] = useState('')
  const [target, setTarget] = useState('new')
  const [saving, setSaving] = useState(false)

  const loadPools = useCallback(async () => {
    try {
      setPools(await api.getCoinPools())
    } catch (e) {
      notify.error(e instanceof Error ? e.message : String(e))
    }
  }, [])
  useEffect(() => {
    void loadPools()
  }, [loadPools])

  const screener = screeners.find((s) => s.id === active)!
  const startSave = (coins: CoinPoolCoin[], interval: string) => {
    const stamp = new Date().toLocaleString(
      language === 'zh' ? 'zh-CN' : 'en-US',
      {
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
      }
    )
    setDraft({ coins, interval, screener: active })
    setName(`${tr(language, screener.zh, screener.en)} ${interval} ${stamp}`)
    setTarget('new')
  }

  const save = async () => {
    if (!draft) return
    setSaving(true)
    try {
      const input = {
        name:
          target === 'new'
            ? name
            : pools.find((p) => p.id === target)?.name || name,
        screener: draft.screener,
        interval: draft.interval,
        coins: draft.coins,
      }
      if (target === 'new') await api.createCoinPool(input)
      else await api.updateCoinPool(target, input)
      notify.success(tr(language, '币池已保存', 'Coin pool saved'))
      setDraft(null)
      await loadPools()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  const remove = async (pool: CoinPool) => {
    const ok = await confirmToast(
      tr(
        language,
        `删除币池「${pool.name}」？正在使用它的策略会改用保存时的币种快照。`,
        `Delete "${pool.name}"? Strategies using it fall back to the coins saved with them.`
      ),
      {
        title: tr(language, '确认删除', 'Confirm delete'),
        okText: tr(language, '删除', 'Delete'),
        cancelText: tr(language, '取消', 'Cancel'),
      }
    )
    if (!ok) return
    try {
      await api.deleteCoinPool(pool.id)
      await loadPools()
    } catch (e) {
      notify.error(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <div className="mx-auto max-w-[1400px] px-3 py-4 md:px-5">
      <header className="mb-4">
        <h1 className="text-2xl font-bold text-nofx-text">
          {tr(language, '币池', 'Coin pool')}
        </h1>
        <p className={ui.muted}>
          {tr(
            language,
            '用不同的选币策略筛选币安合约，把选中的币存成币池，再在「策略」页把币池设为交易币种。',
            'Screen Binance futures with different strategies, save picks as a coin pool, then choose the pool as the coin source on the Strategy page.'
          )}
        </p>
      </header>

      <nav
        className="mb-4 flex flex-wrap gap-2"
        aria-label={tr(language, '选币策略', 'Screeners')}
      >
        {screeners.map((s) => (
          <button
            key={s.id}
            type="button"
            aria-pressed={s.id === active}
            onClick={() => setActive(s.id)}
            className={`rounded-lg border px-3 py-2 text-left ${s.id === active ? 'border-nofx-gold bg-nofx-gold/10' : `${ui.border} bg-nofx-bg-lighter hover:border-nofx-gold`}`}
          >
            <span className="block text-sm font-semibold text-nofx-text">
              {tr(language, s.zh, s.en)}
            </span>
            <span className="block text-[11px] text-nofx-text-muted">
              {tr(language, s.hintZh, s.hintEn)}
            </span>
          </button>
        ))}
      </nav>

      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_340px]">
        <div className="min-w-0">
          {active === 'bollinger_squeeze' ? (
            <BollingerScreener language={language} onSave={startSave} />
          ) : null}
        </div>

        <aside className="space-y-3">
          {draft ? (
            <section className={ui.panel}>
              <h2 className={ui.title}>
                {tr(
                  language,
                  `保存 ${draft.coins.length} 个币`,
                  `Save ${draft.coins.length} coins`
                )}
              </h2>
              <label className={ui.label}>
                {tr(language, '保存到', 'Save to')}
              </label>
              <select
                className={`${ui.input} mb-2`}
                value={target}
                onChange={(e) => setTarget(e.target.value)}
              >
                <option value="new">
                  {tr(language, '新建币池', 'A new coin pool')}
                </option>
                {pools.map((p) => (
                  <option key={p.id} value={p.id}>
                    {tr(language, '替换：', 'Replace: ')}
                    {p.name}
                  </option>
                ))}
              </select>
              {target === 'new' ? (
                <>
                  <label className={ui.label}>
                    {tr(language, '名称', 'Name')}
                  </label>
                  <input
                    className={`${ui.input} mb-2`}
                    value={name}
                    maxLength={60}
                    onChange={(e) => setName(e.target.value)}
                  />
                </>
              ) : null}
              {draft.coins.length > MAX_STRATEGY_COINS ? (
                <p className="mb-2 text-xs" style={{ color: ui.warn }}>
                  {tr(
                    language,
                    `策略每轮最多交易 ${MAX_STRATEGY_COINS} 个币，会按评分取前 ${MAX_STRATEGY_COINS} 个。`,
                    `A strategy trades at most ${MAX_STRATEGY_COINS} coins per cycle: the top ${MAX_STRATEGY_COINS} by score.`
                  )}
                </p>
              ) : null}
              <div className="flex gap-2">
                <button
                  type="button"
                  className={ui.btnGold}
                  disabled={saving || (target === 'new' && !name.trim())}
                  onClick={() => void save()}
                >
                  {tr(language, '保存', 'Save')}
                </button>
                <button
                  type="button"
                  className={ui.btnGhost}
                  onClick={() => setDraft(null)}
                >
                  {tr(language, '取消', 'Cancel')}
                </button>
              </div>
            </section>
          ) : null}

          <section className={ui.panel}>
            <div className="mb-2 flex items-center">
              <h2 className={`${ui.title} mb-0`}>
                {tr(language, '我的币池', 'My coin pools')}
              </h2>
              <button
                type="button"
                className={`${ui.btnGhost} ml-auto`}
                onClick={() => navigate(ROUTES.strategy)}
              >
                {tr(language, '去策略页使用', 'Use in a strategy')}
              </button>
            </div>
            {pools.length === 0 ? (
              <p className={ui.muted}>
                {tr(
                  language,
                  '还没有币池。在左侧勾选币后点「保存到币池」。',
                  'No coin pools yet. Pick coins on the left and save them.'
                )}
              </p>
            ) : null}
            <ul className="space-y-3">
              {pools.map((p) => (
                <li key={p.id} className={`rounded-lg border ${ui.border} p-2`}>
                  <div className="flex items-start gap-2">
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-semibold text-nofx-text">
                        {p.name}
                      </p>
                      <p className={ui.muted}>
                        {p.coins.length} {tr(language, '个币', 'coins')}
                        {p.interval ? ` · ${p.interval}` : ''} ·{' '}
                        {new Date(p.updated_at).toLocaleString()}
                      </p>
                    </div>
                    <button
                      type="button"
                      aria-label={tr(language, '删除币池', 'Delete coin pool')}
                      className="text-nofx-text-muted hover:text-[#d6433a]"
                      onClick={() => void remove(p)}
                    >
                      <Trash2 className="h-4 w-4" />
                    </button>
                  </div>
                  <div className="mt-2 flex flex-wrap gap-1">
                    {p.coins.map((c) => (
                      <span
                        key={c.symbol}
                        title={c.note}
                        className={`flex items-center gap-1 rounded border ${ui.border} px-1.5 py-0.5 text-xs text-nofx-text`}
                      >
                        {c.symbol.replace(/USDT$/, '')}
                        {directionBadge(language, c.direction)}
                        {c.score ? (
                          <span className={ui.muted}>{c.score.toFixed(0)}</span>
                        ) : null}
                      </span>
                    ))}
                  </div>
                </li>
              ))}
            </ul>
          </section>
        </aside>
      </div>
    </div>
  )
}
