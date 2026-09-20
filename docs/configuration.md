# TDL 统一配置参考

由 `go run ./cmd/swc-docs` 根据已接入配置存储的组件 Manifest 生成。不要手工修改字段表。

正常启动只使用应用目录的 `tdl_config.json`。`configuration.manager` SWC 集中负责全局配置的默认值补全、校验、版本检查和原子保存；业务组件通过 RTE 获取各自的配置视图。完整默认模板见 [examples/tdl_config.json](../examples/tdl_config.json)。模板由 `go run ./cmd/swc-docs -config-out examples/tdl_config.json` 生成，不含用户密钥。

本表覆盖 18 个业务组件及统一配置管理 SWC。字段 schema 由业务组件声明，配置管理 SWC 通过统一 Catalog 收集；不再各自写配置文件。缺组件或缺字段使用 schema 默认值，不回退旧配置；未知组件、字段、错误类型或无效组合会阻止加载，停用组件同样校验。

## 文件结构与系统设置

`version` 当前为 `1`。`system.namespace` 默认 `default`，只允许英文字母，选择启动时使用的登录会话与历史任务；WebUI 通过账号管理切换并重启进程，同一进程只运行一个 Telegram 账号。`system.debug` 默认 `false`，控制详细日志，WebUI 保存后立即生效。`components` 的键为下面列出的组件 ID，每个组件由 `enabled` 和 `values` 组成。`trigger.forward` 默认停用，其余组件默认启用；配置管理 SWC 固定启用，不需要在 components 下声明。所有设置全局共用，切换命名空间不会重置端口、密码、代理或其他组件设置。

手工编辑的路径示例：`components["downloader.aria2"].values.monitor_stall_seconds`。组件 ID 中的点属于键名，不代表嵌套对象。JSON 不支持注释。建议停止程序后编辑文件，再启动生效；运行中使用 WebUI 保存，继续保持现有热更新和服务协调机制。

## 首次启动与旧配置迁移

统一文件不存在时，先读取旧 `config.json`（不存在则使用默认值），再导入当前 namespace 对应的 `components/<命名空间 Base64URL 编码>/` 中的组件文件和引用密钥，作为全局设置。已有组件目录的设置优先于旧业务配置。其他命名空间的旧配置不读取、不校验，旧文件全部保持原样。`--component-config <目录>` 仅用于首次导入自定义旧目录。统一文件存在后完全忽略旧文件，也不会在配置损坏时回退。可以先执行 `tdl config-init --home <应用目录>` 离线迁移或校验，无需启动 Telegram、aria2 或 WebUI。原 `migrate-config` / `swc-migrate` 命令保留用于旧格式导出。

敏感值与其他配置一起保存在统一文件，写入权限为 0600（Windows 使用文件系统 ACL）；WebUI 查询仍不回显密钥。根目录实际 `tdl_config.json` 已加入 Git 和 Docker 构建忽略规则。请把它作为包含凭据的文件保管，分享配置时使用默认模板。

## 配置范围与启动参数

`TDL_HOME` 环境变量指定应用目录，未设置时使用可执行文件所在目录；它在读取配置前确定，不能放进同一个 JSON。统一文件固定为 `<应用目录>/tdl_config.json`，会话与任务数据库位于 `.tdl/`，日志位于 `.tdl/log/`。`config-init --home` 仅选择离线工具操作的目录；普通启动仍使用 `TDL_HOME`。没有其他业务配置环境变量覆盖。数据库记录、登录会话、任务状态、浏览器筛选条件与外部 aria2/Docker 的配置不属于 TDL 设置。日志容量/轮转、HTTP 内部缓冲等代码常量当前不是用户配置项。所有现有用户设置及可调重试/超时/清理周期都列在下面。

