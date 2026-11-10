# 带单（Copy-trade）功能交接文档

给接手的开发者或 agent。先读完「工作约定」再动手。

## 1. 功能是什么

监控用户在「带单」页关注的币安合约带单员，回放他们的成交，得到每个人的当前持仓和动作，再汇总成每个币的多空方向和置信度。这些数据有三个用途：

1. 「带单」页展示：发现交易员、我的关注、按币种、动态。
2. 作为策略的币源 `copy_trade`（策略页「带单币种」），选出带单员偏向明确的币交给 AI。
3. 作为任意币源的附加参考 `copy_trade_context`，在提示词里写出每个币上带单员的多空、均价、被套、新开、持仓人明细和最近动作。程序还会按 `copy_trade_follow` 强制开仓方向。

只做合约。币安的股票、商品合约（TRADIFI_PERPETUAL，如 SNDKUSDT、MUUSDT）在 binance 和 binance_paper 交易所上按币安合约交易，在其他交易所映射成 Hyperliquid 的 `xyz:SNDK`。

## 2. 数据流

```
币安 bapi (copytrade/client.go, endpoints.go)
  → Monitor 轮询 (copytrade/monitor.go)：资料每 6h，成交增量，公开持仓
  → 成交存 SQLite (store/copytrade.go)，保留 30 天
  → Replay 回放成持仓和动作 (copytrade/replay.go)
  → assessHealth 按标记价算被套/新开/强平 (copytrade/health.go)
  → Symbols 加权投票得到置信度 (copytrade/views.go, score.go)
  → api/handler_copytrade.go: copyTradeCoins() 设置 kernel.CopyTradeResolver
  → kernel/copy_trade.go: 选币、写提示词、OpenBlockReason 拦截开仓
  → trader/: 决策执行前调用 OpenBlockReason，决策记录存 CopyTrade 摘要
  → 前端看板 DecisionCopyTrade.tsx 显示 AI 看到的带单参考
```

copytrade 和 kernel 互不 import，只靠 `kernel.CopyTradeResolver` 这个函数变量连接，它在 api 层赋值。

## 3. 关键文件

| 文件 | 内容 |
|---|---|
| `copytrade/client.go` | 请求限速、代理、暂停（被限流后退避）、`checkFormat` 检测币安改格式 |
| `copytrade/endpoints.go` | 排行榜、详情、持仓、成交历史接口；`Detail.Private()` 私密账户 |
| `copytrade/monitor.go` | 轮询调度、首次回填 30 天（最多 25 页）、`Load()` 启动时从库重建、关注/取关 |
| `copytrade/replay.go` | 成交 → 持仓和动作；开在监控之前的仓位减仓记为 Untracked |
| `copytrade/health.go` | 被套（亏掉保证金 50%，杠杆未知按 10x 换算）、新开（24h 内开或加）、强平推断 |
| `copytrade/score.go` | 带单员可信度 0–100；币的置信度 = 100·一致度·人数系数·(0.5+0.5·质量)·健康度 |
| `copytrade/views.go` | `Symbols` 投票（按仓位占余额、健康度、高频降权 0.4）、`Holders`、`Feed`、`Fresh` |
| `copytrade/signals.go` | 每小时记录置信度信号，4h/24h 后算命中率；连续同向只算一次 |
| `api/handler_copytrade.go` | 所有 `/api/copytrade/*` 接口；`StartCopyTrade`；`copyTradeCoins` |
| `kernel/copy_trade.go` | 候选币、提示词文本、`copyTradeVsAvg` 追高提示、`copyTradeExitAlert` 离场提醒、`OpenBlockReason` |
| `kernel/engine_prompt.go` | 系统提示里的跟随规则、最小止损 ATR 行；候选币和持仓块里插入带单文本 |
| `market/binance_symbols.go` | `NormalizeFor(symbol, exchange)`、币安 K 线 |
| `store/strategy.go` | `CoinSourceConfig.CopyTrade*` 字段、`NormalizeCopyTrade()`、`RiskControl.MinStopATR` |
| `store/decision.go` | 决策记录的 `copy_trade` 列 |
| `web/src/components/copytrade/` | 带单页各标签页 |
| `web/src/components/strategy/CopyTradeSource.tsx` | 策略页带单币源面板、跟随/自由/反向选择 |
| `web/src/lib/strategyForm.ts` | 表单和后端配置互转，限制值和后端一致 |

`main.go` 里 `NewServer` 和 `StartCopyTrade` 必须在 `LoadTradersFromStore` 之前，否则重启后第一轮决策拿不到带单数据。

