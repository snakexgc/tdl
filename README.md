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
  "ntp": "", // NTP 服务器地址，如 `127.0.0.1`
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

- 常用启动命令

```PowerShell
# 使用配置文件中的代理和排除设置
.\tdl.exe watch

# 临时指定代理
.\tdl.exe watch --proxy http://127.0.0.1:10808

# 临时排除 png 和 jpg
.\tdl.exe watch -e png,jpg
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
📊 Overall Progress ... 35.0% [#######...............................] [3m12s; 5.2 MB/s]
22372 -> video.mp4 ... 80.0% [###########################.........] [2m30s; 3.1 MB/s]
22373 -> image.jpg ... 45.0% [##############......................] [1m15s; 2.8 MB/s]
```

- 第一行显示**所有文件的总体进度**
- 下面的行显示每个文件的独立进度

如果是相册（分组消息），回应其中任意一条，会自动下载**相册内的全部文件**。

按 `Ctrl+C` 停止监听。已下载的文件不受影响。

## 参数说明

### watch 命令参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-d, --dir` | `downloads` | 下载目录 |
| `--template` | `{{ .DialogID }}_{{ .MessageID }}_{{ filenamify .FileName }}` | 下载文件名模板 |
| `--skip-same` | `false` | 跳过同名且同大小的文件 |
| `--rewrite-ext` | `false` | 根据 MIME 类型重写文件扩展名 |
| `-i, --include` | `[]` | 只下载指定扩展名（逗号分隔） |
| `-e, --exclude` | `[]` | 排除指定扩展名（逗号分隔） |

### 全局参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-l, --limit` | `2` | 最大同时下载文件数 |
| `-t, --threads` | `4` | 每个文件的下载线程数 |
| `-n, --namespace` | `default` | Telegram 会话命名空间 |
| `--proxy` | - | 代理地址，格式：`protocol://user:pass@host:port` |
| `--debug` | `false` | 开启调试模式 |

### 示例

```bash
# 基本用法
tdl watch

# 5 个文件同时下载，每个文件 8 线程
tdl watch -l 5 -t 8

# 跳过已下载的文件
tdl watch --skip-same

# 自定义文件名模板
tdl watch --template "{{ .DialogID }}/{{ .FileName }}"

# 使用代理
tdl watch --proxy socks5://127.0.0.1:1080

# 只下载 mp4 和 mp3
tdl watch -i mp4,mp3

# 排除图片
tdl watch -e png,jpg,jpeg,gif
```

## 文件存储

- **配置文件**：`config.json`（与可执行程序同目录）
- **登录数据**：`.tdl/` 文件夹（与可执行程序同目录）
- **下载文件**：默认 `downloads/` 文件夹（与可执行程序同目录）

## 协议

AGPL-3.0 License
