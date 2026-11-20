# 总 AI 币池 · 对接文档（给下游 Agent）

- 日期：2026-11-20
- 适用：需要"读取/消费总 AI 币池"或"把币池接入策略交易"的另一个 Agent
- 代码仓库：`nofx2`（Go 后端 + React 前端）
- 数据源：带单页 AI 逐币评分 → 总 AI 选池 → `coin_pool`（表 `coin_pools`）

> 本文描述代码中已实现的行为（2026-11-20 已按代码复核修正）。环境地址、账号、凭证不写入本文，请向维护者单独获取。

---

## 1. 一句话概括

系统定时用"带单监控 + 多周期技术面 + 衍生品 + 大盘 regime"对每个币做**双向（多/空）AI 评分**（每币重评间隔 `interval_minutes`，默认 60 分钟），再按 `pool_refresh_minutes`（默认 5 分钟）把评分最好的一批（≤10，多空都可，每侧≤7，带滞回）写成一个名为 **`带单AI币池`** 的**普通币池**（`screener = copytrade_ai`，ID 固定不变）。

**它和其他币池一样，只通过策略进入交易和看板：**

- **接入交易**：策略的币源设成 `coin_pool` 并指向该池的 `id`（策略编辑页「我的币池 → 带单AI币池」）。
- **看板展示**：看板信号矩阵显示的是**交易员所用策略本轮加载的候选币**（+持仓），不直接读币池。
- **只读查看**：`GET /api/copytrade/coin-pool` 可拿"币 + 方向 + 分数 + 过期时间"，仅供展示/分析，不是交易入口。

---

## 2. 端到端链路

```
带单监控(copytrade monitor)
   └─ 逐币 → AI 评分(analyzeCoinCore) → 存 copytrade_coin_scores / copytrade_score_memory
                             └─ 前向收益回填 copytrade_score_outcomes（4/24/72h vs BTC）
总 AI 选池(buildCoinPool)  ← 读取最新评分（仅"新鲜"的）
   └─ 规则：≤10 个、多空都可、每侧≤7、进/出滞回、TTL 过期不交易
   └─ 原地更新 coin_pools(screener=copytrade_ai, name=带单AI币池，ID 固定) + 变化日志 coin_pool_logs
          └─ 策略消费：策略 coin_source = coin_pool + coin_pool_id → 引擎每轮读取候选币(+方向)
                 └─ 交易员按策略决策下单；决策记录 candidate_coins
                        └─ 看板信号矩阵 = 最新决策的 candidate_coins + 持仓
```

链路是单向的：**币池 → 策略 → 交易员/看板**。看板不直接读币池，所以看板上看到的币一定是策略真正拿去决策的币。

---

## 3. 数据模型

### 3.1 `coin_pools`（币池，策略可直接使用）
| 字段 | 说明 |
|---|---|
| `id` | 币池 ID（对接策略时用它）；AI 池每个用户只有一个，重建时原地更新，ID 不变 |
| `user_id` | 归属用户（需与策略/交易员同一账号） |
| `name` | AI 池固定为 `带单AI币池` |
| `screener` | AI 池固定为 `copytrade_ai` |
| `coins` | JSON 数组，元素见 3.2 |
| `created_at` / `updated_at` | 更新时间即"币池更新于…" |

### 3.2 `coins[]`（每个币）
```json
{
  "symbol": "ROBOUSDT",
  "direction": "short",          // long | short（AI 池只放这两种）；手工池可能是 neutral
  "score": 56.3,                 // 浮点 0-100，方向强度（|平滑 edge|，无平滑值时用 conviction）
  "note": "edge -56 conv 56",
  "expiry_at": 1763634800000     // 毫秒；过期即视为失效、不应再交易
}
```
- `score`、`note`、`expiry_at` 为 `omitempty`：值为 0/空时**字段不出现**（手工池通常没有 `expiry_at`，即不过期）。

### 3.3 `coin_pool_logs`（币池变化日志）
| 字段 | 说明 |
|---|---|
| `at` | 毫秒时间戳 |
| `added` / `removed` | 本次新增/移除的币数量 |
| `added_json` / `removed_json` | 对应的币列表 JSON |

