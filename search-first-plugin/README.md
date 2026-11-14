# Search First — PI-Desktop 插件

将 [shimo4228/search-first](https://github.com/shimo4228/search-first) 包装为 PI-Desktop skill-pack：选择依赖或实现前，优先检索已有方案与一手资料，输出带来源的 Adopt / Adopt-part / Build 决策。

## 使用

插件启用后，用自然语言要求：

> 使用 search-first，先查一下有没有现成库实现这个功能，再给出采用还是自建的建议。

本插件不注册 `/search-first` 斜杠命令，也不提供搜索引擎或 API key。实时检索使用当前宿主可用的工具；缺少工具时必须报告限制。

## 兼容性

- `skills/search-first.md` 是未经修改的上游 skill。
- `skills/pi-desktop-compatibility.md` 定义宿主兼容规则，遵循 PI-Desktop 的工具、权限、委派和文件访问边界。
- 不安装 Claude Code 专用 `researcher`、workflow 或 research-gate hook。Full 模式按宿主能力执行，不强制并发研究。
- 仅声明 `agent.prompt.inject` 权限，无独立后台进程。

## 安装与开发

此目录已通过脚手架作为开发插件加载，保存后会热重载。可在 PI-Desktop 插件页面管理。

分发安装：在插件页面安装生成的 `dist/community.search-first-0.1.0.piplug`。避免同时启用开发版和安装版，防止重复注入指令。

## 来源与许可

- 上游：https://github.com/shimo4228/search-first
- 固定版本：`2d49527a72bd1b2eb86d910c125b12883ddca775`
- 上游许可：MIT，原文见 `LICENSE`。