## 4. 策略配置字段（`ai_config.coin_source`）

| 字段 | 默认 | 说明 |
|---|---|---|
| `source_type: "copy_trade"` | | 带单币源 |
| `copy_trade_min_confidence` | 40 | 置信度下限 1–100 |
| `copy_trade_direction` | both | both / long / short，只选偏向这一边的币 |
| `copy_trade_limit` | 5 | 最多几个币，1–10 |
| `copy_trade_min_traders` | 2 | 至少几人持有 |
| `copy_trade_context` | copy_trade 时恒为 true | 提示词里附带带单参考 |
| `copy_trade_follow` | copy_trade 为 follow，否则 free | follow / free / fade，程序强制 |
| `copy_trade_context_min_confidence` | 40 | 只加上下文时，跟随/反向的开仓需要的置信度 1–100（带单币源用 `copy_trade_min_confidence`） |

`risk_control.min_stop_atr`（0–5）：止损离价格小于这个倍数的主周期 ATR14 时拒绝开仓。

注意 `coin_source` 在 `ai_config` 下面，不在顶层。

## 5. 本地开发和测试

```bash
export PATH=$HOME/.local/go/bin:$PATH
go build ./... && go test ./copytrade/ ./kernel/ ./store/ ./api/ ./market/ ./trader/...
gofmt -l copytrade kernel store api market trader

cd web && npx vitest run && npm run lint && npx prettier --check "src/**/*.{ts,tsx}" && npx tsc --noEmit
```

本地后端要走代理才能访问币安：

```bash
HTTPS_PROXY=http://127.0.0.1:7897 HTTP_PROXY=http://127.0.0.1:7897 NO_PROXY=127.0.0.1,localhost ./nofx
```

调受保护接口需要 JWT：用 `.env` 里的 `JWT_SECRET` 在临时脚本里签发，放进环境变量使用，不要打印出来，用完删除脚本。

gofmt 会对齐结构体字段；新字段加在结构体中间时注意对齐，或者放到末尾。
## 6. 部署

服务器上用 docker compose 运行，代码目录 `/opt/nofx`，跟踪 `origin/dev`。登录方式向用户要，不要写进仓库。

1. 先备份数据库（每次部署前必须做）：
   ```bash
   cd /opt/nofx && stamp=$(date +%Y%m%d-%H%M%S)
   docker compose exec -T nofx sh -c "sqlite3 -cmd '.timeout 20000' /app/data/data.db \".backup /app/data/data.db.bak-$stamp-before-<说明>\""
   ```
2. 更新代码：`git fetch -q origin dev && git merge --ff-only origin/dev`
3. 构建重启。**只重建改过的那一半**，不要每次两个都建：
   - 改了 Go（多数情况）：`docker compose up -d --build nofx`
   - 只改了 `web/`：`docker compose up -d --build nofx-frontend`
   - 两边都改了才跑 `docker compose up -d --build`

   两个 Dockerfile 都用了 BuildKit 缓存挂载（`/go/pkg/mod`、`/root/.cache/go-build`、`/root/.npm`、`/var/cache/apk`）：第一次构建仍是全量（约 15–20 分钟，Go 是 CGO + TA-Lib），之后改代码只重编受影响的包，通常 1–3 分钟。**不要加 `--no-cache`**，那会把缓存全丢掉。
4. 检查：`docker compose ps`，以及 `docker compose logs --since 3m nofx | grep -iE "copy-trade|error"`。
5. 磁盘：构建缓存会长到几 GB，空间紧张时清旧的：`docker image prune -f`（只删悬空镜像，安全），或 `docker builder prune --keep-storage 8GB`。

服务器的 `.env` 只有 root 能读，不要输出它的内容。

## 7. 现状（2026-11-05）

- 服务器上有一个用「带单」策略的交易员，在 binance_paper 模拟盘运行。设置：1h + 4h K 线各 30 根（30 是 `MaxKlineCount` 上限），最多 4 个仓位，保证金最多用 50%，盈亏比不低于 2，置信度不低于 70，止损至少 1.5 ATR，跟随模式。
- 当前持有 MU 多单，按旧设置开的，仓位价值约是账户的 3 倍。
- 关注的账户里有 8 个是私密账户，不贡献任何数据。「改为关注公域」的单账户按钮前端已有（`FollowingTab.tsx` 的 `followPublic`），剩下的是批量处理老关注。

## 8. 已知问题和待办

按建议顺序。用户还没确认要做的，动手前先问。

