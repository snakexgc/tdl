# 时间同步 SWC

`time.sync` 是进程作用域组件，独立于 Telegram 账号。WebUI 监听就绪后启动该组件；`Start` 只安排 RTE 任务，首次同步立即在后台执行。随后默认每 5 分钟同步一次。同一同步任务不重叠，停止时取消 DNS、UDP、代理握手及 HTTPS 请求并排空任务。

每轮执行顺序为：并发直连 NTP 池 → 全部失败后，通过已配置的统一代理并发重试一轮 → 仍失败或未配置代理时回落到系统时间。收到首个有效响应即取消并排空其他探测，不等待慢服务器超时，也不根据响应里报告的耗时重新排序。

## 配置

在 WebUI「设置 → 网络 → 时间同步」中配置，保存并重启生效。下面是每分钟同步的示例，位于 `tdl_config.json` 的 `components` 下：

```json
"time.sync": {
  "enabled": true,
  "values": {
    "sync_interval_seconds": 60,
    "timeout_seconds": 3
  }
}
```

- `sync_interval_seconds`：同步完成到下一次开始的间隔，默认 `300`，范围 `60–86400` 秒。
- `timeout_seconds`：一轮并发探测的整体超时，包含 DNS、连接、握手及响应，默认 `3`，范围 `1–30` 秒。直连和代理各使用一次此预算，默认最长约 6 秒；直连成功则立即结束。
- `enabled: false`：停用后台校时，RTE 时间读取继续使用系统时间。

服务器池由 SWC 统一维护，设置区块仅提供同步间隔及每轮探测超时。内置池包含：

| 服务 | 地址 |
| --- | --- |
| 阿里云 | `ntp.aliyun.com` |
| 腾讯云 | `time1.cloud.tencent.com` |
| 国家授时中心 | `ntp.ntsc.ac.cn` |
| 苹果 | `time.apple.com` |
| 谷歌 | `time.google.com` |
| 微软 | `time.windows.com` |
| Cloudflare | `time.cloudflare.com` |
| NTP Pool | `cn.pool.ntp.org` |
| 上海交通大学 | `ntp.sjtu.edu.cn` |
| 南京大学 | `ntp.nju.edu.cn` |

