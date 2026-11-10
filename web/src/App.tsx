import { ConfirmDialogProvider } from './components/common/ConfirmDialog'
import { LanguageSwitcher } from './components/common/LanguageSwitcher'
import { AuthProvider } from './contexts/AuthContext'
import { LanguageProvider } from './contexts/LanguageContext'
import { AppRoutes } from './router/AppRoutes'

export default function App() {
  return (
    <LanguageProvider>
      <AuthProvider>
        <ConfirmDialogProvider>
          <LanguageSwitcher />
          <AppRoutes />
        </ConfirmDialogProvider>
      </AuthProvider>
    </LanguageProvider>
  )
}
