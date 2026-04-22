## 快速开始

### 第 1 步：登录

参考官方文档进行登录即可  

<https://docs.iyear.me/tdl/zh/getting-started/quick-start/#login>

### 第 2 步：配置（可选）

程序首次运行会自动创建 `config.json` 配置文件，位于程序同目录。默认配置：

```json
{
  "storage": { "type": "bolt", "path": "" }, // 一般默认，除非你知道你在做什么
  "proxy": "", // 代理地址，如 `http://127.0.0.1:10808`
  "namespace": "default", // 一般默认，除非你知道你在做什么`
  "debug": false, // 是否开启调试模式
  "threads": 4, // 每个文件的下载线程数
  "limit": 2, // 最大同时下载文件数
  "pool_size": 8, // 下载池大小
  "delay": 0, // 下载延迟，单位秒
  "ntp": "", // NTP 服务器地址，如 `pool.ntp.org`
  "reconnect_timeout": 300, // 重连超时时间，单位秒，默认300秒
  "download_dir": "downloads", // 默认下载目录
  "include": [], // 只下载指定扩展名，如 `["mp4", "mp3"]`与下面的exclude互斥
  "exclude": [], // 排除指定扩展名，如 `["png", "jpg"]`与上面的include互斥
  "http": {
    "listen": "0.0.0.0:8080", // 本地 HTTP 下载代理监听地址
    "public_base_url": "http://192.168.1.10:8080", // aria2 可访问到 tdl 的地址
  },
  "aria2": {
    "rpc_url": "http://127.0.0.1:6800/jsonrpc", // aria2 JSON-RPC 地址
    "secret": "", // aria2 RPC secret
    "dir": "", // 留空时使用 aria2 自身默认下载目录
    "timeout_seconds": 30 // RPC 请求超时
  }
}
```

常用配置项：

| 配置项 | 说明 |
|-------|------|
| `proxy` | 代理地址，如 `http://127.0.0.1:10808` |
| `download_dir` | 默认下载目录 |
| `include` | 只下载指定扩展名，如 `["mp4", "mp3"]` |
| `exclude` | 排除指定扩展名，如 `["png", "jpg"]` |
| `limit` | 最大同时提交任务数 |
| `http.public_base_url` | aria2 访问 tdl 下载代理时使用的基础地址 |
| `aria2.rpc_url` | aria2 JSON-RPC 地址 |

如果 aria2 运行在 Docker、NAS、WSL 或另一台机器上，`http.public_base_url` 不能写 `127.0.0.1`，需要写 aria2 所在环境能访问到 tdl 的局域网地址。

### 第 3 步：启动监听

- 请先确保 aria2 已启动并开启 JSON-RPC。
- 直接启动

```bash
tdl watch
```
tdl 会连接到 Telegram 并在后台等待，你会看到：

```
👀 Watching for reactions... Press Ctrl+C to stop
   HTTP listen: 0.0.0.0:8080
   Public base URL: http://192.168.1.10:8080
   aria2 RPC: http://127.0.0.1:6800/jsonrpc
   Output dir: (aria2 default)
   Max concurrent submissions: 2
```

### 第 4 步：回表情，自动提交到 aria2

打开任意 Telegram 客户端（桌面、手机、网页），找到一条带媒体的消息（图片、视频、文件等），**给它添加任意表情回应**。

tdl 会立刻检测到这个回应，生成一个本地 HTTP 下载链接，并通过 aria2 RPC 提交下载任务。终端会显示：

```
🚀 Submitted to aria2: msg 22372 -> downloads/video.mp4
   URL: http://192.168.1.10:8080/download/abc123
   GID: 2089b05ecca3d829
```

如果是相册（分组消息），回应其中任意一条，会自动提交**全部文件**。

下载链接会按 Telegram 媒体 ID 生成稳定地址，并写入本地存储；使用同一 namespace 和登录数据重新启动 `tdl watch` 后，已生成的链接仍可继续访问。

按 `Ctrl+C` 停止监听。已下载的文件不受影响。

## 文件存储

- **配置文件**：`config.json`（与可执行程序同目录）
- **登录数据**：`.tdl/` 文件夹（与可执行程序同目录）
- **下载文件**：由 aria2 写入配置的目标目录

## 协议

AGPL-3.0 License