直连使用 UDP/123；响应必须通过 NTP 客户端的报文关联、模式、同步标志、层级、参考时间和离散度校验，错误响应不能成为时间基准。地址参考：[阿里云](https://help.aliyun.com/zh/ecs/user-guide/alibaba-cloud-ntp-server/)、[腾讯云](https://intl.cloud.tencent.com/zh/document/product/213/32379)、[苹果](https://www.apple.com/server/docs/Network_Services_v10.4.pdf)、[谷歌](https://developers.google.com/time)、[微软](https://learn.microsoft.com/en-us/windows-server/networking/windows-time-service/how-the-windows-time-service-works)。公共服务的可达性取决于部署网络，某个地址故障不会阻塞其他候选。

## 代理重试

复用 `account.telegram.proxy` 的启动快照，经 `network.proxy` 端口注入；不增加第二份代理配置，也不读取环境代理覆盖用户设置。配置保存后仍需重启生效，时间同步不依赖 Telegram 在线。

- SOCKS5：使用 [UDP ASSOCIATE](https://www.rfc-editor.org/rfc/rfc1928) 转发 NTP，支持用户名/密码认证、IPv4/IPv6 地址和代理端解析目标域名。查询结束、超时或取消时同时关闭 UDP 和 TCP 控制连接。
- HTTP/HTTPS：普通 [CONNECT](https://www.rfc-editor.org/rfc/rfc9110.html#section-9.3.6) 只能建立 TCP 隧道，因此通过代理并发读取阿里、腾讯、苹果、谷歌、微软及 Cloudflare 的 HTTPS `Date` 响应。SOCKS5 的 UDP 查询提前失败且仍有本轮预算时也可使用此备选。
- HTTPS 备选保持证书校验，采用随机请求参数及禁用缓存指令，并拒绝带非零 `Age`、缺失/无效 `Date` 或错误状态码的响应；不会在代理失败后偷偷直连。`Date` 只有秒级精度，加上半秒取整和半程延迟估计，与 NTP 精度不同，状态明确标为 `https`。

代理故障写入状态和日志时不包含代理 URL、用户名或密码。

服务器偏好配置已移除，历史配置中的废弃字段在加载时丢弃，下次显式保存时清除；加载不改写文件。同步选中的服务器、偏移和故障只保存为运行状态。

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

时间 SWC 同时显式发布 `ports.ClockName`（`time.clock`）端口，装配层从其宿主 `Resolve` 后绑定到进程 RTE。现有适配器通过 `rte.ClockFrom(ctx)` 或 `rte.Now(ctx)` 使用同一端口；BSW 存储适配器通过 `ports.ClockFrom(ctx)` / `ports.Now(ctx)` 读取相同上下文中的端口，保持下层不依赖 RTE。不要在消费者内创建 NTP 客户端。Telegram 协议时间、命名日期/模板 `now`、WebUI 心跳、下载/转发记录、机器人进度消息、日志、诊断事件及运行状态的业务时间已接入。

校时只调整应用时钟，不修改操作系统时间。成功后以网络校准时间为锚点，通过单调计时推进；两次同步之间系统墙上时间的跳变不会改变该锚点。`Now` 表示当前业务时间，重新校准或回落到系统时间时可能向前或向后调整。

请求 deadline、内存缓存、限速、轮询调度和耗时统计继续使用系统单调时钟、`time.Timer` 与 context 超时；不得用可调整的业务时间度量这些间隔。持久化任务的创建、更新、完成、重试日期和保留期比较使用同一业务时钟；aria2 RPC 控制租约的真实超时仍使用系统时钟。

## 跨平台与 Docker

系统回落使用 Go `time.Now()`：由 Go 运行时调用 Windows、Linux、macOS 等平台原生的时钟接口，不依赖 `date`、PowerShell、`timedatectl` 或容器内的 NTP 守护进程，也不需要修改系统时钟的权限。

普通 Docker 容器读取宿主内核的墙上时间；Docker Desktop 的 Linux 容器读取其虚拟机内核时间。`TZ`、`/etc/localtime` 和 Windows 时区只影响本地显示；NTP 校准的是同一个时间瞬间，不固定加八小时。首次同步前、停用组件或网络与代理均失败时，统一时间端口立即返回该系统时间。Docker 镜像已包含 CA 证书及 `tzdata`，无需增加系统校时权限。

## 故障与状态

首次成功之前使用系统时间。成功后原子发布整个时间锚点和状态，已有消费者自动采用新基准。后续同步时，直连及代理都失败则清除当前偏移、回落到系统时间，在下一周期重试；不会停用其他组件或关闭 WebUI。正常退出导致的取消不覆盖已有校准。

模块健康页显示 `time.sync` 的 `synchronize` 任务、执行次数和最近失败；日志记录选中服务器及偏移。`GET /api/status` 的 `clock` 字段和时间端口 `Status()` 提供：

| 字段 | 含义 |
| --- | --- |
| `server` | 当前 NTP 地址或 HTTPS 时间来源；系统回落时为空 |
| `source` | 当前来源：`system`、`ntp` 或 `https` |
| `via_proxy` | 当前网络时间是否经由统一代理获取 |
| `synchronized` | 当前是否使用网络校准时间；系统回落时为 `false` |
| `offset_ns` | 最近采样时的时间偏移，纳秒；系统回落时为 `0` |
| `last_attempt` | 最近一次已完成的同步尝试时间 |
| `last_success` | 最近一次成功同步时间 |
| `last_error` | 最近失败原因；下次成功清除 |

`last_success` 在回落后保留作为历史信息，`source=system`、`synchronized=false` 和非空 `last_error` 表示本轮网络校时失败。下次同步成功后清除错误并恢复网络时间。
