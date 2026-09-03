## 快速开始

**部署教程已出炉，可以根据教程进行部署：**  

https://snakexgc.github.io/2026/05/13/TDL_Docker_Deployment/  

当前教程还在不断完善，遇到问题欢迎在issues中或者电报群反馈！

Docker 部署也可以直接在控制面板更新：更新器只替换当前容器内的 `tdl` 程序，并以原 PID 启动新版本，不会拉取镜像、更新或重建 Docker 容器。同一容器重启后仍会使用更新后的程序；如果删除并重新创建容器，则会恢复为镜像自带的版本。

### JSON 配置说明

```jsonc
{
  "proxy": "socks5://127.0.0.1:1080", // 代理地址，如 socks5://127.0.0.1:1080 或 http://127.0.0.1:10808
  "proxy_username": "", // 代理用户名；没有认证时留空
  "proxy_password": "", // 代理密码；没有认证时留空
  "namespace": "default", // 当前用户的数据空间；由 Web 登录或切换用户维护，只允许英文字母
  "debug": false, // 是否开启调试模式
  "limit": 2, // 同时下载的文件任务数量
  "pool_size": 8, // 每个 Telegram DC 的连接池和 upload.getFile 并发上限；0 或负数会恢复为 8
  "delay": 0, // 两个下载任务之间的等待时间，单位秒
  "ntp": "", // NTP 服务器地址；留空时启动会自动选择最快的内置服务器并写回此项
  "reconnect_timeout": 3, // 重连超时时间，单位秒
  "download_dir": "G\\Y&M", // 下载目录模板，会拼接在下载根目录后
  "filename": "P_S_F", // 文件名模板；与 download_dir 使用同一组变量
  "filename_max_length": 255, // 最终文件名字节数上限（UTF-8 编码）；超长时优先缩短 I
  "trigger_reactions": [], // 指定触发下载的表情，如 ["👍", "🔥"]；为空时任意表情都可以触发
  "include": [], // 只下载指定扩展名，如 `["mp4", "mp3"]`，与exclude互斥
  "exclude": ["png","jpg"], // 排除指定扩展名，如 `["png", "jpg"]`与include互斥
  "file_size_min_mb": 0, // 文件大小左边界，单位 MB；0 表示不限制最小值
  "file_size_max_mb": 0, // 文件大小右边界，单位 MB；0 表示不限制最大值；左右边界均包含在内
  "http": {
    "address": "0.0.0.0", // HTTP 下载代理监听地址
    "port": 22334, // HTTP 下载代理监听端口
    "public_base_url": "http://127.0.0.1:22334", // aria2 访问 tdl 下载代理时使用的基础地址
    "download_link_ttl_hours": 24 // 下载链接有效期，单位小时；设置为 0 时永久有效且不自动清理
  },
  "webui": {
    "address": "0.0.0.0", // Web 管理面板监听地址
    "port": 22335, // Web 管理面板监听端口
    "username": "admin", // Web 管理面板用户名，首次登录后请立刻修改
    "password": "admin" // Web 管理面板密码，首次登录后请立刻修改
  },
  "modules": {
    "bot": true, // Telegram 机器人控制模块
    "watch": true, // 监听下载模块：表情监听和任务提交
    "http": true, // HTTP 下载代理模块：提供 /download 文件流链接
    "aria2": true, // aria2 管理模块：独立维护 RPC、任务恢复和异常监控
    "forward": false // 监听转发模块
  },
  "downloader": {
    "mode": "aria2" // 下载器模式：aria2 或 internal
  },
  "aria2": {
    "auto_download": true, // 表情触发并生成临时 HTTP 链接后，自动提交到 aria2
    "rpc_url": "http://127.0.0.1:6800/jsonrpc", // aria2 JSON-RPC 地址
    "secret": "123", // aria2 密钥
    "dir": "", // aria2 所在机器上的下载目录，注意区分操作系统，JSON 中 \ 需要写成 \\
    "timeout_seconds": 30 // aria2 超时时间，单位秒
  },
  "bot": {
    "token": "55555555:xxxxxx", // Telegram 机器人 token
    "allowed_users": [123456], // 允许的用户 ID 列表
    "notify": {
      "on_download_start": false, // 下载开始时发送通知
      "on_download_complete": false, // 下载完成时发送通知
      "on_download_pause": false, // 下载暂停时发送通知
      "on_download_error": false, // 下载出错时发送通知
      "live_progress": false, // 下载中持续更新进度消息
      "live_progress_interval_seconds": 5 // 进度更新间隔，单位秒；最小 5
    }
  },
  "forward": {
    "mode": "default", // 转发模式：default 优先官方转发，受保护内容自动降级 clone；clone 始终复制发送
    "target": "", // 默认转发目标的 ID 或用户名；留空表示收藏夹
    "listen": [], // 监听对象：频道/群/用户的 ID 或用户名，如 ["@channel", 123456]
    "listen_comments": true, // 监听频道关联讨论组中的评论消息
    "silent": false, // 静默转发，不触发通知
    "dedupe_ttl_seconds": 600, // 监听转发的消息/相册去重时间，单位秒
    "trigger_reactions": [] // 指定可触发转发的表情，如 ["👍", "🔥"]；留空表示不启用表情触发转发
  }
}
```

