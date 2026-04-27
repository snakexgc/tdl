## 快速开始

### 第 1 步：配置

程序首次运行会自动创建 `config.json` 配置文件，位于程序同目录。默认配置：  

建议使用 vscode / notepad 进行配置(如果你需要修改触发表情的话)  

必须配置的项：

- `bot.token`：Telegram 机器人 token
- `bot.allowed_users`：允许的用户 ID 列表

可选项：

```json
{
  "storage": {
    "path": "",
    "type": "bolt"
  }, //默认，不要修改
  "proxy": "http://127.0.0.1:10808", // 代理地址，如 http://127.0.0.1:10808
  "namespace": "default", // 默认命名空间，不要修改
  "debug": false, // 是否开启调试模式
  "threads": 4, // 服务端限制：单个文件的总并发预算；Range 请求和服务端后台抓取都会共享这 n 个线程
  "limit": 2, // 服务端限制：同一时间最多同时对外提供 n 个文件下载
  "pool_size": 8, // DC 下载池大小
  "delay": 0, // DC 下载延迟，单位秒
  "ntp": "", // NTP 服务器地址，如 "pool.ntp.org"
  "reconnect_timeout": 10, // 重连超时时间，单位秒
  "download_dir": "G/Y&M", // 下载目录模板，会拼接在 aria2 下载根目录后
  "trigger_reactions": [], // 指定触发下载的表情，如 ["👍", "🔥"]；为空时任意表情都可以触发
  "include": [], // 只下载指定扩展名，如 `["mp4", "mp3"]`，与exclude互斥
  "exclude": ["png","jpg"], // 排除指定扩展名，如 `["png", "jpg"]`与include互斥
  "http": {
    "listen": "0.0.0.0:22334", // HTTP 监听地址，如 0.0.0.0:22334
    "public_base_url": "http://127.0.0.1:22334", // aria2 访问 tdl 下载代理时使用的基础地址
    "download_link_ttl_hours": 24, // 下载链接有效期，单位小时；设置为 0 时永久有效且不自动清理
    "buffer": {
      "mode": "memory", // HTTP 下载缓冲模式：memory 或 off
      "size_mb": 64 // 每个活跃文件的内存缓冲上限，单位 MiB
    }
  },
  "webui": {
    "listen": "127.0.0.1:22335", // Web 管理面板监听地址；设置 password 后随 bot 启动
    "username": "admin", // Web 管理面板用户名
    "password": "" // Web 管理面板密码；为空时不启动管理面板
  },
  "aria2": {
    "rpc_url": "http://127.0.0.1:6800/jsonrpc", // aria2 JSON-RPC 地址
    "secret": "123", // aria2 密钥
    "dir": "", // aria2 下载目录，注意区分操作系统 \ 需要转义为 \\ 
    "timeout_seconds": 30 // aria2 超时时间，单位秒
  },
  "bot": {
    "token": "55555555:xxxxxx", // Telegram 机器人 token
    "allowed_users": [123456] // 允许的用户 ID 列表
  }
}
```

常用配置项：

| 配置项                    | 说明                                                                                |
| ---------------------- | --------------------------------------------------------------------------------- |
| `proxy`                | 代理地址，如 `http://127.0.0.1:10808`                                                   |
| `download_dir`         | 下载目录模板，会拼接在 aria2 下载根目录后；支持 `G` 名称、`I` ID、`Y` 年、`M` 月、`D` 日，`/` 或 `\` 分层，`&` 连接同层 |
| `trigger_reactions`    | 触发下载的表情列表，如 `["👍", "🔥"]`；为空时任意表情都可以触发                                         |
| `include`              | 只下载指定扩展名，如 `["mp4", "mp3"]`                                                       |
| `exclude`              | 排除指定扩展名，如 `["png", "jpg"]`                                                        |
| `threads`              | 服务端限制单个文件的总并发预算；同文件的 Range 请求和 tdl 后台抓取 worker 会共享这份预算，aria2 提交任务时也会同步把这个值作为单文件连接数提示 |
| `limit`                | 服务端限制最大同时下载文件数；启动 `tdl watch` 时也会同步到 aria2 的 `max-concurrent-downloads`            |
| `http.public_base_url` | aria2 访问 tdl 下载代理时使用的基础地址                                                         |
| `http.download_link_ttl_hours` | 下载链接有效期，单位小时；默认 24，设置为 0 时永久有效且不自动清理                                       |
| `http.buffer.mode`     | HTTP 下载缓冲模式；`memory` 会在 tdl 内存中预读分片，`off` 保持旧的顺序流式行为                           |
| `http.buffer.size_mb`  | `memory` 模式下每个活跃文件的共享缓冲上限；默认 64，内存紧张可设 32，高带宽可设 128                         |
| `webui.listen`         | Web 管理面板监听地址；设置 `webui.password` 后随 `tdl bot` 启动                                  |
| `webui.username`       | Web 管理面板 Basic Auth 用户名                                                               |
| `webui.password`       | Web 管理面板 Basic Auth 密码；为空时不启动管理面板                                                |
| `aria2.rpc_url`        | aria2 JSON-RPC 地址                                                                 |

如果 aria2 运行在 Docker、NAS、WSL 或另一台机器上，`http.public_base_url` 不能写 `127.0.0.1`，需要写 aria2 所在环境能访问到 tdl 的局域网地址。

`download_dir` 会和 aria2 下载根目录组合使用。若设置了 `aria2.dir`，tdl 会先尝试创建并校验该目录；若未设置，tdl 会从 aria2 的全局配置读取默认下载目录。例如 `download_dir` 为 `Y&M/I/G` 时，Windows 下可能得到 `D:\Download\202604\12345\群组名`，Linux 下可能得到 `/root/download/202604/12345/群组名`。

`threads` 和 `limit` 现在由 tdl 的 HTTP 服务端强制执行，而不只是“建议下载器这么做”。例如设置 `threads=4`、`limit=2` 时：

- 任意客户端同时请求 3 个不同文件时，服务端最多只会真正放行其中 2 个文件开始下载
- 任意客户端对同一个文件发起 64 个 Range 请求时，服务端最多只会同时给这个文件分配 4 个并发 worker；多余请求会在服务端排队等待

`http.buffer.mode=memory` 会让同一个活跃文件共享一块有上限的内存预读缓冲，用来降低 HTTP 顺序写出对 Telegram 分片抓取的反压。默认 `http.buffer.size_mb=64`，总内存预算约为 `limit * size_mb`，再加少量正在抓取的分片内存；如果机器内存较小可设为 32，高带宽或 aria2 与 tdl 同机时可尝试 128。设置为 `off` 可回到旧的顺序流式行为。

`webui.password` 设置后，`tdl bot` 会启动 Web 管理面板，例如访问 `http://127.0.0.1:22335`。面板使用 Basic Auth 鉴权，包含下载管理、KV 链接管理、Telegram 用户状态检查和表单化配置设置。下载管理内置 AriaNg，并通过 tdl 服务端代理读取 `aria2.rpc_url` / `aria2.secret`，通常不需要在浏览器里单独配置 aria2 RPC。

