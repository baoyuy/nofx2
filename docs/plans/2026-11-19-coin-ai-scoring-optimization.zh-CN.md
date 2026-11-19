# 带单 AI 币种评分与自动化选池 — 优化开发文档

- 日期：2026-11-19
- 状态：待确认后开工（本文件是目标与方案，不含实现）
- 范围：`copytrade/`、`api/handler_copytrade_score.go`、`store/copytrade.go`、`store/coin_pool.go`、`kernel/coin_pool*.go`、`store/strategy.go`、`binance` 公开数据、带单前端（`web/src/components/copytrade/*`、`SettingsPage`）
- 前置文档：`2026-11-18-copy-trade-handoff.zh-CN.md`、`2026-11-18-copy-trade-style-scoring.zh-CN.md`
- 目标一句话：把 **「监控带单员 → 单币 AI 打分打标 → 总 AI 选 10 个（多空都可）→ 币池 → 策略按方向自动交易」** 这条链路做到 **多空对称、数据充分、可验证、可控、可回滚**，并把此前遗留的页面问题一并修掉。

## 0. 现状证据（为什么必须优化）

- 已评分 200 个币：**中性 122 / 看多 53 / 看空 25**。AI **会**看空，但下面三处把"看空"压没了：
  1. **列表排序 bug**：`CoinsTab` 的 `aiScore` 把空单当**负分**（`-score-1`），未分析的币记为 `-1` → **高置信度空单排在"未分析"之后**，顶部几乎看不到空单。
  2. **提示词单侧**：只输出一个 `bias + score`，`score` 是"该方向的可信度"，且数据里 `Long:` 在前、多头金额通常更大、允许"拿不准就 neutral"当逃生口 → 模型偏向 long/neutral。
  3. **数据偏多头且缺做空依据**：只给 1h、40 根 K + 带单共识，没有资金费率/OI/多空比/taker/大盘环境。
- **执行层硬缺口**：`kernel/coin_pool.go` 从池子里**只取 symbols**，`CoinPoolCoin.Direction` 未被消费 → 即使池里标"看空"，策略也可能把它做多。
- **无验证闭环**：没有记录每次评分/选池之后的真实涨跌，无法回答"高分到底准不准、空头到底赚不赚"。

## 1. 优化总目标（全部达成才算完成）

| 编号 | 目标 | 验收口径 |
|---|---|---|
| G1 | **多空对称** | 有明确空头证据时必须能出 short；非中性里 short 占比 ≥25%（影子期统计，且不是靠排序硬凑） |
| G2 | **双向可解释** | 每币输出 `bull_score/bear_score/conviction/bias` + 做多理由 + 做空理由 + 失效条件 |
| G3 | **排序正确** | 列表按"带方向的边际分"排序，空单不再沉底，中性不冒充看多 |
| G4 | **时效可见** | 列表与详情都显示"几分钟前"，超过 TTL 标"过期" |
| G5 | **数据充分** | 15m + 1h（+4h regime），资金费率/OI/多空比/taker/相对强弱/大盘环境齐备 |
| G6 | **选池成型** | 总 AI 产出 ≤10 个币，多空都可，带约束与理由 |
| G7 | **方向可执行** | `coin_pool` 的方向约束在引擎生效：看空能开空，禁止对看空币做多 |
| G8 | **可验证** | 每次评分/选池记录前向收益（4/24/72h，相对 BTC），分多空、分分数档统计 |
| G9 | **安全可控** | 默认关闭、池 TTL、单币/单侧/杠杆上限、kill switch、并发与缓存限流 |
| G10 | **遗留页面问题清空** | 第 7 节列出的历史页面临时问题全部修掉 |
| G11 | **上下文稳定** | 每币带上次 thesis 做"定点重估"；排序用平滑值 + 滞回；同一币连续两次 bias 翻转率 <20%，相邻两次排名 Spearman 相关 ≥0.8 |

**北极星指标（决定能否上自动交易）**：影子期内，池内 10 个币的 4/24/72h 前向收益（相对 BTC）满足 **①高分档 > 低分档；②多、空两侧平均超额都为正**。达不到就继续只展示、不自动下单。

## 2. 问题清单（全部要修）

