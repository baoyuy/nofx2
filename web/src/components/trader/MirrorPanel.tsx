import { Component, useState, type ReactNode } from 'react'
import useSWR from 'swr'
import { Loader2 } from 'lucide-react'
import { api } from '../../lib/api'
import type { MirrorAccount, MirrorLink } from '../../lib/api/mirror'
import { confirmToast, notify } from '../../lib/notify'
import { tr, ui } from '../market/marketUi'
import { MirrorSettingsForm } from './MirrorSettingsForm'
import {
  DEFAULT_MIRROR,
  actionLabel,
  describeLink,
  eventTone,
  fromForm,
  settingsOf,
  skipLabel,
  stopReasonLabel,
  toForm,
  type MirrorForm,
} from './mirrorFormat'

const errorText = (e: unknown) => (e instanceof Error ? e.message : String(e))

const fmt = (v: number, d = 2) => (Number.isFinite(v) ? v.toFixed(d) : '--')

const signed = (v: number) => `${v >= 0 ? '+' : ''}${fmt(v)}`

/** Keeps a crash in the copy panel from blanking the whole dashboard. */
class PanelBoundary extends Component<
  { language: string; children: ReactNode },
  { error?: Error }
> {
  state: { error?: Error } = {}
  static getDerivedStateFromError(error: Error) {
    return { error }
  }
  render() {
    if (!this.state.error) return this.props.children
    return (
      <p role="alert" className="text-xs text-nofx-danger">
        {tr(this.props.language, '跟单区域出错：', 'Copy trading failed: ')}
        {this.state.error.message}
      </p>
    )
  }
}

/**
 * Copy trading on the dashboard: other Binance accounts of the user (real
 * or paper) copy the selected AI trader's positions.
 */
export function MirrorPanel(props: Parameters<typeof MirrorPanelInner>[0]) {
  return (
    <PanelBoundary language={props.language}>
      <MirrorPanelInner {...props} />
    </PanelBoundary>
  )
}

function MirrorPanelInner({
  language,
  traderId,
  traderExchangeId,
  className = 'nofx-glass mb-6 p-6',
}: {
  language: string
  traderId: string
  traderExchangeId?: string
  className?: string
}) {
  const {
    data: links,
    error,
    mutate,
  } = useSWR<MirrorLink[]>(
    `mirror-links-${traderId}`,
    () => api.getMirrorLinks(traderId),
    { refreshInterval: 10000 }
  )
  const [adding, setAdding] = useState(false)
  return (
    <section className={className}>
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <h2 className="text-lg font-bold uppercase tracking-wide text-nofx-text-main">
          <span className="text-nofx-gold">⇉</span>{' '}
          {tr(language, '跟单', 'Copy trading')}
        </h2>
        <span className={ui.muted}>
          {tr(
            language,
            '用你的其他币安账户（实盘或模拟盘）跟这个 AI 下单。AI 一下单就跟，开始跟单前 AI 已有的仓位不跟。',
            "Your other Binance accounts (real or paper) copy this AI's orders as they happen. Positions open before the start are not copied."
          )}
        </span>
        {links && !adding ? (
          <button
            type="button"
            className={`${ui.btnGhost} ml-auto`}
            onClick={() => setAdding(true)}
          >
            {tr(language, '+ 新建跟单', '+ New copy')}
          </button>
        ) : null}
      </div>
      {!links ? (
        error ? (
          <p role="alert" className="text-xs text-nofx-danger">
            {tr(language, '跟单数据加载失败：', 'Could not load copies: ')}
            {errorText(error)}
          </p>
        ) : (
          <Loader2 className="h-4 w-4 animate-spin text-nofx-text-muted" />
        )
      ) : null}
      {links && adding ? (
        <NewLink
          language={language}
          traderId={traderId}
          traderExchangeId={traderExchangeId}
          taken={(links || []).map((l) => l.exchange_id)}
          onDone={() => {
            setAdding(false)
            void mutate()
          }}
        />
      ) : null}
      {links && links.length === 0 && !adding ? (
        <p className={ui.muted}>
          {tr(language, '还没有跟单账户。', 'No copies yet.')}
        </p>
      ) : null}
      <div className="grid gap-3">
        {(links || []).map((l) => (
          <LinkCard
            key={l.id}
            language={language}
            link={l}
            onChanged={() => void mutate()}
          />
        ))}
      </div>
    </section>
  )
}

