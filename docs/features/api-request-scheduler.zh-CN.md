# 全项目出站 REST 请求调度与优化

## 目标和实现范围

`internal/apischeduler` 为当前 Go 后端进程提供统一出站请求预算。目标是减少重复读取，在本地预算内并行取数，不是把所有 API 简单提高到 6000 次/分钟。

已经接入：

- `market`、`marketboard`、`coinpool`、`planning/market` 的 HTTP 请求；
- `copytrade`（保留原来的串行间隔、抖动、业务错误码退避）；
- 各交易所适配器的手写 HTTP，以及 Binance、Bybit、Gate、Hyperliquid SDK 的 HTTP Client；
- Lighter SDK 的 nonce/API 公钥查询（SDK 不提供 Client 注入，使用项目内的只读等价适配）；
- 默认 MCP、Claw402 付款发送点、数据 Provider、钱包、Telegram Bot SDK/内部 API、遥测及安全 HTTP 辅助函数。

没有替换 `http.DefaultTransport`，不改代理、证书验证、SSRF 检查、签名算法或订单业务语义。付款请求在原有 single-use transport 克隆之后进入调度器，保留禁止透明重放和禁止重定向的约束。

## 调度方式

每个请求必须同时满足它适用的所有预算，然后才发送；预算原子预留，排队取消不部分消耗其他预算。

1. 按 Provider 分组的滚动分钟权重预算；同服务的已知域名别名共用预算。
2. 每秒请求窗口、并发上限；适配部分 API 的独立接口/历史数据/订单窗口。
3. 私有账户和订单请求有较高队列优先级；匿名后台读取保留 20% 分钟预算和一个并发槽给紧急请求。优先级不绕过上游限额。
4. HTTP 429/418，以及 Binance/Bybit 的部分 403，暂停对应 Provider；尊重 `Retry-After`，不自动重发请求。
5. 读取 `X-MBX-USED-WEIGHT-1M`、Bybit 剩余额度/重置头；高水位不会被乱序的低使用量响应清零。
6. 队列最多 512 项，普通排队最长 20 秒；已签名请求最长排队 2 秒，避免长时间积压过期签名。原始 context/HTTP timeout 仍然生效。预算不足时会明确返回错误，不绕过控制。

Binance K 线按 `limit` 区分权重；全市场 ticker 与单币 ticker 权重不同。Hyperliquid POST `/info` 按可重读请求体的 `type` 区分权重，不消费原请求体。未知接口使用保守估值，**不是已经穷举验证全部上游规则**。

## 真正减少请求的部分

只对白名单内的 Binance 匿名公共 GET 做跨模块请求合并和短缓存：

| 数据 | 缓存上限 |
| --- | --- |
| exchangeInfo | 5 分钟 |
| K 线、24h ticker | 2 秒 |
| premiumIndex | 1 秒 |
| openInterest、fundingRate、openInterestHist | 10 秒 |

仅 HTTP 200、合法 JSON 且允许缓存的响应进入缓存。缓存最多 512 项、32 MiB，单个共享响应不超过 8 MiB。相同并发请求只预留一次上游额度，各调用方获得独立可读的 body/header。一个调用方取消不会取消其他等待者；最后一个调用方离开时取消共享请求。

以下请求不会缓存或合并：含凭证/签名/身份参数的读取，订单和账户数据，POST/DELETE，下单前价格/盘口/时间校验，付费请求，自定义 Provider，以及明确要求 no-cache 的请求。不同自定义 transport（例如代理/TLS 策略不同）不会共享缓存，但仍共用保守的 Provider 预算。

调度器本身不全局缩短轮询或交易决策周期。带单监控额外采用分任务调度：当前公开持仓目标每 20 秒、活跃交易员最新成交目标每 30 秒（原为每分钟且等待整轮结束后计时）；不活跃成交仍每 15 分钟、资料仍每 6 小时。每页历史获取后让出队列，已完成时间戳的成交先展示，完整追平后才推进持久化水位。公开持仓不再等待历史回补和绩效资料。以上是目标截止时间，不是每个关注对象的实际保证；关注数量、12 权重/分钟预算、网络和限流退避都会延长实际周期。初次 30 天回补仍可能耗时，上游本身的缓存/延迟不会被更多请求消除。

看板的行情计算缓存每 30 秒进入后台合并刷新；已有结果不超过 2 分钟时先返回原快照，避免前台等整轮扫描。没有快照或快照更旧时必须等待实际计算。刷新失败短暂退避，原结果的时间戳不重置。该策略只适用于公开行情看板，不用于订单、账户或执行价格。推断持仓标记价格另走公共期货行情通道，目标每 10 秒刷新，不占带单 Web 接口预算。

## 默认本地运行预算

这些是程序的运行预算，**不是上游官方额度声明，也不能保证不会遇到上游限流**。默认预留余量，并保留现有限制。购买套餐、账号等级、API 规则及共享 IP 情况变化后，应按实际规则配置。