文件大小范围的左右边界均包含在内：`1 ~ 5` 仅下载 1MB 至 5MB 的文件，`0 ~ 5` 只限制最大值，`5 ~ 0` 只限制最小值，`0 ~ 0` 完全不限制。非法范围（例如 `5 ~ 2`）会提示并按 `0 ~ 0` 处理。

HTTP 下载代理兼容标准单 Range、`multipart/byteranges` 多 Range、HEAD 和 `If-Range` 断点续传语义，不依赖 aria2 专有行为。每次 Telegram `upload.getFile`（包括重试）才占用一个 DC 许可，请求结束后立即释放；向慢速 HTTP 客户端写入时不占用 DC 许可，也不维护跨请求的文件字节缓存。任意数量的外部下载器分片都会进入同一套按 DC、按文件 FIFO 且工作保守的调度：同一文件的 Range 优先使用尽可能多的空闲连接，当前文件没有待处理分片时后续文件可立即利用剩余连接。每个 DC 最多同时执行 `pool_size` 个 Telegram 文件请求，绝不会因为客户端增加 HTTP 连接或分片数而突破该上限。

HTTP Server 会把成功发送完毕的 GET Range 持久化到对应下载链接记录，并合并并行、重试和重叠分片；当已发送区间完整覆盖文件后，KV 管理会将其显示为“已下载（HTTP）”。HEAD、失败或中断的响应不会计入完成状态。

常用配置项及其说明：

