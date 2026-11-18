# 带单改造：对接文档

- 日期：2026-11-18
- 用途：交给下一个 agent 接手。读完这一份就能开工，细节数据在方案文档里
- 方案和全部回测结果：`docs/plans/2026-11-18-copy-trade-style-scoring.zh-CN.md`
- 更早的背景：`docs/plans/2026-11-11-copy-trade-optimization.zh-CN.md`

## 一句话现状

三版回测做完了，结论是"不打分、直接跟单"最好。用户已同意做**榜单同步**和**模拟跟单记录**，并要求**提交本地改动、部署到服务器**。这三件事都还没开始：代码一行没写，没有提交，没有部署。

## 用户已经定下的事

| 项 | 决定 |
|---|---|
| 关注名单 | 币安带单榜单，90 天、按跟单者收益排序，取前 100 个读得到数据的人 |
| 自动关注 | 停用，代码保留以便回退 |
| 手动关注 | 保留，不受榜单同步影响 |
| 私域、信号账户 | 读不到成交。跳过并顺延；榜单前 100 内的私域单列为"只观察" |
| 摊平型 | 不计票 |
| 高频账户 | 关注，暂不计票 |
| 请求间隔 | 2 秒 |
| 服务器 | **不要在服务器上做请求压力测试**（用户明确说过） |
| 下一步 | 做榜单同步 + 模拟跟单，部署到服务器跑，本地改动提交 |

用户对私域账户很在意（"权重很高，不能轻易放弃"），不要只写一句"跳过"了事，页面上要能看到他们。

## 回测结论（决定了要做什么）

1. **按风格给持仓加权**：不比现有置信度好，三条切换标准都没达到。不要上线。
2. **跟随开仓、按风格周期持有**：短线 4 小时有很薄的优势（跑赢 BTC 57.6%，扣费后中位数约 +0.06%）。波段没通过对照检验。个人历史命中率不能预测之后的开仓。
3. **直接跟单，每个动作都跟**：最好。晚 0–5 分钟、扣 0.05% 手续费，盈利比例 62%，每仓中位数 +0.44%。
   - 只跟开仓会丢掉约三分之二的收益；自己定 4 或 24 小时退出更差。
   - 只跟波段和短线的 36 人，用不到四分之一的本金拿到六成多的收益。
   - **收益几乎全部来自下跌行情里的空单**（空 21.7 份，多 1.3 份）。
   - **名单是今天的榜单**，这些人上榜正是因为这段时间赚钱，结果必然偏好。

所以模拟跟单的目的只有一个：**固定名单、往后记录**，去掉上面两个偏差。在它跑出结果之前，不要让任何新逻辑影响真实下单。

## 要做的事

### 1. 榜单同步

替代自动关注，决定关注谁。

- 每天一次，读 90 天 `COPIER_PNL` 榜单，按顺序取前 100 个 `portfolioType == "PUBLIC"` 的人。`Leader.PortfolioType` 字段已加（`copytrade/endpoints.go`）。今天的实测是要翻到第 163 名才凑够 100 个公开账户，大约 6 页。
- 信号账户（`SIGNAL`）有对应的公开账户，沿用 `checkPick` 里的转换逻辑（`copytrade/auto.go:571`）。
- 进出要有缓冲，避免在边界上反复进出。之前和用户说的口径是：进前 100 就关注，掉到 150 名以外或连续 3 天不在榜上才取关。这是我的提议，用户没有单独确认数字。
- 取关用 `unfollow(id, true)` 保留成交（`copytrade/monitor.go:521`），不要写排除名单。
- 新关注的人 `Source` 建议用新值（如 `"board"`），和 `"auto"`、`"manual"` 区分。注意很多地方把"不是 manual"当成"自动关注管理的"：`onProbation`、`planRemovals`、`mergeAliases`、`dropAliases`、前端卡片上的文字。逐个检查。
- `MaxFollows = 100` 是硬上限（`copytrade/monitor.go:20`），`canFollow` 会拒绝。手动关注加上榜单 100 人会超，需要决定：提高上限，或榜单名额让位给手动关注。**这一点没问过用户，动手前问一句。**
- 自动关注要停掉：`autoLoop` 仍在 `Monitor.Run` 里启动，线上配置是开启的。不停的话它会按旧规则把榜单上的人淘汰掉（高频、摊平型、收益转负等）。
- 私域"只观察"：榜单前 100 内的私域账户存下来，页面单列，显示最后交易时间、30 天业绩、币种成交占比。币种占比接口见"实测"一节，代码里还没有封装。

### 2. 模拟跟单记录

不下单，只记账。

