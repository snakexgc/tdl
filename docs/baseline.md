# 开发基准

本基准使用当前配置、任务和发布格式，不提供旧配置导入、字段迁移或历史数据自动补齐。

## 配置清单

唯一配置文件为 `<TDL_HOME>/tdl_config.json`，格式版本为 `1`。顶层包含 `version`、`system.namespace`、`system.debug` 和 `components`。业务组件各有 `enabled` 开关，配置通过所属组件的 `values` 字段声明；`configuration.manager` 固定启用。字段类型、默认值、范围、敏感性与重启要求见自动生成的 [配置参考](configuration.md)，完整模板见 [examples/tdl_config.json](../examples/tdl_config.json)。

| 组件 | 字段数 | 配置职责 |
| --- | ---: | --- |
| `account.telegram` | 9 | 凭据、代理、并发、连接容量、任务与重连间隔 |
| `console.bot` | 2 | Bot token、允许的用户 |
| `download.control` | 2 | 所选下载器、本地根目录 |
| `download.listen` | 1 | 即时生效的监听规则数组、来源与频道评论视频开关 |
| `downloader.aria2` | 17 | RPC、下载目录、超时、重试、状态采样、停滞与错误处理 |
| `downloader.local` | 2 | 队列扫描与退出超时 |
| `filter.rules` | 4 | 包含、排除、文件大小范围 |
| `forward.rules` | 1 | 分组规则数组，含来源、目标与转发方式 |
| `forwarder` | 10 | 转发方式、默认目标、静默、去重、队列、重试与历史保留 |
| `naming.rules` | 3 | 文件名、目录、名称长度 |
| `notify.telegram` | 8 | 接收者、事件开关、进度间隔、通知超时 |
| `panel.webui` | 4 | 监听地址、端口、登录凭据 |
| `proxy.range` | 8 | 监听、公网地址、链接有效期、等待/保存超时、清理周期 |
| `time.sync` | 2 | 内置 NTP 池的同步间隔、每轮探测超时 |
| `trigger.forward` | 2 | 监听来源、评论监听 |
| `trigger.reaction` | 2 | 下载与转发表情 |
| `storage.maintenance`、`trigger.download`、`trigger.messagelink`、`update.self` | 0 | 仅启停开关 |

`TDL_HOME` 负责定位应用目录；`config-init --home` 是离线工具参数。构建参数、外部 aria2/Docker 设置、数据库记录和浏览器界面状态不属于业务配置文件。

HTTP 下载服务固定随程序启动，本地下载、自动添加 aria2 任务和复制直链共用该服务；旧配置中的 `proxy.range.enabled=false` 不再关闭服务，监听或配置失败写入错误日志。下载方式单选本地或 aria2；旧 `executors` 数组只采用第一个下载器，保留末尾 HTTP 链接行为。仅生成链接的旧配置继续有效。

本地模式不启动 aria2 管理，也不向 aria2 查询任务。下载管理页显示本地保存目录所在磁盘的可用空间及目录内文件数量、逻辑大小；递归统计子目录，排除已知未完成任务，不跟随目录内符号链接。目录尚未创建时查询最近已有父目录，不会为了统计而创建目录。Windows 使用当前用户可用空间并支持盘符、挂载目录和 UNC 路径；Unix 系统按实际文件系统查询。统计最多缓存 30 秒，权限或扫描失败会在页面显示，不能取得的指标显示为未知。

`forward.rules.values.rules` 与 `download.listen.values.rules` 使用对象数组配置，各最多 200 条规则。转发规则的 7 个子字段如下；停用规则同样验证结构，启用规则还会检查循环转发。

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
- 时间同步删除服务器偏好及其迁移，统一使用内置 NTP 池；历史配置中的废弃偏好加载时丢弃，下一次显式保存时清除。
- HTTP 下载记录必须携带有效的 `last_active_at`。删除向创建时间和索引时间的回退；缺失或损坏的记录读取、续期、清理均报错，拒绝时保留原记录。
- 存储初始化采用明确的驱动与路径，删除配置字典、弱类型转换、驱动名称解析和动态注册。移除 `mapstructure`、`validator` 及其不再使用的间接依赖。
- 更新器按 `.goreleaser.yaml` 的当前包名精确匹配系统、架构及 ARM 版本；删除旧包名别名、模糊评分、`.tgz`、裸二进制及包内旧文件名匹配。
- 删除测试专用的生产策略转换入口、废弃的存储枚举生成代码、`GO111MODULE` 构建开关、已停用且指向不存在目录的 E2E 作业，以及 lint 的 `legacy` 排除预设。

现有的旧配置文件导入、旧执行器名称、旧存储驱动、缺失任务状态/版本的自动补齐和会话凭据猜测路径已不存在，相关拒绝测试继续保留。忽略规则仍排除本地配置及私密数据。

## 当前功能边界

设置页在有修改时显示“尚未保存的修改”框，对比当前运行值与修改后的值；用户点击框内“保存并重启”一次保存全部分类的草稿后重启，也可先单独保存区块。普通组件及重连使用只读启动快照，保存后由用户重启生效。`download.listen` 明确声明 `LiveValues`：监听规则校验、记录启用时间并成功写入统一文件后，原子发布运行规则，保存失败保持原规则；该组件启停仍需重启。敏感项的差异只标记修改，不回显值。

“监听下载”使用独立导航 `/listen-downloads`。规则包含 `id`、`name`、`enabled`、`sources`、`listen_comments`；每条选择 1–500 个来源，支持普通群、超级群、频道、机器人和用户，只处理收到的消息。默认评论开关关闭，重叠来源合并。主消息及评论视频经过现有下载流水线的大小、扩展名、命名及下载器规则；自动监听按单条相册消息采集，评论逐文件限制为视频。

首次及再次启用以当次启用时间为起点，重连最多补齐最近 24 小时，实时事件不会提前推进历史补齐游标。评论通过对应讨论群的线程关联，纯文字原帖也建立跟踪；共享调度器最多两路工作，约每 5 分钟错开补查，遵守 Telegram 限流，原帖满 24 小时停止补查。之后可关联的实时评论仍下载视频；无权限、删除、评论关闭和提交结果不明在页面显示。账号 KV 中 `download.listen.*` 保存版本化的启用时间、补齐进度、线程和事件日志；提交结果不明先核对下载器记录，无法确认时保留待确认记录以避免重复提交。

操作说明与本地、浏览器及真实 Telegram 验证结果见 [监听下载](listen-downloads.md)。

监听来源下显示全部监听任务，并通过现有下载控制 API 更新实际状态、进度、速度和错误；停用规则保留历史任务。监听与表情触发共用 `account.telegram` 的同时下载文件数和每个 DC 连接上限，表情任务优先取得尚未授予的文件、分片名额及 aria2 等待队列位置，不中断已经开始的下载。

以下机制属于当前功能，不是历史兼容分支：组件 schema 对省略字段提供默认值；连接重试与资源排空；NTP 池并发校时、代理重试及系统时间回落，校准只更新运行状态；下载执行器按配置顺序降级；官方转发失败后按当前模式复制；WebSocket 不可用时轮询；日志将当前 `host.*` 进程和协议适配器映射到业务组件；命名模板缩写、Telegram/HTTP/aria2 协议处理及跨平台存储。

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