### 3.4 评分相关表（供了解，不一定需要下游读）
- `copytrade_coin_scores`：`user_id+symbol` 主键；`payload` 为完整评分 JSON；`analyzed_at` 毫秒。
- `copytrade_score_memory`：每币上一次研判（bias/bull/bear/conviction/smoothed_edge/...），用于模型上下文与 EMA 平滑。
- `copytrade_score_outcomes`：前向收益回填（4/24/72h 收益、相对 BTC 超额、done 标记），已评估完成的记录保留 7 天。

---

## 4. 后端 API 契约（另一个 Agent 主要用这些）

- Base：`http(s)://<host>/api`（经前端 Nginx 反代；后端直连 `http://<host>:8080/api`）
- 鉴权：所有 `/copytrade/*`、`/coin-pools*`、`/market-board/*` 均为 **protected**，需请求头 `Authorization: Bearer <JWT>`（只认 Bearer，不认 Cookie）。
- 数据按登录用户隔离：评分、AI 池都存在"定时任务所属账号"下（见 6.1）。

### 4.1 读取总 AI 币池（只读查看）
`GET /api/copytrade/coin-pool`

响应：
```json
{
  "id": "b7067610-2bd7-461e-affb-8081a67f7b43",
  "name": "带单AI币池",
  "count": 10,
  "updated_at": 1763634213118,
  "coins": [ { "symbol":"ROBOUSDT","direction":"short","score":56,"note":"...","expiry_at":1763634813118 } ],
  "history": [ { "id":"...","at":1763634213118,"added":2,"removed":1 } ]
}
```
- 从未生成过：`{"id":"","count":0,"coins":[],"updated_at":0,"history":[]}`。
- 生成过但本轮没有合格币：`id` 仍是原池 ID，`count:0`、`coins:[]`（池保留，ID 不变）。
- **这个接口只用于查看/分析。要交易，请走第 8 节"策略接入"**，不要绕过策略直接按它下单。

### 4.2 手动重建币池
`POST /api/copytrade/coin-pool/generate` → 立即按当前评分重建（与定时刷新互斥执行），返回同 4.1 结构。

### 4.3 每币评分列表
`GET /api/copytrade/coin-scores` →
```json
{ "items": [ { "symbol":"...", "bias":"long|short|neutral", "horizon":"...", "bull_score":..,"bear_score":..,"edge":..,"smoothed_edge":..,"conviction":..,"score":..,"confidence":..,"analyzed_at":<ms>,"model_id":".." } ] }
```

### 4.4 单币评分
- `GET /api/copytrade/coin-score?symbol=ROBOUSDT` → `{ symbol, analysis: {...}|null }`
- `POST /api/copytrade/coin-score`，body `{ "symbol":"ROBOUSDT", "interval":"1h", "model_id":"可选", "lang":"zh|en" }` → 立即分析该币。

### 4.5 定时全量分析设置
- `GET /api/copytrade/coin-score/settings` 返回（含运行状态）：
```json
{ "enabled": true, "interval_minutes": 60, "pool_auto": true, "top_n": 0,
  "concurrency": 4, "min_quote_volume": 5000000,
  "pool_refresh_minutes": 5, "pool_ttl_minutes": 12, "min_interval": 5,
  "running": false, "done": 0, "total": 200, "last_run": 0, "last_count": 0,
  "last_error": "", "model": "DeepSeek AI", "coin_count": 200 }
```
- 上面是**默认值**示例（`interval_minutes` 默认 60、最小 5；`concurrency` 默认 4；`pool_refresh_minutes` 默认 5；`pool_ttl_minutes` 默认 12，若小于刷新间隔会被改成 2×刷新间隔）。
- `PUT /api/copytrade/coin-score/settings`：**合并式保存**（只改传入字段）。字段同上：`enabled, interval_minutes, pool_auto, top_n, concurrency, min_quote_volume, pool_refresh_minutes, pool_ttl_minutes`。
- `pool_auto` 只控制"**每次全量分析结束后**是否立即重建币池"；按 `pool_refresh_minutes` 的定时刷新**始终运行**（用来续 `expiry_at`）。
- `POST /api/copytrade/coin-score/run-all`：立即全量分析（异步，轮询 settings 看进度）。
- `GET /api/copytrade/coin-score/outcomes`：前向验证命中率，`{ "rows":[ {horizon,side,band,samples,hit_rate,beat_market,avg_ret,avg_excess} ] }`。
- **基准=市场均值**：`avg_excess = 该方向收益 − 同一时间窗（30 分钟 cohort）内所有被评分币的平均多头收益`；`beat_market` 为跑赢该均值的比例（不再是相对 BTC）。
- **样本去重**：同一个币、同一方向，4 小时内只算一次（读取时去重，入库也按 4h 去重）。