function NewLink({
  language,
  traderId,
  traderExchangeId,
  taken,
  onDone,
}: {
  language: string
  traderId: string
  traderExchangeId?: string
  taken: string[]
  onDone: () => void
}) {
  const { data: accounts } = useSWR<MirrorAccount[]>(
    'mirror-accounts',
    api.getMirrorAccounts
  )
  const [exchangeId, setExchangeId] = useState('')
  const [form, setForm] = useState<MirrorForm>(toForm(DEFAULT_MIRROR))
  const [saving, setSaving] = useState(false)
  const usable = (accounts || []).filter(
    (a) => a.id !== traderExchangeId && !taken.includes(a.id)
  )
  const picked = usable.find((a) => a.id === exchangeId)

  const submit = async () => {
    const s = fromForm(language, form)
    if (typeof s === 'string') {
      notify.error(s)
      return
    }
    if (!picked) {
      notify.error(tr(language, '请选择跟单账户', 'Pick an account'))
      return
    }
    if (!picked.paper) {
      const ok = await confirmToast(
        tr(
          language,
          `「${picked.account_name}」是实盘账户，跟单会用真钱下单。确定开始？`,
          `"${picked.account_name}" is a real account: copies trade real money. Start?`
        ),
        {
          title: tr(language, '实盘跟单', 'Real-money copy'),
          okText: tr(language, '开始跟单', 'Start'),
          cancelText: tr(language, '取消', 'Cancel'),
        }
      )
      if (!ok) return
    }
    setSaving(true)
    try {
      await api.createMirrorLink({
        ...s,
        leader_id: traderId,
        exchange_id: picked.id,
        confirm_real: !picked.paper,
      })
      notify.success(tr(language, '已开始跟单', 'Copying started'))
      onDone()
    } catch (e) {
      notify.error(errorText(e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className={`${ui.panel} mb-3 grid gap-3`}>
      <label className="block">
        <span className={ui.label}>{tr(language, '跟单账户', 'Account')}</span>
        <select
          className={ui.input}
          value={exchangeId}
          onChange={(e) => setExchangeId(e.target.value)}
          aria-label={tr(language, '跟单账户', 'Account')}
        >
          <option value="">
            {tr(language, '选择币安账户', 'Pick a Binance account')}
          </option>
          {usable.map((a) => (
            <option key={a.id} value={a.id}>
              {a.account_name}
              {a.paper
                ? tr(language, '（模拟盘）', ' (paper)')
                : tr(language, '（实盘）', ' (real)')}
              {a.used_by.length
                ? tr(
                    language,
                    ` · 交易员：${a.used_by.join('、')}`,
                    ` · traders: ${a.used_by.join(', ')}`
                  )
                : ''}
            </option>
          ))}
        </select>
        <span className={`${ui.muted} mt-0.5 block`}>
          {accounts && usable.length === 0
            ? tr(
                language,
                '没有可用的币安账户：请先在「配置」里添加一个币安或币安模拟盘账户。',
                'No Binance account to use: add a Binance or Binance paper account under Config first.'
              )
            : tr(
                language,
                '跟单账户不能同时被正在运行的 AI 交易员使用，否则会互相平仓。',
                'The account must not be used by a running AI trader, or they would close each other.'
              )}
        </span>
      </label>
      {picked && !picked.paper ? (
        <p className="text-xs font-semibold" style={{ color: ui.warn }}>
          {tr(
            language,
            '这是实盘账户，跟单会用真钱下单。',
            'This is a real account: copies trade real money.'
          )}
        </p>
      ) : null}
      <MirrorSettingsForm language={language} value={form} onChange={setForm} />
      <div className="flex gap-2">
        <button
          type="button"
          className={ui.btnGold}
          disabled={saving || !picked}
          onClick={() => void submit()}
        >
          {saving ? (
            <Loader2 className="mx-auto h-4 w-4 animate-spin" />
          ) : (
            tr(language, '开始跟单', 'Start copying')
          )}
        </button>
        <button type="button" className={ui.btnGhost} onClick={onDone}>
          {tr(language, '取消', 'Cancel')}
        </button>
      </div>
    </div>
  )
}

function LinkCard({
  language,
  link,
  onChanged,
}: {
  language: string
  link: MirrorLink
  onChanged: () => void
}) {
  const [busy, setBusy] = useState(false)
  const [editing, setEditing] = useState<MirrorForm | null>(null)
  const [showEvents, setShowEvents] = useState(false)
  const st = link.status
  const copied = (link.positions || []).filter(
    (p) => !p.skip && p.follower_qty > 0
  )
  const skipped = (link.positions || []).filter((p) => p.skip)

  const act = async (f: () => Promise<unknown>, done?: string) => {
    setBusy(true)
    try {
      await f()
      if (done) notify.success(done)
      onChanged()
    } catch (e) {
      notify.error(errorText(e))
    } finally {
      setBusy(false)
    }
  }
  const stop = async (closePositions: boolean) => {
    const ok = await confirmToast(
      closePositions
        ? tr(
            language,
            `停止跟单，并平掉「${link.account_name}」上 ${copied.length} 个跟单仓位？`,
            `Stop and close the ${copied.length} copied positions on "${link.account_name}"?`
          )
        : tr(
            language,
            `停止跟单？「${link.account_name}」上的跟单仓位会留着，之后不再跟 AI 加减仓。重新开始后，AI 平仓时仍会跟着平。`,
            `Stop copying? The copies on "${link.account_name}" stay and no longer follow the AI; after a restart they close when the AI closes.`
          ),
      {
        title: tr(language, '停止跟单', 'Stop copying'),
        okText: tr(language, '确定', 'OK'),
        cancelText: tr(language, '取消', 'Cancel'),
      }
    )
    if (!ok) return
    await act(
      async () => {
        const r = await api.stopMirrorLink(link.id, closePositions)
        if (r.close_failed?.length)
          notify.error(
            tr(language, '有仓位没平掉：', 'Some did not close: ') +
              r.close_failed.join('; ')
          )
      },
      tr(language, '已停止', 'Stopped')
    )
  }
  const start = async () => {
    if (!link.paper) {
      const ok = await confirmToast(
        tr(
          language,
          `「${link.account_name}」是实盘账户，跟单会用真钱下单。确定开始？`,
          `"${link.account_name}" is a real account: copies trade real money. Start?`
        ),
        {
          title: tr(language, '实盘跟单', 'Real-money copy'),
          okText: tr(language, '开始跟单', 'Start'),
          cancelText: tr(language, '取消', 'Cancel'),
        }
      )
      if (!ok) return
    }
    await act(
      () => api.startMirrorLink(link.id, !link.paper),
      tr(language, '已开始跟单', 'Copying started')
    )
  }
  const remove = async () => {
    const ok = await confirmToast(
      tr(
        language,
        '删除这个跟单？账户上的仓位不会动，记录会删掉。',
        'Delete this copy? Positions on the account stay; its history goes.'
      ),
      {
        title: tr(language, '删除跟单', 'Delete copy'),
        okText: tr(language, '删除', 'Delete'),
        cancelText: tr(language, '取消', 'Cancel'),
      }
    )
    if (ok) await act(() => api.deleteMirrorLink(link.id))
  }
  const save = async () => {
    if (!editing) return
    const s = fromForm(language, editing)
    if (typeof s === 'string') {
      notify.error(s)
      return
    }
    await act(
      async () => {
        await api.updateMirrorLink(link.id, s)
        setEditing(null)
      },
      tr(language, '已保存，下一单起生效', 'Saved; applies from the next order')
    )
  }

  return (
    <div className={ui.panel}>
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-semibold text-nofx-text">
          {link.account_name || link.exchange_id}
        </span>
        <span
          className={`rounded px-1.5 text-[11px] ${link.paper ? 'bg-nofx-bg-deeper text-nofx-text-muted' : 'bg-nofx-danger/10 text-nofx-danger'}`}
        >
          {link.paper
            ? tr(language, '模拟盘', 'Paper')
            : tr(language, '实盘', 'Real')}
        </span>
        <span
          className={`rounded px-1.5 text-[11px] ${link.running ? 'bg-nofx-success/10 text-nofx-success' : 'bg-nofx-bg-deeper text-nofx-text-muted'}`}
        >
          {link.running
            ? tr(language, '跟单中', 'Copying')
            : tr(language, '已停止', 'Stopped')}
        </span>
        {!link.running && link.stop_reason ? (
          <span className={ui.muted}>
            {stopReasonLabel(language, link.stop_reason)}
          </span>
        ) : null}
        <span className="ml-auto flex flex-wrap gap-1">
          {link.running ? (
            <>
              <button
                type="button"
                className={ui.btnGhost}
                disabled={busy}
                onClick={() => void stop(false)}
              >
                {tr(language, '停止', 'Stop')}
              </button>
              <button
                type="button"
                className={ui.btnGhost}
                disabled={busy || copied.length === 0}
                onClick={() => void stop(true)}
              >
                {tr(language, '停止并平仓', 'Stop & close')}
              </button>
            </>
          ) : (
            <>
              <button
                type="button"
                className={ui.btnGhost}
                disabled={busy}
                onClick={() => void start()}
              >
                {tr(language, '开始', 'Start')}
              </button>
              <button
                type="button"
                className={ui.btnGhost}
                disabled={busy}
                onClick={() => void remove()}
              >
                {tr(language, '删除', 'Delete')}
              </button>
            </>
          )}
          <button
            type="button"
            className={ui.btnGhost}
            disabled={busy}
            onClick={() =>
              setEditing(editing ? null : toForm(settingsOf(link)))
            }
          >
            {editing
              ? tr(language, '收起设置', 'Close settings')
              : tr(language, '设置', 'Settings')}
          </button>
        </span>
      </div>
      <p className={`${ui.muted} mt-1`}>{describeLink(language, link)}</p>
      {st ? (
        <p className="mt-1 text-xs text-nofx-text">
          {tr(language, '账户权益', 'Equity')} {fmt(st.equity)} U ·{' '}
          {tr(language, '可用', 'Free')} {fmt(st.available)} U ·{' '}
          {tr(language, '跟单以来', 'Since start')}{' '}
          <span
            className={st.pnl >= 0 ? 'text-nofx-success' : 'text-nofx-danger'}
          >
            {signed(st.pnl)} U
          </span>
        </p>
      ) : null}
      {st?.error ? (
        <p role="alert" className="mt-1 text-xs text-nofx-danger">
          {st.error}
        </p>
      ) : null}
      {editing ? (
        <div className="mt-3 grid gap-3">
          <MirrorSettingsForm
            language={language}
            value={editing}
            onChange={setEditing}
          />
          <button
            type="button"
            className={ui.btnGold}
            disabled={busy}
            onClick={() => void save()}
          >
            {tr(language, '保存设置', 'Save settings')}
          </button>
        </div>
      ) : null}
      {st?.positions?.length ? (
        <table className="mt-2 w-full text-xs">
          <thead className={ui.muted}>
            <tr>
              <th className="text-left font-normal">
                {tr(language, '币', 'Coin')}
              </th>
              <th className="text-left font-normal">
                {tr(language, '方向', 'Side')}
              </th>
              <th className="text-right font-normal">
                {tr(language, '数量', 'Qty')}
              </th>
              <th className="text-right font-normal">
                {tr(language, '开仓价', 'Entry')}
              </th>
              <th className="text-right font-normal">
                {tr(language, '杠杆', 'Lev')}
              </th>
              <th className="text-right font-normal">
                {tr(language, '浮盈亏', 'uPnL')}
              </th>
            </tr>
          </thead>
          <tbody className="text-nofx-text">
            {(st.positions || []).map((p) => (
              <tr key={`${p.symbol}-${p.side}`}>
                <td>{p.symbol}</td>
                <td
                  className={
                    p.side === 'long' ? 'text-nofx-success' : 'text-nofx-danger'
                  }
                >
                  {p.side === 'long'
                    ? tr(language, '多', 'Long')
                    : tr(language, '空', 'Short')}
                </td>
                <td className="text-right">{p.qty}</td>
                <td className="text-right">{p.entry}</td>
                <td className="text-right">{p.leverage}x</td>
                <td
                  className={`text-right ${p.pnl >= 0 ? 'text-nofx-success' : 'text-nofx-danger'}`}
                >
                  {signed(p.pnl)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : link.running ? (
        <p className={`${ui.muted} mt-2`}>
          {tr(
            language,
            '暂无跟单仓位，等 AI 开新仓。',
            'No copies yet; waiting for the AI to open.'
          )}
        </p>
      ) : null}
      {skipped.length ? (
        <p className={`${ui.muted} mt-1`}>
          {skipped
            .map(
              (p) =>
                `${p.symbol} ${p.side === 'long' ? tr(language, '多', 'long') : tr(language, '空', 'short')}：${skipLabel(language, p.skip)}`
            )
            .join(tr(language, '；', '; '))}
        </p>
      ) : null}
      <button
        type="button"
        className="mt-1 text-xs text-nofx-gold hover:underline"
        onClick={() => setShowEvents(!showEvents)}
      >
        {showEvents
          ? tr(language, '收起记录', 'Hide history')
          : tr(language, '查看跟单记录', 'Show history')}
      </button>
      {showEvents ? <LinkEvents language={language} linkId={link.id} /> : null}
    </div>
  )
}

function LinkEvents({
  language,
  linkId,
}: {
  language: string
  linkId: string
}) {
  const { data } = useSWR(
    `mirror-events-${linkId}`,
    () => api.getMirrorEvents(linkId),
    {
      refreshInterval: 10000,
    }
  )
  if (!data)
    return (
      <Loader2 className="mt-1 h-3 w-3 animate-spin text-nofx-text-muted" />
    )
  if (!data.length)
    return (
      <p className={`${ui.muted} mt-1`}>
        {tr(language, '还没有记录', 'Nothing yet')}
      </p>
    )
  return (
    <div className="mt-1 max-h-64 overflow-y-auto text-xs">
      {data.map((e) => (
        <div key={e.id} className="flex flex-wrap gap-x-2">
          <span className={ui.muted}>{new Date(e.time).toLocaleString()}</span>
          <span className={eventTone(e)}>
            {actionLabel(language, e.action)}
          </span>
          {e.symbol ? (
            <span className="text-nofx-text">
              {e.symbol} {e.side}
            </span>
          ) : null}
          {e.qty ? <span className="text-nofx-text">{e.qty}</span> : null}
          {e.price ? <span className={ui.muted}>@ {e.price}</span> : null}
          {e.detail ? <span className={ui.muted}>{e.detail}</span> : null}
        </div>
      ))}
    </div>
  )
}
