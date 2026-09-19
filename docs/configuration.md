# 组件配置参考

由 `go run ./cmd/swc-docs` 根据已接入配置存储的组件 Manifest 生成。不要手工修改字段表。

正常启动默认使用组件配置，首次启动将旧配置导入应用目录的 `components/<账号的 Base64URL 编码>/`，保留原文件。`--component-config <目录>` 可指定已有目录。文件名为 `swc-<组件 ID>.json`，结构为 `version/enabled/values`。敏感字段分离到 `secrets/` 文件，不在查询响应中返回。

本表覆盖统一目录中的 18 个业务组件。组件文件是业务配置的权威来源；缺文件或缺字段使用 schema 默认值，不回退旧业务配置。协议适配器通过集中映射获取兼容 DTO，SWC 不读取旧全局配置。

未启动或已停用组件仍可编辑配置。标注需要重启的字段先保存，再协调所属服务；失败时运行状态保留错误，配置页显示等待重启。其他字段经准备、校验和持久化后发布。秘密字段省略或留空表示保留已保存的值。组件模式下 ntp 留空使用系统时钟，不再自动改写旧配置文件。

`download.control.executors` 可设置为 `["aria2","local","http"]`，选择本地执行器时必须同时提供本机绝对路径 `local_root`。只有明确未接受任务的错误允许降级；超时、响应丢失或已有任务 ID 时停止提交。aria2 仍需启用对应模块和自动下载。空列表沿用旧 downloader.mode；更改仅影响新提交，不迁移既有任务。

下载模式、本地根目录、并发文件数、DC 连接数、过滤/命名、表情触发、HTTP 公网地址/TTL 和分组转发规则支持热更新。表中“需要重启”指自动协调对应服务及必要依赖，不等于整个进程重启。账号命名空间、存储位置和程序升级需要进程重启。

## account.telegram — Telegram 账号

运行作用域：`account`。

| 字段 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- |
| `proxy` | `string` | `""` | proxy | 是 | 是 |
| `ntp` | `string` | `""` | — | 否 | 是 |
| `file_limit` | `int` | `1` | ≥ 1，≤ 10000 | 否 | 否 |
| `dc_pool_size` | `int` | `8` | ≥ 1，≤ 10000 | 否 | 否 |
| `delay_seconds` | `int` | `0` | ≥ 0，≤ 3600 | 否 | 是 |
| `reconnect_timeout_seconds` | `int` | `3` | ≥ 0，≤ 86400 | 否 | 是 |
| `api_id` | `int` | `0` | ≥ 0 | 否 | 否 |
| `api_hash` | `string` | `""` | — | 是 | 否 |
| `builtin_preset` | `string` | `""` | — | 否 | 否 |
| `use_builtin` | `bool` | `false` | — | 否 | 否 |

## console.bot — Bot 控制台

运行作用域：`account`。

| 字段 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- |
| `token` | `string` | `""` | — | 是 | 是 |
| `proxy` | `string` | `""` | proxy | 是 | 是 |
| `allowed_users` | `strings` | `[]` | — | 否 | 否 |

## download.control — 下载任务控制

运行作用域：`account`。

| 字段 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- |
| `mode` | `string` | `"aria2"` | aria2 / local / internal | 否 | 否 |
| `executors` | `strings` | `[]` | — | 否 | 否 |
| `local_root` | `string` | `""` | — | 否 | 否 |

## downloader.aria2 — aria2 下载器

运行作用域：`account`。

| 字段 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- |
| `rpc_url` | `string` | `"http://127.0.0.1:6800/jsonrpc"` | url | 是 | 是 |
| `secret` | `string` | `""` | — | 是 | 是 |
| `directory` | `string` | `""` | — | 否 | 否 |
| `timeout_seconds` | `int` | `30` | ≥ 1，≤ 3600 | 否 | 是 |
| `auto_download` | `bool` | `true` | — | 否 | 否 |
| `status_interval_ms` | `int` | `60000` | ≥ 100，≤ 3600000 | 否 | 否 |
| `connect_retry_ms` | `int` | `10000` | ≥ 100，≤ 3600000 | 否 | 否 |
| `connect_retry_max_ms` | `int` | `60000` | ≥ 100，≤ 3600000 | 否 | 否 |
| `monitor_poll_ms` | `int` | `30000` | ≥ 100，≤ 3600000 | 否 | 否 |
| `monitor_stall_seconds` | `int` | `180` | ≥ 1，≤ 86400 | 否 | 否 |
| `monitor_pause_seconds` | `int` | `10` | ≥ 1，≤ 3600 | 否 | 否 |
| `monitor_action_seconds` | `int` | `30` | ≥ 1，≤ 300 | 否 | 否 |
| `error_window_seconds` | `int` | `10` | ≥ 1，≤ 3600 | 否 | 否 |
| `error_threshold` | `int` | `3` | ≥ 1，≤ 10000 | 否 | 否 |
| `error_cooldown_seconds` | `int` | `10` | ≥ 1，≤ 3600 | 否 | 否 |
| `error_pause_seconds` | `int` | `5` | ≥ 1，≤ 3600 | 否 | 否 |
| `error_action_seconds` | `int` | `30` | ≥ 1，≤ 300 | 否 | 否 |

