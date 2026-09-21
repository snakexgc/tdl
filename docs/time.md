# 时间同步 SWC

`time.sync` 是进程作用域组件，独立于 Telegram 账号。WebUI 监听就绪后启动该组件；`Start` 只安排 RTE 任务，首次同步立即在后台执行。随后默认每 5 分钟同步一次。同一同步任务不重叠，停止时取消 DNS/UDP 请求并排空任务。

## 配置

在 WebUI「设置 → 网络 → 时间同步」中配置，保存并重启生效。下面是每分钟同步的示例，位于 `tdl_config.json` 的 `components` 下：

```json
"time.sync": {
  "enabled": true,
  "values": {
    "server": "",
    "sync_interval_seconds": 60,
    "timeout_seconds": 3
  }
}
```

- `server`：首选服务器，支持域名、IP 及可选端口。留空时自动选择；已填写时优先尝试三次，失败后并发探测内置候选并选择最快的有效响应。
- `sync_interval_seconds`：同步完成到下一次开始的间隔，默认 `300`，范围 `60–86400` 秒。
- `timeout_seconds`：每个候选的一次探测超时，默认 `3`，范围 `1–30` 秒。
- `enabled: false`：停用后台校时，RTE 时间读取继续使用系统时间。

内置候选为 cn.pool.ntp.org、ntp.aliyun.com、ntp.tencent.com、ntp.sjtu.edu.cn、ntp.nju.edu.cn、time1.google.com、time1.apple.com、time.cloudflare.com、time.windows.com。

旧 `components["account.telegram"].values.ntp` 在加载时兼容迁移到 `components["time.sync"].values.server`。如果新字段已明确填写，以新字段为准，包括明确留空。加载不改写文件，下一次显式保存写入新结构。同步选中的服务器、偏移和故障只保存为运行状态，不覆盖用户偏好。

## 读取时间

组件通过 `Kernel.Clock` 获取 RTE 注入的 `ports.Clock`。所有宿主共享同一绑定，在校时组件未启动、停用或尚未成功同步时，读取仍立即返回。

```go
func (s *Service) Init(ctx context.Context, k rte.Kernel) error {
    s.clock = k.Clock
    return nil
}

// 读取当前业务时间，无网络请求。
now := s.clock.Now()
status := s.clock.Status()
```

时间 SWC 同时显式发布 `ports.ClockName`（`time.clock`）端口，装配层从其宿主 `Resolve` 后绑定到进程 RTE。现有适配器通过 `rte.ClockFrom(ctx)` 或 `rte.Now(ctx)` 使用同一端口；不要在消费者内创建 NTP 客户端。Telegram 协议时间、命名日期/模板 `now` 和 WebUI 心跳时间已接入。

校时只调整应用中的时间偏移，不修改操作系统时间。`Now` 表示当前墙上时间，校准时可能向前或向后调整。请求 deadline、缓存存活时长、限速、重试、调度和耗时统计应继续使用系统单调时钟、`time.Timer` 与 context 超时；不得用可调整的校时时间度量这些间隔。

## 故障与状态

首次成功之前使用系统时间。成功后原子发布时间偏移，已有消费者自动采用新偏移。后续同步失败时继续使用上次偏移，并在下一周期重试；不会停用其他组件或关闭 WebUI。

模块健康页显示 `time.sync` 的 `synchronize` 任务、执行次数和最近失败；日志记录选中服务器及偏移。`GET /api/status` 的 `clock` 字段和时间端口 `Status()` 提供：

| 字段 | 含义 |
| --- | --- |
| `server` | 最近一次成功校时的服务器 |
| `synchronized` | 本进程是否曾成功校时；后续失败不会清除此标志 |
| `offset_ns` | 当前使用的时间偏移，纳秒 |
| `last_attempt` | 最近一次已完成的同步尝试时间 |
| `last_success` | 最近一次成功同步时间 |
| `last_error` | 最近失败原因；下次成功清除 |

`synchronized=true` 且 `last_error` 非空表示正在沿用上次偏移，可通过 `last_success` 判断最近成功时间。
