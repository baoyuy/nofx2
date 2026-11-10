import { createContext, useContext, useState, ReactNode } from 'react'
import type { Language } from '../i18n/translations'
import { getDisplayLanguage, setDisplayLanguage } from '../i18n/displayLanguage'

interface LanguageContextType {
  language: Language
  setLanguage: (lang: Language) => void
}

const LanguageContext = createContext<LanguageContextType | undefined>(
  undefined
)

export function LanguageProvider({ children }: { children: ReactNode }) {
  const [language, setLanguageState] = useState<Language>(getDisplayLanguage)

  const setLanguage = (lang: Language) => {
    setLanguageState(lang)
    setDisplayLanguage(lang)
  }

  return (
    <LanguageContext.Provider value={{ language, setLanguage }}>
      {children}
    </LanguageContext.Provider>
  )
}

export function useLanguage() {
  const context = useContext(LanguageContext)
  if (!context) {
    throw new Error('useLanguage must be used within LanguageProvider')
  }
  return context
}
