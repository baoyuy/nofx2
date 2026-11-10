import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'
import { NoticeText } from './NoticeText'

const error =
  'Binance is rate limiting copy-trade requests; paused, retry later'
afterEach(cleanup)
it('renders Chinese backend errors without requiring a language provider', () => {
  render(
    <p role="alert">
      <NoticeText text={error} language="zh" />
    </p>
  )
  expect(screen.getByRole('alert')).toHaveTextContent(
    '币安带单数据请求触发限流保护'
  )
  expect(screen.getByRole('alert').textContent).not.toMatch(/[a-z]/i)
})
it('changes an already displayed error when the component language changes', () => {
  const view = render(<NoticeText text={error} language="zh" />)
  view.rerender(<NoticeText text={error} language="en" />)
  expect(screen.getByText(error)).toBeVisible()
})