### 4.6 模型选择
- `GET /api/copytrade/coin-score/models` → 可用模型 + 当前选中
- `PUT /api/copytrade/coin-score/model`，body `{ "model_id":"..." }`：同时把**当前登录账号**记为定时评分/AI 池的归属账号（`copytrade_score_owner`）。

### 4.7 方向榜（看板用）
`GET /api/market-board/direction?symbols=ROBOUSDT,SKYUSDT,...` → 只对传入的币算"因子方向"；不传则取成交额前 30。看板传入的是策略候选币 + 持仓。返回 `{ items:[ {rank,symbol,bias('bullish|bearish|neutral'),directionScore,...} ] }`。

### 4.8 币池 CRUD（普通币池，含 AI 池）
- `GET /api/coin-pools` → `{ pools: [ {id,name,screener,interval,coins:[...],created_at,updated_at} ] }`
- `POST /api/coin-pools` / `PUT /api/coin-pools/:id` / `DELETE /api/coin-pools/:id`

---

## 5. AI 评分设计（为什么可信 / 你要注意的口径）

- **双向对称**：同时给 `bull_score` 与 `bear_score`，`bias` 在多空接近时为 `neutral`；不会再"只会看多"。
- **多周期**：15m + 1h + 4h K 线与指标（MA/EMA/MACD/RSI/KDJ/BOLL/ATR）。
- **衍生品**：持仓量 OI 及 1h 变化、大户多空比、taker 买卖比、basis、资金费率；加 BTC 大盘 regime。
- **带单拥挤是"一个因素"**：带单"拥挤多头被套/空头被套"只作为**一个**输入，不自动决定方向（避免一键看空）。
- **多空对称提示词**：做多证据与做空证据逐条对称列出（不再只详列做空 9 条）。
- **相对强弱**：提示词注入"该币 24h 涨跌 vs BTC 24h 涨跌（差值）"及各周期动量，要求模型**按相对强弱定方向**（强于 BTC/市场可做多、弱于则做空），而不是跟着 BTC 绝对方向走。
- **上下文记忆 + EMA 平滑**：`copytrade_score_memory` 记录上次研判，模型做定点重估；`smoothed_edge = 0.3*new + 0.7*prev`，排序用平滑值，减少抖动。
- **前向验证**：`copytrade_score_outcomes` 记录 4/24/72h 收益，按**同一时刻全部被评分币的平均收益**为基准衡量超额收益（同币同向 4h 内只算一次），检验"高分是否真的准、空头是否赚钱"。

> 注意：`bias` 是**研判方向**，不等于一定会开仓；`score` 是**方向强度**（0-100），下游可自行设阈值。

---

## 6. 调度与成本（影响时效）

- **逐币到期轮询**：每个币按"距上次分析 ≥ `interval_minutes`"到期即分析，**最旧的先刷**，受 `concurrency` 限制；不再是整批跑完再等。
- **并发**：`concurrency` 可设置（1–任意，实际受底座上限约束，见第 10 节）。
- **成交额过滤**：`min_quote_volume`（USDT）——24h 成交额低于此值的币**不做 AI 分析**（防插针）；未知(0)不参与过滤。
- **变化触发**：定时任务会跳过"近期已分析且未到期"的币。

### 6.1 定时任务归属账号

定时评分与定时建池没有请求上下文，按以下顺序决定"为哪个账号跑"（评分和 AI 池都存到这个账号下）：

1. `copytrade_score_owner`（最后一次在带单页选择评分模型的账号）；
2. 已保存的 `copytrade_score_model` 对应模型的所属账号；
3. 兜底：任一已启用且有 key 的模型的所属账号（按更新时间）。