**第一批已修（数据可用性 + 回放与权重）**
- 带单数据断了或只有部分账户新鲜时，不再写成"没人持有这个币"：`kernel.CopyTradeResolver` 现在返回 `CopyTradeView{Coins, Fresh, Total, Reason}`，`api.copyTradeView` 用 30 分钟新鲜度和「非私密关注数」算出覆盖率，`formatCopyTrade` 分「完全没数据 / 部分陈旧 / 完整」三种写法，`Context.CopyTradeCoverage` 带给提示词。数据不完整时不发离场提醒，也不给出候选币（`copyTradeCandidates`）。
- 回填窗口之前开的仓：`Monitor.rebuild` 用「Binance 公开持仓量 > 回放累计量」判断该仓早于成交记录（`preOpenedBooks`），再用 `ReplayKnown` 把第一次见到的同向成交标成 `add`、`OpenedAt=0`、`Position.PreExisting=true`，不再当新开仓。
- 被套已统一为当前平均开仓价下的浮亏口径，与表中 `ReturnPct` 一致：保证金亏损达到 50%，或没有保证金时价格反向达到 5%。历史首笔价格不再设置 `Trapped`；`HistoricalEntryWarning` 单独提示更不利首笔记录的参考偏离，并给出数值、阈值和是否使用当前杠杆折算。它不是历史实际盈亏，也不增加被套人数或额外置信度惩罚。记录可能不完整。
- `healthFactor` 仍按当前亏损连续降权，只有当前达到被套阈值时才取消「新开 ×1.3」加成（`p.Fresh && !p.Trapped`）；独立历史提示不参与该判定。
- 离场提醒阈值改成「离场人数 ≥ 同方向人数的 1/3」（`exitAlertShare`），不再是 1 个人减仓就提醒。

**第二批已修（门槛、说明、迁移、预览）**
- 跟随/反向模式原来只看方向：非带单币源加上下文时，1 个人、置信度 5 也能开仓。现在 `OpenBlockReason` 会检查置信度，带单币源用 `copy_trade_min_confidence`，只加上下文时用新字段 `copy_trade_context_min_confidence`（默认 40），策略页在选了跟随/反向时显示这个输入框，系统提示词也会写出这个门槛。
- `api/server.go` 给 AI 的策略说明补上了 `copy_trade_follow` 和 `copy_trade_context_min_confidence`，`primary_count` 改回真实的 10–30。
- PostgreSQL：`decision_records` 表已存在时会手动补 `copy_trade` 列，不再整条决策记录插入失败。
- `/copytrade/symbols?fresh=1` 只返回 30 分钟内刷新过的带单员数据，策略页「当前会交易」用它，并在只有部分账户新鲜时提示实际可选币可能更少。

**第三批已修（带单员风格）**
- 新增 `copytrade/style.go`：用 30 天成交回放每个币的完整来回，算持仓时长中位数、每笔加仓次数、逆势加仓比例、亏损离场占比，分 短线 <4h / 波段 4h–3d / 长线 >3d / 摊平型 / 样本不足(<5 笔) / 未知(私密)。每个币的第一次来回不计入样本（可能是回填前开的，持仓时长会偏短）。结果挂在 `Snapshot.Style`，`Monitor.rebuild` 每次重算。
- 摊平型票权 ×0.3（`martingaleWeight`）：判定是「平均每笔至少 1 次加仓 + 过半加仓在水下」，不看离场盈亏（摊平型多半等回本才走），在 `views.Symbols` 里生效。
- 提示词：持仓人明细带 `averages into losses` / `scalp` / `long` 标签，系统提示词解释这些标签和 0.3 的票权。
- 前端：「我的关注」显示风格徽章并可按风格筛选；「按币种」的持仓人明细显示风格标签。

**第四批已修（已知代码问题清单）**
- 杠杆未知说清楚：推断出来的持仓（没有真实杠杆）在提示词里写成 `-6.2% price (leverage unknown)`，系统提示词说明百分比是价格变动、"被套"按 10x 假设。
- 持仓时间参与权重：`holdFactor`（<1h ×0.6、<4h ×0.8、更久 ×1，开在成交记录之前的按长持算），在 `views.Symbols` 里和健康度、高频、风格一起算。
- 加仓不再抬高票权：`Position.FirstValue` 记录首次成交的量价，`stakeFactor` 用它而不是累计成本；`openedValue` 在缺失时回退到当前价值。
- 置信度下限 10：`store.MinCopyTradeMinConfidence`，`NormalizeCopyTrade`/`ClampLimits` 和前端 `COPY_TRADE_LIMITS` 一起改，避免把门槛设成 1 等于没有门槛。
- `getEffectiveCoinCount` 先归一化 `CoinSourceConfig`，Limit 为 0 时按默认 5 个币估算 token，而不是退到 3。
- 决策记录不再重复存带单原文：`DecisionCopyTrade.Text` 删除，原文本来就在 `input_prompt` 里；前端决策卡改为提示去「用户提示词」查看。
- 「我的关注」的「关注时间」排序已实现（按 `followed_at` 倒序）。

