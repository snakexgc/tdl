# AUTOSAR 迁移状态与验收清单

更新：2026-09-19。本文件是唯一当前清单，包含工作区未提交、未跟踪修改，不代表发布版本。多账号是独立后续阶段。

**框架和主要业务组件已落地，本轮推进了装配、配置和控制面边界，仍未达到全面替代和交付完成标准。**

## 当前实现

| 范围 | 实现与证据 | 尚缺条件 |
| --- | --- | --- |
| 组件目录 | `application/catalog.go` 集中登记 17 个组件的 Manifest、静态工厂、校验、作用域、命令、页面资源和路由；`rte/directory.go` 动态解析宿主 | 带真实资源的工厂和常驻宿主仍有专用装配入口 |
| 依赖协调 | `app/runtime/assembly.go`、`foundations.go` 将 HTTP、aria2、Bot、面板、watch、账号资源、常驻策略及下载控制交给通用 Reconciler；创建与最终停机共用依赖图 | 连接内组件仍由对应连接宿主装配 |
| 停机与隔离 | 停止失败保留旧实例；启动部分失败先清理；依赖变更按旧关系释放；生产测试验证消费者超时后保留共享账号资源，后续停机可重试 | 真实传输失败与迟到回调验收 |
| 独立组件 | 组件目录提供启停 API 和按钮、版本冲突检测；停用 filter 保留账号端口；停用 notify 不阻止 Bot；必需策略缺失阻塞 watch | 常驻策略仍共用宿主，启用状态变化会重建宿主及消费者；连接内单组件隔离需继续细化 |
| 配置归属 | `internal/componentconfig` 覆盖账号代理/时钟/配额、Bot、通知、aria2、Range、面板、转发触发及策略；新业务接收组件视图 | 旧协议适配器仍保留兼容 DTO 和部分独立入口 |
| 配置保存 | 离线/停用可编辑；schema 和语义校验先于保存；秘密分离；重启字段待生效提示；版本冲突 HTTP 409 | 版本检查保护本进程 API，不提供外部编辑器或多进程跨文件事务 |
| 迁移工具 | 正式 `tdl migrate-config` 与原工具共用实现，导出 17 个组件及启用状态；预览不初始化守护进程 | 真实用户配置的启动、恢复及回退演练 |
| 下载观测 | aria2 重试关联发现、状态报告、完成标记与清理由下载组件驱动，关闭面板不影响后台维护 | 本地媒体恢复及剩余下载适配编排继续收拢 |
| 数据一致性 | `taskhub/aria2_observation.go` 在事务内复核链接身份、远端版本、终态；支持无索引旧记录；删除/暂停后的迟到回报不覆盖控制 | 剩余数据集读写归属审计 |
| 账号操作 | 切换校验、并发比较和 Spam 判断归账号组件；SDK 留在登录适配器；停止取消并等待操作 | 专用账号验证登录、切换、Spam 和共享连接退出 |
| Bot 命令 | 18 个菜单命令由功能组件声明，保留下载别名；新命令声明 Port 后自动解析当前组件实例；DTO 分发统一验证权限、私聊与重复注册 | 旧格式化/键盘会话及部分编排仍在 `app/bot` |
| 页面和诊断 | Manifest 声明视图、脚本、样式与排序；SPA 统一按需加载 init/load/stop；新 JSON API 声明 Port 即自动绑定、统一鉴权；停用后隐藏导航并拒绝业务调用 | 旧 HTTP 适配表及处理器内部编排继续收拢；视觉验收 |
| 原有业务 | Range、local、forwarder、登录、taskhub/NvM、共享账号资源和更新组件继续使用既有实现 | 实际内容校验、转发降级、原生更新替换 |
| 发布与多账号 | 保留构建参数及 CI 平台矩阵；多账号未实施 | 容器启动、目标系统运行及发布包验收 |

## 验证证据

- `rte/reconciler_test.go`：停止超时、再次停止、无重叠实例、部分启动失败、旧依赖顺序、无关资源隔离。
- `rte/directory_test.go`：离线校验、秘密保留、停用保存、健康查询、版本冲突、保留待重启字段。
- `internal/componentconfig/config_test.go`：非默认配置完整往返、缺文件默认值、快照隔离。
- `cmd/migrate_config_test.go`：正式 CLI 预览不初始化服务、不输出秘密，完整导出。
- `internal/integration/aria2_observation_test.go`：无面板时发现重试关联并标记完成，排除外部链接。
- `bsw/cdd/taskhub/aria2_observation_test.go`：Bolt、legacy、file 驱动上验证暂停/删除后的迟到报告。
- `application/account.telegram/actions_test.go`：账号切换校验、并发冲突、操作互斥、停止取消 Spam。
- `application/console.bot/dispatch_test.go`、`application/console_commands_test.go`：新命令贡献、权限、私聊、别名和停用声明。
- `app/runtime/component_ports_test.go`、`bot_components_test.go`：生产端口和配置恢复、停用策略/通知的隔离。
- `app/runtime/foundations_test.go`：生产资源停机、重复停机、超时保留共享连接、组件禁用/恢复及启停版本冲突。
- `app/webui/component_actions_test.go`：示例组件声明接入页面、脚本、配置及 JSON API；鉴权、非法输入、热更新、禁用与端口所有权。
- `app/bot/command_adapters_test.go`：声明式命令每次解析当前实例，替换后使用新实例、停用后拒绝调用。
- `internal/integration/router.test.mjs`：新页面按需加载、生命周期退出、复用已初始化页面、停用页面不加载；不代替浏览器视觉验收。

## 本轮执行记录

- `go test -race ./...`：通过；包含新回归，未改动包部分使用缓存。
- `go build ./...`：通过。
- `golangci-lint run ./...`：0 issues。
- Linux/amd64、Windows/amd64、macOS/arm64（CGO 关闭）交叉编译：通过。
- 配置文档重新生成与原文件一致；`git diff --check`：通过。
- 17 个页面脚本的 `node --check`：通过。
- `node --experimental-vm-modules --test internal/integration/router.test.mjs`：通过。
- 隔离 HTTP 预览：17 个组件、停用状态、离线保存、旧版本冲突及页面/脚本资源返回通过；临时服务已关闭。
- 默认/空 `swc-check` 只检查静态注册表；8/0 不能证明生产迁移完成。
- 真实 Telegram/aria2：未提供专用测试账号、聊天及实例，未执行真实消息、下载或账号操作。
- 浏览器视觉：当前会话没有可用浏览器，未执行。
- 容器运行：Docker daemon 的命名管道不可用，未执行。
- 原生更新替换与所有发布平台运行：未执行。交叉编译不等于运行验收通过。

## 下一步

1. 将策略共用宿主与连接宿主内的启停细化到必要依赖范围，避免停用单组件重启无关消费者。
2. 收拢本地媒体恢复与剩余存储访问，继续缩减控制面业务编排。
3. 使用已接入的通用命令/页面/JSON 端口收拢旧 Bot 与 HTTP 适配表，补齐真实菜单和页面交互验收。
4. 专用环境执行登录、Range、下载内容校验、aria2 重试、转发降级和账号切换。
5. 完成视觉、容器、原生更新和发布平台运行；单账号签收后再进入多账号。

历史记录见 [旧状态表](history/migration-status-before-2026-09-19.md) 与 [旧组件说明](history/components-before-2026-09-19.md)。历史待办被后续实现覆盖时，以本表和当前代码为准。