| 配置项                    | 说明                                                                                |
| ---------------------- | --------------------------------------------------------------------------------- |
| `proxy`                | 代理地址，支持 `socks5://127.0.0.1:1080`、`socks5h://127.0.0.1:1080`、`http://127.0.0.1:10808`、`https://127.0.0.1:10808` |
| `proxy_username`       | 代理用户名；代理不需要认证时留空。如果 `proxy` 地址中已经包含认证信息，此项不会覆盖地址内的用户名 |
| `proxy_password`       | 代理密码；代理不需要认证时留空。如果 `proxy` 地址中已经包含认证信息，此项不会覆盖地址内的密码 |
| `namespace`            | 当前用户的数据空间；登录前填写用户名或在 Web 面板切换用户时自动更新，只允许英文字母                            |
| `download_dir`         | 下载目录模板，会拼接在下载根目录后；与 `filename` 使用同一组变量，`/` 或 `\` 分层，`&` 连接同层，例如 `G/Y&M` |
| `filename`             | 文件名模板；与 `download_dir` 使用同一组变量，例如 `P_S_F` 或 `G-I-F`；仍兼容原有 Go template 写法 |
| `filename_max_length`  | 最终文件名字节数上限（UTF-8 编码，含扩展名）；默认 `255`（文件系统上限），超长时优先缩短 `I`（保留头尾并以 `...` 代替中间内容）；若仍超限，对整体做兜底截断并保留扩展名 |
| `trigger_reactions`    | 触发下载的表情列表，如 `["👍", "🔥"]`；为空时任意表情都可以触发                                         |
| `include`              | 只下载指定扩展名，如 `["mp4", "mp3"]`                                                       |
| `exclude`              | 排除指定扩展名，如 `["png", "jpg"]`                                                        |
| `file_size_min_mb`     | 文件大小左边界，单位 MB；`0` 表示不限制最小值；旧版 `file_size_mb` 会自动迁移为此项               |
| `file_size_max_mb`     | 文件大小右边界，单位 MB；`0` 表示不限制最大值；两个非零边界均包含在下载范围内               |
| `limit`                | 同时下载的文件任务数量；默认 2 |
| `pool_size`            | 每个 Telegram DC 的连接池与 `upload.getFile` 并发上限；默认 8，设置为 0 或负数会恢复为 8；同时作为 aria2 的 `split` 和 `max-connection-per-server`，其他下载器即使请求更多 Range 也只会在服务端排队 |
| `ntp`                  | NTP 时间校准服务器；留空时启动会检测内置服务器并保存最快可用项，已填写时会先按 3 秒超时重试 3 次 |
| `http.address`         | tdl 下载代理监听地址；默认 `0.0.0.0`，修改后需要重启                                                         |
| `http.port`            | tdl 下载代理监听端口；默认 `22334`，修改后需要重启                                                         |
| `http.public_base_url` | aria2 访问 tdl 下载代理时使用的基础地址                                                         |
| `http.download_link_ttl_hours` | 下载链接有效期，单位小时；默认 24，设置为 0 时永久有效且不自动清理                                       |
| `webui.address`        | Web 管理面板监听地址；默认 `0.0.0.0`，修改后需要重启                                  |
| `webui.port`           | Web 管理面板监听端口；默认 `22335`，修改后需要重启                                  |
| `webui.username`       | Web 管理面板用户名                                                               |
| `webui.password`       | Web 管理面板密码；默认 `admin`，首次登录后请立刻修改                                                |
| `modules.bot`          | Telegram 机器人控制模块；关闭后不再接收机器人私聊命令                                             |
| `modules.watch`        | 监听下载模块；负责 Telegram 表情监听和任务提交                                    |
| `modules.http`         | 独立的 HTTP 下载代理模块；只负责启停 `/download` 文件流服务，关闭或重启它不会连带重启监听下载/aria2 自动化 |
| `modules.aria2`        | 独立的 aria2 管理模块；负责 RPC 连接、任务恢复和异常监控，不负责监听表情或提供 HTTP 文件流 |
| `modules.forward`      | 监听转发模块；监听配置的 Telegram 对象并转发新消息 |
| `downloader.mode`      | 下载器模式；`aria2` 使用外部 aria2，`internal` 使用 tdl 内部简易本地下载器                                  |
| `aria2.auto_download`  | 表情触发并生成临时 HTTP 链接后是否自动提交到 aria2；仅在 watch、aria2 模块均启用且下载器模式为 `aria2` 时生效 |
| `aria2.rpc_url`        | aria2 JSON-RPC 地址                                                                 |
| `bot.notify.on_download_start` | 下载开始时机器人发送通知消息；默认 `false` |
| `bot.notify.on_download_complete` | 下载完成时机器人发送通知消息；默认 `false` |
| `bot.notify.on_download_pause` | 下载暂停时机器人发送通知消息；默认 `false` |
| `bot.notify.on_download_error` | 下载出错时机器人发送通知消息；默认 `false` |
| `bot.notify.live_progress` | 开启后下载开始时发送一条进度消息，并按间隔持续编辑更新直到任务结束；默认 `false` |
| `bot.notify.live_progress_interval_seconds` | 进度消息刷新间隔，单位秒；最小 `5`，默认 `5` |
| `forward.mode`         | 转发模式；`default` 优先官方转发，受保护内容自动降级 `clone`，`clone` 始终复制发送 |
| `forward.target`       | 默认转发目标；机器人 `/forward` 未指定目标或表情触发转发时使用，留空表示收藏夹 |
| `forward.listen`       | 监听对象列表；频道/群/用户的 ID 或用户名，频道会尝试同步监听其关联评论区 |
| `forward.listen_comments` | 是否监听频道关联讨论组中的评论消息；账号需有权限访问该讨论组 |
| `forward.silent`       | 静默转发；开启后转发的消息不触发通知 |
| `forward.dedupe_ttl_seconds` | 监听转发的消息/相册去重时间，单位秒；默认 `600` |
| `forward.trigger_reactions` | 可触发转发的表情列表，如 `["👍", "🔥"]`；与 `watch` 一样支持表情触发，但留空表示不启用表情触发转发，仅自动转发监听对象的新消息 |

`download_dir` 和 `filename` 可用变量：

| 变量 | 含义 |
| --- | --- |
| `F` | 原始文件名，**不含扩展名**；系统会在最终文件名末尾自动追加与源文件一致的扩展名 |
| `I` | 触发下载的那条消息文字内容；只保留中文、英文字母、数字，超过 80 个字符时保留头尾，中间用 `...` 代替；当文件名超过 `filename_max_length` 字节时优先缩短此变量 |
| `G` | 聊天、群组或频道的对外显示名称 |
| `P` | 聊天、群组或频道 ID |
| `S` | 当前媒体消息 ID |
| `R` | 触发消息 ID |
| `A` | 相册/媒体组 ID，没有时为空 |
| `Y` | 年，例如 `2026` |
| `M` | 月，例如 `05` |
| `D` | 日，例如 `22` |

示例：

| 配置 | 效果 |
| --- | --- |
| `download_dir = "G/Y&M"` | 按群组/频道名称、年月分目录 |
| `filename = "P_S_F"` | 使用来源 ID、消息 ID、原文件名组成文件名，扩展名自动追加 |
| `filename = "G-I-F"` | 使用群组/频道名称、触发消息文字、原文件名组成文件名；若文件名与消息内容相同，`I` 自动省略 |

## 反馈
[加入电报](https://t.me/+mHQOJCcxV64xMDE1)  
或者  
[提issues](https://github.com/snakexgc/tdl/issues)  

## Star History
**如果项目好用，还请给一个star！**  

[![Star History Chart](https://api.star-history.com/chart?repos=snakexgc/tdl&type=date&legend=top-left)](https://www.star-history.com/?repos=snakexgc%2Ftdl&type=date&legend=top-left)

## 协议

AGPL-3.0 License