- **名单要固定**：记录开始时把名单存下来，之后榜单变了也继续记这批人。这是去掉幸存者偏差的关键，不要图省事直接用"当前关注的人"。
- 每个带单员的每个动作（开、加、减、平）记一笔模拟成交：
  - 时间用**我们实际看到它的时间**，不是他的成交时间。两者的差就是延迟，单独存一列。
  - 价格用看到那一刻的标记价（`m.currentMarks()`，注意 `markLoop` 只在有人持仓时才更新）。
  - 同时存带单员自己的成交价，用来算延迟的代价。
- 口径和回测保持一致，结果才能比：按比例跟每个动作，仓位按最大时的金额算一份本金，每笔扣 0.05%。实现参考 `copytrade/copysim.go` 的 `mirror`。
- 至少按这些维度能汇总：风格、多空、是否 2 人以上同向、延迟分档、相对同向 BTC。
- 要回答的问题，按重要性：
  1. 固定名单、没见过的行情下，直接跟单还赚不赚。
  2. 多单和空单各自的表现。
  3. 只跟波段和短线是否仍然更省本金。
  4. 实际延迟是多少，代价多大。
  5. "前两周表现好的人后面也好"这条线索是否成立。
- 新表要进 `CopyTradeStore.initTables` 的 `AutoMigrate`（`store/copytrade.go:82`）。
- 成交保留期现在是 30 天（`retention`，`copytrade/monitor.go:25`），到期会删。模拟成交要单独存，别依赖原始成交。

### 3. 页面

自动关注面板换成榜单同步状态，加一个模拟跟单的结果页。第一轮审查出的问题一起改，清单在本文末尾。

### 4. 提交

用户要求提交本地改动，**我没有提交**。动手前注意：

- 工作区有约 90 个文件的改动，**其中很多不是这次带单的工作**：`events/`、`chanlun/`、`api/handler_event_*.go`、`store/event_*.go`、`trader/auto_trader_event*.go`、`kernel/engine*.go`、`main.go`、大量 `web/` 文件、`.pi/`、`scripts/explosive_analyze.py`。这些来自别的会话，我没看过，不知道是否完整。
- 当前在 `main` 上，比 `origin/main` 多 22 个提交，没有推送过。
- 建议只提交带单相关的文件（列表见下），其余的问用户怎么处理。`copytrade/monitor.go`、`copytrade/replay.go`、`store/copytrade.go` 里混有更早会话的改动（`tradfi` 标记等），是带单相关的，可以一起提交。
- 提交信息沿用仓库的格式，例如 `feat(copytrade): ...`，中文描述。

这次会话改动或新增的文件：

```
copytrade/client.go            间隔 4 秒改 2 秒
copytrade/auto.go              只改了一行注释
copytrade/endpoints.go         Leader 增加 PortfolioType
copytrade/accuracy.go          scoreCallsOver（周期可变）
copytrade/monitor.go           Snapshot.StyleAccuracy（只在回测里赋值）
copytrade/signals.go           命中率增加 style 两个分组（只在回测里出现）
copytrade/backtest.go          回放里计算按风格评分和开仓跟随
copytrade/stylescore.go        新增：按风格评分（第一版）
copytrade/callfollow.go        新增：跟随开仓（第二版）
copytrade/copysim.go           新增：直接跟单模拟（第三版）
copytrade/fetch.go             新增：一次性抓成交、IsLimited
copytrade/*_test.go            上面三个新文件各有测试
store/copytrade.go             CopyTradeSignal 增加 3 个不入库的字段（gorm:"-"）
internal/apischeduler/config.go   binance-copytrade 预算 12 改 30
internal/apischeduler/policy.go   间隔 4 秒改 2 秒
cmd/copytrade_backtest/main.go    多打印几张表
cmd/copytrade_fetch/              新增：抓榜单成交
cmd/copytrade_copysim/            新增：直接跟单回测
web/src/components/copytrade/DiscoverTab.tsx   文案"每页约 4 秒"改 2 秒
docs/plans/2026-11-18-*.zh-CN.md  方案和本文
```

### 5. 部署

用户说"部署到服务器上跑"。已知的情况：

- 服务器别名 `my-server`，配置在仓库根目录的 `server-ssh.config`，root 密码登录，密码由用户每次提供，不要保存。
- Docker 部署：容器 `nofx-trading`（后端）和 `nofx-frontend`。数据在宿主机 `/opt/nofx/data`，SQLite 是 `data/data.db`。
- 只读查询：`docker exec -i nofx-trading sqlite3 -readonly 'file:/app/data/data.db?mode=ro'`
- **发版流程从来没确认过**（镜像在哪构建、怎么推、谁重启）。这是 11 月 11 日就挂着的问题，开工前必须问用户。
- 线上跑的是 11 月 7 日的版本。11 月 11 日那一轮的修复也都还没上线，这次部署会把它们一起带上去，范围比"榜单同步"大得多。上线清单见 11 月 11 日方案的"实施结果"一节。
- 线上有一个真实交易的交易员"带单"，用的是 `copy_trade` 数据源。部署和重启会影响它。名单从自动关注换成榜单，它看到的币也会变。**上线前要让用户知道这一点**，并确认是否先把它停掉。
- 磁盘曾经是 85% 满，数据目录里有很多手动备份。
- 服务器 `.env` 里如果设了 `NOFX_API_SCHEDULER_CONFIG`，会覆盖带单接口的请求预算。没查过。

