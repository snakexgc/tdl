
## 快速开始

### 第 1 步：登录
参考官方文档进行登录即可
https://docs.iyear.me/tdl/zh/getting-started/quick-start/#login

### 第 2 步：启动监听
- 直接启动
```bash
tdl watch
```

- 常用启动命令
```
.\tdl.exe watch --proxy http://127.0.0.1:10808 -e png,jpg
```
解释：
1. 使用http://127.0.0.1:10808作为代理
2. 排除png和jpg文件

tdl 会连接到 Telegram 并在后台等待，你会看到：
```
👀 Watching for reactions... Press Ctrl+C to stop
   Download dir: /path/to/downloads
   Max concurrent files: 2
   Threads per file: 4
```

### 第 3 步：回表情，自动下载

打开任意 Telegram 客户端（桌面、手机、网页），找到一条带媒体的消息（图片、视频、文件等），**给它添加任意表情回应**。

tdl 会立刻检测到这个回应，并自动开始下载该文件。终端会显示实时进度条：

```
[download] 45.2% ██████████░░░░░░░░░░░░ 12.3 MB/s | video.mp4 | ETA: 00:08
```

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

### 全局参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-l, --limit` | `2` | 最大同时下载文件数 |
| `-t, --threads` | `4` | 每个文件的下载线程数 |
| `-n, --namespace` | `default` | Telegram 会话命名空间 |
| `--proxy` | | 代理地址，格式：`protocol://user:pass@host:port` |
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
```

## 协议

AGPL-3.0 License
