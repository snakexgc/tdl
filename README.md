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
  "exclude": [] // 排除指定扩展名，如 `["png", "jpg"]`与上面的include互斥
}
```

常用配置项：

| 配置项 | 说明 |
|-------|------|
| `proxy` | 代理地址，如 `http://127.0.0.1:10808` |
| `download_dir` | 默认下载目录 |
| `include` | 只下载指定扩展名，如 `["mp4", "mp3"]` |
| `exclude` | 排除指定扩展名，如 `["png", "jpg"]` |
| `threads` | 每个文件的下载线程数 |
| `limit` | 最大同时下载文件数 |

### 第 3 步：启动监听

- 直接启动

```bash
tdl watch
```
tdl 会连接到 Telegram 并在后台等待，你会看到：

```
👀 Watching for reactions... Press Ctrl+C to stop
   Download dir: downloads
   Max concurrent files: 2
   Threads per file: 4
```

### 第 4 步：回表情，自动下载

打开任意 Telegram 客户端（桌面、手机、网页），找到一条带媒体的消息（图片、视频、文件等），**给它添加任意表情回应**。

tdl 会立刻检测到这个回应，并自动开始下载该文件。终端会显示实时进度条：

```
22372 -> video.mp4 ... 80.0% [###########################.........] [2m30s; 3.1 MB/s]
22373 -> image.jpg ... 45.0% [##############......................] [1m15s; 2.8 MB/s]
```

如果是相册（分组消息），回应其中任意一条，会自动下载**全部文件**。

按 `Ctrl+C` 停止监听。已下载的文件不受影响。

## 文件存储

- **配置文件**：`config.json`（与可执行程序同目录）
- **登录数据**：`.tdl/` 文件夹（与可执行程序同目录）
- **下载文件**：默认 `downloads/` 文件夹（与可执行程序同目录）

## 协议

AGPL-3.0 License