| # | 问题 | 现状（代码位置） | 目标 |
|---|---|---|---|
| P1 | 空单排到"未分析"之后 | `CoinsTab.tsx` `aiScore`：short → `-score-1`，缺失 → `-1` | 按 `signed_edge` 排序；空单按强度正常参与；缺失排最后 |
| P2 | 中性按正分排，冒充看多 | `aiScore` 中性返回 `+score` | 中性不进"看多"排序，单列或按 0 |
| P3 | 提示词单侧 score，天然不对称 | `buildCoinScorePrompt`（api/handler_copytrade_score.go:503） | 改为 `bull_score/bear_score/conviction` 双向 |
| P4 | "拿不准→neutral"当逃生口 | 同上 | neutral 仅当多空证据接近，且必须给理由 |
| P5 | 只有 1h、40 根 K | `analyzeCoinCore`（固定 `"1h"`，40 根） | 15m/1h(+4h) 多周期 + 多周期一致性 |
| P6 | 缺衍生品数据 | 只有 `FundingRate`（ticker 带） | 加 OI、大户多空比、taker 主动买卖、basis |
| P7 | 缺大盘 regime | 无 | 加 BTC 趋势/波动/相关性，作降权/过滤 |
| P8 | 带单共识偏多、未当反向 | 只喂"多空人数/金额" | 加净增减仓、被套/浮亏占比，"拥挤多头被套"作为**看空**证据 |
| P9 | 无前向验证 | 无 | 新增 `coin_score_outcomes` + 命中率统计 |
| P10 | 无总选池层 | 无 | 新增选池（规则+AI 复核），产出 ≤10 的多空池 |
| P11 | `coin_pool` 方向不被引擎消费 | `kernel/coin_pool.go` 只回 symbols | 引擎消费 `Direction` 作方向约束 |
| P12 | 策略可能对看空币开多 | 同上 | 禁止反向开仓；方向以池为准 |
| P13 | 全币每轮打分，成本/并发无分层 | `runCoinScoreAll` 并发 4 跑全部 | 分层：规则缩候选 → 精算 → 池复核；并发/缓存可配 |
| P14 | 展示无时效/过期 | `CoinsTab` AI 列无时间 | 显示"x 分钟前"、过期标记 |
| P15 | 设置页只能"全部/定时"，无法选池精算 | `CoinScoreSettings.tsx` | 增加"只算候选/只算池""Top-N""周期"等选项 |
| P16 | 历史页面遗留问题 | 见第 7 节 | 全部修掉 |
| P17 | 评分无上下文、排名抖动大 | 每轮从零生成、raw score 排序 | 带上次 thesis 定点重估 + EMA 平滑 + 滞回 + 变化触发 + 低温度 |

## 3. 目标态分层设计

```
L1 监控层（已有）
   带单员持仓/动作 → 候选币 Universe（去重、流动性过滤）
        ↓
L2 单币打分层（AI）
   多周期 + 衍生品 + 带单反向特征 + 大盘 regime
   → {bull_score, bear_score, bias, conviction, horizon, long_case[], short_case[], invalidates}
        ↓ 存 coin_scores（含 analysis 时刻价格/模型/schema 版本）
L3 选池层（规则 + 总 AI 复核）
   signed_edge 排序 + 约束（每侧上限、去相关、流动性、最低 conviction、TTL）
   → coin_pool（CoinPoolCoin{Direction, Score, Note, Conviction, Expiry}）
        ↓
L4 执行层（策略）
   读 coin_pool；Direction 作为**方向约束**；仓位/杠杆/止损用规则
        ↓
验证层：coin_score_outcomes / coin_pool_outcomes 前向收益 + 命中率面板
安全层：默认关闭、TTL、上限、kill switch、并发/缓存/限流
```

## 4. 数据结构变更

1. **`copytrade_coin_scores`（v2）**：`payload` 升到 v2，新增字段：
   `bull_score, bear_score, conviction, regime(bull|bear|range), tf_alignment{15m,1h,4h}, data_flags(funding,oi,lsr,taker), schema_version`。
   旧 payload 做兼容读取（缺字段按 v1 处理，`bull=score if long else 0`）。
2. **新增 `coin_score_outcomes`**：`score_id, symbol, bias, bull/bear, entry_price(分析时价), analyzed_at, ret_4h/24h/72h, bench_4h/24h/72h, done_*`。由定时任务回填。
3. **新增 `coin_pool_runs` + `coin_pool_outcomes`**：记录每次选池结果（10 个 pick 含方向）及其前向收益。
4. **`coin_pools.CoinPoolCoin`**：已有 `Direction/Score/Note`；新增 `Conviction, ExpiryAt`。
5. **`coin_pools` 消费方向**：引擎读池时带上 `Direction`（见第 6 节）。

## 5. 单币评分 Prompt 规格（v2）

