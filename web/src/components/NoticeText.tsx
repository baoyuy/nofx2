import { localizeNotice } from '../i18n/notices'

// Parents already receive the active language through context or props. Do not
// require a provider here: wallet/setup components can also render standalone.
export function NoticeText({
  text,
  language,
}: {
  text: string
  language?: string
}) {
  return <>{localizeNotice(text, language)}</>
}
