import { getDisplayLanguage } from './displayLanguage'

export type NoticeTone = 'error' | 'success' | 'warning' | 'info'

const exact: Record<string, string> = {
  'request timed out': '请求超时，请稍后重试',
  'network error': '网络连接异常，请检查网络或代理',
  'failed to fetch': '无法连接服务器，请检查网络或代理',
  'the upstream service took too long to respond':
    '上游服务响应超时，请稍后重试',
  'unable to reach the server': '无法连接服务器，请检查网络或代理',
  'session expired': '登录已过期，请重新登录',
  'permission denied': '没有操作权限',
  'you do not have permission to access this resource':
    '你没有访问此资源的权限',
  'api not found': '请求的接口不存在',
  'the requested endpoint does not exist (404)':
    '请求的接口不存在（错误码：404）',
  'server error': '服务器异常，请稍后重试',
  'please try again later or contact support':
    '请稍后重试；如持续出现，请联系维护人员',
  'operation failed': '操作失败，请稍后重试',
  'request failed': '请求失败，请稍后重试',
  'copy failed': '复制失败，请重试或手动复制',
  'save failed': '保存失败，请检查配置后重试',
  'trader custom_prompt/override_base_prompt are not supported for grid strategies; clear both settings or use an ai strategy':
    '网格策略暂不支持交易员独立提示词或覆盖基础规则，请清空这两项，或改用普通智能交易策略；本次设置未保存',
  'trader strategy is unavailable; no changes were saved':
    '关联策略不可用，请重新选择可访问的策略；本次配置未保存',
  'invalid trader strategy configuration; no changes were saved':
    '关联策略配置无效，请先修复策略；本次配置未保存',
  'stop the trader before detaching its strategy':
    '请先明确停止该交易员，再解除策略绑定；当前配置未修改',
  'test failed': '连接测试失败，请检查网络和配置',
  'follow failed': '关注失败，请稍后重试',
  'unfollow failed': '取消关注失败，请稍后重试',
  'already following this trader': '已经关注这位带单员，无需重复关注',
  'no lead trader with this id': '未找到对应的带单员，请检查编号或链接',
  'invalid proxy url': '代理地址格式不正确',
  'invalid proxy port': '代理端口不正确，请填写 1 至 65535 之间的端口',
  'proxy needs a host and a port, e.g. http://1.2.3.4:8080':
    '代理地址需要包含主机和端口，例如：http://127.0.0.1:7897',
  'proxy must start with http://, https://, socks5:// or socks5h://':
    '代理地址必须以 http://、https://、socks5:// 或 socks5h:// 开头',
  'proxy url must not have a path or query':
    '代理地址不能包含额外路径或查询参数',
  'enter a lead trader id or the link of their binance copy-trading page':
    '请输入带单员编号或币安带单主页链接',
  confirm: '确认',
  cancel: '取消',
  close: '关闭',
  retry: '重试',
  'loading…': '加载中…',
  'loading...': '加载中…',
  success: '操作成功',
  saved: '已保存',
}