## 限速，容易踩的坑

- 限速有**两层**，改一处没用：`copytrade/client.go` 的 `minSpacing`，和 `internal/apischeduler` 里 `binance-copytrade` 的预算。现在都是 2 秒、每分钟 30 次。
- 实测（本机代理出口 IP，不是服务器）：2 秒和 1 秒间隔各 5 分钟都没被拒；0.5 秒间隔在 13 分钟累计第 767 次时被拒（错误码 `11012005`）。之后 27 分钟，同一 IP 用 2 秒间隔连翻 6 页成交又被拒。再等 7 分钟、用 4 秒间隔，31 分钟 549 次请求全部成功。
- 结论：限制不只看瞬时速度，被拒之后一段时间内更严。**2 秒在服务器上能否长期跑，没有验证过。** 之前向用户提议过自动退让（24 小时内被限流 2 次就回到 4 秒，并在状态栏显示），用户同意了整套提议但这一条没实现。建议和榜单同步一起做。
- 100 人、每分钟 30 次请求：如果 60 人同时活跃，每人大约 2 到 3 分钟轮到一次。这是估算，没实测。模拟跟单记下的延迟会给出真实数字。

## 实测：私域账户能读到什么

未登录状态，对榜单第 2 名（私域，ID `4794150057005208320`）：

| 接口（前缀 `https://www.binance.com/bapi/futures`） | 私域 | 公开 |
|---|---|---|
| `POST /v1/friendly/future/copy-trade/lead-portfolio/trade-history` | 空 | 有 |
| `GET /v1/friendly/future/copy-trade/lead-data/positions` | 空 | 有 |
| `POST /v1/friendly/future/copy-trade/lead-portfolio/order-history` | 空 | 有 |
| `POST /v1/friendly/future/copy-trade/lead-portfolio/transfer-history` | 空 | 有 |
| `GET /v1/friendly/future/copy-trade/lead-portfolio/detail` | 有 | 有 |
| `GET /v1/public/future/copy-trade/lead-portfolio/performance` | 有 | 有 |
| `GET /v1/public/future/copy-trade/lead-portfolio/performance/coin?portfolioId=..&timeRange=30D` | 有（各币成交占比） | 有 |
| `GET /v1/public/future/copy-trade/lead-portfolio/chart-data?portfolioId=..&timeRange=30D&dataType=ROI` | 有（每日收益） | 有 |

`order-history` 和 `transfer-history` 是这次新发现的接口，代码里没有用到。后两个接口也没有封装。

没验证过的：登录后或成为跟单者之后能否看到更多；用小号真实跟单来读私域的成交。

## 工具和数据

```
# 抓榜单前 100 个公开账户的成交，遇到限流立即停，再跑会跳过已读完的人
go run ./cmd/copytrade_fetch -db data/backtest/board.db -n 100 -spacing 4s -max-pages 5

# 前两版回测（按小时回放）
go run ./cmd/copytrade_backtest -db data/backtest/board.db -days 30 -bars-cache data/backtest/bars.json

# 第三版：直接跟单（5 分钟 K 线）
go run ./cmd/copytrade_copysim -db data/backtest/board.db -days 14 -bars-dir data/backtest/bars5m
```

`data/` 不进版本库，只在这台机器上：

| 路径 | 内容 |
|---|---|
| `data/backtest/board.db` | 99 人、65,675 笔成交，11 月 18 日抓取 |
| `data/backtest/v2-baseline/` | **基线，不要覆盖**。前两版的结果和当时的数据库。用户要求"记住这一版"，之后的版本都和它比 |
| `data/backtest/copysim_14d.txt` | 第三版的完整输出 |
| `data/backtest/bars.json`、`bars5m/` | K 线缓存 |

抓取工具每人最多读 5 页（1000 笔），45 人没读满 30 天，高频账户只有几小时到几天。

## 验证

```
go build ./...
go test ./copytrade/ ./api/ ./store/ ./internal/apischeduler/ -count=1
cd web && npx tsc --noEmit -p . && npx vitest run src/components/copytrade src/pages/CopyTradePage.test.tsx
```

交接时以上全部通过。我**没有在浏览器里打开过页面**，前端的问题都是读代码得出的。

## 不要动的