未启动或已停用组件仍可编辑配置。标注需要重启的字段先保存，再协调所属服务；失败时运行状态保留错误，配置页显示等待重启。其他字段经准备、校验和持久化后发布。WebUI 中秘密字段省略或留空表示保留已保存的值；直接编辑 JSON 时，空值表示清空，省略则使用默认值。ntp 留空使用系统时钟，不再探测或自动写回 NTP 地址。

网络代理统一在 WebUI 的“网络配置 → 网络代理”中配置；网络配置位于设置标签首位并默认打开，选择协议后填写 IP 或域名加端口，需要认证时展开填写用户名和密码。由 account.telegram.proxy 保存，Telegram、机器人和软件更新共用。console.bot.proxy 与 update.self.proxy 仅兼容读取旧文件，不再生效或提供编辑入口。模块启停位于独立“模块管理”；运行日志、组件健康及诊断事件位于“检查更新”之前的独立“日志管理”，支持按全部功能、功能分组和细分模块查看。软件升级位于独立“检查更新”。

`download.control.executors` 可设置为 `["aria2","local","http"]`，选择本地执行器时必须同时提供本机绝对路径 `local_root`。只有明确未接受任务的错误允许降级；超时、响应丢失或已有任务 ID 时停止提交。aria2 仍需启用对应模块和自动下载。空列表沿用旧 downloader.mode；更改仅影响新提交，不迁移既有任务。

下载模式、本地根目录、并发文件数、DC 连接数、过滤/命名、表情触发、HTTP 公网地址/TTL 和分组转发规则支持热更新。表中“需要重启”指自动协调对应服务及必要依赖，不等于整个进程重启。账号命名空间、存储位置和程序升级需要进程重启。

WebUI 的“系统设置 → 一键完全重置”须再次确认后执行。重置会停止服务、关闭数据库与日志，再清空应用目录内 `.tdl/`、`components/` 的内容，删除 `tdl_config.json`、旧 `config.json` 及其写入临时文件，包含全局配置、历史组件配置与密钥，以及所有命名空间的登录凭据和任务记录。自定义旧组件目录仅清理 TDL 的组件配置文件、密钥和迁移记录。目录本身保留以兼容挂载；完成后程序退出，下次启动需重新配置与登录，容器遵循自身重启策略。清理结果显示在程序控制台。

新配置的 Telegram 内置预设默认为 `desktop`，WebUI 提供下拉选择并提示不建议修改；“强制使用内部预设”默认勾选。已有配置保留其预设及自定义凭据选择，旧版 `config.json` 省略开关时仍沿用历史自动选择。API ID 未设置时显示空输入框，API Hash 不回显，两者留空均保留原值。断线重试参数位于“网络配置 → 高级选项”，默认折叠；原有重试行为保持不变。

日志管理默认显示当前账号和系统公共记录，支持级别、诊断类型、时间、关键词筛选及 JSON 导出；每页 200 条，最新页每 5 秒刷新，历史页与离开页面时停止自动刷新。面板查询最近 2000 条结构化记录，写入 `.tdl/log/events.jsonl`；原有 `latest.log` 继续保留。两类文件每 10 MB 轮转，备份最多 3 份且最长保留 7 天。重启恢复当前结构化日志文件，轮转备份需在服务器文件系统查看；旧版本只有文本日志的记录不自动转换。详细日志开关位于系统设置，保存后对后续日志立即生效。CMD／终端仅保留启动、停止和 WebUI 地址等简短提示，详细运行日志在 WebUI 查看；文本日志和结构化日志统一脱敏，日志写入故障按输出端在页面提示，已有内存记录仍可查看。沿用 DEBUG（高频细节与正常取消）、INFO（关键结果）、WARN（可恢复异常）、ERROR（最终失败）四级；默认 INFO，详细日志开启后记录 DEBUG。分级、字段和开发约定见 [日志规范](logging.md)。

## account.telegram — Telegram 账号

运行作用域：`account`。