**要让某个账号的策略能选到 AI 池，请用该账号在带单页选一次评分模型。**

---

## 7. 选池规则（总 AI 选池）

- 上限：**总数 ≤10**，**多/空每侧 ≤7**。
- 进/出滞回：新币 `conviction ≥ 20` 才进；池内已有币 `≥ 10` 即保留（避免噪声进出）。
- `conviction = |smoothed_edge|`（无平滑值时用 conviction 字段）。
- **方向一致**：`bias` 与 `smoothed_edge` 的符号相反（例如最新判"多"但平滑值仍偏空）时，本轮不入池，避免"方向是多、分数却来自空头强度"。
- 只收"新鲜评分"：评分年龄 > `max(interval_minutes + pool_refresh_minutes, pool_ttl_minutes)` 的币不入选。
- **TTL**：`expiry_at = 生成时间 + pool_ttl_minutes`；每次刷新重算。引擎遇到 `expiry_at` 已过就**不交易**该币。
- 落地：`screener=copytrade_ai`、`name=带单AI币池`，每个账号一个，**原地更新、ID 固定**。本轮没有合格币时把池更新为空（不删除），从未建过池且无合格币时不创建。

---

## 8. 接入策略（让策略交易这个池）

策略配置里的币源（`config.ai_config.coin_source`）设为：
```json
{ "source_type": "coin_pool", "coin_pool_id": "<币池 id>" }
```
- 前端操作：策略编辑 → 币源选「我的币池」→ 选「带单AI币池」→ 保存；交易员选用该策略。
- 引擎（kernel）每轮通过 `CoinPoolResolver/CoinPoolDetailResolver` 读取该池的 `symbol + direction` 作为候选币，并过滤 `expiry_at` 过期的币。
- 候选币规则：

| 池状态 | 候选币 |
|---|---|
| 有未过期的币 | 这些币（带方向） |
| 池存在但为空 / 全部过期 | **无候选**：本轮不开新仓，已有持仓照常管理 |
| 池不存在（被手动删除） | 回退到策略保存时的币种快照（无方向、无过期） |

- 候选币的方向会写进策略提示词（软约束："该币只做多/只做空，禁止反向"）。
- **硬拦截（禁止反向开仓）默认关闭**；如需开启，设置环境变量 `NOFX_RESPECT_POOL_DIRECTION=1`（会直接影响下单，谨慎）。
- 前置条件：**币池归属 `user_id` 必须与策略/交易员同账号**（见 6.1），否则选不到该池。
- 旧版本在"空池"时会删池再新建，池 ID 会变，引用旧 ID 的策略会一直交易保存时的快照。升级后请在策略里**重新选择一次「带单AI币池」**。

---

## 9. 接入看板（信号矩阵）

- 看板「信号矩阵」只跟随**交易员**：币 = 最新一轮决策的 `candidate_coins`（由策略币源加载）+ 当前持仓，逐个按 `market-board/direction` 取因子方向。
- 交易员的策略用的是「带单AI币池」时，看板显示的就是这个池里本轮有效的币；池变了，下一轮决策后看板跟着变。
- 看板**不直接读** `/copytrade/coin-pool`，也没有"AI币池/合并"切换（`352c551`/`a861802` 中的直读与切换已撤回）。
- 看板只做展示/复核，不直接下单；下单由策略完成。

---

## 10. 部署与运维

- 服务器地址、登录方式、数据库位置等运维信息**不写入本文**，向维护者获取；改动线上需维护者明确同意。
- 容器：`nofx-trading`（后端）、`nofx-frontend`（前端），`docker compose` 管理。
- API 限流/缓存层：`internal/apischeduler`。**模型主机（未知 host）走 `default` profile**，已把上限放宽为：`MaxConcurrent=256, 200 req/s, 12000 次/分`（此前是 4/2/60，会把 AI 并发卡在 4）。
- 缓存说明：`apischeduler` 是"微缓存 + 并发合并 + 限流"，不是历史缓存。各接口 TTL：K线 2s、ticker 2s、premiumIndex 1s、OI/funding/OIHist 10s、大户多空比/taker 60s；其余 0（不缓存）。跨轮次不会复用。
- 备份：改配置/DB 前先备份数据库文件。

