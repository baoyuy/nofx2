# 服务器发布验收

用户已明确批准将最新本地版本与建议策略发布服务器。

发布时间：2026-11-23 22:44（Asia/Shanghai）。发布标识：20261123T143654Z-risk。

- 前后端已用本地源码构建并发布；1045个发布文件SHA256逐一核对一致。
- 后端镜像manifest：a47a8d0b5818a42fe3f46b7295120678c7015d13410d46f727b20cc11473cf15。
- 前端镜像manifest：a1210400905301f760fd62725c8fde7125777d938b8bd381a2d63aae85614a20。
- 运行目录 /opt/nofx；构建目录 /opt/nofx-releases/20261123T143654Z-risk。
- 备份目录 /opt/nofx-backups/20261123T143654Z-risk：旧源码、在线及停机SQLite、在线及停机paper状态、旧配置、镜像信息、源码清单、验收结果和未执行的回滚脚本。旧镜像标签 pre-20261123T143654Z-risk。
- 本地相关四包回归及前端生产构建通过；服务器空库新镜像启动检查通过；正式API、前端代理和容器健康通过。

仅调整目标“ai 带单”，exchange_type=binance_paper；原动态币池保留，其他两个交易员维持停止状态。目标重启自动恢复。切换时无持仓，钱包966.5881839866767 USDT保留，历史记录未清空。

策略：5分钟扫描；最多3仓；杠杆上限3倍；保证金35%；单笔风险0.7%；组合2.5%；UTC日亏损2%；峰值回撤8%；往返成本预算0.2%；净盈亏比2；最低信心75；主周期ATR范围1.5—4；R保护启用（1R保本、2R追踪、峰值回吐30%）。crypto范围，跟随币池方向。

20%是每笔初始保证金的毛收益目标，通过策略提示词要求真实价格结构支持的止盈位置，属于目标，不是代码保证的最低盈利。约5笔/日是观察目标，不是硬开仓配额；提前保本、追踪和逻辑失效退出可以低于20%。未为实现该目标提高杠杆。

首轮新决策2790，2026-11-23 22:45:14保存，成功，无错误，AI请求34199ms。系统提示词已核对执行协议、20%目标、五笔观察目标、0.70%单笔预算、follow与crypto。10个候选均wait，无强制交易。初始账户风险基线从升级后观察到的966.5882开始，历史交易统计仍保留。

磁盘剩余约1.2GB（97%使用），发布未删除旧镜像、历史备份或其他应用数据。需要后续有选择地清理历史构建缓存/备份或扩容；不应无差别prune。此记录不宣称已验证长期盈利表现。

## 2026-11-24 改为拉取 GitHub 镜像

发布标识 20261124T155935Z-ghcr。此前服务器用 `docker-compose.yml` 的 `build:` 在本机编译，
清空构建缓存后一次构建超过十分钟；现在改为拉取 GitHub Actions 预先构建好的镜像，
拉取与替换在一分钟内完成。

- 新增 `docker-compose.server.yml`：与原 compose 的端口、挂载、env_file、healthcheck 完全一致，
  仅把 `build:` 换成 `ghcr.io/baoyuy/nofx2/nofx-{backend,frontend}:${NOFX_IMAGE_TAG:-latest}`。
- 镜像由 `.github/workflows/docker-build.yml` 在推送 main 后构建，含 amd64/arm64。
  本次镜像 revision 4b399b0，与仓库 main 一致。
- 服务器 `/opt/nofx/update.sh`：打回滚标签 → pull → 停后端数秒并备份数据库 → 启动 → 清理旧镜像。
- 回滚：`/opt/nofx-backups/20261124T155935Z-ghcr/rollback.sh`，切回 `*:pre-` 标签镜像，不动数据库。
- 磁盘：清理 18.37GB 构建缓存与 4.7GB 无用镜像（playwright、golang:1.26.10），
  91% 降至 37%。保留了 11-22/11-23 的回滚镜像标签与数据库备份。
- 部署后验证：前后端 healthcheck healthy、/api/health ok、前端 3000 返回 200、
  三个交易员仍为停止、208 平仓/4 持仓记录不变、9659 条评分结果保留、
  copytrade_score_outcomes 新增 config_version 与 features 字段迁移成功、启动无 panic。
- 未启动任何交易员；本次仅更换代码与统计口径。

CI 状态：Docker 构建、Test、Go Test Coverage 通过；planning 工作流的
`govulncheck` 失败，报 GO-2026-6278（gorilla/websocket v1.5.3，需升到
v1.5.4-0.20240701034025-d67f41855da4）。该依赖与版本在本次改动前已存在，
不是本次引入，尚未修复。