| 字段 | 含义 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- | --- |
| `proxy` | 统一网络代理 | `string` | `""` | proxy | 是 | 是 |
| `ntp` | 时间校准服务器 | `string` | `""` | — | 否 | 是 |
| `file_limit` | 并发下载文件数 | `int` | `1` | ≥ 1，≤ 10000 | 否 | 否 |
| `dc_pool_size` | 每 DC 下载容量 | `int` | `8` | ≥ 1，≤ 10000 | 否 | 否 |
| `delay_seconds` | 任务间隔（秒） | `int` | `0` | ≥ 0，≤ 3600 | 否 | 是 |
| `reconnect_timeout_seconds` | 断线重试间隔（秒） | `int` | `3` | ≥ 0，≤ 86400 | 否 | 是 |
| `api_id` | API ID | `int` | `0` | ≥ 0 | 否 | 否 |
| `api_hash` | API Hash | `string` | `""` | — | 是 | 否 |
| `builtin_preset` | 内置预设 | `string` | `"desktop"` | desktop / builtin /  | 否 | 否 |
| `use_builtin` | 强制使用内部预设 | `bool` | `true` | — | 否 | 否 |

## configuration.manager — 统一配置管理

运行作用域：`process`。

固定启用，统一管理顶层 system 设置及全局组件设置；自身不另建组件配置。

## console.bot — Bot 控制台

运行作用域：`account`。

| 字段 | 含义 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- | --- |
| `token` | 机器人 Token | `string` | `""` | — | 是 | 是 |
| `proxy` | 旧版机器人代理（已停用） | `string` | `""` | 已停用，仅兼容读取旧文件；统一使用 account.telegram.proxy | 是 | 否 |
| `allowed_users` | 允许的用户 ID | `strings` | `[]` | — | 否 | 否 |

## download.control — 下载任务控制

运行作用域：`account`。

| 字段 | 含义 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- | --- |
| `mode` | 默认下载方式 | `string` | `"aria2"` | aria2 / local / internal | 否 | 否 |
| `executors` | 执行器优先级 | `strings` | `[]` | — | 否 | 否 |
| `local_root` | 本地下载根目录 | `string` | `""` | — | 否 | 否 |

## downloader.aria2 — aria2 下载器

运行作用域：`account`。

| 字段 | 含义 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- | --- |
| `rpc_url` | RPC 连接地址 | `string` | `"http://127.0.0.1:6800/jsonrpc"` | url | 是 | 是 |
| `secret` | RPC 密钥 | `string` | `""` | — | 是 | 是 |
| `directory` | 远程下载目录 | `string` | `""` | — | 否 | 否 |
| `timeout_seconds` | 连接超时（秒） | `int` | `30` | ≥ 1，≤ 3600 | 否 | 是 |
| `auto_download` | 触发后自动下载 | `bool` | `true` | — | 否 | 否 |
| `status_interval_ms` | 状态同步间隔（毫秒） | `int` | `60000` | ≥ 100，≤ 3600000 | 否 | 否 |
| `connect_retry_ms` | 连接首次重试间隔（毫秒） | `int` | `10000` | ≥ 100，≤ 3600000 | 否 | 否 |
| `connect_retry_max_ms` | 连接最大重试间隔（毫秒） | `int` | `60000` | ≥ 100，≤ 3600000 | 否 | 否 |
| `monitor_poll_ms` | 零速检测间隔（毫秒） | `int` | `30000` | ≥ 100，≤ 3600000 | 否 | 否 |
| `monitor_stall_seconds` | 零速持续阈值（秒） | `int` | `180` | ≥ 1，≤ 86400 | 否 | 否 |
| `monitor_pause_seconds` | 零速恢复暂停时间（秒） | `int` | `10` | ≥ 1，≤ 3600 | 否 | 否 |
| `monitor_action_seconds` | 零速治理操作超时（秒） | `int` | `30` | ≥ 1，≤ 300 | 否 | 否 |
| `error_window_seconds` | Telegram 错误统计窗口（秒） | `int` | `10` | ≥ 1，≤ 3600 | 否 | 否 |
| `error_threshold` | Telegram 错误触发次数 | `int` | `3` | ≥ 1，≤ 10000 | 否 | 否 |
| `error_cooldown_seconds` | Telegram 错误治理冷却时间（秒） | `int` | `10` | ≥ 1，≤ 3600 | 否 | 否 |
| `error_pause_seconds` | Telegram 错误恢复暂停时间（秒） | `int` | `5` | ≥ 1，≤ 3600 | 否 | 否 |
| `error_action_seconds` | Telegram 错误治理操作超时（秒） | `int` | `30` | ≥ 1，≤ 300 | 否 | 否 |

