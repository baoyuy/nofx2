import { afterEach, expect, test, vi } from 'vitest'
import { randomUUID } from './uuid'

const v4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/

afterEach(() => vi.restoreAllMocks())

test('works without crypto.randomUUID (plain HTTP origin)', () => {
  Object.defineProperty(crypto, 'randomUUID', { value: undefined, configurable: true })
  const a = randomUUID()
  const b = randomUUID()
  expect(a).toMatch(v4)
  expect(a).not.toBe(b)
})
