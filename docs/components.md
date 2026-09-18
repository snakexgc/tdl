# 组件开发指南

进度和未完成边界见 [当前清单](migration-status.md)，字段见 [生成参考](configuration.md)，历史叙述见 [归档](history/components-before-2026-09-19.md)。

## 职责

| 位置 | 职责 |
| --- | --- |
| `application/<组件>/` | 业务规则、配置、生命周期、命令/页面声明、业务端口 |
| `interfaces/` | 普通 DTO、账号维度、类型化端口和事件 |
| `rte/` | 通用装配、生命周期、配置、事件、Runnable、目录与诊断 |
| `bsw/` | 协议传输、共享资源、受限数据集及事务 |
| `application/catalog.go` | 组件元数据及静态工厂的统一集成声明 |
| `app/runtime/assembly.go`、`foundations.go` | 实际资源、常驻宿主的工厂、启停、重启条件和依赖 |
| `app/` | 现有协议及兼容适配器；部分旧编排仍在收拢 |

SWC 不导入其他 SWC、app、存储驱动或旧 pkg/config。架构检查也检查测试；需要 BSW 的跨层测试放在 `internal/integration`。

## 新增组件

1. 声明 Manifest：ID、配置、类型化端口、事件、页面和命令。
2. 实现 Init、Start、Stop、Reconfigure；在线配置实现 PrepareConfig。
3. 在统一目录加入声明。无资源工厂可静态登记；动态组件在对应资源边界绑定实际适配器。
4. 业务只使用声明过的端口及 DTO，SDK 和数据库留在边界适配器。
5. 增加针对失败模式的回归，生成配置参考。

目录与 Reconciler 不包含组件名称分支。静态工厂默认由常驻宿主装配，声明 `Host: "bot"` 的工厂由 Bot 资源宿主装配。新增普通静态组件不需修改 Manager 的业务分支；需要真实连接的组件在其资源边界绑定。

## 配置与生命周期

Manifest 支持字符串、整数、布尔、字符串列表、范围、枚举、格式、秘密和 RestartRequired。缺文件/字段使用默认值，组件模式不从旧业务配置补缺。

PrepareConfig 只校验并构建快照，不启动资源或写文件；commit 不可失败且不得回调 Runtime。校验失败保留旧配置。目录允许离线编辑；热更新在持久化后发布；重启参数在资源协调成功后发布对应视图。

秘密值不在查询中返回；省略或空字符串保留已保存值。表单提交 revision，过期保存返回 HTTP 409。该机制保护本进程 API，不是外部编辑器或多进程文件锁。

进程、账号、连接保留不同作用域。ManagedUnit 声明依赖、启用状态、重启签名和启停函数。先停止消费者，再释放提供者；超时保留实例并允许再次停止，禁止重叠启动。部分启动失败也要清理。事件/Runnable 退出后才能释放连接，不额外叠加无限重试。

共享连接和配额由账号资源唯一持有，临时登录资源隔离。旧配置到普通兼容 DTO 的集中映射位于 internal/componentconfig；新业务不得调用 config.Get。

## 命令与页面

组件 Commands 返回 ConsoleCommand（Name、Description、Aliases、Port），Manifest 登记后由目录填充 Owner、汇总菜单。

ConsoleCommandHandler.Execute 接收 ConsoleRequest，返回 ConsoleResponse，不接触 telego。Dispatcher 验证权限、私聊、重复命令/别名及取消。在 Provides 声明处理端口、命令 Port 指向该端口后，生产 Bot 自动解析当前实例；替换或停用不会留下旧处理器。旧格式化处理器仍在 app/bot/command_adapters.go 绑定。

Assets、Routes、Manifest.Pages 在目录登记，统一资源集合保留既有 URL。SPA Page 声明 Path、Title、View、Module、Style、Order；片段存放在 `views/<View>.html`，脚本导出 `page = { init, load, stop }`，钩子可省略。面板按需加载片段和脚本，离开页面停止轮询；停用页面不显示导航。

JSON API 在 Provides 声明 WebAction 端口，Routes 的 Port 指向它。面板自动执行鉴权、限制 JSON 请求大小、调用 Handle 并返回普通 DTO；无需新增中央 HTTP 分支。配置页由 schema 生成。完整接入示例见 `app/webui/component_actions_test.go`；旧接口适配表仍保留以维持兼容。

## 数据与下载

使用 taskhub 受限仓库，保留旧键、JSON、索引及无索引记录，不暴露数据库句柄或绕过写入者。

在 RPC 前读取版本，落盘时复核版本和终态。aria2 观测还验证源链接身份，事务更新关联与完成标记。删除不能复活，暂停后的迟到回报不能覆盖控制。

只有明确未接受任务才允许跨执行器降级；超时、断线或响应丢失不能证明未接受，不得重复提交。字节流不进入事件总线。

## 验证

```powershell
go test -race ./...
go build ./...
golangci-lint run ./...
go run ./cmd/swc-check
go run ./cmd/swc-check -empty
go run ./cmd/swc-docs
node --experimental-vm-modules --test internal/integration/router.test.mjs
git diff --check
```

静态检查、生产装配、配置往返和真实后端验收分别记录。迁移及回退见 [操作说明](config-migration.md)。
