## 快速开始

### 第 1 步：配置

程序首次运行会自动创建 `config.json` 配置文件，位于程序同目录。默认配置：  

建议使用 vscode / notepad 进行配置(如果你需要修改触发表情的话)  

最小启动配置：

- `webui.password`：Web 管理面板登录密码。设置后，直接启动程序即可进入管理面板。

如果还需要 Telegram 机器人私聊控制，再配置：

- `bot.token`：Telegram 机器人 token。
- `bot.allowed_users`：允许控制机器人的 Telegram 用户 ID 列表。

可选项：

```json
{
  "proxy": "http://127.0.0.1:10808", // 代理地址，如 http://127.0.0.1:10808
  "namespace": "default", // 当前用户的数据空间；由 Web 登录或切换用户维护，只允许英文字母
  "debug": false, // 是否开启调试模式
  "pool_size": 8, // Telegram 连接池大小，也用于单文件分片下载并发
  "delay": 0, // 两个下载任务之间的等待时间，单位秒
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
    "listen": "127.0.0.1:22335", // Web 管理面板访问地址
    "username": "admin", // Web 管理面板用户名
    "password": "" // Web 管理面板密码；为空时不启动管理面板
  },
  "modules": {
    "bot": true, // Telegram 机器人控制模块
    "watch": true // 监听下载模块：表情监听、本地下载链接、aria2 提交
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
| `namespace`            | 当前用户的数据空间；登录前填写用户名或在 Web 面板切换用户时自动更新，只允许英文字母                            |
| `download_dir`         | 下载目录模板，会拼接在 aria2 下载根目录后；支持 `G` 名称、`I` ID、`Y` 年、`M` 月、`D` 日，`/` 或 `\` 分层，`&` 连接同层 |
| `trigger_reactions`    | 触发下载的表情列表，如 `["👍", "🔥"]`；为空时任意表情都可以触发                                         |
| `include`              | 只下载指定扩展名，如 `["mp4", "mp3"]`                                                       |
| `exclude`              | 排除指定扩展名，如 `["png", "jpg"]`                                                        |
| `pool_size`            | Telegram 连接池大小，也用于单个文件的分片下载并发；默认 8 适合多数场景 |
| `http.public_base_url` | aria2 访问 tdl 下载代理时使用的基础地址                                                         |
| `http.download_link_ttl_hours` | 下载链接有效期，单位小时；默认 24，设置为 0 时永久有效且不自动清理                                       |
| `http.buffer.mode`     | HTTP 下载缓冲模式；`memory` 会在 tdl 内存中预读分片，`off` 保持旧的顺序流式行为                           |
| `http.buffer.size_mb`  | `memory` 模式下每个活跃文件的共享缓冲上限；默认 64，内存紧张可设 32，高带宽可设 128                         |
| `webui.listen`         | Web 管理面板访问地址；设置 `webui.password` 后程序即可启动管理面板                                  |
| `webui.username`       | Web 管理面板用户名                                                               |
| `webui.password`       | Web 管理面板密码；为空时不启动管理面板                                                |
| `modules.bot`          | Telegram 机器人控制模块；关闭后不再接收机器人私聊命令                                             |
| `modules.watch`        | 监听下载模块；包含 Telegram 表情监听、本地下载链接和 aria2 提交                                    |
| `aria2.rpc_url`        | aria2 JSON-RPC 地址                                                                 |

本地 KV 和 Telegram 登录数据固定使用 bolt 存储，保存在程序同目录的 `.tdl/` 文件夹中，不需要在 `config.json` 中配置。

如果 aria2 运行在 Docker、NAS、WSL 或另一台机器上，`http.public_base_url` 不能写 `127.0.0.1`，需要写 aria2 所在环境能访问到 tdl 的局域网地址。

`download_dir` 会和 aria2 下载根目录组合使用。若设置了 `aria2.dir`，tdl 会先尝试创建并校验该目录；若未设置，tdl 会从 aria2 的全局配置读取默认下载目录。例如 `download_dir` 为 `Y&M/I/G` 时，Windows 下可能得到 `D:\Download\202604\12345\群组名`，Linux 下可能得到 `/root/download/202604/12345/群组名`。

tdl 对 aria2 暴露单条 HTTP 下载连接，文件内部仍会由 tdl 使用 `pool_size` 并发抓取 Telegram 分片。监听下载模块同一时间只处理 1 个文件，避免多个大文件同时占用 Telegram 连接导致 EOF 或频繁重试。

`http.buffer.mode=memory` 会让当前活跃文件共享一块有上限的内存预读缓冲，用来降低 HTTP 顺序写出对 Telegram 分片抓取的反压。默认 `http.buffer.size_mb=64`；如果机器内存较小可设为 32，高带宽或 aria2 与 tdl 同机时可尝试 128。设置为 `off` 可回到旧的顺序流式行为。

`webui.password` 设置后，直接启动程序会打开 Web 管理面板，例如访问 `http://127.0.0.1:22335`。面板包含下载管理、KV 链接管理、Telegram 用户登录、模块管理、配置设置和检查更新。下载管理内置 AriaNg，并通过 tdl 服务端代理读取 `aria2.rpc_url` / `aria2.secret`，通常不需要在浏览器里单独配置 aria2 RPC。

Web 管理面板的“模块管理”可以在运行时启用或关闭功能。`监听下载` 是一个整体模块，包含 Telegram 表情监听、本地 HTTP 下载链接和 aria2 任务提交；关闭该模块会停止这组功能，Web 管理面板仍会保持运行。

Telegram 用户登录前需要先在 Web 面板填写一个用户名，tdl 会把这个用户名作为 `namespace` 保存对应的登录数据。用户名只允许英文字母，例如 `alice`。在“用户管理”里可以从 `.tdl` 目录中已有登录态的用户列表切换或删除用户；切换完成后程序会自动重启，让 WebUI、机器人和监听下载都加载到新的用户数据空间。

Web 管理面板的“检查更新”页面会对比本地版本和 GitHub 最新 Release，并可下载更新后自动重启。若配置了 `proxy`，检查更新和下载更新都会走该代理。机器人也支持 `/update_tdl` 检查更新，按提示发送 `/update_tdl confirm` 后会自动下载、替换并重启当前程序。

### 第 2 步：启动程序

- 请先确保 aria2 已启动并开启 JSON-RPC。
- 直接运行程序。
- 在 Windows 上可以直接双击启动，控制台窗口会保留用于查看运行日志。重启和更新后的再启动也会继续使用当前控制台输出。
- 如果只配置了 `webui`，程序也可以正常启动管理面板；bot 和监听下载可以稍后在“模块管理”中启用。

当机器人和监听下载模块都已配置并启用时，你会看到类似输出：

```
🤖 Bot @dl_bot (ID: 1234666) started
👀 Watching for reactions... Press Ctrl+C to stop
   HTTP listen: 0.0.0.0:22334
   Public base URL: http://127.0.0.1:22334
   aria2 RPC: http://127.0.0.1:6800/jsonrpc
   Output root: D:\downloads
   Download dir template: G/Y&M
   Telegram pool / per-file streams: 8
   Download link TTL: 24h
   HTTP buffer: memory (64 MiB per active file)
   Max concurrent downloads: 1
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

## 反馈
[加入电报](https://t.me/+mHQOJCcxV64xMDE1)  

或者  

[提issues](https://github.com/snakexgc/tdl/issues)  

## 协议

AGPL-3.0 License