**第五批（自动关注 + 老仓降权）**
- `copytrade/auto.go`：用户只设目标人数（5–100，默认 50），设置存在 `system_config` 的 `copytrade_auto_follow`，默认关闭。每小时跑一次（启动 5 分钟后第一次）。
- 补人：读 90 天跟单收益榜前 2 页、90 天收益榜、30 天跟单收益榜，`pickable` 过滤带单满 60 天、90 天收益 > 0、回撤 ≤ 30%、跟单者盈利，按 `ScoreTrader` 排序；`checkPick` 再查资料和 30 天表现（非私域、ACTIVE、余额 ≥ 5000、30 天收益 > 0、回撤 ≤ 30%；不公开持仓的也要，持仓按公开成交推算）。每轮最多查 20 人、补 10 人；没过资料检查的记一条 `skip` 日志（原因 `private` / `closed` / `pick_balance=余额` / `pick_roi30=收益,mdd30=回撤`），24 小时内不再查（内存里记，重启后重查）。
- 观察期：新选的人 7 天内只受「停止带单 / 私域」两条规则约束，投票 ×0.5（`probationWeight`）。
- 淘汰（`DropReasons`，需资料已同步且 30 天成交回填完成）：停止带单、私域、摊平型、30 天收益 < 0、回撤 > 40%、余额 < 1000、过半已定价持仓是 7 天以上的被套单（且至少 2 个）、两周无交易且无持仓。每 24 小时最多淘汰目标人数的 10%（超出目标时不受限）；淘汰后 30 天内不再选（`copytrade_exclusions`）。目标调低时先淘汰违规的，再按分数从低到高减员。
- `CopyTradeFollow.Source`：`auto`（默认，老关注也算 auto）/ `manual`（手动关注）。手动关注永不被自动取关，只在「我的关注」显示「建议移除」。手动取关会写入永久排除，手动重新关注会解除排除。操作记录在 `copytrade_auto_log`（保留 500 条）。
- 接口：`GET/PUT /api/copytrade/auto`、`DELETE /api/copytrade/auto/exclusions/:id`。前端 `AutoFollowPanel.tsx` 在「我的关注」顶部。
- 持仓时间权重改为：<1h ×0.6、<4h ×0.8、<3 天 ×1、3–7 天 ×0.6、≥7 天或开在回填之前 ×0.3。年龄权重放进 `base`，不影响 `Confidence.Health`（只反映亏损）。依据：服务器数据里 7 天以上的持仓 85% 浮亏。

**第六批（选币质量的五项门槛）**
- 带单员个人准确率（`copytrade/accuracy.go`）：用 30 天成交里每次开仓/加仓（同币同方向 24 小时内重复的算一次）对比 24 小时后的 1 小时 K 线收盘价。每 6 小时重算一次（启动 10 分钟后第一次），每个币请求一次 fapi 1h K 线（约 746 根），间隔 2 秒。投票按 `accuracyFactor` 加权：满 10 次后按收缩后的准确率（向 50% 拉 10 个虚拟样本）在 ×0.5–×1.5 之间；满 30 次且收缩后低于 45% 时触发自动关注淘汰规则 `inaccurate`。结果在内存里（`Monitor.accuracy`），重启后 10 分钟内为空，投票按 ×1。
- 新开仓共识：`SideSummary.Openers` = 偏向那一边 24 小时内新开/加仓、没被套、非高频、非摊平型的人数。`copy_trade_min_openers`（默认 0 关闭）：带单币源去掉不够的币，跟随/反向模式拒绝开仓。
- 命中率门槛：`hitRates` 按方向拆分（`HitRate.Direction` = all/long/short）；`Monitor.RecentHitRates` 缓存 10 分钟给引擎。`copy_trade_min_hit_rate` + `copy_trade_min_samples`（默认 30）：跟随/反向开仓前查该币置信度区间 + 方向的 30 天 24 小时命中率，样本不够或低于门槛就拒绝；反向模式用 100 − 命中率。提示词每个币多一行「Track record」。
- 行情过滤（`kernel/copy_trade_gates.go`）：`copy_trade_min_volume_millions` 按 K 线估算 24 小时成交额，低于门槛拒绝开仓（只要开了带单参考就生效）；`copy_trade_trend_filter` 跟随/反向时 4 小时 EMA20 低于 EMA50（差 0.2% 以上）不做多、高于不做空。持仓量过滤沿用原来的 `risk_control.min_oi_value_millions`。
- 多空分开的命中率：带单页「历史命中率」面板可选「多空合计 / 只看偏多 / 只看偏空」。
- 前端：策略页带单面板和「附带带单参考」下都有「信号质量门槛」；「我的关注」卡片显示个人准确率；「按币种」显示有效新开人数。