- `Monitor.Snapshots()` 里的 `distinctPortfolios` 去重。用户明确说过是有意为之，不要移动或删除。
- 影响选币或开仓的新逻辑，一律做成默认关闭的开关，改默认值之前先问用户。
- `agents.md` 里的规范针对的是产品里的交易助手，不是给写代码的 agent 的工作规则，但其中"高风险动作先确认"的要求和这里一致。

## 其它提醒

- 在 zsh 里写循环，变量名不要用 `path`，它会覆盖命令搜索路径，后面的命令全部找不到。
- `Snapshot.StyleAccuracy` 和命中率的 `style` 分组只在回测里有值，线上是空的。第一版结论是不上线，这部分代码可以留着当回测用，也可以删，看用户意思。
- 平均收益会被个别暴涨暴跌的币主导（例如 IRYSUSDT 10 月 23 日几小时内从 0.04 到 0.49 再回到 0.11，核对过是真实行情）。比较方案时看命中率、跑赢比例和中位数。

## 页面审查清单

第一轮审查的结果，都还没改。文件在 `web/src/` 下。

**会误导判断或造成误操作**

1. "源数据最旧"在正常情况下也会变黄变红。页面按 15 分钟黄、1 小时红（`components/copytrade/format.ts` 的 `staleness`），但后端对冷门的人本来就是 15 分钟查一次，3 天没成交的 1 小时一次，私域和已关闭账户的时间不再前进。后端的 `Quality()`（`copytrade/freshness.go`）已经考虑了这些，页面没用。卡片右上角的时间颜色是同一个问题。
2. 批量取关会删到看不见的人。勾选后再改筛选，被隐藏的人仍在选中集合里；确认框只写人数不列名字（`components/copytrade/FollowingTab.tsx` 的 `bulkUnfollow`）。
3. 取关的确认框只提删除成交，没提这个人会被永久排除（`excludeUnfollowed` 写的排除没有到期时间）。榜单同步后要重新想：手动取关一个榜单上的人，同步时该不该再加回来。
4. 调低目标人数不需要确认，下一轮就会清出超出的人，不受每天 10% 的限制。自动关注停用后这条消失。
5. 数据没到时显示成"没有数据"：列表加载中或请求失败时显示"还没有关注任何带单员"，标签写"我的关注 (0)"。按币种和实时动态同样。
6. 状态灯不反映单个带单员的错误。后端返回了 `failing`，页面没用（`pages/CopyTradePage.tsx` 的 `StatusBar`）。

**规则说明和后端不一致**（`components/copytrade/AutoFollowPanel.tsx`）：收益转负的条件、每天的淘汰额度、排除天数、高频和命中率淘汰都写得不对。面板换掉后这条消失。

**功能缺口**

- 展开的卡片不再更新（`FollowingTab.tsx` 里 `getFollow` 的 SWR 没有 `refreshInterval`）。
- 持仓最多显示 6 个，没有展开的办法；动作展开后最多 20 条。
- "展开更多动作"在不到 3 条时也显示。
- 提示基本只在鼠标悬停时可见。代码加了 `tabIndex`，但浏览器不会在聚焦或点击时显示 `title`；页面上还写着"点或聚焦分数看各项得分"。
- 标签页和选中的币不在地址里，刷新就回到"我的关注"。
- 按币种页没点选时，详情跟着排名第一的币走，排名一变图表就换币（`components/copytrade/CoinsTab.tsx`）。
- K 线只在切换币或周期时取一次。
- 搜币种只能搜到持仓和最近 5 条动作，因为列表接口只发 5 条（`api/handler_copytrade.go` 的 `handleCopyTradeFollows`）。

**性能**

- 命中率每 4 秒查一次库：`CoinsTab.tsx` 用 `LOCAL_SNAPSHOT_OPTIONS` 轮询 `getHitRate(30)`，每次读 30 天的信号表。注释说和命中率面板共用一次请求，但面板用的是 `useEffect`，各取各的。
- 按币种页同时有 5 个 4 秒轮询，其中 4 个各自重算全部带单员的快照。100 人时值得留意。

**小问题**

- 状态栏"关注 49/100"的分母是硬上限，旁边面板的分母是目标。
- 持有人表格下方的英文说明把"新开"写成 opened or added，和中文相反。
- "更多操作"菜单不能用 Esc 关闭。

## 开工前要问用户的

1. 发版流程：镜像怎么构建、怎么上服务器、谁重启。
2. 部署会带上 11 月 11 日那一轮的全部修复，并影响线上的"带单"交易员。是否先停掉它。
3. 工作区里不属于带单的改动（events、chanlun、web 等）怎么处理，是否一起提交。
4. 手动关注加榜单 100 人会超过 `MaxFollows`，上限怎么定。
5. 进出榜单的缓冲数字（150 名、连续 3 天）是否同意。