| Profile | 分钟权重 | 每秒请求上限 | 并发 | 未知接口预留权重 |
| --- | ---: | ---: | ---: | ---: |
| default / 自定义域名 | 60 | 2 | 4 | 1 |
| binance-futures | 1200 | 15 | 8 | 40 |
| binance-spot | 2400 | 10 | 8 | 100 |
| binance-copytrade | 12 | 1 | 1 | 1 |
| bybit | 1800 | 40 | 8 | 1 |
| okx / bitget | 600 | 10 | 8 | 1 |
| hyperliquid | 600 | 10 | 8 | 100 |
| gate | 300 | 5 | 6 | 1 |
| kucoin | 120 | 3 | 4 | 5 |
| lighter | 60 | 2 | 4 | 1 |
| aster | 300 | 5 | 6 | 10 |
| indodax / coinank | 60 | 2 | 4 | 1 |
| claw402 | 60 | 3 | 4 | 1 |
| twelvedata | 6 | 1 | 2 | 1 |
| alpaca | 120 | 4 | 4 | 1 |

带单还受每 4 秒一次的独立窗口、原客户端间隔及退避限制。生产/测试网预算分开；不同代理保守地共用 Provider 预算，不用换代理扩大额度。

## 配置

不配置也会自动启用。`main.go` 在加载 `.env` 后、启动服务/交易员之前校验配置。无效 JSON 或非法预算会阻止启动，错误日志不回显配置原文。

在 `.env` 中设置单行 JSON（示例保持默认 Binance 预算，不代表可随意提高）：

```dotenv
NOFX_API_SCHEDULER_CONFIG='{"profiles":{"binance-futures":{"weight_per_minute":1200,"requests_per_second":15,"max_concurrent":8,"default_weight":40}},"rules":[{"provider":"binance-futures","method":"GET","path":"/fapi/v1/openInterest","weight":1,"capacity":20,"window_seconds":1}]}'
```

- `profiles` 覆盖对应 Profile 的完整四个字段；自定义上游可用精确 hostname 作为键。
- `rules` 用精确 `provider + method + path` 覆盖请求权重，并可增加独立滚动窗口；不允许 query、凭证、通配路径。新增窗口与内置限制同时生效，不删除已有保护。
- 修改后需重新启动服务；不会自动停止/启动交易员。Docker Compose 已通过 `env_file` 注入 `.env`。
- 若上游限制为 6000 次/分钟，仍需先确认它是请求数还是权重、是否另有秒级/账号/接口限制；不能把 6000 一律填写到所有 Profile。
- 配置预算大于真实额度会产生限流风险；调度器不能替用户验证账号套餐。

## 观测效果

登录后调用：

```text
GET /api/api-scheduler/status
Authorization: Bearer <token>
```

返回 `scope: "process"` 和各 Provider 的 `requests`、`cache_hits`、`coalesced`、`rate_limited`、`rejected`、`in_flight`、`queued`、`wait_ms`。计数不含 API key、请求 URL、账户 ID 或 payload；重启后重置。`requests` 是预留并尝试发送的次数，不是上游官方账单。

观察缓存命中/合并是否增加、重复扫描是否减少、排队是否持续积压及限流是否下降。预算饱和时，优先减少重复扫描/历史回补，不要直接加大额度。

## 已知边界

- 预算是**单进程**的。多个实例共用 NAT/IP、浏览器直连、其他程序流量不在统一账本内；必须分摊额度或使用外部共享限流服务。重启会清空本地窗口，因此重启期间也需要保留余量。
- 浏览器直连 Hyperliquid 的 WebSocket、第三方 SDK 的非 REST 通道不在这个后端 REST 调度器中。没有改变 WebSocket 重连或订阅语义。
- 模型的 TPM、每日额度、付费预算、Hyperliquid 账户历史成交量决定的动作额度等不能由一个 RPM 参数替代；已有业务限制保留，必要时配置更严格的 Profile/规则。
- 不会自动发现所有账户级业务错误码。copytrade 原有 HTTP-200 限流码处理保留；其余接口以响应头、HTTP 状态和现有适配器处理为准。
- 为保护签名和资金，额度不足时可能返回排队错误。对下单结果不确定的情形继续使用原订单核对流程，不能盲目重试。

## 验证

核心测试使用假 transport/本地 HTTP 服务器，不需要交易所凭证：

```bash
go test -race ./internal/apischeduler -count=3 -cover
go test ./api ./copytrade ./coinpool ./marketboard ./planning/market ./trader/binance ./trader/bitget ./trader/okx ./trader/hyperliquid ./mcp/payment
go test ./trader/lighter -run TestScheduledLighterSDK
go test ./... -run '^$'
```

核心测试覆盖滑动权重窗口、原子预留、并发槽生命周期、排队取消、优先级、上游隔离、Retry-After/用量头、公共请求合并、缓存过期、代理隔离和交易/付费请求不缓存。测试中的 22 次相同公共读取合并为 1 次上游请求，仅说明该场景的去重效果，不是实盘整体提速比例。

### 自定义 HTTP Client 注入

默认 MCP 客户端自动包装安全 HTTP Client。显式使用 `mcp.WithHTTPClient(custom)` 时保留调用方注入语义（包括单元测试假 transport），调用方应传入 `apischeduler.Wrap(custom)` 才参与进程预算。付款的发现/付款发送点仍统一使用 `apischeduler.Do`，并在解开透明调度包装后克隆单次传输配置。

Hyperliquid `candleSnapshot` 按请求时间范围和周期估算返回根数、加上端点余量计算权重；无法分类时才按最大根数保守预留。