## downloader.local — 本地下载器

运行作用域：`connection`。

| 字段 | 含义 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- | --- |
| `poll_interval_ms` | 待执行任务扫描间隔（毫秒） | `int` | `5000` | ≥ 100，≤ 3600000 | 否 | 否 |
| `shutdown_timeout_seconds` | 停机暂停记录超时（秒） | `int` | `5` | ≥ 1，≤ 300 | 否 | 否 |

## filter.rules — 下载过滤规则

运行作用域：`account`。

| 字段 | 含义 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- | --- |
| `include` | 允许的扩展名 | `strings` | `[]` | — | 否 | 否 |
| `exclude` | 排除的扩展名 | `strings` | `[]` | — | 否 | 否 |
| `min_mb` | 最小文件大小（MB） | `int` | `0` | ≥ 0 | 否 | 否 |
| `max_mb` | 最大文件大小（MB） | `int` | `0` | ≥ 0 | 否 | 否 |

## forward.rules — 分组转发规则

运行作用域：`account`。

| 字段 | 含义 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- | --- |
| `rules` | 来源 → 目标 | `objects` | `[]` | — | 否 | 否 |

`rules` 每项包含 `id`（非空且唯一）、`name`（最多 100 字）、`enabled`、`sources`（来源数组）、`targets`（目标数组）、`mode`（default/clone）、`silent`。来源/目标格式为 `user:123`、`chat:123`、`channel:123`；目标也可为 `self`。最多 200 条规则，每条最多 500 个来源和 100 个目标，不允许转发环路。示例：`{"id":"saved","name":"收藏","enabled":true,"sources":["channel:123"],"targets":["self"],"mode":"default","silent":false}`。

## forwarder — 转发队列

运行作用域：`connection`。

| 字段 | 含义 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- | --- |
| `mode` | 默认转发模式 | `string` | `"default"` | default / clone | 否 | 否 |
| `target` | 默认目标 | `string` | `""` | — | 否 | 否 |
| `silent` | 静默发送 | `bool` | `false` | — | 否 | 否 |
| `dedupe_ttl_seconds` | 去重有效期（秒） | `int` | `600` | ≥ 0，≤ 8640000 | 否 | 否 |
| `poll_interval_ms` | 队列扫描间隔（毫秒） | `int` | `2000` | ≥ 100，≤ 3600000 | 否 | 否 |
| `retry_base_seconds` | 首次重试间隔（秒） | `int` | `5` | ≥ 1，≤ 86400 | 否 | 否 |
| `retry_max_seconds` | 最大重试间隔（秒） | `int` | `300` | ≥ 1，≤ 86400 | 否 | 否 |
| `max_attempts` | 失败次数上限 | `int` | `10` | ≥ 1，≤ 100 | 否 | 否 |
| `history_hours` | 已结束任务保留时间（小时） | `int` | `24` | ≥ 1，≤ 8760 | 否 | 否 |
| `history_limit` | 已结束任务保留数量 | `int` | `200` | ≥ 1，≤ 100000 | 否 | 否 |

## naming.rules — 目录与文件命名

运行作用域：`account`。

| 字段 | 含义 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- | --- |
| `filename` | 文件名模板 | `string` | `"P_S_F"` | — | 否 | 否 |
| `directory` | 目录模板 | `string` | `"G\\Y\u0026M"` | — | 否 | 否 |
| `max_bytes` | 文件名字节上限 | `int` | `255` | ≥ 0，≤ 255 | 否 | 否 |

