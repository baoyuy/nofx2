import type { Language } from './translations'

let activeLanguage: Language = 'zh'

// Shared by React and non-React request/notification code. Read at display time,
// not module load time, so switching languages takes effect without a refresh.
export function getDisplayLanguage(): Language {
  try {
    const stored = localStorage.getItem('language')
    if (stored === 'zh' || stored === 'en' || stored === 'id') return stored
  } catch {
    // Private browsing / unavailable storage: use the in-memory selection.
  }
  return activeLanguage
}

export function setDisplayLanguage(language: Language) {
  activeLanguage = language
  try {
    localStorage.setItem('language', language)
  } catch {
    // The language selection must still work without persistent storage.
  }
}