**系统提示（目标）**：
```
You are a disciplined crypto perpetual futures analyst. Judge ONE coin from BOTH sides using
the copy-trade consensus, multi-timeframe technicals and derivatives data provided.
Reply ONE strict JSON only:
{"bull_score":0-100,"bear_score":0-100,"bias":"long|short|neutral",
 "conviction":0-100,"horizon":"short|swing","tf_alignment":{"15m":-1|0|1,"1h":-1|0|1,"4h":-1|0|1},
 "long_case":["..."],"short_case":["..."],"invalidates":"one sentence",
 "market_context":"..." ,"sources":["copytrade","technicals","derivatives","regime"]}
Rules:
- Score bull and bear INDEPENDENTLY and symmetrically. Do not favor long.
- bias = the side with the clearly higher score; use neutral ONLY when the two are within 10 and you say why.
- conviction = |bull_score-bear_score|. Be honest; low conviction is fine.
- Check short-side criteria explicitly: price below EMA/MA, lower highs, MACD<0, RSI<50,
  negative/falling structure, positive funding (longs crowded), OI up while price falls, underperformance vs BTC.
- Check long-side criteria symmetrically.
- NEVER give entry/stop/take-profit and never instruct an order. Conditional wording only.
- Write cases/invalidates/market_context in <zh|en>.
```
**用户数据（目标，全部对称给多空）**：
- 多周期 K 线：15m 及 1h 各 60 根（4h 20 根）；标注多周期方向；
- 指标：MA/EMA/MACD/RSI/KDJ/BOLL/ATR（每周期）；
- 衍生品：资金费率（及近 3 次）、OI 及 1h/4h 变化、大户多空比、taker 买卖比、basis；
- 价格结构：近高/近低、是否突破/破位、相对 BTC 强弱、ATR%；
- 带单共识：多空人数/金额、**净增减仓**、**被套/浮亏占比**、fresh openers、
  confidence/rank/shadow、前 5 持有人；并显式说明"拥挤多头被套 = 反向信号"；
- 大盘 regime：BTC 趋势/波动/资金费率；risk-off 时对多头降权、对空头加分；
- 语言与时效：注明每段数据的时间。

## 5.1 上下文与稳定性设计（防排名抖动）

目标：同一币的结论不要每次"换态度"，排名不要因微小分数变化而反复横跳。

**模型内（定点重估，不是记忆续写）**
- 新增 `coin_score_memory`（每用户+币一条）：`thesis, bull, bear, bias, conviction, key_levels, invalidation, last_at, last_price`。
- 每次打分把上次 thesis 一并喂给模型，并强制：
  - 以当前数据为准；仅当出现新证据或"失效条件被触发"才调整；
  - 输出 `changed: true/false` 与 `change_reason`；
  - 不得为了"保持一致"而忽略当前数据（防自我锚定）。
- 输出 schema 增加 `changed`、`change_reason`。

**模型外（确定性防抖，最有效）**
- `edge = bull − bear`；对每币 edge 做 **EMA 平滑**（如 α=0.3），排序用平滑值。
- **滞回**：进池阈值 > 出池阈值（例：进池 edge≥+20，掉到 ≤+10 才出；空头对称），避免排名在阈值附近抖动。
- **变化触发**：仅当价格 > N×ATR%、带单共识/资金费率/OI 明显变化、或有新开仓时才重算，否则复用缓存（省 token）。
- **低温度**：打分 `temperature≈0`；并列时用稳定 tie-break（上次排名 / 成交额）。

**验收（并入 G11）**
- 连续两次评分 bias 翻转率 <20%；相邻两次排名 Spearman ≥0.8；
- 选池换手率有上限（例：单轮更换 ≤3 个），且每次更换都有 `change_reason`。

## 6. 选池层与执行层规格

**选池（L3）**
- 记 `edge = bull - bear`；候选先过：流动性门槛、最低 `conviction`、时效（≤TTL）、去相关（同板块/高相关只留 1）。
- 约束：每侧上限（默认多 ≤7、空 ≤7，总数 ≤10）；不足就少给，**不强行凑**。
- 先用规则排序取前 ~20，再让总 AI 复核/解释并剔除明显冲突项；输出每条 `{symbol,direction,conviction,reason,invalidates,expiry}`。
- 落 `coin_pool`（`screener="copytrade_ai"`），带 `ExpiryAt`。

**执行（L4）**
- `kernel/coin_pool.go`：不仅返回 symbols，还要返回 `Direction`；`engine` 对池中币**按 direction 约束**：direction=short 时禁止开多、只允许空（或按策略"跟随/反向"映射）。
- 池过期/数据陈旧/被限流 → 不下单（视为无候选）。
- 仓位/杠杆/止损/单币上限用**规则**，不用 AI。

## 7. 展示与排序修复 + 历史页面临时问题（P16，全部要修）

**AI 列与排序**
- 列表每项显示：`AI 多 72 ▲ / 空 65 ▼ / 中性`，旁边**显示"x 分钟前"**，过期灰显。
- 排序改为 **signed_edge**：看多按 `+bull`、看空按 `-bear`、中性不计入"看多"排序；**空单不再沉底、不再排在未分析之后**。
- 增加筛选：只看多 / 只看空 / 只看高分 / 只看新鲜。