Web 管理面板的“检查更新”页面会对比本地版本和 GitHub 最新 Release，并可下载更新后自动重启。若配置了 `proxy`，检查更新和下载更新都会走该代理。机器人也支持 `/update_tdl` 检查更新，按提示发送 `/update_tdl confirm` 后会自动下载、替换并重启当前程序。

### 第 2 步：启动机器人

- 请先确保 aria2 已启动并开启 JSON-RPC。
- 直接运行程序
- 程序默认启动 bot 模式；在 Windows 上可以直接双击启动，控制台窗口会保留用于查看运行日志。重启和更新后的再启动也会继续使用当前控制台输出。
tdl 会连接到 Telegram 并在后台等待，你会看到：

```
🤖 Bot @dl_bot (ID: 1234666) started
👀 Watching for reactions... Press Ctrl+C to stop
   HTTP listen: 0.0.0.0:22334
   Public base URL: http://127.0.0.1:22334
   aria2 RPC: http://127.0.0.1:6800/jsonrpc
   Output root: D:\downloads
   Download dir template: G/Y&M
   Per-file HTTP streams: 4
   Download link TTL: 24h
   HTTP buffer: memory (64 MiB per active file)
   Max concurrent downloads: 2
⚠️ http.public_base_url uses loopback address 127.0.0.1; this only works when aria2 runs on the same machine and network namespace
🔄 Bot is running... Press Ctrl+C to stop
```

### 第 3 步：回表情

打开任意 Telegram 客户端（桌面、手机、网页），找到一条带媒体的消息（图片、视频、文件等），**给它添加表情回应**。如果 `trigger_reactions` 为空，任意表情都可以触发；如果配置了表情列表，只有添加列表中的表情才会触发下载。

tdl 会立刻检测到这个回应，生成一个本地 HTTP 下载链接，并通过 aria2 RPC 提交下载任务。终端会显示：

```
🚀 Submitted to aria2: msg 22372 -> downloads/video.mp4
   URL: http://192.168.1.10:8080/download/abc123
   GID: 2089b05ecca3d829
```

如果是相册（分组消息），回应其中任意一条，会自动提交**全部文件**。

下载链接会按 Telegram 媒体 ID 生成稳定地址，并写入本地存储。默认保留 24 小时；这段时间内即使 `tdl watch` 异常退出后重启，aria2 仍可继续访问原链接断点续传。若 Telegram 文件引用过期，tdl 会尝试从原消息刷新引用；超过 `http.download_link_ttl_hours` 的链接会自动清理，避免 KV 持续增长。将 `http.download_link_ttl_hours` 设置为 `0` 时，链接永久有效且不会自动清理。

按 `Ctrl+C` 停止监听。已下载的文件不受影响。

## 文件存储

- **配置文件**：`config.json`（与可执行程序同目录）
- **登录数据**：`.tdl/` 文件夹（与可执行程序同目录）
- **下载文件**：由 aria2 写入配置的目标目录

## 反馈
[加入电报](https://t.me/+mHQOJCcxV64xMDE1)  

或者[提issues](https://github.com/snakexgc/tdl/issues)  

## 协议

AGPL-3.0 License
