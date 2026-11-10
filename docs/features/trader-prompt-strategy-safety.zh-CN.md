# Trader 独立规则与运行中策略更新的安全语义

## 独立 prompt（普通 AI 决策路径）

`custom_prompt` 和 `override_base_prompt` 是 trader 私有设置，不写入或修改关联策略对象。正常 AI 周期把两者作为一次原子快照传给 kernel，最终进入 `AIClient.CallWithMessages(systemPrompt, userPrompt)` 的 system message；同一策略关联的其他 trader 不受影响。API 使用 `SetPromptSettings` 同时更新两者；兼容的单字段 setter 也有锁保护，但调用方需要同时修改两字段时应使用原子接口。

优先级：**系统固定协议/安全约束 > trader 规则 > 策略可编辑规则**。

- 补充模式（`override_base_prompt=false`）：保留策略的角色、频率、入场、决策流程和 CustomPrompt，最后追加 trader 规则；仅在可编辑偏好冲突时 trader 规则优先。
- 覆盖模式（`override_base_prompt=true` 且 trader prompt 非空）：替换角色、频率、入场、决策流程及策略 CustomPrompt 这些**用户可编辑规则段落**（含其默认文案）。不是替换整条 system message。
- 任何模式都保留数据字典、交易范围、风控约束、输出 JSON/`<reasoning>`/`<decision>` 协议以及已有的信号执行策略约束。现有程序端交易校验也不变。Prompt 中的优先级说明不是绕过后端风控的授权，更不保证模型绝对遵从任意自然语言规则。
- 空字符串或纯空白规则等于清空 trader 规则；即使 override 标志仍为 true，也恢复策略规则，不生成空白系统约束。建议清空时发送 `{"custom_prompt":"","override_base_prompt":false}`。
- 策略数据库重载只替换策略快照，不清除 trader 私有规则。API 保存的 trader 设置仍由既有 manager 加载路径在进程重载时恢复。
- 模型上下文估算包含 trader 规则的保守 token 余量；超限在市场数据/模型请求前拒绝本次分析。
- `vergex_signal` 保留其不可覆盖的方向/持仓状态机。trader 文本进入该路径的 User Preference 后的独立段落，但不能取消该信号策略的执行约束；本次没有改动签名、付费传输或模型客户端。

## 单一 XYZ 标的

单标的 `xyz:` 策略的显式 CustomPrompt 按原文保留（包括中文、用户提及 Claw402 的自定义文本）。内置方向模板只在 CustomPrompt 真正为空时提供默认规则，不再覆盖用户规则；trader 覆盖模式也会抑制该可编辑默认模板。其他既有资产范围、风控、输出及支付/签名约束不变。

## 网格边界

网格有独立的决策/挂单路径，当前**不支持 trader 级 custom_prompt 或 override_base_prompt**。非空自定义规则或 true 覆盖标志会明确拒绝；允许清空两个字段来修复旧配置。Prompt 更新 API 在数据库写入前检查；切换策略为网格时检查关联 trader；setter 返回错误；启动构造检查数据库中遗留的不支持配置，且早于交易所客户端构造，不会先改变账户设置。

新建/编辑 trader 的通用入口同样在写库前调用 `trader.ValidateTraderPrompt(strategyType, prompt, override)`，创建流程还会在外部配置探测后、保存前再次校验；不能绕过专用 prompt API 保存网格不支持的设置。

## 修改关联运行中 trader 的策略

策略更新 API 在**全部拒绝性校验之后**才执行唯一的数据库更新，包括 token 超限检查。因此拒绝时配置、名称、描述、公开状态、可见性和更新时间均不改变。

- 只要关联 trader 的数据库运行标志或内存运行标志任一个为 true，运行中网格策略的更新一律返回 HTTP 409（包括元数据更新）；明确要求先停止 trader。
- 运行中 AI/grid 之间切换同样返回 409，避免固定运行分支忽略类型变更。
- 普通 AI 策略的规则更新仍可保存，在后续周期重载；重载失败时该周期拒绝继续使用旧策略下单。
- 停止后可修改网格，下一次启动先重载数据库策略再选择 AI/grid 分支。运行中的重载防线也拒绝网格热修改和 AI/grid 切换。
- 这些检查**不会自动停止、重启、撤单、平仓，也不会热修改网格资金、杠杆或挂单结构**。停止操作本身不代表所有交易所挂单已取消；再次启动前应由用户核实挂单和仓位。
- 本次用进程内生命周期锁串行化策略/prompt API 保存与 Run 启动时的重载、运行标志发布；并发 prompt API 保存也保持数据库/内存顺序一致。锁不跨交易循环或模型/交易所请求；多实例部署仍需要统一的跨进程生命周期/策略版本锁。直接绕过 API 修改数据库不属于受支持的热更新接口。

## 回归验证

- `trader/auto_trader_prompt_test.go`：mock 捕获实际最终 messages，覆盖补充、覆盖、清空、多 trader 隔离、共享配置不被 clamp 修改、策略重载、并发原子快照和网格拒绝。
- `kernel/engine_prompt_overlay_test.go`：XYZ 显式规则/空配置默认、信号策略硬协议保留。
- `api/strategy_update_safety_test.go`：实际 Gin handler + 临时 SQLite store，验证 token 拒绝无写入、运行中网格/类型切换拒绝、停止后接受、普通 AI 规则更新、网格 prompt 拒绝/清空与用户隔离。

测试只使用临时数据库和 mock AI，不启动 trader、不请求付费模型、不修改本地用户数据库。
