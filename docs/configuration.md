# 组件配置参考

由 `go run ./cmd/swc-docs` 根据已接入配置存储的组件 Manifest 生成。不要手工修改字段表。

使用 `--component-config <目录>` 启用组件配置；文件名为 `swc-<组件 ID>.json`，结构为 `version/enabled/values`。敏感字段通过配置存储分离到 `secrets/` 文件，不在组件查询响应中返回。

本表包含八个静态组件，以及本地下载器、aria2 治理、转发队列、Range 和下载路由的配置。动态组件运行时可热更新并保存配置；aria2 RPC 连接地址、共享并发配额及其他未拆分设置仍使用兼容配置，本表不表示整个配置迁移已完成。

`download.control.executors` 可设置为 `["aria2","local","http"]`，选择本地执行器时必须同时提供本机绝对路径 `local_root`。只有明确未接受任务的错误允许降级；超时、响应丢失或已有任务 ID 时停止提交。aria2 仍需启用对应模块和自动下载。空列表沿用旧 downloader.mode；更改仅影响新提交，不迁移既有任务。

## account.telegram — Telegram 账号

| 字段 | 类型 | 默认值 | 范围 | 敏感 |
| --- | --- | --- | --- | --- |
| `api_id` | `int` | `0` | ≥ 0 | 否 |
| `api_hash` | `string` | `""` | — | 是 |
| `builtin_preset` | `string` | `""` | — | 否 |
| `use_builtin` | `bool` | `false` | — | 否 |

## console.bot — Bot 控制台

| 字段 | 类型 | 默认值 | 范围 | 敏感 |
| --- | --- | --- | --- | --- |
| `allowed_users` | `strings` | `[]` | — | 否 |

## filter.rules — 下载过滤规则

| 字段 | 类型 | 默认值 | 范围 | 敏感 |
| --- | --- | --- | --- | --- |
| `include` | `strings` | `[]` | — | 否 |
| `exclude` | `strings` | `[]` | — | 否 |
| `min_mb` | `int` | `0` | ≥ 0 | 否 |
| `max_mb` | `int` | `0` | ≥ 0 | 否 |

## naming.rules — 目录与文件命名

| 字段 | 类型 | 默认值 | 范围 | 敏感 |
| --- | --- | --- | --- | --- |
| `filename` | `string` | `"P_S_F"` | — | 否 |
| `directory` | `string` | `"G\\Y\u0026M"` | — | 否 |
| `max_bytes` | `int` | `255` | ≥ 0，≤ 255 | 否 |

## notify.telegram — Telegram 通知

| 字段 | 类型 | 默认值 | 范围 | 敏感 |
| --- | --- | --- | --- | --- |
| `recipients` | `strings` | `[]` | — | 否 |
| `timeout_seconds` | `int` | `15` | ≥ 1，≤ 300 | 否 |

## trigger.messagelink — 消息链接触发

无配置字段。

## trigger.reaction — 表情触发

| 字段 | 类型 | 默认值 | 范围 | 敏感 |
| --- | --- | --- | --- | --- |
| `download` | `strings` | `[]` | — | 否 |
| `forward` | `strings` | `[]` | — | 否 |

## update.self — 版本更新

| 字段 | 类型 | 默认值 | 范围 | 敏感 |
| --- | --- | --- | --- | --- |
| `proxy` | `string` | `""` | — | 是 |

## downloader.aria2 — aria2 下载器

| 字段 | 类型 | 默认值 | 范围 | 敏感 |
| --- | --- | --- | --- | --- |
| `status_interval_ms` | `int` | `60000` | ≥ 100，≤ 3600000 | 否 |
| `connect_retry_ms` | `int` | `10000` | ≥ 100，≤ 3600000 | 否 |
| `connect_retry_max_ms` | `int` | `60000` | ≥ 100，≤ 3600000 | 否 |
| `monitor_poll_ms` | `int` | `30000` | ≥ 100，≤ 3600000 | 否 |
| `monitor_stall_seconds` | `int` | `180` | ≥ 1，≤ 86400 | 否 |
| `monitor_pause_seconds` | `int` | `10` | ≥ 1，≤ 3600 | 否 |
| `monitor_action_seconds` | `int` | `30` | ≥ 1，≤ 300 | 否 |
| `error_window_seconds` | `int` | `10` | ≥ 1，≤ 3600 | 否 |
| `error_threshold` | `int` | `3` | ≥ 1，≤ 10000 | 否 |
| `error_cooldown_seconds` | `int` | `10` | ≥ 1，≤ 3600 | 否 |
| `error_pause_seconds` | `int` | `5` | ≥ 1，≤ 3600 | 否 |
| `error_action_seconds` | `int` | `30` | ≥ 1，≤ 300 | 否 |

## downloader.local — 本地下载器

| 字段 | 类型 | 默认值 | 范围 | 敏感 |
| --- | --- | --- | --- | --- |
| `poll_interval_ms` | `int` | `5000` | ≥ 100，≤ 3600000 | 否 |
| `shutdown_timeout_seconds` | `int` | `5` | ≥ 1，≤ 300 | 否 |

## forwarder — 转发队列

| 字段 | 类型 | 默认值 | 范围 | 敏感 |
| --- | --- | --- | --- | --- |
| `poll_interval_ms` | `int` | `2000` | ≥ 100，≤ 3600000 | 否 |
| `retry_base_seconds` | `int` | `5` | ≥ 1，≤ 86400 | 否 |
| `retry_max_seconds` | `int` | `300` | ≥ 1，≤ 86400 | 否 |
| `max_attempts` | `int` | `10` | ≥ 1，≤ 100 | 否 |
| `history_hours` | `int` | `24` | ≥ 1，≤ 8760 | 否 |
| `history_limit` | `int` | `200` | ≥ 1，≤ 100000 | 否 |

## proxy.range — HTTP Range 代理

| 字段 | 类型 | 默认值 | 范围 | 敏感 |
| --- | --- | --- | --- | --- |
| `client_wait_seconds` | `int` | `30` | ≥ 1，≤ 600 | 否 |
| `persist_timeout_seconds` | `int` | `5` | ≥ 1，≤ 300 | 否 |
| `task_cleanup_seconds` | `int` | `3600` | ≥ 1，≤ 86400 | 否 |
| `source_cleanup_seconds` | `int` | `60` | ≥ 1，≤ 3600 | 否 |

## download.control — 下载任务控制

| 字段 | 类型 | 默认值 | 范围 | 敏感 |
| --- | --- | --- | --- | --- |
| `executors` | `strings` | `[]` | — | 否 |
| `local_root` | `string` | `""` | — | 否 |