// Ordered, task-specific explanations. Never print raw backend payloads or
// credentials as a fallback. This is a presentation adapter, not an error-code
// classifier: callers retain original codes/fields for programmatic decisions.
const rules: [RegExp, string | ((match: RegExpMatchArray) => string)][] = [
  [
    /stop trader .+ before modifying its grid strategy or switching AI\/grid strategy type; no changes were saved/i,
    '请先明确停止关联交易员，再修改网格策略或切换策略类型；本次修改未保存，也未自动停止或撤单。',
  ],
  [
    /paid request outcome unknown|payment result is unconfirmed/i,
    '付费请求的响应或结算结果尚未确认，不会自动重试。请先核对原付款，勿重复付款。',
  ],
  [
    /outcome unknown|submission.*uncertain|unresolved.*(?:execution|order)|(?:execution|order).*unresolved/i,
    '订单提交或执行结果尚未确认，请先到交易所核对订单和持仓，勿重复下单。',
  ],
  [
    /AI wallet.*(?:empty|insufficient|needs)|wallet.*(?:insufficient|not enough).*USDC/i,
    '模型费用钱包余额不足，请检查费用钱包；这与交易所账户资金相互独立。',
  ],
  [/invalid trend board response/i, '趋势榜返回的数据格式异常，暂时无法显示。'],
  [
    /request rejected by|user rejected|user denied/i,
    '你已在钱包中拒绝该请求，操作未完成。',
  ],
  [
    /binance is rate limiting copy-trade requests; paused, retry later|copy-trade data paused after binance rate limiting/i,
    '币安带单数据请求触发限流保护，已暂停获取，稍后将自动重试。',
  ],
  [
    /binance rate limited the request: connection dropped/i,
    '与币安的连接中断，已触发暂停保护，稍后将自动重试；请检查网络或代理。',
  ],
  [
    /binance rate limited the request: blocked by a verification page/i,
    '币安要求进行访问验证，已暂停获取数据，请检查网络或代理后稍候重试。',
  ],
  [
    /binance rate limited the request: (?:HTTP|code) (\d+)/i,
    (m) =>
      `币安限制了数据访问（错误码：${m[1]}），已暂停获取，稍后将自动重试。`,
  ],
  [
    /binance changed (?:the copy-trade data format|the data format)|positions without a symbol|fills without time|without a symbol or entry price/i,
    '币安返回的数据格式异常，部分必需字段缺失，相关数据暂时无法更新。',
  ],
  [
    /this is a private copy-trading portfolio/i,
    '这是私域带单账户，币安不公开它的成交和持仓，且未找到可关注的公域账户。',
  ],
  [
    /you can follow at most (\d+) traders/i,
    (m) => `最多可以关注 ${m[1]} 位带单员。`,
  ],
  [/api scheduler.*queue is full/i, '请求队列已满，请稍后重试。'],
  [/api scheduler.*budget unavailable/i, '接口请求额度暂时不足，请稍候重试。'],
  [
    /binance did not answer in time|context deadline exceeded|request timed out|timeout.*exceeded|timed? ?out/i,
    '请求超时，请检查网络或代理，稍后重试。',
  ],
  [
    /could not load (?:the )?binance (?:ticker|tickers|candles|symbols|depth)|could not screen binance|could not build .*binance|could not reach binance/i,
    '暂时无法获取币安行情，请检查网络或代理后重试。',
  ],
  [
    /connection reset|connection refused|connection dropped|network error|failed to fetch|could not reach|no such host|tls handshake|proxyconnect|\bEOF\b/i,
    '网络连接异常，请检查网络或代理后重试。',
  ],
  [
    /insufficient (?:funds|balance|margin)|not enough (?:funds|balance|margin)/i,
    '可用余额或保证金不足，请检查账户资金。',
  ],
  [
    /invalid api[- ]?key|api[- ]?key.*invalid|invalid signature|signature.*invalid/i,
    '接口密钥或签名验证失败，请检查凭证、权限和系统时间。',
  ],
  [
    /invalid credentials|invalid email or password|incorrect password/i,
    '邮箱或密码不正确，请检查后重试。',
  ],
  [
    /unauthorized|session expired|authentication required|not authenticated/i,
    '登录已过期或未登录，请重新登录。',
  ],
  [/forbidden|permission denied/i, '没有操作权限，请检查账户权限。'],
  [
    /too many requests|rate.?limit/i,
    '请求过于频繁，已触发限流保护，请稍后重试。',
  ],
  [
    /no close was sent|not.submitted/i,
    '平仓请求未发送，请检查账户及持仓后重试。',
  ],
  [
    /stop.loss.*(?:fail|not set)|protection.*fail/i,
    '止损保护未确认设置成功，请立即检查交易所的持仓和保护订单。',
  ],
  [
    /take.profit.*(?:fail|not set)/i,
    '止盈订单未确认设置成功，请检查交易所的委托状态。',
  ],
  [
    /failed to (?:load|fetch|get) (.+)/i,
    '数据加载失败，请检查网络和配置后重试。',
  ],
  [
    /failed to (?:save|update|create|delete|remove)/i,
    '配置操作失败，请检查输入及权限后重试。',
  ],
  [
    /\bHTTP (\d{3})\b/i,
    (m) => `上游服务请求失败（错误码：${m[1]}），请稍后重试。`,
  ],
  [
    /request failed with status code (\d{3})/i,
    (m) => `请求失败（错误码：${m[1]}），请稍后重试。`,
  ],
]

export function localizeNotice(
  message: string,
  language: string = getDisplayLanguage(),
  tone: NoticeTone = 'error'
): string {
  if (language !== 'zh' || !message.trim()) return message
  const normalized = message.trim()
  const translated = exact[normalized.toLowerCase()]
  if (translated) return translated
  // Existing authored Chinese, user-entered names, symbols and addresses are
  // not arbitrary English prose and must not be deleted or mistranslated.
  const prefixed = normalized.match(
    /^([\p{Script=Han}\s，。！：:]+[：:])\s*(.+)$/u
  )
  if (
    prefixed &&
    /[a-z]/i.test(prefixed[2]) &&
    !/\p{Script=Han}/u.test(prefixed[2])
  ) {
    return prefixed[1] + localizeNotice(prefixed[2], language, tone)
  }
  if (/\p{Script=Han}/u.test(normalized)) return message
  for (const [pattern, translation] of rules) {
    const match = normalized.match(pattern)
    if (match)
      return typeof translation === 'function'
        ? translation(match)
        : translation
  }
  if (!/[a-z]/i.test(normalized)) return message
  switch (tone) {
    case 'success':
      return '操作已成功完成。'
    case 'warning':
      return '操作需要注意，请检查配置和执行结果；如有异常，请联系维护人员。'
    case 'info':
      return '请检查当前操作状态；如需帮助，请联系维护人员。'
    default:
      return '操作未完成，请检查配置后重试；如持续出现，请联系维护人员。'
  }
}
