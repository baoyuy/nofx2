// Class names shared by the market page panels, using the NOFX light theme
// tokens (bg-nofx-*, text-nofx-*) so it matches the rest of the app.
export const ui = {
  panel:
    'rounded-lg border border-[rgba(25,36,52,0.14)] bg-nofx-bg-lighter p-3',
  title: 'mb-2 text-sm font-semibold text-nofx-text',
  label: 'mb-1 block text-xs text-nofx-text-muted',
  input:
    'w-full rounded-lg border border-[rgba(25,36,52,0.14)] bg-nofx-bg-deeper px-2 py-1.5 text-sm text-nofx-text outline-hidden focus:border-nofx-gold',
  // Same look as input without the full width, for inline selects.
  inputInline:
    'rounded-lg border border-[rgba(25,36,52,0.14)] bg-nofx-bg-deeper px-2 py-1.5 text-sm text-nofx-text outline-hidden focus:border-nofx-gold',
  btnGold:
    'w-full rounded-lg bg-nofx-gold px-3 py-2 text-sm font-semibold text-white hover:bg-nofx-gold-highlight disabled:cursor-not-allowed disabled:opacity-50',
  btnGhost:
    'rounded-lg border border-[rgba(25,36,52,0.14)] px-2 py-1 text-xs text-nofx-text hover:border-nofx-gold disabled:opacity-50',
  muted: 'text-xs text-nofx-text-muted',
  text: 'text-nofx-text',
  warn: '#b45309',
  border: 'border-[rgba(25,36,52,0.14)]',
}

export const tr = (language: string, zh: string, en: string) =>
  language === 'zh' ? zh : en

export function loadPref(key: string, fallback: string): string {
  try {
    return localStorage.getItem(`nofx.market.${key}`) || fallback
  } catch {
    return fallback
  }
}

// AI analyses are saved by the backend (one per symbol per user). The
// browser-only copy from the previous version is cleared once.
export function clearLegacyAnalyses() {
  try {
    localStorage.removeItem('nofx.market.analyses')
  } catch {
    // storage unavailable: nothing to clear
  }
}

// mergeAnalyses keeps the newer result per symbol, so an analysis that
// finishes while the saved list is still loading is not overwritten.
export function mergeAnalyses<T extends { analyzed_at: number }>(
  current: Record<string, T>,
  incoming: Record<string, T>
): Record<string, T> {
  const next = { ...current }
  for (const [symbol, r] of Object.entries(incoming)) {
    if (!next[symbol] || next[symbol].analyzed_at < r.analyzed_at)
      next[symbol] = r
  }
  return next
}

export function timeAgo(language: string, unixSeconds: number): string {
  const mins = Math.max(0, Math.floor((Date.now() / 1000 - unixSeconds) / 60))
  if (mins < 1) return tr(language, '刚刚', 'just now')
  if (mins < 60) return tr(language, `${mins} 分钟前`, `${mins} min ago`)
  const hours = Math.floor(mins / 60)
  if (hours < 24) return tr(language, `${hours} 小时前`, `${hours} h ago`)
  return tr(
    language,
    `${Math.floor(hours / 24)} 天前`,
    `${Math.floor(hours / 24)} d ago`
  )
}

export function savePref(key: string, value: string) {
  try {
    localStorage.setItem(`nofx.market.${key}`, value)
  } catch {
    // storage unavailable: preferences just are not remembered
  }
}