**第七批（跟单：别的币安账户跟看板上的 AI 下单）**
- 和上面「跟币安带单员」是两件事：这里 AI 交易员是带单方，用户自己的其他币安账户（实盘 `binance` 或模拟盘 `binance_paper`）是跟单方。只支持币安。
- 后端 `mirror/`：每个运行中的跟单（`mirror_links`）一个 goroutine，每 10 秒对比一次 AI 和跟单账户的持仓（`plan.go`，纯函数），按差异开/加/减/平。AI 自己下单后经 `trader.SetPositionHook` 立刻唤醒（等 3 秒让 AI 挂好止损），交易所侧触发的止损止盈/强平靠 10 秒轮询发现。每个 AI 仓位的跟单状态存在 `mirror_positions`，记录在 `mirror_events`（每个跟单保留 1000 条）。
- 规则：开始（或重新开始）时 AI 已有的仓位和这期间的加仓不跟，只跟之后的；平仓和减仓总是跟。固定金额 = 每单 `fixed_margin` U 保证金；按比例 = AI 这单保证金占它权益的比例 × `ratio` × 跟单账户权益。杠杆跟 AI（`max_leverage` 封顶，0 不限）或固定。`follow_protect` 跟 AI 的止盈止损（每 30 秒或下单后对比一次，变了就撤掉该币的止盈止损重挂）；不跟也会跟着 AI 平仓。加仓/减仓按 AI 的变化比例。开仓失败跳过并记原因，平仓失败每轮重试（同样的错误 10 分钟只记一次）。跟单仓位被外部平掉后不再重开。用户在同一个币上自己多拿的部分，AI 平仓时不动。`max_margin` 单笔保证金上限；`max_loss` 账户比开始时亏这么多就平掉跟单仓位并停止。
- 账户互斥（`api/handler_mirror.go` 的 `mirrorBusy`，每轮都查）：不能是 AI 自己的账户，不能有正在运行的 AI 交易员在用，不能被另一个运行中的跟单占用。同一个 API key 配成两行也算同一个账户。实盘必须带 `confirm_real`。
- 接口：`GET /api/mirror/accounts`、`GET/POST /api/mirror/links`、`PUT/DELETE /api/mirror/links/:id`、`POST /api/mirror/links/:id/start|stop`（stop 可带 `close_positions`）、`GET /api/mirror/links/:id/events`。前端 `web/src/components/trader/MirrorPanel.tsx` 在看板页。
- 测试：`mirror/engine_test.go`（假账户跑完整开/加/减/平/止损、比例、亏损上限、失败重试、外部平仓）、`api/handler_mirror_test.go`（账户安全检查）、`web/src/components/trader/mirrorFormat.test.ts`。

**其他**
- 私密账户批量换成公开账户，没有公开账户的取消关注；老关注重新回填到 30 天（把 `FillsUntil` 置 0 会触发回填，每个账户最多 25 页请求）。
- 「代理设置」在设置页的「数据」标签里，不是带单页的 tab；要挪位置属于产品决定，没动。

## 9. 工作约定

- 用中文回复用户。用户说"先回答我"时，只解释，不改代码。
- 每个改动都提交并推送到 `origin/dev`。提交信息末尾加 `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`。git 身份和 SSH key 只在这个仓库里配置，不要改全局配置。
- 不要提交 `.env`、`data/`、`nofx` 二进制文件。
- 不要下真实订单。验证用 binance_paper 模拟盘。
- 用户要求时才部署，部署前先备份数据库。
- 币安限流时退避，不要绕过它的反爬机制，也不要自己额外发请求去探测。
- 改表单限制值时，前端 `strategyForm.ts` 的 `LIMITS` 和后端 `ClampLimits` 一起改。
- 测试：`copytrade/*_test.go`、`kernel/copy_trade_test.go`、`web/src/components/strategy/CopyTradeSource.test.ts`、`web/src/components/copytrade/*.test.ts`。
