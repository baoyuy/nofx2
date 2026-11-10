import {
  DEFAULT_HORN_INTERVAL,
  DEFAULT_MIN_OI_VALUE,
  LIMITS,
  hornIntervals,
  type HornInterval,
  clampNumber,
  indicatorOptions,
  parseCoins,
  ruleSections,
  timeframeOptions,
  type FreeForm,
  type IndicatorKey,
  type RuleSection,
} from './strategyForm'

// Strategy file: JSON with comments (.jsonc). Every parameter carries a
// comment saying what it does and its allowed range, so a person or an AI
// can read and tune it. Loading also accepts plain JSON.

export const STRATEGY_FILE_FORMAT = 'nofx-strategy'
export const STRATEGY_FILE_VERSION = 1

const s = (v: unknown) => JSON.stringify(v)

export function strategyToJsonc(form: FreeForm, language: 'zh' | 'en'): string {
  const zh = language === 'zh'
  const t = (a: string, b: string) => (zh ? a : b)
  const coins =
    form.coinSource === 'pool'
      ? form.poolCoins || []
      : form.coinSource === 'horn'
        ? []
        : parseCoins(form.coinsInput)
  const ind = (key: IndicatorKey) => form.indicators[key]
  const rule = (key: RuleSection) => form.rules?.[key] || ''
  const lines = [
    '// ' +
      t(
        'NOFX 策略文件（JSONC：允许 // 注释）',
        'NOFX strategy file (JSONC: // comments allowed)'
      ),
    '// ' +
      t(
        '用途：在 NOFX「策略」页导入/导出。交易所为币安 U 本位永续合约，AI 每个周期读取下面的设置后决定开仓、平仓或观望。',
        'Imported and exported on the NOFX Strategy page. Trades Binance USDT-M perpetuals; each cycle the AI reads these settings and decides to open, close or wait.'
      ),
    '//',
    '// ' + t('给 AI 优化时的说明：', 'Notes for an AI tuning this file:'),
    '// ' +
      t(
        '1. 只改值，不要改字段名和结构；注释里写了每个值的允许范围，超出范围导入时会被截断。',
        '1. Change values only, not keys or structure. Each comment gives the allowed range; out-of-range values are clamped on import.'
      ),
    '// ' +
      t(
        '2. 「代码强制」的限制由程序执行，AI 无法突破；「AI 参考」的限制写进提示词，由 AI 自行遵守。',
        '2. "Code enforced" limits are applied by the program; "AI guided" limits are written into the prompt for the AI to follow.'
      ),
    '// ' +
      t(
        '3. 优化时请说明改了什么、为什么，并提醒风险：更高杠杆和保证金使用率会放大亏损。',
        '3. Explain what you changed and why, and flag risk: higher leverage and margin usage amplify losses.'
      ),
    '{',
    `  "format": ${s(STRATEGY_FILE_FORMAT)}, // ${t('文件类型标记，不要改', 'File type marker, do not change')}`,
    `  "version": ${STRATEGY_FILE_VERSION}, // ${t('文件格式版本，不要改', 'File format version, do not change')}`,
    `  "name": ${s(form.name)}, // ${t('策略名称', 'Strategy name')}`,
    `  "description": ${s(form.description)}, // ${t('备注', 'Note')}`,
    '',
    `  // ${t('—— 交易哪些币 ——', '—— Coins to trade ——')}`,
    '  "coins": {',
    `    "source": ${s(form.coinSource || 'static')}, // ${t('"static" = 固定列表；"pool" = 每轮读取「币池」页保存的币池；"horn" = 每轮实时筛选布林喇叭口，取前 10 个', '"static" = fixed list; "pool" = read a saved coin pool each cycle; "horn" = re-screen the Bollinger horn every cycle, top 10')}`,
    `    "horn_interval": ${s(form.hornInterval || DEFAULT_HORN_INTERVAL)}, // ${t(`source 为 horn 时用哪个周期的布林线筛选。可选 ${hornIntervals.join(' ')}`, `Bollinger interval used when source is horn. Options ${hornIntervals.join(' ')}`)}`,
    `    "list": ${s(coins)}, // ${t(`币安 U 本位永续合约代码，最多 ${LIMITS.maxCoins} 个。source 为 pool 时这是保存时的快照`, `Binance USDT-M perpetual symbols, at most ${LIMITS.maxCoins}. For a pool this is the snapshot at save time`)}`,
    `    "pool_id": ${s(form.coinSource === 'pool' ? form.coinPoolID || '' : '')} // ${t('币池 ID，只在本账户有效；在别的账户导入时会改用上面的 list', 'Coin pool ID, valid only in this account; elsewhere the list above is used')}`,
    '  },',
    '',
    `  // ${t('—— AI 看哪些数据 ——', '—— What the AI looks at ——')}`,
    '  "market_data": {',
    `    "timeframe": ${s(form.timeframe)}, // ${t('主 K 线周期。可选', 'Primary candle timeframe. Options')}: ${timeframeOptions.join(' ')}${t('。周期越短交易越频繁、噪音越多', '. Shorter means more trades and more noise')}`,
    `    "extra_timeframes": ${s(form.extraTimeframes || [])}, // ${t(`额外参考的周期（多周期共振），最多 ${LIMITS.maxExtraTimeframes} 个，通常选比主周期更长的，如主周期 15m 配 1h、4h`, `Extra timeframes for multi-timeframe confirmation, at most ${LIMITS.maxExtraTimeframes}; usually longer than the primary, e.g. 15m with 1h and 4h`)}`,
    `    "bars": ${form.bars}, // ${t(`每个周期给 AI 看的 K 线根数，${LIMITS.bars.min}-${LIMITS.bars.max}。越多看得越远，但每轮花的 token 也越多`, `Candles per timeframe shown to the AI, ${LIMITS.bars.min}-${LIMITS.bars.max}. More shows further back but costs more tokens per cycle`)}`,
    '    "indicators": {',
    ...indicatorOptions.map(
      (o) =>
        `      "${o.key.replace('enable_', '')}": ${ind(o.key)}, // ${t(o.zh + '：' + o.hintZh, o.en + ': ' + o.hintEn)}${t('（本地用 K 线计算，免费）', ' (computed locally from candles, free)')}`
    ),
    `      "open_interest": ${Boolean(form.enableOI)}, // ${t('持仓量（币安合约公开数据，免费）：持仓增减反映资金进出', 'Open interest (free Binance futures data): rising or falling OI shows money moving in or out')}`,
    `      "funding_rate": ${Boolean(form.enableFunding)} // ${t('资金费率（免费）：过高说明多头拥挤，过低说明空头拥挤', 'Funding rate (free): very high means crowded longs, very low crowded shorts')}`,
    '    }',
    '  },',
    '',
    `  // ${t('—— 风险控制 ——', '—— Risk control ——')}`,
    '  "risk": {',
    `    "btc_eth_leverage": ${form.leverage}, // ${t(`BTC/ETH 最大杠杆，${LIMITS.leverage.min}-${LIMITS.leverage.max}（AI 参考）。10 倍时价格反向约 10% 就会爆仓`, `Max BTC/ETH leverage, ${LIMITS.leverage.min}-${LIMITS.leverage.max} (AI guided). At 10x a ~10% adverse move liquidates`)}`,
    `    "altcoin_leverage": ${form.altLeverage ?? form.leverage}, // ${t('山寨币最大杠杆，范围同上（AI 参考）。山寨币波动大，通常应低于 BTC/ETH', 'Max altcoin leverage, same range (AI guided). Altcoins swing more, so usually lower than BTC/ETH')}`,
    `    "btc_eth_max_position_ratio": ${form.btcEthPositionRatio ?? 5}, // ${t(`BTC/ETH 单笔仓位价值上限 = 账户权益 × 这个倍数，${LIMITS.positionRatio.min}-${LIMITS.positionRatio.max}（代码强制）`, `Max BTC/ETH position value = equity × this, ${LIMITS.positionRatio.min}-${LIMITS.positionRatio.max} (code enforced)`)}`,
    `    "altcoin_max_position_ratio": ${form.altPositionRatio ?? 5}, // ${t('山寨币单笔仓位价值上限倍数，范围同上（代码强制）', 'Max altcoin position value multiple, same range (code enforced)')}`,
    `    "max_positions": ${form.maxPositions}, // ${t(`最多同时持有几个仓位，${LIMITS.maxPositions.min}-${LIMITS.maxPositions.max}（代码强制）`, `Max open positions at once, ${LIMITS.maxPositions.min}-${LIMITS.maxPositions.max} (code enforced)`)}`,
    `    "max_margin_usage": ${form.marginUsage}, // ${t(`最多占用账户多少保证金，${LIMITS.marginUsage.min}-${LIMITS.marginUsage.max}，0.5 = 50%（代码强制）`, `Max share of the account used as margin, ${LIMITS.marginUsage.min}-${LIMITS.marginUsage.max}; 0.5 = 50% (code enforced)`)}`,
    `    "min_position_usdt": ${form.minPositionSize ?? 12}, // ${t(`单笔最小开仓金额（USDT），${LIMITS.minPositionSize.min}-${LIMITS.minPositionSize.max}（代码强制）。太小会低于交易所最小下单额`, `Min position size in USDT, ${LIMITS.minPositionSize.min}-${LIMITS.minPositionSize.max} (code enforced). Too small falls below the exchange minimum`)}`,
    `    "min_oi_value_millions": ${form.minOIValue ?? DEFAULT_MIN_OI_VALUE}, // ${t(`候选币最低持仓量（百万美元），${LIMITS.minOIValue.min}-${LIMITS.minOIValue.max}，0 = 不过滤（代码强制）。低于它的币不交给 AI`, `Min candidate open interest in million USD, ${LIMITS.minOIValue.min}-${LIMITS.minOIValue.max}, 0 = off (code enforced). Coins below it are not sent to the AI`)}`,
    `    "min_confidence": ${form.minConfidence}, // ${t(`AI 把握度达到多少才开仓，${LIMITS.confidence.min}-${LIMITS.confidence.max}（AI 参考）。越高开仓越少`, `AI confidence needed to open, ${LIMITS.confidence.min}-${LIMITS.confidence.max} (AI guided). Higher means fewer trades`)}`,
    `    "min_risk_reward": ${form.minRiskReward} // ${t(`最低盈亏比（止盈距离 / 止损距离），${LIMITS.riskReward.min}-${LIMITS.riskReward.max}（AI 参考）`, `Min reward/risk (target distance / stop distance), ${LIMITS.riskReward.min}-${LIMITS.riskReward.max} (AI guided)`)}`,
    '  },',
    '',
    `  // ${t('—— AI 交易规则（写进提示词，可用中文或英文；留空 = 使用内置默认规则）——', '—— AI trading rules (written into the prompt, any language; empty = built-in default) ——')}`,
    '  "rules": {',
    `    "role_definition": ${s(rule('role_definition'))}, // ${t('角色：AI 是什么样的交易员、交易什么', 'Role: what kind of trader the AI is and what it trades')}`,
    `    "trading_frequency": ${s(rule('trading_frequency'))}, // ${t('交易频率：多久交易一次、持仓多久', 'Trading frequency: how often to trade and how long to hold')}`,
    `    "entry_standards": ${s(rule('entry_standards'))}, // ${t('开仓标准：满足什么条件才开仓', 'Entry standards: conditions required to open a position')}`,
    `    "decision_process": ${s(rule('decision_process'))}, // ${t('决策流程：每轮按什么步骤思考', 'Decision process: the steps to follow each cycle')}`,
    `    "extra_instructions": ${s(form.customPrompt)} // ${t('额外要求，例如「只做多」「不在周末开仓」。不能突破上面的风险控制', 'Extra instructions, e.g. "long only". Cannot override the risk control above')}`,
    '  }',
    '}',
    '',
  ]
  return lines.join('\n')
}

