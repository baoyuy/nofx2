import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter, useLocation } from 'react-router-dom'
import HeaderBar from '../components/common/HeaderBar'
import { getCurrentPageForPath, PAGE_PATHS, ROUTES } from './paths'

afterEach(cleanup)
function Location() {
  return <output>{useLocation().pathname}</output>
}

describe('events navigation', () => {
  it('maps the events route and puts its tab immediately after dashboard', () => {
    expect(ROUTES.events).toBe('/events')
    expect(PAGE_PATHS.events).toBe('/events')
    expect(getCurrentPageForPath('/events')).toBe('events')
    render(
      <MemoryRouter initialEntries={['/events']}>
        <HeaderBar isLoggedIn language="en" />
        <Location />
      </MemoryRouter>
    )
    const events = screen.getByRole('button', { name: 'Events' })
    expect(events.getAttribute('aria-current')).toBe('page')
    expect(events.previousElementSibling?.textContent).toMatch(/dashboard/i)
    fireEvent.click(events)
    expect(screen.getByRole('status').textContent).toBe('/events')
  })
  it('requires login before opening the events tab', () => {
    const onLoginRequired = vi.fn()
    render(
      <MemoryRouter>
        <HeaderBar language="zh" onLoginRequired={onLoginRequired} />
        <Location />
      </MemoryRouter>
    )
    fireEvent.click(screen.getByRole('button', { name: '事件' }))
    expect(onLoginRequired).toHaveBeenCalledWith('事件')
    expect(screen.getByRole('status').textContent).toBe('/')
  })
})