---

## 11. 给下游 Agent 的对接建议（最常用）

**目标：让 AI 币池里的币被交易。正确做法是走策略，不要自己按币池下单。**

1. 确认 AI 池归属账号就是要交易的账号（6.1）：用该账号在带单页选一次评分模型。
2. 确认池已生成：`GET /api/copytrade/coin-pool` 返回非空 `id`。
3. 策略币源设为 `{ "source_type": "coin_pool", "coin_pool_id": "<该 id>" }`（或在策略编辑页选「我的币池 → 带单AI币池」）。
4. 交易员选用该策略并启动（启动实盘前需用户确认）。
5. 核对：交易员日志出现 `Strategy engine fetched candidate coins: N`；看板信号矩阵出现同一批币。
6. 池为空或全部过期时本轮无候选是**预期行为**，不是故障。

只读分析（不交易）时可以直接读接口：
```js
const r = await fetch(`${BASE}/api/copytrade/coin-pool`, { headers: { Authorization: `Bearer ${TOKEN}` } });
const { id, coins } = await r.json();
const now = Date.now();
const usable = coins.filter(c => !c.expiry_at || c.expiry_at > now);
// c.symbol, c.direction(long|short), c.score(浮点 0-100)
```

---

## 12. 已知限制 / 注意事项

1. **另一个 Agent 若要"策略级"接入**：确保币池 `user_id` 与它操作的交易员账号一致（6.1）；否则 `GET /coin-pools` 或策略选池看不到该池。
2. **默认关闭的影响交易项**：方向硬拦截关闭；自动改实盘策略不会发生，需要显式配置。
3. **服务器前端是旧分叉**：之前线上就地打过"矩阵直接读池"的补丁，与本仓库现在的设计（看板只跟随策略）不一致；下次部署前端时以仓库代码为准，覆盖掉该补丁。
4. **K线/TTL 时效**：池每 `pool_refresh_minutes` 刷新并重算 `expiry_at`；若定时分析停摆，评分会变"不新鲜"而不入池（这是预期保护）。
5. **并发过高**：`concurrency` 设太大可能触发模型供应商（如 DeepSeek）自身限流（429），建议 20–40。
6. **偶发取数错误**：并发下 `Binance depth / Hyperliquid klines context canceled` 为瞬态超时，不影响主流程；若持续需排查网络/限流。

---

## 13. 关键系统配置键（`system_config`）

| key | 内容 |
|---|---|
| `copytrade_score_model` | 默认评分模型 ID |
| `copytrade_score_owner` | 定时评分/AI 池归属账号（选评分模型时写入） |
| `copytrade_coin_score_auto` | 定时分析设置 JSON：`{enabled,interval_minutes,pool_auto,top_n,concurrency,min_quote_volume,pool_refresh_minutes,pool_ttl_minutes}` |

---

## 14. 关键常量（AI 选池）

| 常量 | 值 |
|---|---|
| 池名 `poolName` | `带单AI币池` |
| Scanner `poolScreener` | `copytrade_ai` |
| 总数上限 `poolMaxTotal` | 10 |
| 每侧上限 `poolPerSide` | 7 |
| 进入阈值 `poolEntryConv` | 20 |
| 保留阈值 `poolExitConv` | 10 |
| TTL 默认 | 12 分钟（可配） |
| 刷新间隔默认 | 5 分钟（可配） |

---

## 15. 相关提交（便于追溯）

`050b9e0` 币池定时刷新/TTL/新鲜/变化日志 → `daa0a48` 设置+面板 → `495aca5` 新鲜窗口修复 → `352c551`/`a861802` 看板矩阵读池与数据源切换（**已撤回**：看板改回只跟随策略候选）→ `72fb45c` 衍生品接口缓存 → `ee54efb`/`f3b6c01` 调度并发上调 → 2026-11-20 修正：AI 池 ID 固定、空池不回退快照、定时任务归属账号、方向一致过滤。

（如需接口字段的最终真值，以 `api/handler_copytrade_score.go`、`api/handler_copytrade_select.go`、`store/coin_pool.go` 为准。）
