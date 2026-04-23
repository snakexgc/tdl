## 快速开始

### 第 1 步：配置

程序首次运行会自动创建 `config.json` 配置文件，位于程序同目录。默认配置：

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
  "threads": 4, // 单文件下载线程数
  "limit": 2, // 最大同时下载文件数
  "pool_size": 8, // DC 下载池大小
  "delay": 0, // DC 下载延迟，单位秒
  "ntp": "", // NTP 服务器地址，如 "pool.ntp.org"
  "reconnect_timeout": 10, // 重连超时时间，单位秒
  "download_dir": "G/Y&M", // 下载目录模板，会拼接在 aria2 下载根目录后
  "include": [], // 只下载指定扩展名，如 `["mp4", "mp3"]`，与exclude互斥
  "exclude": ["png","jpg"], // 排除指定扩展名，如 `["png", "jpg"]`与include互斥
  "http": {
    "listen": "0.0.0.0:22334", // HTTP 监听地址，如 0.0.0.0:22334
    "public_base_url": "http://127.0.0.1:22334" // aria2 访问 tdl 下载代理时使用的基础地址
  },
  "aria2": {
    "rpc_url": "http://127.0.0.1:6800/jsonrpc", // aria2 JSON-RPC 地址
    "secret": "123", // aria2 密钥
    "dir": "", // aria2 下载目录
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
| `include`              | 只下载指定扩展名，如 `["mp4", "mp3"]`                                                       |
| `exclude`              | 排除指定扩展名，如 `["png", "jpg"]`                                                        |
| `limit`                | 最大同时下载文件数；启动 `tdl watch` 时会同步到 aria2 的 `max-concurrent-downloads`                 |
| `http.public_base_url` | aria2 访问 tdl 下载代理时使用的基础地址                                                         |
| `aria2.rpc_url`        | aria2 JSON-RPC 地址                                                                 |

如果 aria2 运行在 Docker、NAS、WSL 或另一台机器上，`http.public_base_url` 不能写 `127.0.0.1`，需要写 aria2 所在环境能访问到 tdl 的局域网地址。

`download_dir` 会和 aria2 下载根目录组合使用。若设置了 `aria2.dir`，tdl 会先尝试创建并校验该目录；若未设置，tdl 会从 aria2 的全局配置读取默认下载目录。例如 `download_dir` 为 `Y&M/I/G` 时，Windows 下可能得到 `D:\Download\202604\12345\群组名`，Linux 下可能得到 `/root/download/202604/12345/群组名`。

### 第 2 步：启动机器人

- 请先确保 aria2 已启动并开启 JSON-RPC。
- 直接运行程序
tdl 会连接到 Telegram 并在后台等待，你会看到：

```
🤖 Bot @dl_bot (ID: 123444) started
👀 Watching for reactions... Press Ctrl+C to stop
   HTTP listen: 0.0.0.0:22334
   Public base URL: http://127.0.0.1:22334
   aria2 RPC: http://127.0.0.1:6800/jsonrpc
   Output root: D:\downloads
   Download dir template: G/Y&M
   Max concurrent downloads: 2
⚠️ http.public_base_url uses loopback address 127.0.0.1; this only works when aria2 runs on the same machine and network namespace
🔄 Bot is running... Press Ctrl+C to stop
```

### 第 3 步：回表情

打开任意 Telegram 客户端（桌面、手机、网页），找到一条带媒体的消息（图片、视频、文件等），**给它添加任意表情回应**。

tdl 会立刻检测到这个回应，生成一个本地 HTTP 下载链接，并通过 aria2 RPC 提交下载任务。终端会显示：

```
🚀 Submitted to aria2: msg 22372 -> downloads/video.mp4
   URL: http://192.168.1.10:8080/download/abc123
   GID: 2089b05ecca3d829
```

如果是相册（分组消息），回应其中任意一条，会自动提交**全部文件**。

下载链接会按 Telegram 媒体 ID 生成稳定地址，并写入本地存储保留 24 小时；这段时间内即使 `tdl watch` 异常退出后重启，aria2 仍可继续访问原链接断点续传。若 Telegram 文件引用过期，tdl 会尝试从原消息刷新引用；超过 24 小时的链接会自动清理，避免 KV 持续增长。

按 `Ctrl+C` 停止监听。已下载的文件不受影响。

## 文件存储

- **配置文件**：`config.json`（与可执行程序同目录）
- **登录数据**：`.tdl/` 文件夹（与可执行程序同目录）
- **下载文件**：由 aria2 写入配置的目标目录

## 协议

AGPL-3.0 License
