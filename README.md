## 快速开始

**部署教程已出炉，可以根据教程进行部署：**  

https://snakexgc.github.io/2026/05/13/TDL_Docker_Deployment/  

当前教程还在不断完善，遇到问题欢迎在issues中或者电报群反馈！

### Json配置细明

```json
{
  "proxy": "socks5://127.0.0.1:1080", // 代理地址，如 socks5://127.0.0.1:1080 或 http://127.0.0.1:10808
  "proxy_username": "", // 代理用户名；没有认证时留空
  "proxy_password": "", // 代理密码；没有认证时留空
  "namespace": "default", // 当前用户的数据空间；由 Web 登录或切换用户维护，只允许英文字母
  "debug": false, // 是否开启调试模式
  "pool_size": 8, // Telegram 连接池大小，也用于单文件分片下载并发
  "delay": 0, // 两个下载任务之间的等待时间，单位秒
  "ntp": "", // NTP 服务器地址；留空时启动会自动选择最快的内置服务器并写回此项
  "reconnect_timeout": 3, // 重连超时时间，单位秒
  "download_dir": "G\\Y&M", // 下载目录模板，会拼接在下载根目录后
  "trigger_reactions": [], // 指定触发下载的表情，如 ["👍", "🔥"]；为空时任意表情都可以触发
  "include": [], // 只下载指定扩展名，如 `["mp4", "mp3"]`，与exclude互斥
  "exclude": ["png","jpg"], // 排除指定扩展名，如 `["png", "jpg"]`与include互斥
  "file_size_mb": 0, // 文件大小过滤，单位 MB；0 表示不限制，小于该大小的文件会在后缀过滤后跳过
  "http": {
    "address": "0.0.0.0", // HTTP 下载代理监听地址
    "port": 22334, // HTTP 下载代理监听端口
    "public_base_url": "http://127.0.0.1:22334", // aria2 访问 tdl 下载代理时使用的基础地址
    "download_link_ttl_hours": 24, // 下载链接有效期，单位小时；设置为 0 时永久有效且不自动清理
    "buffer": {
      "mode": "memory", // HTTP 下载缓冲模式：memory 或 off
      "size_mb": 64 // 每个活跃文件的内存缓冲上限，单位 MiB
    }
  },
  "webui": {
    "address": "0.0.0.0", // Web 管理面板监听地址
    "port": 22335, // Web 管理面板监听端口
    "username": "admin", // Web 管理面板用户名，首次登录后请立刻修改
    "password": "admin" // Web 管理面板密码，首次登录后请立刻修改
  },
  "modules": {
    "bot": true, // Telegram 机器人控制模块
    "watch": true // 监听下载模块：表情监听、下载链接和任务提交
  },
  "downloader": {
    "mode": "aria2" // 下载器模式：aria2 或 internal
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

常用配置项及其说明：

| 配置项                    | 说明                                                                                |
| ---------------------- | --------------------------------------------------------------------------------- |
| `proxy`                | 代理地址，支持 `socks5://127.0.0.1:1080`、`socks5h://127.0.0.1:1080`、`http://127.0.0.1:10808`、`https://127.0.0.1:10808` |
| `proxy_username`       | 代理用户名；代理不需要认证时留空。如果 `proxy` 地址中已经包含认证信息，此项不会覆盖地址内的用户名 |
| `proxy_password`       | 代理密码；代理不需要认证时留空。如果 `proxy` 地址中已经包含认证信息，此项不会覆盖地址内的密码 |
| `namespace`            | 当前用户的数据空间；登录前填写用户名或在 Web 面板切换用户时自动更新，只允许英文字母                            |
| `download_dir`         | 下载目录模板，会拼接在下载根目录后；支持 `G` 名称、`I` ID、`Y` 年、`M` 月、`D` 日，`/` 或 `\` 分层，`&` 连接同层 |
| `trigger_reactions`    | 触发下载的表情列表，如 `["👍", "🔥"]`；为空时任意表情都可以触发                                         |
| `include`              | 只下载指定扩展名，如 `["mp4", "mp3"]`                                                       |
| `exclude`              | 排除指定扩展名，如 `["png", "jpg"]`                                                        |
| `file_size_mb`         | 文件大小过滤，单位 MB；`0` 表示不限制，小于该大小的文件会在 `include`/`exclude` 后跳过               |
| `pool_size`            | Telegram 连接池大小，也用于单个文件的分片下载并发；默认 8 适合多数场景 |
| `ntp`                  | NTP 时间校准服务器；留空时启动会检测内置服务器并保存最快可用项，已填写时会先按 3 秒超时重试 3 次 |
| `http.address`         | tdl 下载代理监听地址；默认 `0.0.0.0`，修改后需要重启                                                         |
| `http.port`            | tdl 下载代理监听端口；默认 `22334`，修改后需要重启                                                         |
| `http.public_base_url` | aria2 访问 tdl 下载代理时使用的基础地址                                                         |
| `http.download_link_ttl_hours` | 下载链接有效期，单位小时；默认 24，设置为 0 时永久有效且不自动清理                                       |
| `http.buffer.mode`     | HTTP 下载缓冲模式；`memory` 会在 tdl 内存中预读分片，`off` 保持旧的顺序流式行为                           |
| `http.buffer.size_mb`  | `memory` 模式下每个活跃文件的共享缓冲上限；默认 64，内存紧张可设 32，高带宽可设 128                         |
| `webui.address`        | Web 管理面板监听地址；默认 `0.0.0.0`，修改后需要重启                                  |
| `webui.port`           | Web 管理面板监听端口；默认 `22335`，修改后需要重启                                  |
| `webui.username`       | Web 管理面板用户名                                                               |
| `webui.password`       | Web 管理面板密码；默认 `admin`，首次登录后请立刻修改                                                |
| `modules.bot`          | Telegram 机器人控制模块；关闭后不再接收机器人私聊命令                                             |
| `modules.watch`        | 监听下载模块；包含 Telegram 表情监听、下载链接和任务提交                                    |
| `downloader.mode`      | 下载器模式；`aria2` 使用外部 aria2，`internal` 使用 tdl 内部简易本地下载器                                  |
| `aria2.rpc_url`        | aria2 JSON-RPC 地址                                                                 |

## 反馈
[加入电报](https://t.me/+mHQOJCcxV64xMDE1)  
或者  
[提issues](https://github.com/snakexgc/tdl/issues)  

## Star History

<a href="https://www.star-history.com/?repos=snakexgc%2Ftdl&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/chart?repos=snakexgc/tdl&type=date&theme=dark&legend=top-left" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/chart?repos=snakexgc/tdl&type=date&legend=top-left" />
   <img alt="Star History Chart" src="https://api.star-history.com/chart?repos=snakexgc/tdl&type=date&legend=top-left" />
 </picture>
</a>

## 协议

AGPL-3.0 License