## notify.telegram — Telegram 通知

运行作用域：`account`。

| 字段 | 含义 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- | --- |
| `on_download_start` | 下载开始通知 | `bool` | `false` | — | 否 | 否 |
| `on_download_complete` | 下载完成通知 | `bool` | `false` | — | 否 | 否 |
| `on_download_pause` | 下载暂停通知 | `bool` | `false` | — | 否 | 否 |
| `on_download_error` | 下载失败通知 | `bool` | `false` | — | 否 | 否 |
| `live_progress` | 实时进度通知 | `bool` | `false` | — | 否 | 否 |
| `live_progress_interval_seconds` | 进度更新间隔（秒） | `int` | `5` | ≥ 5，≤ 86400 | 否 | 否 |
| `recipients` | 接收者 ID | `strings` | `[]` | — | 否 | 否 |
| `timeout_seconds` | 发送超时（秒） | `int` | `15` | ≥ 1，≤ 300 | 否 | 否 |

## panel.webui — Web 管理面板

运行作用域：`process`。

| 字段 | 含义 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- | --- |
| `address` | 监听地址 | `string` | `"0.0.0.0"` | — | 否 | 是 |
| `port` | 监听端口 | `int` | `22335` | ≥ 1，≤ 65535 | 否 | 是 |
| `username` | 登录用户名 | `string` | `"admin"` | nonempty | 否 | 是 |
| `password` | 登录密码 | `string` | `"admin"` | — | 是 | 是 |

## proxy.range — HTTP Range 代理

运行作用域：`process`。

| 字段 | 含义 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- | --- |
| `address` | 监听地址 | `string` | `"0.0.0.0"` | — | 否 | 是 |
| `port` | 监听端口 | `int` | `22334` | ≥ 1，≤ 65535 | 否 | 是 |
| `public_base_url` | 公开访问地址 | `string` | `""` | url | 否 | 否 |
| `link_ttl_hours` | 链接有效期（小时） | `int` | `24` | ≥ 0，≤ 876000 | 否 | 否 |
| `client_wait_seconds` | 等待账号连接超时（秒） | `int` | `30` | ≥ 1，≤ 600 | 否 | 否 |
| `persist_timeout_seconds` | 传输记录保存超时（秒） | `int` | `5` | ≥ 1，≤ 300 | 否 | 否 |
| `task_cleanup_seconds` | 过期任务清理间隔（秒） | `int` | `3600` | ≥ 1，≤ 86400 | 否 | 否 |
| `source_cleanup_seconds` | 闲置源清理间隔（秒） | `int` | `60` | ≥ 1，≤ 3600 | 否 | 否 |

## storage.maintenance — 存储维护

运行作用域：`account`。

无配置字段。

## trigger.download — 下载触发意图

运行作用域：`connection`。

无配置字段。

## trigger.forward — 转发触发意图

运行作用域：`connection`。

| 字段 | 含义 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- | --- |
| `listen` | 监听来源 | `strings` | `[]` | — | 否 | 否 |
| `listen_comments` | 监听频道评论 | `bool` | `true` | — | 否 | 否 |

## trigger.messagelink — 消息链接触发

运行作用域：`account`。

无配置字段。

## trigger.reaction — 表情触发

运行作用域：`account`。

| 字段 | 含义 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- | --- |
| `download` | 下载表情 | `strings` | `[]` | — | 否 | 否 |
| `forward` | 转发表情 | `strings` | `[]` | — | 否 | 否 |

## update.self — 版本更新

运行作用域：`account`。

| 字段 | 含义 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- | --- |
| `proxy` | 旧版更新代理（已停用） | `string` | `""` | 已停用，仅兼容读取旧文件；统一使用 account.telegram.proxy | 是 | 否 |

