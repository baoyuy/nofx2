import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { LanguageProvider, useLanguage } from './LanguageContext'
import { getDisplayLanguage, setDisplayLanguage } from '../i18n/displayLanguage'

function LanguageProbe() {
  const { language, setLanguage } = useLanguage()
  return (
    <>
      <div data-testid="language">{language}</div>
      <button onClick={() => setLanguage(language === 'zh' ? 'en' : 'zh')}>
        切换语言
      </button>
    </>
  )
}

describe('language preference', () => {
  beforeEach(() => {
    setDisplayLanguage('zh')
    localStorage.clear()
  })
  afterEach(() => {
    cleanup()
    setDisplayLanguage('zh')
    localStorage.clear()
  })

  it('defaults to Chinese when no preference is saved', () => {
    render(
      <LanguageProvider>
        <LanguageProbe />
      </LanguageProvider>
    )
    expect(screen.getByTestId('language')).toHaveTextContent('zh')
  })

  it.each(['zh', 'en', 'id'])(
    'respects the saved %s preference',
    (language) => {
      localStorage.setItem('language', language)
      render(
        <LanguageProvider>
          <LanguageProbe />
        </LanguageProvider>
      )
      expect(screen.getByTestId('language')).toHaveTextContent(language)
    }
  )

  it('updates React, persistent storage and non-React notifications together', () => {
    render(
      <LanguageProvider>
        <LanguageProbe />
      </LanguageProvider>
    )
    fireEvent.click(screen.getByRole('button', { name: '切换语言' }))
    expect(screen.getByTestId('language')).toHaveTextContent('en')
    expect(localStorage.getItem('language')).toBe('en')
    expect(getDisplayLanguage()).toBe('en')
    fireEvent.click(screen.getByRole('button', { name: '切换语言' }))
    expect(getDisplayLanguage()).toBe('zh')
  })
})
