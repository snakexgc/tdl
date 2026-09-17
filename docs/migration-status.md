# AUTOSAR 迁移验收状态

此表记录代码实际接入状态，不把创建目录或注册空组件视为完成。原计划的多账号阶段仍为 TODO。

| 范围 | 状态 | 剩余验收 |
| --- | --- | --- |
| 单 Go 模块、原 core 可见性 | 已完成 | 原 clone 转发依赖的下载/上传实现保留 |
| RTE 端口装配和生命周期 | 已实现 | 替换生产环境的 app/runtime 编排；动态启停/重启控制 |
| 事件与 Runnable | 已实现并测试；aria2 治理后台任务已纳入 Runnable 取消和等待 | 业务事件与其余周期任务全面接入 |
| filter.rules / naming.rules | 生产宿主统一持有生命周期和批量配置，监听器注入端口；独立入口保留自有宿主 | 随整体宿主迁移去除兼容配置适配 |
| trigger.messagelink | 校验已接入 | 消息获取、下载意图发布 |
| trigger.reaction | 匹配和去重已接入 | 监听连接、意图发布；独立配置控制入口 |
| update.self | 实现和测试已迁移，原入口已适配 | 主宿主配置、功能页归属 |
| taskhub / NvM | HTTP、本地、aria2、转发任务写入已统一；四个任务数据集已限制写入范围 | 统一执行器与状态转换；账号等其余数据集 |
| aria2 RPC | 控制请求和代理共用传输 | downloader.aria2 全组件迁移；AriaNg 页面归属 |
| 配置持久化 | 独立 secret 文件、八组件转换命令、过滤/命名及 Bot 权限/通知生产文件接入与 schema 配置页已实现并测试 | 其余组件主程序接入与配置映射、完全移除兼容配置 |
| account.telegram / tgauth | 凭据端口、配置、WebUI 脱敏、会话指纹与事务提交已接入 | 会话和连接唯一持有、登录端口、主宿主统一生命周期 |
| proxy.range / comif | 共享文件/DC 配额调度器及测试已迁入 BSW comif | Range 组件迁移、宿主统一资源所有权、配额配置更新 |
| downloader.local / downloader.aria2 | 公共提交端口及账号维度已接入 local/aria2，治理器退出等待已接入 | 完整 Executor 控制、选择与降级、任务报告、组件生命周期 |
| forwarder | 队列已由宿主实例化并注入 Bot/watch/WebUI，数据集纳入 taskhub | 业务端口、命令和 SWC 生命周期归属 |
| console.bot / notify.telegram | console 已接管菜单、私聊限制和权限；notify 接管分发、去重、进度编辑及超时清理；共用 Bot 宿主已接入组件配置文件和 Manager 配置页 | 命令业务处理器的完整端口迁移、业务通知事件接入、整体生命周期编排 |
| panel.webui / hmiif | 已提供按 Manifest 生成的通用组件配置页，现连接生产过滤/命名宿主 | 功能页与动态导航归属、其余组件接入、去除实现依赖；新页视觉验收 |
| 诊断、监控、发布与文档 | 部分完成 | Dem/Dcm/WdgM、构建参数单一来源、配置参考、迁移交付验收 |

优先继续处理生产宿主和端口契约，再迁移账号与下载链路。每次迁移保留旧数据兼容和业务回归，完成后再移除适配层。
