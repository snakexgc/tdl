# 开发基准

本基准使用当前配置、任务和发布格式，不提供旧配置导入、字段迁移或历史数据自动补齐。

## 配置清单

唯一配置文件为 `<TDL_HOME>/tdl_config.json`，格式版本为 `1`。顶层包含 `version`、`system.namespace`、`system.debug` 和 `components`。18 个业务组件各有 `enabled` 开关，共声明 75 个 `values` 字段；`configuration.manager` 固定启用。字段类型、默认值、范围、敏感性与重启要求见自动生成的 [配置参考](configuration.md)，完整模板见 [examples/tdl_config.json](../examples/tdl_config.json)。

| 组件 | 字段数 | 配置职责 |
| --- | ---: | --- |
| `account.telegram` | 10 | 凭据、代理、时间校准、并发、连接容量、任务与重连间隔 |
| `console.bot` | 2 | Bot token、允许的用户 |
| `download.control` | 2 | 执行器顺序、本地根目录 |
| `downloader.aria2` | 17 | RPC、下载目录、超时、重试、状态采样、停滞与错误处理 |
| `downloader.local` | 2 | 队列扫描与退出超时 |
| `filter.rules` | 4 | 包含、排除、文件大小范围 |
| `forward.rules` | 1 | 分组规则数组，含来源、目标与转发方式 |
| `forwarder` | 10 | 转发方式、默认目标、静默、去重、队列、重试与历史保留 |
| `naming.rules` | 3 | 文件名、目录、名称长度 |
| `notify.telegram` | 8 | 接收者、事件开关、进度间隔、通知超时 |
| `panel.webui` | 4 | 监听地址、端口、登录凭据 |
| `proxy.range` | 8 | 监听、公网地址、链接有效期、等待/保存超时、清理周期 |
| `trigger.forward` | 2 | 监听来源、评论监听 |
| `trigger.reaction` | 2 | 下载与转发表情 |
| `storage.maintenance`、`trigger.download`、`trigger.messagelink`、`update.self` | 0 | 仅启停开关 |

`TDL_HOME` 负责定位应用目录；`config-init --home` 是离线工具参数。构建参数、外部 aria2/Docker 设置、数据库记录和浏览器界面状态不属于业务配置文件。

`forward.rules.values.rules` 是唯一的对象数组配置，最多 200 条规则。每条规则的 7 个子字段如下；停用规则同样验证结构，启用规则还会检查循环转发。

| 子字段 | 类型 | 约束与行为 |
| --- | --- | --- |
| `id` | string | 必填、非空，在规则列表中唯一 |
| `name` | string | 显示名称，最多 100 个字符；省略为空 |
| `enabled` | bool | 是否启用该规则；省略为 false |
| `sources` | strings | 1–500 个 `user:<正整数>`、`chat:<正整数>` 或 `channel:<正整数>` |
| `targets` | strings | 1–100 个聊天引用，也可为 `self`；不能与来源相同 |
| `mode` | string | 必须明确选择 `default` 或 `clone` |
| `silent` | bool | 静默发送；省略为 false |

## 清理结果

- 配置默认值统一由组件声明生成。删除平面配置的重复默认值、宽松校验、运行时数值修正，以及缺失仓库时回用旧快照的路径；策略组件直接读取统一仓库。原 `internal/componentconfig` 投影合并到 `pkg/config`，快照仅用于传输适配器。删除独立组件文件遗留的版本字段，只保留统一文件版本。
- 重连间隔必须为正数，默认 3 秒，删除 `0 → 5 秒` 的隐式替代。未知组件、字段、嵌套规则字段及无效组合直接报错；停用组件同样校验。
- HTTP 下载记录必须携带有效的 `last_active_at`。删除向创建时间和索引时间的回退；缺失或损坏的记录读取、续期、清理均报错，拒绝时保留原记录。
- 存储初始化采用明确的驱动与路径，删除配置字典、弱类型转换、驱动名称解析和动态注册。移除 `mapstructure`、`validator` 及其不再使用的间接依赖。
- 更新器按 `.goreleaser.yaml` 的当前包名精确匹配系统、架构及 ARM 版本；删除旧包名别名、模糊评分、`.tgz`、裸二进制及包内旧文件名匹配。
- 删除测试专用的生产策略转换入口、废弃的存储枚举生成代码、`GO111MODULE` 构建开关、已停用且指向不存在目录的 E2E 作业，以及 lint 的 `legacy` 排除预设。

现有的旧配置文件导入、旧执行器名称、旧存储驱动、缺失任务状态/版本的自动补齐和会话凭据猜测路径已不存在，相关拒绝测试继续保留。忽略规则仍排除本地配置及私密数据。

## 当前功能边界

设置页在有修改时显示“尚未保存的修改”框，对比当前运行值与修改后的值；用户点击框内“保存并重启”一次保存全部分类的草稿后重启，也可先单独保存区块。运行组件及重连使用只读启动快照。保存不触发热更新或自动重启。敏感项的差异只标记修改，不回显值。

以下机制属于当前功能，不是历史兼容分支：组件 schema 对省略字段提供默认值；连接重试与资源排空；启动时自动选择并持久化 NTP 服务器；下载执行器按配置顺序降级；官方转发失败后按当前模式复制；WebSocket 不可用时轮询；日志将当前 `host.*` 进程和协议适配器映射到业务组件；命名模板缩写、Telegram/HTTP/aria2 协议处理及跨平台存储。

新增配置应先在所属组件声明 schema 和校验，再运行 `go run ./cmd/swc-docs -config-out examples/tdl_config.json`。适配器确实需要的设置才加入快照投影；不要重新增加另一套默认值、旧字段别名或静默纠错。更改持久化格式应明确拒绝不支持的数据。

## 检查命令

```text
go test ./...
go test -race ./...
golangci-lint run ./...
node --experimental-vm-modules --test internal/integration/*.test.mjs
go run ./cmd/swc-check
go run ./cmd/swc-check -empty
go run ./cmd/swc-docs -config-out examples/tdl_config.json
go build ./...
```

涉及真实 Telegram 账号或外部 aria2 的测试仍需对应环境；普通检查不会启动真实账号或修改本地会话。
