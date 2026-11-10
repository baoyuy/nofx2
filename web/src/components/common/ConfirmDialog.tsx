import React, {
  createContext,
  useContext,
  useState,
  useCallback,
  useEffect,
} from 'react'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogTitle,
} from '../ui/alert-dialog'
import { setGlobalConfirm } from '../../lib/notify'
import { localizeNotice } from '../../i18n/notices'
import { useLanguage } from '../../contexts/LanguageContext'

interface ConfirmOptions {
  title?: string
  message: string
  okText?: string
  cancelText?: string
}

interface ConfirmDialogContextType {
  confirm: (options: ConfirmOptions) => Promise<boolean>
}

const ConfirmDialogContext = createContext<
  ConfirmDialogContextType | undefined
>(undefined)

export function useConfirmDialog() {
  const context = useContext(ConfirmDialogContext)
  if (!context) {
    throw new Error(
      'useConfirmDialog must be used within ConfirmDialogProvider'
    )
  }
  return context
}

interface ConfirmState {
  isOpen: boolean
  title?: string
  message: string
  okText: string
  cancelText: string
  resolve?: (value: boolean) => void
}

export function ConfirmDialogProvider({
  children,
}: {
  children: React.ReactNode
}) {
  const { language } = useLanguage()
  const defaultOkText = language === 'zh' ? '确认' : 'Confirm'
  const defaultCancelText = language === 'zh' ? '取消' : 'Cancel'
  const [state, setState] = useState<ConfirmState>({
    isOpen: false,
    message: '',
    okText: 'Confirm',
    cancelText: 'Cancel',
  })

  const confirm = useCallback(
    (options: ConfirmOptions): Promise<boolean> => {
      return new Promise((resolve) => {
        setState({
          isOpen: true,
          title: options.title,
          message: options.message,
          okText: options.okText || defaultOkText,
          cancelText: options.cancelText || defaultCancelText,
          resolve,
        })
      })
    },
    [defaultOkText, defaultCancelText]
  )

  // Register global confirm function
  useEffect(() => {
    setGlobalConfirm(confirm)
  }, [confirm])

  const handleClose = useCallback((result: boolean) => {
    setState((prev) => {
      prev.resolve?.(result)
      return {
        ...prev,
        isOpen: false,
      }
    })
  }, [])

  return (
    <ConfirmDialogContext.Provider value={{ confirm }}>
      {children}
      <AlertDialog
        open={state.isOpen}
        onOpenChange={(open) => !open && handleClose(false)}
      >
        <AlertDialogContent>
          <div className="flex flex-col gap-5 text-center">
            {state.title && (
              <AlertDialogTitle className="text-xl">
                {state.title}
              </AlertDialogTitle>
            )}
            <AlertDialogDescription className="text-(--text-primary) text-base font-medium">
              {state.message}
            </AlertDialogDescription>
          </div>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={() => handleClose(false)}>
              {localizeNotice(state.cancelText, language, 'info')}
            </AlertDialogCancel>
            <AlertDialogAction onClick={() => handleClose(true)}>
              {localizeNotice(state.okText, language, 'info')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </ConfirmDialogContext.Provider>
  )
}