// walkJson visits each character, telling whether it sits inside a string.
function walkJson(
  text: string,
  visit: (i: number, inString: boolean) => number | void
): void {
  let inString = false
  for (let i = 0; i < text.length; i++) {
    const c = text[i]
    if (inString) {
      visit(i, true)
      if (c === '\\') visit(++i, true)
      else if (c === '"') inString = false
      continue
    }
    if (c === '"') {
      inString = true
      visit(i, true)
      continue
    }
    const skip = visit(i, false)
    if (typeof skip === 'number') i = skip
  }
}

// stripJsonc removes // and /* */ comments, then trailing commas, outside
// of strings, so the result parses as plain JSON.
export function stripJsonc(text: string): string {
  let noComments = ''
  walkJson(text, (i, inString) => {
    const c = text[i]
    if (inString) {
      noComments += c ?? ''
      return
    }
    if (c === '/' && text[i + 1] === '/') {
      let j = i
      while (j < text.length && text[j] !== '\n') j++
      noComments += '\n'
      return j
    }
    if (c === '/' && text[i + 1] === '*') {
      let j = i + 2
      while (j < text.length && !(text[j] === '*' && text[j + 1] === '/')) j++
      return j + 1
    }
    noComments += c
  })
  let out = ''
  walkJson(noComments, (i, inString) => {
    const c = noComments[i]
    if (!inString && c === ',') {
      let j = i + 1
      while (j < noComments.length && /\s/.test(noComments[j])) j++
      if (noComments[j] === '}' || noComments[j] === ']') return
    }
    out += c ?? ''
  })
  return out
}

