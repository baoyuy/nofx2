import { cloneElement, isValidElement, type ReactNode } from 'react'
import { toast as sonnerToast } from 'sonner'
import { localizeNotice, type NoticeTone } from '../i18n/notices'

function localizeNode(value: unknown, tone: NoticeTone): unknown {
  if (typeof value === 'string') return localizeNotice(value, undefined, tone)
  if (typeof value === 'function') {
    return (...args: unknown[]) => localizeNode(value(...args), tone)
  }
  if (Array.isArray(value)) return value.map((item) => localizeNode(item, tone))
  if (
    isValidElement<{ children?: ReactNode }>(value) &&
    value.props.children !== undefined
  ) {
    return cloneElement(value, {
      children: localizeNode(value.props.children, tone) as ReactNode,
    })
  }
  return value
}

function localizeOptions(value: unknown, tone: NoticeTone): unknown {
  if (!value || typeof value !== 'object') return value
  const options = { ...value } as Record<string, unknown>
  for (const key of ['description', 'loading', 'success', 'error', 'message']) {
    if (options[key] !== undefined)
      options[key] = localizeNode(
        options[key],
        key === 'success' ? 'success' : tone
      )
  }
  for (const key of ['action', 'cancel']) {
    const action = options[key]
    if (action && typeof action === 'object' && 'label' in action) {
      options[key] = { ...action, label: localizeNode(action.label, tone) }
    }
  }
  return options
}

// Same callable API, IDs, options and return values as Sonner. Never alter
// dismissal IDs or custom renderer semantics. Resolve language at notification
// time, including asynchronous promise results, not when the module is imported.
export const toast: typeof sonnerToast = new Proxy(sonnerToast, {
  apply(target, thisArg, args) {
    return Reflect.apply(target, thisArg, [
      localizeNode(args[0], 'info'),
      localizeOptions(args[1], 'info'),
    ])
  },
  get(target, property, receiver) {
    const original = Reflect.get(target, property, receiver)
    const tone: NoticeTone =
      property === 'success'
        ? 'success'
        : property === 'warning'
          ? 'warning'
          : property === 'error'
            ? 'error'
            : 'info'
    if (
      ['success', 'error', 'warning', 'info', 'message', 'loading'].includes(
        String(property)
      )
    ) {
      return (...args: unknown[]) =>
        Reflect.apply(original, target, [
          localizeNode(args[0], tone),
          localizeOptions(args[1], tone),
        ])
    }
    if (property === 'promise') {
      return (...args: unknown[]) =>
        Reflect.apply(original, target, [
          args[0],
          localizeOptions(args[1], 'error'),
        ])
    }
    return original
  },
})