## downloader.local — 本地下载器

运行作用域：`connection`。

| 字段 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- |
| `poll_interval_ms` | `int` | `5000` | ≥ 100，≤ 3600000 | 否 | 否 |
| `shutdown_timeout_seconds` | `int` | `5` | ≥ 1，≤ 300 | 否 | 否 |

## filter.rules — 下载过滤规则

运行作用域：`account`。

| 字段 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- |
| `include` | `strings` | `[]` | — | 否 | 否 |
| `exclude` | `strings` | `[]` | — | 否 | 否 |
| `min_mb` | `int` | `0` | ≥ 0 | 否 | 否 |
| `max_mb` | `int` | `0` | ≥ 0 | 否 | 否 |

## forward.rules — 分组转发规则

运行作用域：`account`。

| 字段 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- |
| `rules` | `objects` | `[]` | — | 否 | 否 |

## forwarder — 转发队列

运行作用域：`connection`。

| 字段 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- |
| `mode` | `string` | `"default"` | default / clone | 否 | 否 |
| `target` | `string` | `""` | — | 否 | 否 |
| `silent` | `bool` | `false` | — | 否 | 否 |
| `dedupe_ttl_seconds` | `int` | `600` | ≥ 0，≤ 8640000 | 否 | 否 |
| `poll_interval_ms` | `int` | `2000` | ≥ 100，≤ 3600000 | 否 | 否 |
| `retry_base_seconds` | `int` | `5` | ≥ 1，≤ 86400 | 否 | 否 |
| `retry_max_seconds` | `int` | `300` | ≥ 1，≤ 86400 | 否 | 否 |
| `max_attempts` | `int` | `10` | ≥ 1，≤ 100 | 否 | 否 |
| `history_hours` | `int` | `24` | ≥ 1，≤ 8760 | 否 | 否 |
| `history_limit` | `int` | `200` | ≥ 1，≤ 100000 | 否 | 否 |

## naming.rules — 目录与文件命名

运行作用域：`account`。

| 字段 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- |
| `filename` | `string` | `"P_S_F"` | — | 否 | 否 |
| `directory` | `string` | `"G\\Y\u0026M"` | — | 否 | 否 |
| `max_bytes` | `int` | `255` | ≥ 0，≤ 255 | 否 | 否 |

## notify.telegram — Telegram 通知

运行作用域：`account`。

| 字段 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- |
| `on_download_start` | `bool` | `false` | — | 否 | 否 |
| `on_download_complete` | `bool` | `false` | — | 否 | 否 |
| `on_download_pause` | `bool` | `false` | — | 否 | 否 |
| `on_download_error` | `bool` | `false` | — | 否 | 否 |
| `live_progress` | `bool` | `false` | — | 否 | 否 |
| `live_progress_interval_seconds` | `int` | `5` | ≥ 5，≤ 86400 | 否 | 否 |
| `recipients` | `strings` | `[]` | — | 否 | 否 |
| `timeout_seconds` | `int` | `15` | ≥ 1，≤ 300 | 否 | 否 |

## panel.webui — Web 管理面板

运行作用域：`process`。

| 字段 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- |
| `address` | `string` | `"0.0.0.0"` | — | 否 | 是 |
| `port` | `int` | `22335` | ≥ 1，≤ 65535 | 否 | 是 |
| `username` | `string` | `"admin"` | nonempty | 否 | 是 |
| `password` | `string` | `"admin"` | — | 是 | 是 |

## proxy.range — HTTP Range 代理

运行作用域：`process`。

| 字段 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- |
| `address` | `string` | `"0.0.0.0"` | — | 否 | 是 |
| `port` | `int` | `22334` | ≥ 1，≤ 65535 | 否 | 是 |
| `public_base_url` | `string` | `""` | url | 否 | 否 |
| `link_ttl_hours` | `int` | `24` | ≥ 0，≤ 876000 | 否 | 否 |
| `client_wait_seconds` | `int` | `30` | ≥ 1，≤ 600 | 否 | 否 |
| `persist_timeout_seconds` | `int` | `5` | ≥ 1，≤ 300 | 否 | 否 |
| `task_cleanup_seconds` | `int` | `3600` | ≥ 1，≤ 86400 | 否 | 否 |
| `source_cleanup_seconds` | `int` | `60` | ≥ 1，≤ 3600 | 否 | 否 |

## storage.maintenance — 存储维护

运行作用域：`account`。

无配置字段。

## trigger.download — 下载触发意图

运行作用域：`connection`。

无配置字段。

## trigger.forward — 转发触发意图

运行作用域：`connection`。

| 字段 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- |
| `listen` | `strings` | `[]` | — | 否 | 否 |
| `listen_comments` | `bool` | `true` | — | 否 | 否 |

## trigger.messagelink — 消息链接触发

运行作用域：`account`。

无配置字段。

## trigger.reaction — 表情触发

运行作用域：`account`。

| 字段 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- |
| `download` | `strings` | `[]` | — | 否 | 否 |
| `forward` | `strings` | `[]` | — | 否 | 否 |

## update.self — 版本更新

运行作用域：`account`。

| 字段 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |
| --- | --- | --- | --- | --- | --- |
| `proxy` | `string` | `""` | — | 是 | 否 |