export interface ImportResult {
  patch: Partial<FreeForm>
  warnings: string[]
}

type Obj = Record<string, unknown>
const obj = (v: unknown): Obj =>
  v && typeof v === 'object' && !Array.isArray(v) ? (v as Obj) : {}
const str = (v: unknown, max: number) =>
  typeof v === 'string' ? v.slice(0, max) : undefined

// parseStrategyFile reads a strategy file into form values, clamping every
// number to the editor's range. availablePools decides whether pool_id can
// be kept in this account.
export function parseStrategyFile(
  text: string,
  availablePools: { id: string; coins: { symbol: string }[] }[],
  language: 'zh' | 'en'
): ImportResult {
  const t = (a: string, b: string) => (language === 'zh' ? a : b)
  let root: Obj
  try {
    root = obj(JSON.parse(stripJsonc(text)))
  } catch {
    throw new Error(
      t('文件不是有效的 JSON / JSONC', 'The file is not valid JSON / JSONC')
    )
  }
  if (root.format !== STRATEGY_FILE_FORMAT) {
    throw new Error(
      t(
        '这不是 NOFX 策略文件（缺少 "format": "nofx-strategy"）',
        'Not a NOFX strategy file (missing "format": "nofx-strategy")'
      )
    )
  }
  const warnings: string[] = []
  const patch: Partial<FreeForm> = {}
  const num = (
    value: unknown,
    range: { min: number; max: number },
    label: string
  ) => {
    if (value === undefined) return undefined
    const n = Number(value)
    if (!Number.isFinite(n)) {
      warnings.push(
        t(`${label} 不是数字，已忽略`, `${label} is not a number; ignored`)
      )
      return undefined
    }
    const clamped = clampNumber(n, range, n)
    if (clamped !== n)
      warnings.push(
        t(
          `${label} 超出范围，已调整为 ${clamped}`,
          `${label} out of range; set to ${clamped}`
        )
      )
    return clamped
  }

  const name = str(root.name, 100)
  if (name !== undefined && name.trim()) patch.name = name.trim()
  const description = str(root.description, 500)
  if (description !== undefined) patch.description = description

  const coins = obj(root.coins)
  const list = Array.isArray(coins.list)
    ? parseCoins(coins.list.map(String).join(' '))
    : []
  if (Array.isArray(coins.list) && coins.list.length > LIMITS.maxCoins) {
    warnings.push(
      t(
        `币种超过 ${LIMITS.maxCoins} 个，只保留前 ${LIMITS.maxCoins} 个`,
        `More than ${LIMITS.maxCoins} coins; kept the first ${LIMITS.maxCoins}`
      )
    )
  }
  const pool = availablePools.find((p) => p.id === coins.pool_id)
  if (coins.source === 'horn') {
    patch.coinSource = 'horn'
    if (typeof coins.horn_interval === 'string') {
      if ((hornIntervals as readonly string[]).includes(coins.horn_interval))
        patch.hornInterval = coins.horn_interval as HornInterval
      else
        warnings.push(
          t(
            `布林喇叭口周期 ${coins.horn_interval} 不支持，已用 ${DEFAULT_HORN_INTERVAL}`,
            `Horn interval ${coins.horn_interval} is not supported; using ${DEFAULT_HORN_INTERVAL}`
          )
        )
    }
  } else if (coins.source === 'pool' && pool) {
    patch.coinSource = 'pool'
    patch.coinPoolID = pool.id
    patch.poolCoins = pool.coins.map((c) => c.symbol)
  } else {
    if (coins.source === 'pool') {
      warnings.push(
        t(
          '文件里的币池不在当前账户，已改用文件中的币种列表',
          'The coin pool is not in this account; using the coin list from the file'
        )
      )
    }
    if (list.length) {
      patch.coinSource = 'static'
      patch.coinsInput = list.join(', ')
    }
  }

  const market = obj(root.market_data)
  if (typeof market.timeframe === 'string') {
    if (timeframeOptions.includes(market.timeframe))
      patch.timeframe = market.timeframe
    else
      warnings.push(
        t(
          `周期 ${market.timeframe} 不支持，已保留原设置`,
          `Timeframe ${market.timeframe} is not supported; kept the current one`
        )
      )
  }
  if (Array.isArray(market.extra_timeframes)) {
    const valid = market.extra_timeframes.filter(
      (tf): tf is string =>
        typeof tf === 'string' && timeframeOptions.includes(tf)
    )
    patch.extraTimeframes = [...new Set(valid)]
      .filter((tf) => tf !== (patch.timeframe || market.timeframe))
      .slice(0, LIMITS.maxExtraTimeframes)
  }
  const bars = num(market.bars, LIMITS.bars, t('K 线根数', 'bars'))
  if (bars !== undefined) patch.bars = Math.round(bars)
  const indicators = obj(market.indicators)
  if (Object.keys(indicators).length) {
    patch.indicators = Object.fromEntries(
      indicatorOptions.map((o) => [
        o.key,
        Boolean(indicators[o.key.replace('enable_', '')]),
      ])
    ) as Record<IndicatorKey, boolean>
    patch.enableOI = Boolean(indicators.open_interest)
    patch.enableFunding = Boolean(indicators.funding_rate)
  }

  const risk = obj(root.risk)
  const set = <K extends keyof FreeForm>(
    key: K,
    value: number | undefined,
    round = false
  ) => {
    if (value !== undefined)
      (patch as Record<string, unknown>)[key] = round
        ? Math.round(value)
        : value
  }
  set(
    'leverage',
    num(
      risk.btc_eth_leverage,
      LIMITS.leverage,
      t('BTC/ETH 杠杆', 'BTC/ETH leverage')
    ),
    true
  )
  set(
    'altLeverage',
    num(
      risk.altcoin_leverage,
      LIMITS.leverage,
      t('山寨币杠杆', 'altcoin leverage')
    ),
    true
  )
  set(
    'btcEthPositionRatio',
    num(
      risk.btc_eth_max_position_ratio,
      LIMITS.positionRatio,
      t('BTC/ETH 仓位倍数', 'BTC/ETH position ratio')
    )
  )
  set(
    'altPositionRatio',
    num(
      risk.altcoin_max_position_ratio,
      LIMITS.positionRatio,
      t('山寨币仓位倍数', 'altcoin position ratio')
    )
  )
  set(
    'maxPositions',
    num(
      risk.max_positions,
      LIMITS.maxPositions,
      t('最多持仓数', 'max positions')
    ),
    true
  )
  set(
    'marginUsage',
    num(
      risk.max_margin_usage,
      LIMITS.marginUsage,
      t('保证金使用率', 'margin usage')
    )
  )
  set(
    'minPositionSize',
    num(
      risk.min_position_usdt,
      LIMITS.minPositionSize,
      t('最小开仓金额', 'min position size')
    )
  )
  set(
    'minOIValue',
    num(
      risk.min_oi_value_millions,
      LIMITS.minOIValue,
      t('候选币最低持仓量', 'min candidate open interest')
    )
  )
  set(
    'minConfidence',
    num(
      risk.min_confidence,
      LIMITS.confidence,
      t('最低把握度', 'min confidence')
    ),
    true
  )
  set(
    'minRiskReward',
    num(
      risk.min_risk_reward,
      LIMITS.riskReward,
      t('最低盈亏比', 'min risk/reward')
    )
  )

  const rules = obj(root.rules)
  if (Object.keys(rules).length) {
    patch.rules = Object.fromEntries(
      ruleSections.map((key) => [key, str(rules[key], 4000) ?? ''])
    ) as Record<RuleSection, string>
    patch.customPrompt = str(rules.extra_instructions, 4000) ?? ''
  }
  return { patch, warnings }
}