**历史遗留（来自 2026-11-18 handoff 页面审查清单，未完成项）**
- 标签页/选中的币写进 URL（刷新不丢）；
- K 线在切换币/周期都重新取；
- 搜币覆盖不只"持仓 + 最近 5 条动作"（列表接口补数据或前端懒加载）；
- 提示在聚焦/点击时也可见（不只用 title 悬停）；
- 按币种页默认不跟随排名第一的币跳变；
- 命中率不再每 4 秒查库（与面板共用一次请求 / 降频）。

## 8. 验证体系规格（G8）

- 每个 `coin_score` 记录 `entry_price`（分析时现价）；定时任务在 +4h/+24h/+72h 回填收益与 BTC 基准。
- 面板按 **多/空 × 分数档（≥70/40–70/<40）× 周期** 统计：命中率、平均超额、跑赢比例、样本数。
- 选池层另算：池内 10 个的整体与分方向表现、与"随机取同数量"对照。
- 达标线见第 1 节北极星指标。

## 9. 安全 / 成本 / 运维

- 默认全关（打分自动、选池自动、自动交易都默认关闭）。
- 池 TTL（默认 30–60 分钟）；数据陈旧或限流即停手。
- 上限：单币/单侧持仓、最大杠杆、单轮最多币数、AI 调用并发与每分钟上限。
- Kill switch：一键停自动交易/停止调度。
- 成本：分层——规则先缩候选，只对候选精算，池只对 10 个复核；缓存与 TTL 复用。
- 部署沿用现有"定向文件部署 + 服务器本地 build"（两仓库历史不一致，git 直连不通），改动前备份 DB/镜像。

## 10. 分阶段计划（每阶段有验收）

| 阶段 | 内容 | 影响交易 | 验收 |
|---|---|---|---|
| P0 地基（先做） | 修 P1/P2 排序、prompt v2 双向、数据 15m/1h(+4h)+衍生品+regime、P8 反向特征、P14 时效、P9 前向记录 | 否 | bias 分布合理（short≥25% 非中性）；列表空单可见可排序；outcomes 开始累积 |
| P1 选池（影子） | L3 选池（规则+AI 复核），产 coin_pool，不交易 | 否 | 每轮产出 ≤10 且多空都可；池结果入 `coin_pool_outcomes` |
| P2 执行接线（默认关/paper） | 引擎消费 Direction；策略接池；风控/上限/TTL | 是（默认关） | 看空能开空、禁止反向；paper 跑通 |
| P3 小资金实盘 | 按北极星指标达标后小资金 + 风控放开 | 是 | 影子期指标达标 + 回撤可控 |

## 11. 验收标准（Definition of Done，"全部优化掉"）

1. G1–G11 全部满足；
2. 第 2 节 P1–P16 全部关闭；
3. 影子期（建议 ≥2 周）北极星指标达标；
4. 空头样本在 4/24/72h 上有正超额，且不是靠个别币；
5. 自动化默认关闭、有一键停与回滚。

## 12. 风险与回滚

- 三级 AI 串联会累积误差并互相强化偏见（都看多）→ 每级加确定性约束与 fallback；AI 只给方向/证据，仓位风控用规则。
- 数据接口新增（OI/多空比/taker）在服务器 IP 可能有限流 → 加缓存与退避；失败降级不阻塞。
- 回滚：DB/镜像/文件均按现有 `.deploy-backup` + `pre-*` 镜像回滚；功能均带默认关闭开关。

## 13. 涉及文件（预计）

- 后端：`api/handler_copytrade_score.go`、`store/copytrade.go`（新增 outcomes/pool runs）、`store/coin_pool.go`、`kernel/coin_pool.go`、`kernel/engine*.go`、`binance`（OI/LSR/taker/basis 公开接口）。
- 前端：`web/src/components/copytrade/CoinsTab.tsx`、`CoinScoreSettings.tsx`、`web/src/lib/api/copyTrade.ts`、`SettingsPage.tsx`、可能的币池/看板页。
- 文档：本文件。

## 14. 待确认决策

1. 周期组合：15m+1h 还是 15m+1h+4h（4h 增加 token）？
2. 选池每侧上限（多 ≤? 空 ≤?）与总数（10 固定？）。
3. 池 TTL 与打分频率。
4. 是否本期就做 L4（引擎方向约束），还是先只到 L3 影子。
5. 新增衍生品数据源的可用性（服务器 IP 是否被 /futures/data 限流）。
6. 平滑参数（EMA α）、滞回阈值、变化触发阈值（N×ATR%）用哪组默认值。
