# tdl 插件化重建计划（AUTOSAR 架构）

> 把一个「大 config + 800 行编排函数 + 六个互相知晓的模块」的 tdl，重建为 **AUTOSAR 分层的插件框架**：主进程只做框架，一切功能皆 SWC（软件组件），配置按 ECUC 参数分散，每个 SWC 自带功能页与配置页。
>
> 编写时间：2026-09-16　基线：`master`（含未提交的依赖升级修改）
> 配套文档：`.workbuddy/output/tdl-整体分析报告.html`（现状与风险）、`.workbuddy/output/tdl-插件化重构方案.html`（方案设计）

---

## 目录

- [0. 文档说明与决策记录](#0-文档说明与决策记录)
- [1. 设计理念与可验收原则](#1-设计理念与可验收原则)
- [2. 现状摘要](#2-现状摘要)
- [3. AUTOSAR 架构与目录结构](#3-autosar-架构与目录结构)
- [4. 配置（ECUC）设计](#4-配置ecuc设计)
- [5. 存储（NvM）设计](#5-存储nvm设计)
- [6. 关键设计专题](#6-关键设计专题)
- [7. 文件级迁移映射](#7-文件级迁移映射)
- [8. 实施计划](#8-实施计划)
- [9. 工程规范与 CI 强制规则](#9-工程规范与-ci-强制规则)
- [10. 风险与应对](#10-风险与应对)
- [11. 附录](#11-附录)

---

## 0. 文档说明与决策记录

### 0.1 目的

解决一个具体矛盾：**tdl 的既有能力值得保留，但它的组织方式已经无法承载新功能。**

现状是「一个大 config + 一个 800 行编排函数 + 六个互相知道对方存在的模块」。每加一个功能要动五处：配置结构体、`Validate`、前端表单、`runtime.Manager.ApplyConfig`、模块状态拼装函数。本计划的目标是让「加一个功能」变成**只新增一个 SWC 目录 + 一段 Manifest 声明**。

### 0.2 术语（AUTOSAR 对齐）

| 术语 | AUTOSAR 概念 | 在本项目中的含义 | tdl 现状对应物 |
| --- | --- | --- | --- |
| SWC | Software Component | 功能组件，即「插件」。每个 SWC 是一个目录 | `app/watch`、`app/aria2` 等模块 |
| Application Layer | 应用层 | 承载 SWC 的目录：`application/` | `app/` |
| RTE | Runtime Environment | 端口装配、事件路由、参数分发、Runnable 调度 | `app/runtime/manager.go` |
| BSW | Basic Software | 内核：Services / ECUAL / MCAL 三层 | `pkg/*` + `core/*` 的部分 |
| Services | 服务层 | 有状态、被多处使用的系统服务 | 散落在 `runtime`、`pkg/kv`、`app/http/metrics.go` |
| ECUAL | ECU 抽象层 | 把底层能力统一成接口（HTTP、存储、网络、HMI） | `app/http` 的服务器部分、`pkg/kv` |
| MCAL | 微控制器抽象层 | 最底层资源访问（协议传输、文件读写） | `core/tclient`、`core/dcpool`、bbolt 访问 |
| CDD | Complex Device Driver | 跨层访问的特殊组件 | 无对应物（任务存储分散在 4 个包） |
| Port | Port Interface | SWC 之间的通信契约（Go 接口） | `Options` 结构体字段、鸭子类型接口 |
| Connector | Connector | 端口之间的连接关系 | 硬编码在 `NewManager` 的代码顺序里 |
| Runnable | Runnable Entity | SWC 的可执行单元（周期 / 事件触发） | 各模块自写的 goroutine |
| ECUC | ECU Configuration | 配置参数容器，按模块组织 | 一个扁平的 `config.json` |
| ARXML | ECU Extract | 组件与连接描述文件 | 缺失 |
| NvM | Non-Volatile Memory | 非易失存储服务 | `pkg/kv` |
| Dem / Dcm | Diagnostic Event / Communication | 诊断事件与诊断读取（健康检查） | `app/http/metrics.go` 的部分 |
| WdgM | Watchdog Manager | 存活监控 | 无对应物 |

### 0.3 已确定的 6 项决策

| # | 决策 | 结论 |
| --- | --- | --- |
| 1 | 本地下载器 | **保留**。原「内部下载器」重命名为**本地下载器**，SWC ID 为 `downloader.local`。tdl 的 `downloader.mode = internal` 取值改为 `local` |
| 2 | 多账号 | **保留概念，标为 TODO**。`namespace` 概念被 `account` 取代。端口签名与数据结构**从一开始就带 account 维度**，但只实现单账号（详见 §6.1） |
| 3 | 容器内自更新 | **删除**。`update.self` 只保留版本检查、通知、原生二进制自更新；容器内检测到后置灰并提示「拉取新镜像并重启容器」。`_docker` 版本后缀、`TDL_DOCKER` 依赖随之移除 |
| 4 | AriaNg | **归入 aria2 管理 SWC**。作为 `application/downloader.aria2/web/aria2ng.html`，由该 SWC 的 Manifest 声明为一个功能页；RPC 反向代理也由该 SWC 提供，与出站 RPC 客户端**共用同一份实现** |
| 5 | 硬编码 AppID/AppHash | **归入登录 SWC（`account.telegram`）的配置**。默认沿用项目内置凭据（保持现状行为、老用户零迁移），用户填了自己的 `api_id`/`api_hash` 后改用用户自己的。核查结论见 §0.4，方案见 §6.2 |
| 6 | Go 模块 | **单模块**。取消 `core/` 子模块与 `replace`，用 `internal/` 可见性约束代替 |

### 0.4 第 5 项核查结论：硬编码凭据是否在起作用

**核查范围**：`pkg/tclient/app.go`、`pkg/tclient/tclient.go`、`core/tclient/tclient.go`、`app/login/bot_auth.go`、`app/login/bot_auth_test.go`。

**硬编码的是什么**

`pkg/tclient/app.go:13-19`：

| 名称 | AppID | AppHash | 来源（代码注释） |
| --- | --- | --- | --- |
| `builtin` | 15055931 | `021d433426cbb920eeb95164498fe3d3` | "application created by iyear"（上游 tdl 项目的应用） |
| `desktop` | 2040 | `b18441a1ff607e10a989891a5462e627` | Telegram Desktop 官方凭据（注释引用 opentele 文档） |

**是否在起作用：是，而且 `desktop` 是唯一活跃的路径。**

完整调用链：

1. 登录开始时，`runWithTemporarySession` 把临时存储的 `app` 键写成 `desktop`（`bot_auth.go:346`）。
2. 登录成功后，`commitTemporarySession` 把 `desktop` 写进正式存储（`bot_auth.go:413`）。
3. 建客户端时，`pkg/tclient.New` → `GetApp(kv)` 读 `app` 键（`pkg/tclient/tclient.go:24-35`），取出对应的 `AppID`/`AppHash`，传给 `core/tclient.Options` → `telegram.Options`。
4. `GetApp` **只有在读不到 `app` 键时才回退到 `builtin`**。

结论：
- **凡是走过 tdl 自己登录流程的账号，用的都是 Telegram Desktop 的官方 AppID 2040 与官方 AppHash。**
- `builtin`（iyear 的 15055931）只在「KV 中缺少 `app` 键」时才生效 —— 即从别处导入会话文件、或旧版本升级残留的场景。**接近死路径。**
- **用户完全无法选择**：`pkg/config` 的 `Config` 结构体（`config.go:104-130`）里没有任何 api_id / api_hash / app 相关字段，面板与 bot 也没有入口。

**它有什么作用，为什么不能当小事**

`api_id` + `api_hash` 是 MTProto `initConnection` 的必填参数，决定 Telegram 服务端把你这个客户端识别成「哪个应用」。影响三层：

1. **合规与封号风险（主要问题）**。使用 Telegram Desktop 的官方凭据属于「借壳」——不是你自己申请的应用。这违反 Telegram 的 API 使用条款；官方凭据会被 Telegram 主动调整或封禁，历史上多个第三方客户端因套用官方凭据被处理。同理，`15055931` 是上游项目的应用，你既无法控制它、出问题也找不到人。
2. **额度与风控画像**。官方客户端的 api_id 额度通常更高、更不容易被限流，这也是为什么 tdl 选了 2040 —— 它换来的是「好用」，代价是把风险转嫁给使用者。
3. **凭据与会话强绑定**。auth key 与创建会话时的 `api_id` 绑定，换凭据通常会导致会话失效、必须重新登录。这解释了 `GetApp` 的报错文案 "can't find app: %s, please try re-login"（`pkg/tclient/tclient.go:31`）。

**处理方式：归入登录 SWC 的配置，内置值作为默认，用户设置后覆盖。**

理由不只是合规。从架构角度看，这是唯一一个**用户既不能查看、也不能配置、而且直接决定账号安全**的参数，却被硬编码在 `pkg/tclient/app.go` 里。它不符合「配置分散到 SWC」的原则 —— 它本该是 `account.telegram` SWC（登录 SWC）的一个配置项。

设计见 §6.2。核心要点：`api_id`/`api_hash` 成为登录 SWC 的参数；**留空则使用内置预设（默认 `desktop`，即项目当前实际在用的那组），填了两项则使用用户自己的凭据**；半填视为校验错误而不是静默回退；凭据指纹写入 `account` 数据集，变更时提示重新登录但不删除旧会话。

**这样定带来的关键收益**：默认值与现状完全一致，**老用户升级后无需重新登录，零迁移成本**。代价是默认路径仍带合规风险，因此配置页必须固定给出警示与申请指引（§10 R4 记录了这个已知残留风险）。

---

## 1. 设计理念与可验收原则

7 条原则，每条都给出**可机器验证**的验收方式，避免变成口号。

### G1 主进程只做框架

**原则**：框架（`rte/` + `bsw/`）不依赖 `application/`。清空集成点清单即可得到一个能编译运行的空框架。

**现状冲突**：`app/runtime/manager.go` 直接 import 了 `app/bot`、`app/watch`、`app/http`、`app/aria2`、`app/forward`、`app/webui` 六个业务包，并在 `NewManager`（`manager.go:164-185`）硬编码构造它们。

**实现方式**：Go 没有可用的运行时插件加载（`plugin` 包在 Windows 不可用、要求完全相同工具链）。因此采用 **编译期注册 + 单一集成点**：

```go
// application/swc.go —— 唯一的集成点（对应 AUTOSAR 的集成商角色）
package application

import (
    _ "…/application/account.telegram"
    _ "…/application/trigger.reaction"
    _ "…/application/filter.rules"
    // … 一行一个 SWC；注释掉一行即卸载该 SWC
)
```

每个 SWC 包的 `init()` 把工厂注册到 `rte/registry`。框架侧因此完全不知道任何 SWC 的存在。

**验收**：清空 `application/swc.go` 的 import 后，`go build ./...` 通过，启动输出「已装载 0 个 SWC」。`go list -deps ./rte/... ./bsw/...` 与 `application/` 的交集为空。

### G2 一切功能皆 SWC

**原则**：任何功能都能单独禁用、启用、重启、升级，不影响其他 SWC（除非声明为必需依赖）。

**现状冲突**：`webui` 被明确拒绝关闭（`manager.go:410-411`）；六个模块靠 `config.Modules` 中心开关表控制，真实依赖未表达。

**验收**：随机禁用一个 SWC，其余全部正常启动；被禁用者显示「未启用」而非报错。配置里 `enabled: false` 即可，不需要重编译。

### G3 SWC 之间零直接依赖

**验收（CI 强制）**：`application/A` 的 import 集合与 `application/` 的交集为空。

**现状冲突**：`app/watch` import `app/http`、`app/forward`；`app/bot` import `app/aria2`、`app/login`、`app/updater`、`app/watch`；`app/webui` import 了五个业务包。

### G4 每个 SWC 独立

**原则**：独立目录、独立生命周期、独立状态、独立失败边界。

**验收**：注入一个 `Init` 必定失败的 SWC，进程仍启动，其余为 `Running`，该 SWC 为 `Failed` 且面板可显示原因。

### G5 配置分散（ECUC 参数按模块组织）

**验收**：SWC 的 `ConfigView` 只能访问自己 Manifest 声明的路径，越界读取在开发模式 panic；修改 `downloader.aria2` 的配置不会触发其他 SWC 的 `Reconfigure`。

### G6 每个功能页附带配置页

**验收**：新增一个只有 3 个配置项的示例 SWC，不写任何前端代码，刷新面板即可看到导航项、功能页、配置页，配置可保存生效。

### G7 写入所有权唯一

**验收**：`nvm.RegisterDataset(name, writerSWC)` 重复注册即启动失败；CI 静态检查非 writer 不得引用该数据集的 key。

---

## 2. 现状摘要

### 2.1 结构与规模

```
main.go                       进程入口 + __apply-update 自更新分支 + 重启
cmd/                          Cobra 命令树（仅 version 子命令）+ NTP 引导
app/    101 个 .go            业务层 10 个包（runtime/bot/watch/http/aria2/forward/webui/login/updater/download）
pkg/     26 个 .go            通用层 10 个包（config/consts/kv/tclient/tplfunc/key/filterMap/validator/utils/ps）
core/    35 个 .go 17 包      独立 Go 子模块（tclient/dcpool/storage/tmedia/forwarder/downloader/uploader/…）
```

### 2.2 六个模块与它们的真实依赖

| 模块 | 对应包 | 由谁启停 | 实际依赖谁 |
| --- | --- | --- | --- |
| `bot` | `app/bot` | `config.Modules.Bot` | watch（`WatchControl`）、aria2、login、updater |
| `watch` | `app/watch` | `config.Modules.Watch` | http（`HTTPService`）、forward（`Jobs()`）、aria2（`DownloadSubmitter`） |
| `http` | `app/http` | `config.Modules.HTTP` | 被 watch 与 aria2 消费 |
| `aria2` | `app/aria2` | `config.Modules.Aria2` | 由 watch 注入 Submitter；反向接 http 的错误上报 |
| `forward` | `app/forward` | `config.Modules.Forward` | 与 watch 共用同一条 Telegram 连接 |
| `webui` | `app/webui` | 强制开启 | 除 `app/aria2` 外几乎全部业务包 |

**关键观察**：依赖关系**没有任何地方声明**，只存在于 `NewManager` 与 `applyConfigLocked` 的代码顺序里。

### 2.3 现状中最值得保留的技术资产

| 资产 | 位置 | 为什么值钱 |
| --- | --- | --- |
| HTTP Range 语义完整实现 | `app/http/http.go` | 单 Range / multipart / HEAD / If-Range 全支持，不依赖 aria2 专有行为 |
| 按 DC + 文件 FIFO 的配额调度 | `app/http/transfer/scheduler.go` | 保证每 DC ≤ `pool_size` 个 `getFile`，账号安全的关键 |
| 已发送区间合并判定完成态 | `app/http/delivery_status.go` | 并行/重试/重叠分片正确合并 |
| 分块级瞬时错误重试 | `app/http/telegram_source.go` | 空响应/挂起/连接重置在块内恢复，不撕裂响应 |
| aria2 两个治理器 | `app/aria2/regulator.go`、`speed_monitor.go` | 错误风暴降并发；零速抖动重建连接 |
| 转发 direct→clone 降级 | `core/forwarder/` | 受保护内容只能靠 clone 转出 |
| 登录临时会话策略 | `app/login/bot_auth.go:405` | 验证成功后才提交正式 KV，避免半成品会话 |
| 文件名/目录模板系统 | `app/watch/download_dir.go` | 10 个变量 + 字节级长度上限 + 同批冲突去重 |
| 无构建链的前端 | `app/webui/static/` | 原生 ESM + `go:embed`，零依赖单文件交付 |

---

## 3. AUTOSAR 架构与目录结构

### 3.1 分层

```
┌──────────────────────────────────────────────────────────────────────┐
│  Application Layer                       application/                │
│  13 个 SWC，每个一个目录，彼此零 import                                  │
└───────────────────────────┬──────────────────────────────────────────┘
                            │ 端口调用（Port）+ 事件（S/R）
┌───────────────────────────▼──────────────────────────────────────────┐
│  RTE                                     rte/                        │
│  端口装配(registry) · 事件路由(eventbus) · 参数分发(config)             │
│  Runnable 调度(schedule) · 模式管理(mode)                              │
└───────────────────────────┬──────────────────────────────────────────┘
                            │ BSW 服务调用
┌───────────────────────────▼──────────────────────────────────────────┐
│  BSW                                     bsw/                        │
│  ┌ Services 服务层 ─────────────────────────────────────────────┐    │
│  │  ecum(状态管理) bswm(模式仲裁) schm(调度) nvm(存储服务)         │    │
│  │  csm(密钥) dem(诊断事件) dcm(诊断读取) wdgm(存活监控) dlt(日志)  │    │
│  ├ ECUAL ECU 抽象层 ────────────────────────────────────────────┤    │
│  │  httpif(HTTP) memif(存储) fsif(路径) netif(网络)               │    │
│  │  comif(通信通道+配额) mediaif(媒体) hmiif(面板骨架)             │    │
│  ├ MCAL 微控制器抽象层 ──────────────────────────────────────────┤    │
│  │  tgconn(MTProto 传输) boltdb(bbolt 文件)                      │    │
│  └ CDD 复杂设备驱动 ────────────────────────────────────────────┘    │
│     taskhub(任务中枢) tgauth(账号会话与配额)                            │
└──────────────────────────────────────────────────────────────────────┘
                            │
┌───────────────────────────▼──────────────────────────────────────────┐
│  interfaces/  ★ 契约层（ARXML 类比）—— 不依赖任何实现                    │
│  manifest(SWC 描述) · ports(端口接口) · types(数据类型) · events(主题)   │
└──────────────────────────────────────────────────────────────────────┘
```

### 3.2 目录树

```
tdl/
├── cmd/
│   └── tdl/
│       └── main.go                     Startup：引导 RTE、装载集成点
│
├── interfaces/                         ★ ARXML 类比：契约定义，无实现
│   ├── manifest/
│   │   ├── swc.go                      SWC 描述：Manifest / SWCType / 多实例 / Provide / Require / Page
│   │   └── config.go                   ECUC 参数定义：ConfigField / FieldType / Action
│   ├── ports/
│   │   ├── ports.go                    端口名与契约版本常量
│   │   ├── rangesource.go              S/R + C/S：数据源
│   │   ├── executor.go                 C/S：下载执行器
│   │   ├── namingrules.go              C/S：命名规则
│   │   ├── filterrules.go              C/S：过滤规则
│   │   ├── notifier.go                 C/S：通知
│   │   ├── console.go                  C/S：控制面
│   │   ├── accountstore.go             C/S：账号查询与选择
│   │   ├── taskobserver.go             S/R：任务观察
│   │   └── externalmode.go             Mode：外部依赖模式
│   ├── types/
│   │   ├── account.go                  AccountID / AccountInfo
│   │   ├── media.go                    MediaRef / ByteRange / SourceID / TargetHint
│   │   ├── task.go                     TaskSpec / Task / TaskState / Handle / Progress
│   │   └── notice.go                   Notice / Level
│   └── events/
│       ├── topics.go                   主题常量
│       └── payload.go                  载荷类型（全部带 AccountID）
│
├── rte/                                ★ Runtime Environment
│   ├── rte.go                          Kernel 门面：SWC 只能看到它
│   ├── registry/                       端口注册、解析、类型与版本校验、拓扑排序
│   ├── eventbus/                       发布订阅、请求响应、订阅者隔离
│   ├── config/                         ECUC 参数加载/校验/保存/分发/版本迁移
│   ├── schedule/                       Runnable 触发：周期、延时、命名任务组
│   └── mode/                           模式管理：ECU 阶段 → SWC 模式
│
├── bsw/                                ★ Basic Software
│   ├── services/
│   │   ├── ecum/                       ECU 状态管理：启动/关机阶段编排
│   │   ├── bswm/                       BSW 模式仲裁：SWC 状态机、依赖拓扑、启停决策
│   │   ├── schm/                       调度器实现：tick 与任务组
│   │   ├── nvm/                        非易失存储服务：数据集注册、所有权校验、生命周期
│   │   ├── csm/                        密钥服务：敏感值读写与自动脱敏
│   │   ├── dem/                        诊断事件管理：错误记录、聚合、风暴检测
│   │   ├── dcm/                        诊断读取：健康检查聚合、诊断快照
│   │   ├── wdgm/                       存活监控：SWC 心跳与卡死检测
│   │   └── dlt/                        日志与追踪：结构化日志、轮转、SWC 维度命名
│   ├── ecual/
│   │   ├── httpif/                     HTTP 服务器：监听、路由挂载、冲突检测
│   │   ├── memif/                      存储抽象：bbolt → 数据集句柄
│   │   ├── fsif/                       文件系统与路径抽象
│   │   ├── netif/                      网络抽象：代理 dialer、超时、传输
│   │   ├── comif/                      通信接口：DC 通道抽象 + Lease 配额
│   │   ├── mediaif/                    Telegram 媒体 → MediaRef 的统一转换
│   │   └── hmiif/                      人机界面抽象：面板骨架 + 配置页渲染器
│   ├── mcal/
│   │   ├── tgconn/                     MTProto 传输：客户端构造、DC 列表、密钥、时钟
│   │   └── boltdb/                     bbolt 文件访问与事务
│   └── cdd/
│       ├── taskhub/                    任务中枢（唯一写入者）+ 执行器选择
│       └── tgauth/                     账号注册表：会话、DC 池、配额（多账号预留）
│
├── application/                        ★ Application Layer：全部功能即 SWC
│   ├── swc.go                          集成点：一行一个 SWC 的 import
│   ├── account.telegram/               Service SWC · per-account
│   ├── trigger.reaction/               Application SWC · per-account
│   ├── trigger.messagelink/            Application SWC · per-account
│   ├── filter.rules/                   Service SWC · single
│   ├── naming.rules/                   Service SWC · single
│   ├── proxy.range/                    Service SWC · single
│   ├── downloader.aria2/               Service SWC · single（含 web/aria2ng.html）
│   ├── downloader.local/               Service SWC · single
│   ├── forwarder/                      Application SWC · per-account
│   ├── console.bot/                    Service SWC · single
│   ├── notify.telegram/                Application SWC · single
│   ├── panel.webui/                    Application SWC · single
│   └── update.self/                    Application SWC · single
│
├── config/                             运行期 ECUC 参数（不入库）
│   ├── ecuc.json                       框架级参数
│   ├── bsw/                            BSW/CDD 参数：taskhub.json、tgauth.json …
│   ├── swc/                            SWC 参数
│   │   ├── naming.rules.json           单实例 SWC
│   │   └── account.telegram/           多实例 SWC 按账号分目录
│   │       └── default.json
│   ├── secrets.json                    敏感值（0600）
│   └── .schema/                        各模块 schema 快照（版本迁移用）
│
├── scripts/                            安装脚本
├── docs/                               文档 + 自动生成的配置参考
└── go.mod                              单模块
```

**资源同目录原则**：每个 SWC 的静态资源放在自己的 `web/` 子目录（如 `application/downloader.aria2/web/`），由 SWC 的 `go:embed` 内嵌并在 Manifest 里声明，不再有全局的前端目录。

### 3.3 层间依赖方向（CI 可强制的核心收益）

这是采用 AUTOSAR 目录结构**最实际的好处**：**目录即依赖方向约束**，一条 CI 规则就能守住架构不腐化。

| 层 | 允许 import | 禁止 |
| --- | --- | --- |
| `cmd/*` | 全部（唯一集成点） | — |
| `application/*` | `interfaces/*`、`rte` | 其他 `application/*`、`bsw/*`（仅允许 `bsw` 暴露在 `rte` 门面后的能力） |
| `rte/*` | `interfaces/*` | `application/*`、`bsw/services` 以下各层（通过注入的实现访问） |
| `bsw/services/*` | `interfaces/*`、`bsw/ecual/*`、`bsw/mcal/*`、`bsw/cdd/*` | `application/*`、`rte/*` |
| `bsw/ecual/*` | `interfaces/*`、`bsw/mcal/*` | `application/*`、`rte/*`、`bsw/services/*` |
| `bsw/mcal/*` | `interfaces/types`（仅类型）、标准库、第三方库 | 其他一切自有包 |
| `bsw/cdd/*` | `interfaces/*`、`bsw/services/*`、`bsw/ecual/*` | `application/*`、`rte/*` |

**CI 检查方式**（按目录前缀判定层号，逆序依赖即失败）：

```bash
# 层号：mcal=1  ecual=2  services/cdd=3  rte=4  application=5  cmd=6
# 规则：import 的层号必须 <= 自己的层号
# 额外规则：application/A 之间、以及 application 内部的相互 import 必须为 0
```

### 3.4 13 个 SWC 清单

| SWC ID | SWC 类型 | 多实例 | 职责 | Provides | Requires | 来源（tdl） |
| --- | --- | --- | --- | --- | --- | --- |
| `account.telegram` | Service SWC | **per-account** | 会话管理、登录（面板 + 控制台）、凭据校验、账号信息 | `AccountStore` | — | `app/login`、`bot/login_manager.go`、`webui/login.go`、`users.go`、`pkg/tclient` |
| `trigger.reaction` | Application SWC | **per-account** | 监听 reaction 与编辑消息，判定命中后发布下载意图，去重 | — | Telegram（门面）、AccountStore | `app/watch/reactions.go` |
| `trigger.messagelink` | Application SWC | **per-account** | 校验解析 5 种消息链接形态，发布下载意图 | — | Telegram、AccountStore | `app/watch/message_link.go` |
| `filter.rules` | Service SWC | single | 扩展名白/黑名单 + 字节区间过滤（同步端口） | `FilterRules` | — | `app/watch/filter.go` |
| `naming.rules` | Service SWC | single | 目录/文件名模板渲染、长度上限、冲突去重、路径解析 | `NamingRules` | — | `app/watch/download_dir.go`、`filename.go`、`pkg/tplfunc` |
| `proxy.range` | Service SWC | single | HTTP Range 数据源：单/多 Range、HEAD、If-Range、分块并行、瞬时错误重试 | `RangeSource` | Telegram、NvM | `app/http/http.go`、`session.go`、`telegram_source.go` |
| `downloader.aria2` | Service SWC | single | aria2 RPC、任务提交、两个治理器、重连恢复、AriaNg 页面与其 RPC 代理 | `Executor` | RangeSource、NvM | `app/aria2/*`、`webui/aria2.go`（合并）、`webui/aria2ng.html` |
| `downloader.local` | Service SWC | single | **本地下载器**：落盘、断点续传、并发限流、速率采样 | `Executor` | RangeSource、NvM | `app/watch/internal_downloader*.go` |
| `forwarder` | Application SWC | **per-account** | 持久化串行转发队列、direct/clone、相册整体转发、去重 | — | Telegram、可选 Notifier | `app/forward/*`、`watch/forward.go` |
| `console.bot` | Service SWC | single | 控制面宿主：命令注册表、权限校验、长轮询、回调键盘 | `Console` | — | `app/bot/bot.go`、`allowed_users.go`、`helpers.go` |
| `notify.telegram` | Application SWC | single | 消费任务事件，四类通知 + 实时进度消息 | `Notifier` | 可选 Console | `app/bot/notifier.go`、`aria2_events.go` |
| `panel.webui` | Application SWC | single | 面板业务页：仪表盘、任务列表、转发队列、账号页、更新页 | — | TaskObserver、各服务端口 | `app/webui/dashboard.go`、`downloads.go`、`forwards.go` |
| `update.self` | Application SWC | single | 版本检查、通知、原生自更新（容器内不提供应用） | — | 可选 Notifier | `app/updater/*` |

**拆解效果**：tdl 的 `watch` 巨包（33 文件）→ 5 个 SWC；`webui` → `hmiif`（骨架）+ `panel.webui`（业务页）+ `account.telegram`（登录）；`bot` → `console.bot` + `notify.telegram`。

### 3.5 2 个 CDD

AUTOSAR 的 CDD 定位是「需要跨层直接访问资源、不遵循分层规则的特殊模块」。本项目里有两个东西符合这个描述 —— 它们是**「只能存在一份」的共享权威**：

| CDD | 为什么不能做成普通 SWC |
| --- | --- |
| `taskhub` | 它是任务数据的**唯一写入者**。做成 SWC 的话，其他 SWC 要么 import 它（违反 G3），要么通过端口访问（那它就是内核服务，只是位置不对）。放 CDD 是更诚实的表达 |
| `tgauth` | 只有一个 MTProto 会话、一套 `pool_size` 配额。若让每个下载 SWC 各自持有连接池，`pool_size` 保证立即失效，后果是 Telegram 限流甚至账号风控 |

> **判断标准只有一条：凡是「只能存在一份」的东西，就必须在内核层，无论它是纯基础设施还是带业务语义的权威。**

### 3.6 端口与事件

端口名常量集中在 `interfaces/ports/ports.go`，杜绝字符串拼写错误。

| 端口 | 类别 | 契约要点 |
| --- | --- | --- |
| `RangeSource` | S/R + C/S | `Register(ctx, acct, ref, hint) (SourceID, error)`；`URL`、`Size`、`ETag`、`Open(ctx, acct, id, rng, w)`、`Unregister` |
| `Executor` | C/S | `Submit(ctx, acct, spec) (Handle, error)`；`Pause` / `Resume` / `Remove` / `Snapshot` |
| `NamingRules` | C/S | `Render(ctx, in NamingInput) (NamingResult, error)` — 目标目录与文件名 |
| `FilterRules` | C/S | `ShouldHandle(ctx, in FilterInput) (bool, Reason)` — 同步返回 |
| `Notifier` | C/S | `Notify(ctx, acct, Notice) error` |
| `Console` | C/S | `RegisterCommand(cmd Command)`、`RegisterAction(a Action)` |
| `AccountStore` | C/S | `List()`、`Get(id)`、`Default()`、`Resolve(username)` |
| `TaskObserver` | S/R | `Subscribe(fn func(Task))` |
| `ExternalMode` | Mode | 外部依赖就绪状态（如 aria2 可用 / 不可用），供 SWC 降级 |
| 内部 | — | `comif.Lease(ctx, acct, dc, fileKey) (Release, error)` — **唯一并发入口** |

**关键设计：所有跨账号的端口签名都带 `acct AccountID`。** 这是决策 2 里「先保留、标 TODO」的落地方式 —— 签名一开始就带账号维度，将来实现多账号不需要改契约（改契约是破坏性的，代价远高于多传一个参数）。

**事件主题**（载荷全部带 `AccountID`）：

```
trigger.download.intent    {Account, Ref, Source, TargetHint}
trigger.forward.intent     {Account, Ref, Dest, Mode, Silent}
task.created               {Account, TaskID, Spec}
task.progress              {Account, TaskID, Done, Total, Speed}
task.finished              {Account, TaskID, Path}
task.failed                {Account, TaskID, Err}
account.session.changed    {Account, Valid, User}
account.credentials.changed{Account, NeedsRelogin}      ← api_id 变更时
telegram.stream.error      {Account, TaskID, Err}       proxy.range → dem
system.config.changed      {SWC, Fields}
system.swc.state           {SWC, Instance, State, Detail}
```

> **硬性约定：事件总线不进热路径。** 字节流、分块下载、进度采样必须走端口直接调用。

### 3.7 概念映射（AUTOSAR ↔ 本框架 ↔ tdl 现状）

| AUTOSAR | 本框架 | tdl 现状 | 差距 |
| --- | --- | --- | --- |
| Application SWC / Service SWC | `application/*` 13 个 | `app/` 下 10 个包 | 现状互相 import 成网；SWC 必须零 import |
| Port / Interface | `interfaces/ports/*` | `Options` 字段、鸭子类型接口 | 无版本、无声明、无校验 |
| ARXML / ECU Extract | `interfaces/manifest` | 缺失 | 需从零建立 |
| Connector | `rte/registry` | 硬编码在 `NewManager` 顺序里 | 需声明化 + 启动期校验 |
| RTE | `rte/*` | `app/runtime/manager.go` | 现状是 800 行手工连线 |
| Services | `bsw/services/*` | 散落在 `runtime`、`pkg/kv`、`metrics.go` | 职责未分离 |
| ECUAL | `bsw/ecual/*` | `app/http` 服务器部分、`pkg/kv` | 未抽象 |
| MCAL | `bsw/mcal/*` | `core/tclient`、`core/dcpool`、bbolt | 未分层 |
| CDD | `bsw/cdd/*` | 无对应物（任务存储分散在 4 个包） | 需新建 |
| ECUC | `config/` 按模块分文件 | 一个扁平 `config.json` | 需 schema 与自动配置页 |
| Runnable | `rte/schedule` | 各模块自写 goroutine | 需统一调度 |
| Mode Management | `rte/mode` + SWC 状态机 | `runtime` 里 5 个私有 `*State()` 拼字符串 | 状态自己回答 |
| NvM | `bsw/services/nvm` + `nvm` 数据集 | `pkg/kv` | 需加所有权机制 |
| Dem / Dcm | `dem` / `dcm` | `app/http/metrics.go` 的部分 | 需分离 |
| WdgM | `wdgm` | 无 | 需新建（插件存活监控） |

### 3.8 类比的边界（诚实说明）

采用 AUTOSAR 结构带来的是**约束力**，但也必须承认三处延伸，避免被结构本身误导：

1. **`hmiif` 不在 AUTOSAR 的 BSW 范围内**。AUTOSAR 把 HMI 留给应用层，本项目把面板骨架放在 ECUAL 是为了保持「底层能力抽象」的语义一致（面板对 SWC 而言就像显示设备）。这是对类比的扩展，不是标准。
2. **MCAL/ECUAL 在这里是「协议栈与系统能力」的分层，不是真实的硬件分层**。`tgconn` 对应 CAN/Ethernet 驱动的位置，`boltdb` 对应内存驱动的位置 —— 是**位置类比**，不是功能等同。
3. **没有代码生成**。AUTOSAR 的 RTE 由工具从 ARXML 生成，一致性有强保证；本项目的 RTE 是手写实现，一致性靠「启动期校验 + CI 依赖方向检查」（§9）。代价是描述与实现的一致性靠纪律，这是必须用 CI 兜住的地方。

**为什么仍然值得这么做**：AUTOSAR 那 14 层 BSW 是为多供应商、跨组织、以年为单位的交付服务的，照搬会让一个自用工具更难维护。真正可迁移的是三条机制（描述文件声明依赖、RTE 装配与路由、ECUC 按模块配置）和一条纪律（**分层即依赖方向**）。前三条在 §3.6、§4 落地，最后一条在 §3.3 落地。

---

## 4. 配置（ECUC）设计

### 4.1 三条规则

| # | 规则 | 说明 |
| --- | --- | --- |
| R1 | SWC 只读自己的参数 | `Init` 拿到的 `ConfigView` 只含本 SWC `Config` 声明的路径。越界读取在开发模式 panic（白名单 map 包装实现） |
| R2 | 跨 SWC 共享值上提为框架/B SW 参数 | 典型：`proxy` 被 MTProto、bot HTTP 客户端、updater 三处使用 → `config/ecuc.json` 的 `network` 段；`pool_size`/`limit` 是全局配额 → `config/bsw/tgauth.json` 与 `schm` 参数。SWC 通过端口读取，不重复配置 |
| R3 | 参数变更只通知相关模块 | 改 `config/swc/downloader.aria2.json` 只触发该 SWC 的 `Reconfigure`；不再像 `ApplyConfig` 那样重放全部模块 |

### 4.2 单实例与多实例 SWC 的配置布局

```
config/
├── ecuc.json                       框架级：日志、数据目录、面板监听/口令、网络代理、默认账号
├── bsw/
│   ├── taskhub.json                执行器优先级、任务 TTL、索引批量落盘间隔
│   └── tgauth.json                 账号表、每账号的并发配额
├── swc/
│   ├── naming.rules.json           单实例 SWC：一个文件
│   ├── filter.rules.json
│   ├── proxy.range.json
│   ├── downloader.aria2.json
│   ├── downloader.local.json
│   ├── console.bot.json
│   ├── notify.telegram.json
│   ├── panel.webui.json
│   ├── update.self.json
│   └── account.telegram/           多实例 SWC：一账号一个文件
│       └── default.json
├── secrets.json                    敏感值（0600）：api_hash、bot token、aria2 secret、代理密码、面板口令
└── .schema/                        各模块 schema 快照，用于版本迁移与差异检测
```

**规则**：SWC 的 `Manifest.Multiplicity` 决定布局 —— `single` 用 `swc/{id}.json`，`per-account` 用 `swc/{id}/{account}.json`。多实例 SWC 可额外提供 `swc/{id}/_default.json` 作为各账号共用的基底（默认值合并顺序：schema 默认值 → `_default.json` → `{account}.json`）。

### 4.3 参数归属映射（迁移工具 `tdl migrate-config` 的实现依据）

| 旧字段（`config.json`） | 新归属 | 理由 |
| --- | --- | --- |
| `proxy` / `proxy_username` / `proxy_password` | `ecuc.json` → `network`（密码进 `secrets.json`） | 三处使用，跨 SWC 共享 |
| `namespace` | `ecuc.json` → `default_account` | **概念被 account 取代**（决策 2） |
| `debug` | `ecuc.json` → `log.level` | 日志级别由框架统一控制 |
| `limit` | `ecuc.json` → `limits.max_files` | 全局并发配额 |
| `pool_size` | `bsw/tgauth.json` → `per_dc_capacity` | 每账号每 DC 的连接配额 |
| `delay` | `bsw/tgauth.json` → `submit_delay_seconds` | 提交间隔，属通信节流 |
| `ntp` / `reconnect_timeout` | `bsw/tgauth.json` | MTProto 连接参数 |
| `download_dir` / `filename` / `filename_max_length` | `swc/naming.rules.json` | 命名策略独立成 SWC |
| `trigger_reactions` | `swc/trigger.reaction/{account}.json` | 触发策略按账号不同 |
| `include` / `exclude` / `file_size_min_mb` / `file_size_max_mb` | `swc/filter.rules.json` | 过滤规则独立，将来可加新规则类型 |
| `http.address` / `port` / `public_base_url` / `download_link_ttl_hours` | `swc/proxy.range.json` | |
| `webui.address` / `port` | `ecuc.json` → `hmi.listen` | 面板骨架属框架 |
| `webui.username` / `password` | `ecuc.json` → `hmi.user`（口令进 `secrets.json`） | 必须先于 SWC 完成认证 |
| `modules.bot` / `watch` / `http` / `aria2` / `forward` | **删除** | 改用各 SWC 自己的 `enabled` + 依赖解析 |
| `downloader.mode` | `bsw/taskhub.json` → `executor_priority` | 从「硬编码二选一」改为「按优先级选可用执行器」。取值 `internal` → **`local`** |
| `aria2.*` | `swc/downloader.aria2.json`（`auto_download` → `auto_submit`） | |
| `bot.token` | `swc/console.bot.json` + `secrets.json` | |
| `bot.allowed_users` | `swc/console.bot.json` | 属控制面权限，不属账号 |
| `bot.notify.*` | `swc/notify.telegram.json` | 通知从 bot 抽出独立成 SWC |
| `forward.*` | `swc/forwarder/{account}.json` | 转发目标与监听对象按账号不同 |
| **（新增）** api 凭据 | `swc/account.telegram/{account}.json` + `secrets.json`（api_hash） | 见 §6.2。留空即用内置预设，保持现状行为 |

### 4.4 配置页生成器（`hmiif` 提供）

**同一份 `ConfigField` 承担三件事**：定义配置文件（默认值合并、未知字段告警）、驱动校验（类型/范围/枚举/自定义）、生成配置页。

| `ConfigField.Type` | 渲染 | 校验 |
| --- | --- | --- |
| `string` / `text` | 单行 / 多行文本 | 长度 + `Validate` |
| `int` / `float` | 数字输入（显示 `Min`/`Max`/`Unit`） | 范围 |
| `bool` | 开关 | — |
| `enum` | 下拉（`Enum` 数组） | 枚举成员 |
| `strings` | 标签输入（回车添加、可删、可排序） | 去重去空 |
| `duration` | 数字 + 单位（秒/分/时） | 范围 |
| `size` | 数字 + 单位（MB/GB） | 范围 |
| `size_range` | 双端输入，0 表示不限 | 区间合法性（`5~2` 报错） |
| `path` | 文本 + 目录提示 | 路径形态 |
| `secret` | 密码框，**留空 = 保持不变** | 长度 |
| `proxy` | 协议下拉 + 地址 + 用户名 + 密码 | URL 合法性、协议白名单 |
| `account_ref` | 账号下拉（多账号启用后） | 账号存在性 |
| `action` | 按钮（触发 SWC 提供的动作） | — |
| `notice` | **只读提示块**：渲染一段说明文字，支持加粗与外部链接；**不产生配置值** | — |

> `notice` 是配置页的「说明性内容」原语，用于分组提示、风险告知、申请指引等场景。它按位置插入到配置页的相应分组，用户可以阅读但不可编辑，也不写入配置文件。登录 SWC 的凭据分组正是用它来展示 api 凭据的风险提示与 `my.telegram.org` 申请指引（§6.2）；`naming.rules` 用它展示模板变量对照表（§6.2 未列，属同类用法）。

**三个具体收益**：

1. **新增配置项从改 4 处变成改 1 处**。现在要动 `pkg/config` 结构体、`Validate`、`webui/config_api.go` 的脱敏与路径写入、前端 `config.js` 手写的 `sections` 常量。
2. **「secret 留空 = 保持不变」三处逻辑合一**。现在依赖后端 `publicConfig` 脱敏（`config_api.go:164-174`）、前端跳过空密码控件（`config.js:348`）、后端再次兜底（`config_api.go:184-192`）三处同时正确；新方案只需 schema 标 `Secret: true`，渲染器与保存器都据此处理。
3. **校验不再各处重复**。现在 `filter.rules` 的「include 与 exclude 互斥」只写在 README 里；新方案在 schema 里表达，配置页直接标红拒绝保存。

---

## 5. 存储（NvM）设计

### 5.1 数据集契约：5 个有主数据集

```go
// bsw/services/nvm
type Service interface {
    // RegisterDataset 注册数据集；同名重复注册即启动失败（唯一写入者保证）
    // scope 决定 key 是否带账号前缀
    RegisterDataset(name string, writer manifest.SWCID, scope Scope) Dataset
    Dataset(name string) (Dataset, bool)
}

type Scope string
const (
    ScopeAccount Scope = "account" // key 形如 {name}.{account}.{id}
    ScopeGlobal  Scope = "global"  // key 形如 {name}.{id}
)

type Dataset interface {
    Get(ctx context.Context, acct types.AccountID, key string) ([]byte, error)
    Set(ctx context.Context, acct types.AccountID, key string, value []byte) error  // 仅 writer 可调
    Delete(ctx context.Context, acct types.AccountID, key string) error             // 仅 writer 可调
    Keys(ctx context.Context, acct types.AccountID) ([]string, error)
}
```

| 数据集 | 写入者 | Scope | key 形态 | 内容 |
| --- | --- | --- | --- | --- |
| `account` | `account.telegram` | account | `session`、`app_credential` | gotd 会话、凭据指纹 |
| `telegram` | `tgauth`（CDD） | account | `state`、`chan`、`peers`、`peers_phone`、`peers_contacts` | updates 状态与实体缓存 |
| `tasks` | `taskhub`（CDD） | account | `tasks.{id}`、`tasks.index` | 任务记录（媒体引用、目标路径、已发送区间、执行器句柄、断点位置、状态） |
| `forward` | `forwarder` | account | `forward.jobs.{id}`、`forward.jobs.index` | 转发队列 |
| `secrets` | `csm`（Services） | global | `api_hash.{account}`、`bot_token`、`aria2_secret`、`panel_password`、`proxy_password` | 敏感值 |

### 5.2 与 tdl 的 13 条无主 key 前缀对照

| tdl 的 key 前缀 | 新归属 | 变化 |
| --- | --- | --- |
| `session` | `account.session.{acct}` | **现在就加账号前缀**（多账号预留，见 §6.1） |
| `app` | `account.app_credential.{acct}` | 从「模式名」升级为「凭据指纹」 |
| `state:<uid>` / `chan:<uid>` | `telegram.state|chan.{acct}` | uid 由账号唯一确定，不必再进 key |
| `peers:*` | `telegram.peers*` | 同上 |
| `watch.download.{id}` / `watch.download.index` | `tasks.*` | 与 aria2、本地下载器任务**合并为同一份记录** |
| `watch.aria2.task.{gid}` / `watch.aria2.index` | `tasks.*` | **消除双写**（tdl 中这两个 key 被 `app/aria2` 与 `app/webui` 双方写入） |
| `watch.internal.task.{id}` / `watch.internal.index` | `tasks.*` | 断点位置成为任务记录的一个字段 |
| `forward.job.{id}` / `forward.index` | `forward.*` | 加账号前缀 |

**收益**：13 条无主前缀 → 5 个有主数据集；3 份任务归属判定（`app/aria2/reconnect.go` 的 `IsTDLTask`、`app/webui/aria2.go:818-849` 的 `hasTDLDownloadURI`、`app/webui/downloads.go:480-513` 的 `downloadTaskIDFromURL`）收敛为 1 份。

### 5.3 gotd 适配器

`core/storage` 的三个适配器（`Session` → `telegram.SessionStorage`、`Peers` → `peers.Storage`、`State` → `updates.StateStorage`）迁入 `bsw/ecual/mediaif` 之外的位置：实际上它们只是 `nvm` 数据集到 gotd 接口的适配器，放在 `bsw/ecual/memif`。

**必须修掉的两个缺陷**：
1. `core/storage/state.go` 的 `SetPts` / `SetQts` / `SetDate` / `SetSeq` / `SetChannelPts` 全部是 Get → 改字段 → Set，**无任何同步**，并发调用互相覆盖。改为 `nvm` 提供的原子 `Update(key, mutate func([]byte) ([]byte, error))`。
2. `SetState` 会静默清空全部分频道 pts（`state.go:58`），无注释说明意图。要么明确文档化，要么改为不清空并单独提供 `ClearChannels`。

---

## 6. 关键设计专题

### 6.1 多账号（决策 2）：现在只实现单账号，但契约不欠债

**背景**：`namespace` 的本意是「同时监听多个账户」，但当前实现只是「一个进程一套会话，切换需重启」。用户要求保留这个概念并标为 TODO。

**核心设计判断**：多账号最大的成本不在 `tgauth` 里放 N 个会话，而在于**契约是否预留了账号维度**。如果端口签名、事件载荷、配置布局、存储 key 都不带账号，将来做多账号就是全项目破坏性变更（改端口 → 改所有实现 → 改所有测试）。所以：

> **现在就做**：所有跨账号的端口签名带 `acct AccountID`；所有事件载荷带 `Account`；所有 account-scoped 数据集的 key 带账号前缀；配置按 `Multiplicity` 分布局。
>
> **现在不做**：N 个并发会话、SWC 多实例化、账号切换 UI、按账号分组的视图。

**落地的 6 个「现在做」**

| # | 内容 | 位置 |
| --- | --- | --- |
| 1 | `AccountID` 类型 + `AccountInfo` | `interfaces/types/account.go` |
| 2 | 端口签名带 `acct`：`RangeSource`、`Executor`、`Notifier`、`comif.Lease` | `interfaces/ports/*` |
| 3 | 事件载荷带 `Account` | `interfaces/events/payload.go` |
| 4 | `Manifest.Multiplicity`（`single` / `per-account`）+ `InstanceID = {swcID}@{account}` | `interfaces/manifest/swc.go` |
| 5 | account-scoped 数据集的 key 前缀（`account.session.default` 而非 `session`） | `bsw/services/nvm` |
| 6 | 配置布局按 Multiplicity 分（`swc/{id}.json` vs `swc/{id}/{account}.json`） | `rte/config` |

**标为 TODO 的 8 项**（记录在 `docs/TODO-multi-account.md`，实施排在 M5 之后）

| TODO | 内容 | 依赖 |
| --- | --- | --- |
| M1 | `tgauth` 支持 N 个账号运行时：每账号独立会话、独立 DC 池 | 无 |
| M2 | 配额维度从 `(dc)` 改为 `(account, dc)`（`comif.Lease` 已带 acct，实现内加维度即可） | M1 |
| M3 | RTE 按账号表实例化 `per-account` SWC，实例 ID 为 `{swcID}@{account}` | M1、M4 |
| M4 | SWC 生命周期支持「实例级」启停：某账号登录失败只影响该账号的实例 | M3 |
| M5 | 配置实例化：`swc/{id}/{account}.json` 与 `_default.json` 的合并 | 无 |
| M6 | 控制面多账号：命令需要账号参数（缺省用默认账号），命令回复区分账号 | M3 |
| M7 | 面板：账号切换器 + 按账号分组的任务/队列视图 | M3 |
| M8 | 存储：`nvm` 的 account-scoped key 路由（**前缀已预留，只需实现路由**） | 无 |

**单账号下的行为**：`config/ecuc.json` 的 `default_account: "default"`，`config/swc/account.telegram/default.json` 是唯一账号配置。所有 SWC 只实例化一份，`acct` 恒为 `"default"`。**用户视角与今天完全一致。**

### 6.2 api 凭据（决策 5 的落地方案）

**现状问题回顾**（详见 §0.4）：所有走过 tdl 登录流程的账号都在使用 Telegram Desktop 官方 AppID 2040；用户无法查看也无法配置；`builtin` 只在缺键时兜底。

**决策原则：内置凭据作为默认，用户设置后覆盖。**

这条原则带来一个关键收益：**默认值与现状完全一致 ⇒ 老用户升级后无需重新登录，零迁移成本**。只有主动填写自己凭据的用户才需要重登一次 —— 这是凭据与会话强绑定的必然结果，无法绕过。

**配置形态**（凭据是登录 SWC 的参数；`account.telegram` 是 per-account 多实例）

```jsonc
// config/swc/account.telegram/default.json
{
  "enabled": true,
  "credential": {
    // 两项都留空（api_id = 0 且 api_hash 为空）→ 使用内置预设，与当前行为一致
    // 两项都填写 → 改用用户自己的凭据
    "api_id": 0,
    // api_hash 存 secrets.json → api_hash.default（敏感值，不写入普通配置）
    "builtin_preset": "desktop"   // 高级项：内置默认用哪一组，desktop（默认）| upstream
  }
}
```

```jsonc
// config/swc/account.telegram/_default.json（多账号时给所有账号共用的基底，可选）
{
  "credential": { "api_id": 12345678 }
}
```

**内置预设（相当于把当前的硬编码搬进配置，作为默认值）**

| 预设 | AppID | AppHash | 说明 |
| --- | --- | --- | --- |
| `desktop`（默认） | 2040 | `b18441a1ff607e10a989891a5462e627` | 当前项目实际在用的那组（Telegram Desktop 官方凭据） |
| `upstream` | 15055931 | `021d433426cbb920eeb95164498fe3d3` | 上游项目的应用，对应原 `builtin` |

**行为规则**

| 条件 | 行为 |
| --- | --- |
| `api_id == 0` 且 api_hash 为空 | 使用 `builtin_preset` 指定的内置凭据（默认 `desktop`）。**等同于今天 `GetApp` 的回退逻辑** |
| `api_id > 0` 且 api_hash 非空 | 使用用户凭据 |
| 只填了其中一项 | **校验失败**，配置页标红并拒绝保存（不静默回退，避免用户以为已生效） |
| `api_id < 0`，或 api_hash 不是 32 位十六进制 | 校验失败 |

**设计要点**

| 要点 | 做法 |
| --- | --- |
| 配置归属 | 凭据是登录 SWC（`account.telegram`）的参数，不再散落在 `pkg/tclient/app.go` 里。多账号下每个账号一份，可用 `_default.json` 共用 |
| 凭据与会话绑定 | `account` 数据集存 `app_credential` = `sha256(api_id + ":" + api_hash)` 前 16 字节（**不存明文**）。连接前比对：指纹不一致且已有会话 → 发布 `account.credentials.changed{NeedsRelogin: true}`，控制台与面板提示重新登录 |
| **风险提示（配置页内，不做弹窗）** | 内置预设都来自第三方（`desktop` 是 Telegram Desktop 官方凭据，`upstream` 是上游项目的应用）。在该分组顶部用 `ConfigField.Type = notice` 固定插入一段提示块（§4.4），内容含风险说明与 `my.telegram.org` 申请指引：「使用内置凭据属于借用他人应用，可能违反 Telegram API 条款并带来封号风险，建议申请自己的 api_id / api_hash 后填入此处」。**明确不做强制弹窗、不做二次确认**——提示只在配置页呈现，不打断任何操作流程 |
| **会话数据必须保留（硬要求）** | 用户改凭据导致会话失效时，**只标记 `NeedsRelogin`，绝不删除旧会话数据**。用户改回内置预设（或改回原凭据）后应立即恢复可用，不需要重新登录。这条是为了避免「误填错的凭据 → 可用会话被清掉 → 再也回不去」的不可逆损失。实现上：`account` 数据集的 `session` 键与 `app_credential` 指纹键**只写不删**，登录流程在写入新会话前不清理旧值 |
| 校验 | 见上表行为规则；半填与格式错误都由 schema 校验拦下 |
| 顺手修掉 | `pkg/tclient/tclient.go:25` 的 `kv.Get(context.TODO(), …)` 丢失调用链上下文 → 改为传 `ctx` |
| 需要改的测试 | `app/login/bot_auth_test.go:33` 与 `:44` 断言 `AppDesktop`，改为断言凭据指纹（走内置预设时，指纹应等于 `desktop` 预设的指纹） |

**迁移**：无需迁移动作，也无需过渡通道。旧数据里的 `app` 键（值为 `desktop` 或 `builtin`）在首次启动时被读取一次，转换为对应的 `builtin_preset` 写入新配置并记录指纹，之后该键废弃。**因为默认值与旧值一致，旧会话继续可用。**

**与上一版方案的差异**：上一版建议移除官方凭据、默认改为用户自填，代价是老用户升级即失去可用会话（需要一条只读兼容通道过渡一个完整版本周期）。改为「内置为默认、用户可覆盖」后，**这个迁移风险被消除**，同时保留了用户自主选择的能力。代价是默认路径仍带合规风险，因此 §10 的 R4 从「必须做迁移过渡」改为「必须做好风险提示与引导」。

### 6.3 执行器与任务中枢（决策 1 的落地）

**保留两个执行器**，`downloader.aria2` 与 `downloader.local`，都由内核的 `taskhub` 按优先级选择：

```jsonc
// config/bsw/taskhub.json
{
  "executor_priority": ["aria2", "local"],  // 第一个可用的被执行
  "auto_fallback": true,                     // aria2 不可用时自动降级到 local
  "task_ttl_hours": 24,                      // 0 = 永久
  "index_flush_seconds": 5                   // 索引异步落盘间隔（缓解单写者瓶颈）
}
```

取代 `config.Downloader.Mode` 的硬编码二选一。用户仍可选择「只用本地下载器」—— 配置 `executor_priority: ["local"]`。

**命名统一**：

| 旧 | 新 |
| --- | --- |
| 「内部下载器」 | **本地下载器** |
| SWC ID `downloader.internal` | `downloader.aria2` 的对位：**`downloader.local`** |
| 配置 `downloader.mode: "internal"` | `executor_priority: ["local"]` |
| 控制台命令 `/internal_*` | `/local_*`（建议保留 `/internal_*` 作为别名一个版本周期） |
| 文件 `app/watch/internal_downloader_*.go` | `application/downloader.local/*.go` |
| 配置 `swc/downloader.internal.json` | `swc/downloader.local.json` |

**任务生命周期（统一后）**：

```
触发 SWC 发布 trigger.download.intent
  → taskhub 订阅，调 FilterRules.ShouldHandle（同步端口）
  → 调 NamingRules.Render（同步端口）
  → 建 Task，写 tasks 数据集，发布 task.created
  → ExecutorSelector 选可用执行器，Submit
      aria2：RangeSource.Register → URL → aria2.addUri
      local：RangeSource.Register → Open → 本地落盘
  → 执行器通过 taskhub.Report 回写进度与终态（执行器不直接写库）
  → 发布 task.progress / task.finished / task.failed
  → 订阅者响应：notify.telegram 发消息、panel.webui 刷新、download.aria2 治理器消费 stream.error
```

### 6.4 AriaNg 归属（决策 4 的落地）

AriaNg 不再是被删除的 vendored 产物，而是 `downloader.aria2` SWC 自己的一个页面。

```
application/downloader.aria2/
├── manifest.go            声明 3 个页面
├── plugin.go              生命周期
├── client.go              出站 RPC 客户端（迁移 app/aria2/client.go）
├── rpcproxy.go            AriaNg 用的入站 RPC 反向代理（迁移 webui/aria2.go 的代理与改写）
├── control.go             Executor 实现（迁移 app/aria2/control.go）
├── manager.go             治理器编排（迁移 app/aria2/manager.go）
├── regulator.go           错误调节器
├── speed_monitor.go       零速看门狗
├── reconnect.go           重连挂起与恢复
└── web/
    ├── aria2ng.html       AriaNg 单文件产物（从 webui/ 迁入）
    └── tasks.html         SWC 自己的精简任务页
```

Manifest 声明的页面：

| Kind | slug | 标题 | 导航 |
| --- | --- | --- | --- |
| `feature` | `tasks` | aria2 任务 | 是（分组「下载器」） |
| `feature` | `ariang` | AriaNg 完整界面 | 是（分组「下载器」） |
| `config` | — | （由 schema 自动生成） | 是（分组「下载器」） |

**顺带修掉一处重复（tdl 的 P0-2）**：当前 `app/webui/aria2.go` 与 `app/aria2/client.go` 是两套 RPC 实现，已漂移（`tdl-webui-aria2` vs `tdl-watch-aria2` 的 user-agent、重试策略、超时、`continue/allow-overwrite` 参数）。归入同一 SWC 后，**出站客户端与 AriaNg 代理共用同一个 RPC 层与同一份 `applyTDLHTTPConnectionOptions`**，漂移不可能再发生。

路由与资源：

| 用途 | 路径 |
| --- | --- |
| AriaNg 页面 | `/p/downloader.aria2/ariang` |
| AriaNg 静态资源 | `/plugins/downloader.aria2/assets/aria2ng.html` |
| AriaNg 的 RPC 端点（注入 token、改写 addUri 参数） | `/plugins/downloader.aria2/rpc` |

### 6.5 自更新范围（决策 3 的落地）

| 能力 | 处置 |
| --- | --- |
| 版本检查（GitHub Releases API） | **保留** |
| 新版本通知（面板横幅 + 控制台消息） | **保留** |
| 原生二进制自更新（下载 → 校验 → 原子替换 → 以原参数重启） | **保留** |
| `/reboot` 重启 | **保留** |
| **容器内原地替换二进制 + 以原 PID 启动** | **删除** |
| `isDockerVersion` / `isDockerRuntime` / `releaseVersionForCompare` 的 docker 分支 | **删除** |
| `docker.yml` 的 `VERSION=<tag>_docker` | **删除**，镜像与二进制共用 `vX.Y.Z` |
| 容器检测 | **改为仅用于 UI 提示**：容器内时面板把「应用更新」置灰，提示「请拉取新镜像并重启容器」 |
| `consts.GOARM` ldflags 注入 | **保留注入**（成本为零）。它从「自更新正确性依赖」降级为「版本信息完整性」 |

**副作用（正面）**：这一决策一次性消掉了 tdl 的一处 P0（`Dockerfile:25-28` 漏注入 `GOARM` 导致 ARM 容器内自更新取错架构包）以及一串隐式契约 —— `_docker` 版本后缀、`TDL_DOCKER` 环境变量的语义耦合、`release.yml` 里针对容器版本的比较分支。同时让发布流程简化为一套版本号。

---

## 7. 文件级迁移映射

### 7.1 `app/` 的归属

| tdl 路径 | 目标位置 | 处理方式 |
| --- | --- | --- |
| `app/runtime/manager.go` | `rte/registry` + `bsw/services/bswm` | **重写**。800 行 `applyConfigLocked` 拆成装配校验 + 各 SWC 自己的 `Reconfigure` |
| `app/runtime/manager_test.go` | `bsw/services/bswm` 测试 | 迁移并扩充（拓扑排序、依赖缺失、SWC 失败隔离） |
| `app/bot/bot.go` | `application/console.bot/` | **重写**。长轮询与命令分发保留，命令实现下沉到各 SWC 注册 |
| `app/bot/login_manager.go` | `application/account.telegram/` | 迁移 |
| `app/bot/allowed_users.go`、`helpers.go`、`telego_logger.go` | `application/console.bot/` | 迁移（权限属控制面） |
| `app/bot/notifier.go`、`aria2_events.go` | `application/notify.telegram/` | 迁移，批处理与限流保留 |
| `app/bot/download_commands.go` | 下沉到 `downloader.aria2` + `downloader.local` | **拆分**，各自注册命令 |
| `app/bot/aria2_commands.go` | `application/downloader.aria2/` | 迁移为命令注册 |
| `app/bot/internal_commands.go` | `application/downloader.local/` | 迁移，命令改 `/local_*` |
| `app/bot/forward_commands.go` | `application/forwarder/` | 迁移为命令注册 |
| `app/bot/update_commands.go` | `application/update.self/` | 迁移为命令注册 |
| `app/bot/kv_commands.go`、`kv_cleaner.go` | **删除**（或降级为 `dcm` 的一个清理动作） | 见 §11.4 |
| `app/watch/watch.go` | 拆三处：连接与 update 分发 → `bsw/cdd/tgauth`；装配 → `rte`；启动横幅 → 各 SWC 自打印 | **拆解** |
| `app/watch/reactions.go` | `application/trigger.reaction/` | 迁移 |
| `app/watch/message_link.go` | `application/trigger.messagelink/` | 迁移 |
| `app/watch/filter.go` | `application/filter.rules/` | 迁移为实现 `FilterRules` 端口 |
| `app/watch/download_dir.go`、`filename.go` | `application/naming.rules/` | 迁移为实现 `NamingRules` 端口 |
| `app/watch/jobs.go` | 拆两处：媒体收集与相册展开 → `bsw/ecual/mediaif`；任务构建与提交 → `bsw/cdd/taskhub` | **拆解** |
| `app/watch/controller.go` | **删除** | 启停由 `bswm` 统一管理，不需要每个 SWC 自带 Controller |
| `app/watch/forward.go` | `application/forwarder/` | 迁移 |
| `app/watch/options.go` | 各 SWC 的 `ConfigView` | **拆解**，37 字段按归属切开 |
| `app/watch/updates.go`、`runtime.go` | `bsw/cdd/tgauth` | 迁移 |
| `app/watch/internal_downloader*.go`（7 文件） | `application/downloader.local/` | 迁移为实现 `Executor` 端口；状态写入交给 `taskhub` |
| `app/http/http.go` | `application/proxy.range/` | 迁移，Range 语义**原样保留** |
| `app/http/controller.go` | `application/proxy.range/`（生命周期部分 → `bswm`） | 拆分 |
| `app/http/session.go`、`telegram_source.go` | `application/proxy.range/` | 迁移，分块并行与瞬时重试**原样保留** |
| `app/http/transfer/scheduler.go` | `bsw/ecual/comif` | **迁入内核**，泛化为「按 (account, dc) 配额 + 按文件 FIFO」 |
| `app/http/task_store.go`、`delivery_status.go` | `bsw/cdd/taskhub` | **迁入内核**，唯一写入者的实现 |
| `app/http/metrics.go` | `bsw/services/dem`（错误与计数）+ `dcm`（暴露） | **拆解** |
| `app/aria2/client.go` | `application/downloader.aria2/client.go` | 迁移，理顺两套重叠的重试策略 |
| `app/aria2/control.go` | `application/downloader.aria2/control.go` + `taskhub`（归属判定） | **拆解** |
| `app/aria2/manager.go` | `application/downloader.aria2/plugin.go` | 迁移为 SWC 生命周期 |
| `app/aria2/regulator.go`、`speed_monitor.go` | `application/downloader.aria2/` | 迁移，抽公共部分；错误源改为订阅 `dem` |
| `app/aria2/reconnect.go` | `application/downloader.aria2/` | 迁移，去掉别名层 |
| `app/aria2/task_store.go` | **删除**，并入 `taskhub` | 消除双写的一半 |
| `app/forward/queue.go`、`store.go` | `application/forwarder/` + `nvm`（`forward` 数据集） | 迁移，全局单例 `Jobs()` 改 SWC 实例 |
| `app/forward/elem.go`、`forward.go` | `application/forwarder/` | 迁移 |
| `app/webui/server.go` | 拆两处：壳与认证 → `bsw/ecual/hmiif`；路由清单 → 各 SWC 注册 | **拆解** |
| `app/webui/auth.go`、`assets.go` | `bsw/ecual/hmiif` | 迁移，**修客户端识别**（可信代理 + `X-Forwarded-For`） |
| `app/webui/login.go` | `application/account.telegram/` | 迁移 |
| `app/webui/users.go` | `application/account.telegram/` | **拆解**，spam 检测移出 handler 并复用已登录会话 |
| `app/webui/config_api.go` | `rte/config`（读写 API）+ `hmiif`（配置页渲染） | **拆解**，reflect 任意路径写入改为 schema 驱动 |
| `app/webui/dashboard.go` | `dcm`（指标聚合）+ `panel.webui`（概览页） | **拆解** |
| `app/webui/downloads.go` | `panel.webui` + `taskhub` 读取端口 | **拆解**，handler / 业务 / 数据访问三层分开 |
| `app/webui/forwards.go` | `panel.webui` + `forwarder` 端口 | 拆解 |
| `app/webui/aria2.go` | `application/downloader.aria2/`（合并进 `client.go` 与 `rpcproxy.go`） | **删除独立实现**，解决 P0-2 |
| `app/webui/aria2ng.html` | `application/downloader.aria2/web/aria2ng.html` | 迁移（决策 4） |
| `app/webui/static/js/*`、`css/*`、`views/*` | `bsw/ecual/hmiif/web/`（壳）+ 各 SWC 的 `web/` | 重新组织 |
| `app/updater/*` | `application/update.self/` | 迁移，**删除容器内自更新路径**（决策 3） |
| `app/download/submission.go` | `interfaces/ports/executor.go` + `interfaces/types/task.go` | **升级为契约类型** |

### 7.2 `pkg/` 的归属

| tdl 路径 | 目标位置 | 处理方式 |
| --- | --- | --- |
| `pkg/config/config.go` | `rte/config` + 各 SWC 的 `Config` 声明 | **拆解**。类型按 SWC 切开；`Validate` 改 schema 驱动；`Get()` 单例改 `ConfigView` 注入 |
| `pkg/config/ntp.go` | `bsw/cdd/tgauth` | 迁移（NTP 是 MTProto 连接参数） |
| `pkg/consts/consts.go`、`path.go` | `bsw/ecual/fsif` | **重写**。去掉 `init()` 副作用与 panic，改显式 `Init(home) (Paths, error)` |
| `pkg/consts/version.go` | `bsw/services/dcm/version` | 迁移，版本输出补上 `GOARM` |
| `pkg/kv/kv.go` | `bsw/services/nvm`（数据集接口） | 保留设计，增加所有权与 scope |
| `pkg/kv/bolt.go` | `bsw/mcal/boltdb` | 保留，抽出独立的 bucket 层（去掉 `legacyKV` 类型复用） |
| `pkg/kv/legacy.go`、`file.go` | **删除** | 新项目无历史数据，只保留 bolt 单驱动 |
| `pkg/kv/kv_enum.go` | **删除** | 单驱动后不需要枚举生成 |
| `pkg/kv/kv_test.go` | `bsw/mcal/boltdb` 测试 | **保留**（「`Get` 必须返回自有拷贝」的断言很重要） |
| `pkg/key/key.go` | **删除**（并入 `nvm` 数据集定义） | 单函数包 |
| `pkg/filterMap/filterMap.go` | **删除**（内联到 `filter.rules`） | 9 行单函数包 |
| `pkg/validator/validator.go` | **删除**（内联非空判断） | 16 行包，只为一个非空校验引入第三方依赖 |
| `pkg/utils/byte.go` | `bsw/services/dlt/format` | 迁移 |
| `pkg/ps/ps.go` | `bsw/services/dcm` | 迁移，去掉 `init()` panic |
| `pkg/tclient/app.go` | `application/account.telegram/credentials.go` | **重写**（决策 5，见 §6.2） |
| `pkg/tclient/tclient.go` | **删除**（并入 `tgauth` 与 `account.telegram`） | 消除与 `core/tclient` 的同名冲突 |
| `pkg/tplfunc/*` | `application/naming.rules/tplfunc/` | 迁移（模板函数只服务于命名） |

### 7.3 `core/` 的归属

| tdl 路径 | 目标位置 | 处理方式 |
| --- | --- | --- |
| `core/tclient/tclient.go` | `bsw/mcal/tgconn` | 迁移。DC 列表/公钥的包级变量改显式配置 |
| `core/dcpool/*` | `bsw/cdd/tgauth` | 迁移。**修 `Close()` 竞态与关闭期 `context.TODO()` 网络 IO** |
| `core/storage/storage.go` | `bsw/services/nvm`（接口） | 迁移 |
| `core/storage/session.go`、`peers.go`、`state.go` | `bsw/ecual/memif`（gotd 适配器） | 迁移。**修 `State` 的读-改-写无同步** |
| `core/storage/keygen/*` | `bsw/services/nvm` | 迁移，成为 key 命名**唯一**来源 |
| `core/tmedia/*` | `bsw/ecual/mediaif`（tg → MediaRef）+ `interfaces/types/media.go`（跨 SWC 类型） | **拆解** |
| `core/forwarder/*` | `application/forwarder/` | 迁移，`MaxPartSize` 常量自带定义 |
| `core/downloader/*` | **删除** | 编排层全仓零调用 |
| `core/uploader/*` | **删除** | 同上 |
| `core/util/mediautil/*` | **删除** | 唯一使用方是 `uploader` |
| `core/middlewares/takeout/*` | `bsw/mcal/tgconn` | 迁移，去掉硬编码 4GB 上限或改配置 |
| `core/middlewares/retry/*`、`recovery/*` | `bsw/mcal/tgconn` | 迁移，内部错误列表改具名常量 |
| `core/util/netutil/*` | `bsw/ecual/netif` | 迁移。**必须处理全局 `InsecureSkipVerify: true`** |
| `core/util/tutil/tutil.go` | 拆三处：链接解析 → `trigger.messagelink`；peer/消息/相册获取 → `tgauth`；`BestThreads` → `tgauth` | **拆解** |
| `core/util/tutil/device.go` | `bsw/mcal/tgconn` | 迁移 |
| `core/util/fsutil/*` | **删除** | `PathExists` 内联，其余零引用 |
| `core/util/logutil/*` | `bsw/services/dlt` | 与 `core/logctx` 合并 |
| `core/logctx/*` | `bsw/services/dlt` | 合并，删除零引用的 `Named` |
| `core/go.mod`、`go.sum` | **删除** | 单模块（决策 6） |

### 7.4 汇总

| 处理方式 | 数量级 |
| --- | --- |
| 直接删除 | `core/` 整个子模块（35 文件）+ 约 15 个文件/包（`webui/aria2.go`、`task_store.go`、`controller.go`、5 个单函数包、3 个旧 KV 驱动…） |
| 拆解（一个文件拆到多个 SWC） | 约 12 个（`watch.go`、`jobs.go`、`options.go`、`control.go`、`server.go`、`users.go`、`config_api.go`、`dashboard.go`、`downloads.go`、`metrics.go`、`tmedia`、`tutil`） |
| 原样迁移（核心算法） | 约 20 个（Range 语义、调度器、瞬时重试、区间合并、两个治理器、模板系统、forwarder、治理器…） |
| 重写 | 5 个（`manager.go`、`bot.go`、`tclient` 应用层、凭据、paths） |

---

## 8. 实施计划

### 8.1 里程碑

| 里程碑 | 内容 | 出口标准 |
| --- | --- | --- |
| **M0 契约定稿** | `interfaces/` 完整实现 + 2 个假 SWC | 跑通「声明端口 → 装配校验 → 收到事件」闭环 |
| **M1 框架可用** | `rte/` + `bsw/services` + `bsw/ecual/hmiif` | 能登录面板、看到 SWC 列表、用 schema 渲染并保存配置 |
| **M2 核心链路通** | `tgauth` + `taskhub` + `proxy.range` + `downloader.aria2` | 端到端完成一次 aria2 下载 |
| **M3 触发策略通** | `account.telegram` + `trigger.*` + `filter.rules` + `naming.rules` | 表情触发端到端可用，命名行为与 tdl 一致 |
| **M4 全面替代** | `forwarder` + `console.bot` + `notify.telegram` + `panel.webui` + `downloader.local` | 功能对齐 tdl，每个 SWC 有独立配置页 |
| **M5 交付就绪** | `update.self` + `dem`/`dcm` + 构建链 + 迁移工具 + 文档 | 一条发布路径；迁移工具验收通过；CI 规则生效 |
| **M6 多账号（TODO）** | 见 §6.1 的 M1–M8 | 两个账号可同时监听，互不影响 |

### 8.2 P0：契约定稿

**目标**：不碰任何现有业务代码，用最低成本验证契约设计（因为 `interfaces/` 的接口签名是整个项目最难改的部分）。

- [ ] 新仓库骨架：`go.mod`（单模块）、`cmd/tdl/main.go`（空壳）、目录结构
- [ ] `interfaces/manifest/swc.go`：`Manifest`、`SWCType`、`Multiplicity`、`Provide`、`Require`、`Page`
- [ ] `interfaces/manifest/config.go`：`ConfigField`、`FieldType`、`Action`
- [ ] `interfaces/types/*`：`AccountID`、`MediaRef`、`ByteRange`、`TaskSpec`、`Task`、`Handle`、`Progress`、`Notice`
- [ ] `interfaces/ports/*`：9 个端口接口（**签名全部带 `acct`**）
- [ ] `interfaces/events/*`：主题常量与载荷（**载荷全部带 `Account`**）
- [ ] `rte/rte.go`：`Kernel` 门面
- [ ] `rte/registry`：注册、解析、类型校验、版本校验、拓扑排序
- [ ] `rte/eventbus`：发布订阅、请求响应、订阅者隔离
- [ ] `rte/config`（最小版）：schema 加载与 `ConfigView` 白名单
- [ ] `rte/schedule`（最小版）：周期与延时任务
- [ ] `bsw/services/bswm`（最小版）：`Validate` 阶段与 Init/Start/Stop 编排
- [ ] `application/swc.go` 集成点 + 假 SWC A（提供端口）+ 假 SWC B（消费端口 + 订阅事件）
- [ ] 失败路径测试：缺必需端口 / 类型不匹配 / 依赖成环 / 一个 SWC Init 失败 / 端口重复提供

**验收**

1. `go run ./cmd/tdl` 输出「已装载 2 个 SWC，装配校验通过」。
2. 删除假 SWC A 后启动，输出明确诊断：「SWC B 需要端口 rangesource（>=1.0），未被任何 SWC 提供」。
3. SWC B 的 `Init` 返回错误时进程不退出，A 仍 `Running`，B 为 `Failed`。
4. 清空 `application/swc.go` 后仍能编译，启动输出「已装载 0 个 SWC」。
5. 依赖方向检查脚本可运行（§9 规则 1）。

### 8.3 P1：框架可用

- [ ] `bsw/ecual/fsif`：显式初始化，去掉 `init()` 副作用与 panic
- [ ] `bsw/services/dlt`：合并 `core/logctx` + `core/util/logutil`，SWC 维度命名与轮转
- [ ] `rte/config`：默认值合并、schema 校验、原子保存、热更新分发、**版本迁移**
- [ ] `bsw/services/csm`：`secrets.json` 读写，0600，接口自动脱敏
- [ ] `bsw/mcal/boltdb`：bolt 单驱动（迁 `pkg/kv/bolt.go`，抽出独立 bucket 层）
- [ ] `bsw/ecual/memif`：gotd 适配器（迁 `core/storage`，**修 `State` 同步问题**）
- [ ] `bsw/services/nvm`：数据集注册、scope、写入所有权校验、`Update` 原子变更
- [ ] `bsw/services/schm`：周期任务、延时任务、命名任务组
- [ ] `bsw/services/dem`：错误记录、聚合、风暴检测（为 aria2 治理器提供输入）
- [ ] `bsw/services/dcm`：指标注册、健康检查聚合、诊断快照
- [ ] `bsw/services/wdgm`：SWC 心跳与卡死检测
- [ ] `bsw/ecual/httpif`：监听、路由挂载、前缀冲突检测
- [ ] `bsw/ecual/hmiif`：认证（含客户端识别修正）、导航生成、路由、页面容器
- [ ] `bsw/ecual/hmiif`：**配置页渲染器**（13 种控件 + 校验 + secret 语义）
- [ ] `bsw/ecual/hmiif/web`：前端壳（原生 ESM，含 `poll(view, ms, fn)` 与按视图分片的 state）
- [ ] 一个完整的示例 SWC（功能页 + 配置页 + 状态卡片）

**验收**：不写任何前端代码，示例 SWC 的导航项、功能页、配置页自动出现；类型错误与范围越界在保存时被拒绝；改示例 SWC 配置不影响其他 SWC。

### 8.4 P2：核心链路

- [ ] `bsw/mcal/tgconn`：客户端构造、DC 列表、密钥、时钟（迁 `core/tclient`）
- [ ] `bsw/mcal/tgconn`：takeout、retry、recovery 中间件（迁 `core/middlewares`）
- [ ] `bsw/cdd/tgauth`：DC 池（迁 `core/dcpool`，**修 Close 竞态与关闭期网络 IO**）
- [ ] `bsw/cdd/tgauth`：会话持有、update 分发、peer 解析、单条/相册消息获取（迁 `tutil`）
- [ ] `bsw/ecual/comif`：**`Lease` 配额仲裁**（迁并泛化 `app/http/transfer/scheduler.go`，维度改 `(account, dc)`）
- [ ] `bsw/ecual/netif`：代理 dialer（迁 `core/util/netutil`，**处理 `InsecureSkipVerify`**）
- [ ] `bsw/ecual/mediaif`：tg → `MediaRef`（迁 `core/tmedia`）
- [ ] `bsw/cdd/taskhub`：任务 CRUD、`Report` 回写、执行器选择、事件发布、索引异步落盘
- [ ] `application/proxy.range`：Range 语义（**原样迁移**）
- [ ] `application/proxy.range`：分块并行与瞬时错误重试（原样迁移）
- [ ] `application/proxy.range`：已发送区间上报（改为调 `taskhub.Report`）
- [ ] `application/downloader.aria2`：RPC 客户端（迁 `app/aria2/client.go`）
- [ ] `application/downloader.aria2`：`Executor` 实现
- [ ] `application/downloader.aria2`：两个治理器 + 重连恢复

**验收**

1. 通过 aria2 完成一次完整下载，文件内容校验一致。
2. `pool_size` 上限在并发压测下不被突破（复用 tdl 的 `scheduler_test.go` 12 个用例作为基线）。
3. 静态检查断言：`tasks` 数据集的 `Set`/`Delete` 调用点全部位于 `bsw/cdd/taskhub`。
4. 中断下载后重启，断点续传行为正确。
5. 触发一次 `telegram.stream.error` 风暴，验证 `dem` 记录并被治理器消费。

### 8.5 P3：触发与策略

- [ ] `application/account.telegram`：会话校验、登录状态机（**保留临时会话策略**）
- [ ] `application/account.telegram`：**凭据配置改造**（§6.2）：schema（`api_id` / api_hash / `builtin_preset`）、内置预设表、用户覆盖规则（全空用内置、全填用用户、半填报错）、凭据指纹、配置页 `notice` 提示块与申请指引（**不做弹窗**）、旧 `app` 键的一次性转换
- [ ] `application/account.telegram`：**会话数据只写不删**（§6.2 硬要求）：改凭据只标记 `NeedsRelogin`，改回内置预设应立即恢复可用
- [ ] `application/account.telegram`：面板登录端点（迁 `webui/login.go`、`users.go` 登录部分）
- [ ] `application/account.telegram`：控制台登录入口（迁 `bot/login_manager.go`）
- [ ] `application/account.telegram`：spam 检测（移出 handler，复用已登录会话）
- [ ] `application/trigger.reaction`：reaction 与编辑消息监听、去重窗口、意图发布
- [ ] `application/trigger.messagelink`：5 种链接形态校验与解析
- [ ] `application/filter.rules`：`FilterRules` 实现（**保留「扩展名先判、再判大小」顺序**）
- [ ] `application/naming.rules`：模板系统（迁 `download_dir.go` + `filename.go` + `tplfunc`）
- [ ] `application/naming.rules`：字节级长度上限、`I` 优先缩短、同批冲突去重

**验收**：tdl 的 `download_dir_test.go`（13 用例）、`watch_filter_test.go`、`reaction_test.go`（11 用例）、`message_link_test.go` 改造成新 SWC 测试并全部通过 —— 这是「行为对齐」的硬证据。

### 8.6 P4：控制、输出与面板

- [ ] `application/forwarder`：队列（迁 `app/forward`，全局单例改 SWC 实例）
- [ ] `application/forwarder`：direct / clone 两种模式（迁 `core/forwarder`）
- [ ] `application/forwarder`：监听对象与表情触发（迁 `watch/forward.go`）+ 注册 `/forward` 命令
- [ ] `application/console.bot`：控制面宿主（长轮询、命令注册表、权限校验、回调键盘）
- [ ] `application/console.bot`：接收各 SWC 的命令注册
- [ ] `application/notify.telegram`：四类通知 + 实时进度（迁 `bot/notifier.go`、`aria2_events.go`）
- [ ] `application/downloader.local`：本地下载器（迁 `internal_downloader*.go`）+ 注册 `/local_*` 命令
- [ ] `application/panel.webui`：仪表盘、任务列表、转发队列、账号页、更新页
- [ ] `application/downloader.aria2`：AriaNg 页面 + RPC 反向代理（§6.4）

**验收**

1. 转发（direct 失败降级 clone、相册整体转发、去重 TTL）行为与 tdl 一致。
2. bot 全部命令可用，且命令实现在各自 SWC 里，`console.bot` 不含任何下载/转发业务逻辑。
3. 面板业务页可用，`panel.webui` 没有任何存储直连。
4. AriaNg 页面可正常浏览与操作 aria2，且其 RPC 与 SWC 出站客户端共用同一实现。
5. 本地下载器与 aria2 都可用；`executor_priority` 切换与 `auto_fallback` 生效。

### 8.7 P5：收尾

- [ ] `application/update.self`：版本检查 + 通知 + 原生自更新 + 容器内提示（§6.5）
- [ ] `bsw/services/dcm` 面板页：指标、健康检查、SWC 诊断快照
- [ ] 构建链统一：Makefile 薄包装 + GoReleaser 单一配置 + Dockerfile 复用同一份 ldflags；`docker.yml` 去掉 `_docker` 版本后缀
- [ ] `tdl migrate-config`：旧 `config.json` → 新配置目录（依据 §4.3）
- [ ] CI：lint + build + test + §9 的三条强制规则
- [ ] 文档：README、**SWC 开发指南**（怎么写一个新 SWC）、配置参考（从 schema 自动生成）
- [ ] `docs/TODO-multi-account.md`：§6.1 的 M1–M8
- [ ] 清理：确认 §7.4 的删除清单已全部移除

### 8.8 依赖与并行

```
P0 ──┬── P1 ──┬── P2 ──┐
     │        │        ├── P4 ── P5 ── M6(TODO)
     │        └── P3 ──┘
     │
     └─ 契约稳定后 P2 与 P3 可并行（两者的接口在 P0 已定稿）
```

**P2 与 P3 可以并行** —— 这是插件化的第一个实际收益：契约定稿后，模块可以分头开发而不互相阻塞。

---

## 9. 工程规范与 CI 强制规则

### 9.1 三条必须机器强制的规则

```bash
# 规则 1：分层依赖方向（§3.3 的层号表）
# 按目录前缀判定层号，import 的层号必须 <= 自身层号
#   mcal=1  ecual=2  services/cdd=3  rte=4  application=5  cmd=6
# 附加：application/* 之间、以及 application 内部相互 import 必须为 0
# 实现建议：用 go/packages 遍历，逐包比对

# 规则 2：数据集唯一写入者
# 运行时：nvm.RegisterDataset(name, writer) 重复注册即启动失败
# 静态：grep 出所有数据集的 Set/Delete 调用点，校验其所在包 == 注册的 writer

# 规则 3：热路径不依赖事件总线
# ports.RangeSource 的实现不得 import rte/eventbus
grep -rn 'rte/eventbus' application/proxy.range/ && exit 1 || true
```

### 9.2 命名规范

| 项 | 规范 |
| --- | --- |
| SWC ID | `<域>.<对象>`，域取固定集合：`account` / `trigger` / `filter` / `naming` / `proxy` / `downloader` / `forwarder` / `console` / `notify` / `panel` / `update` |
| SWC 目录名 | 与 SWC ID 完全一致，如 `application/downloader.aria2/` |
| 端口名 | 大驼峰接口名，常量同名小写（`ports.RangeSource = "rangesource"`） |
| 事件主题 | `域.对象.动作`，全小写点分 |
| 配置字段 | 扁平用 `snake_case`；嵌套用点分（`regulator.window_seconds`） |
| 数据集名 | 小写单词（`account`、`telegram`、`tasks`、`forward`、`secrets`） |
| BSW 模块名 | 沿用 AUTOSAR 缩写（`ecum`/`bswm`/`schm`/`nvm`/`csm`/`dem`/`dcm`/`wdgm`/`dlt`），在 `docs/` 里给出全称对照 |
| 错误 | 统一 `go-faster/errors`，禁止混用 `fmt.Errorf`（tdl 现状混用） |
| 版本 | 语义版本；破坏性变更必须提升 `Require.Version` 下限 |

### 9.3 每个 SWC 的目录约定

```
application/<swc.id>/
├── manifest.go      Manifest() —— 唯一描述入口
├── plugin.go        Init / Start / Reconfigure / Stop / Status
├── config.go        自己的 ConfigView 类型（可选，复杂配置时拆出）
├── <业务文件>.go
├── web/             静态资源（可选）
└── *_test.go
```

---

## 10. 风险与应对

| # | 风险 | 影响 | 应对 |
| --- | --- | --- | --- |
| R1 | **过度抽象**：13 SWC + 9 端口 + 事件总线，可能比现状更难读懂「一次下载发生了什么」 | 维护成本上升 | 维护一条「阅读路径」文档，只描述从触发到落盘的 6 步；全砍可砍项后 SWC 数为 13（决策 1 与 4 都选择了保留，因此不会更少） |
| R2 | **性能退化**：事件总线与端口层被误用到热路径 | 下载吞吐下降 | 写死规范 + CI 规则 3；字节流、分块下载、进度采样禁止过总线 |
| R3 | **多账号契约欠债**：现在不做多账号，将来发现签名不够用 | 破坏性变更 | §6.1 的 6 个「现在做」就是为此；端口与事件**从一开始带 account 维度**，存储 key 现在就用账号前缀 |
| R4 | **内置凭据的合规与封号风险（已知残留）** | 默认路径仍在使用第三方应用（`desktop` 是 Telegram Desktop 官方凭据，`upstream` 是上游项目应用） | 决策 5 选择保留内置作为默认，以换取老用户零迁移成本；提示强度也已确认：**只在登录 SWC 的配置页用 `notice` 字段提醒，不做强制弹窗**。另一条硬要求是改凭据后不删除旧会话数据、允许改回内置预设。此项为已知风险，需你知悉并接受 |
| R5 | **`taskhub` 成为瓶颈**：单写者 + 单 mutex | 面板刷新与命令响应变慢 | 内存态作为读路径主副本，KV 只做持久化；索引按 `index_flush_seconds` 异步批量落盘（tdl 现状的 `TaskStore.Records` 是 N+1 次 KV 读） |
| R6 | **`Lease` 泛化后语义漂移** | 破坏 `pool_size` 保证，触发 Telegram 风控 | 以 tdl 的 `scheduler_test.go` 12 个用例为回归基线；维度从 `(dc)` 改 `(account, dc)` 时单账号行为必须完全不变 |
| R7 | **迁移过程长，新旧并行** | 中途放弃或长期双维护 | P0 成本极低（只有接口与假 SWC，不动业务）；P2 起每阶段都能独立交付价值，不做一次性大爆炸切换 |
| R8 | **配置页生成的表达力上限** | 复杂交互（规则列表带优先级、模板实时预览）生成不出来 | `ConfigField` 留 `Action` 逃生舱；保证 90% 普通配置零代码 |
| R9 | **AUTOSAR 术语增加理解成本** | 接手者要先学 BSW 缩写 | `docs/` 提供术语对照表（§11.3）；SWC 目录名用业务语义（`downloader.aria2`），只有内核层用 AUTOSAR 缩写 |
| R10 | **单人维护 13 SWC + 13 内核组件** | 精力分散 | SWC 按需实现，不一次全建；内核优先复用成熟库（zap、bbolt） |

---

## 11. 附录

### 11.1 内核组件清单（11 服务 + 2 CDD）

| 层 | 模块 | 单一职责 |
| --- | --- | --- |
| Services | `ecum` | 启动/关机阶段编排 |
| Services | `bswm` | SWC 状态机、依赖拓扑、启停与重启决策 |
| Services | `schm` | 周期与延时任务、命名任务组 |
| Services | `nvm` | 数据集注册、scope、写入所有权、持久化 |
| Services | `csm` | 敏感值存储与脱敏 |
| Services | `dem` | 诊断事件记录、聚合、风暴检测 |
| Services | `dcm` | 健康检查聚合、指标暴露、诊断快照 |
| Services | `wdgm` | SWC 心跳与卡死检测 |
| Services | `dlt` | 结构化日志、轮转、SWC 维度命名 |
| ECUAL | `httpif` | HTTP 监听、路由挂载、冲突检测 |
| ECUAL | `memif` | bbolt → 数据集句柄；gotd 适配器 |
| ECUAL | `fsif` | 文件系统与路径 |
| ECUAL | `netif` | 代理 dialer、超时、传输 |
| ECUAL | `comif` | DC 通道抽象 + `Lease` 配额 |
| ECUAL | `mediaif` | Telegram 媒体 → `MediaRef` |
| ECUAL | `hmiif` | 面板骨架 + 配置页渲染器 |
| MCAL | `tgconn` | MTProto 客户端构造、DC 列表、密钥、时钟、中间件 |
| MCAL | `boltdb` | bbolt 文件访问与事务 |
| CDD | `taskhub` | 任务中枢（唯一写入者）+ 执行器选择 |
| CDD | `tgauth` | 账号注册表：会话、DC 池、配额 |

### 11.2 复用 tdl 的测试资产（行为对齐基线）

| tdl 测试 | 迁移到 | 覆盖行为 |
| --- | --- | --- |
| `app/http/transfer/scheduler_test.go`（12） | `bsw/ecual/comif` | DC 配额、文件 FIFO、取消不漏许可 |
| `app/http/http_test.go`（约 40） | `application/proxy.range` | Range 语义、多 Range、If-Range、重试、慢写不占许可 |
| `app/watch/download_dir_test.go`（13） | `application/naming.rules` | 模板渲染、长度上限、冲突去重、路径风格 |
| `app/watch/watch_filter_test.go`（3） | `application/filter.rules` | 扩展名优先、大小区间 |
| `app/watch/reaction_test.go`（11） | `application/trigger.reaction` | 触发判定、去重、编辑消息路径 |
| `app/watch/message_link_test.go`（2） | `application/trigger.messagelink` | 链接形态校验 |
| `app/forward/queue_test.go`（10） | `application/forwarder` | 队列状态机、退避、TTL 清理 |
| `app/aria2/client_test.go`（14） | `application/downloader.aria2` | RPC 编解码、重试、超时 |
| `app/aria2/control_test.go`（4） | `application/downloader.aria2` | 任务归属、批量操作 |
| `app/aria2/regulator_test.go`（3）、`speed_monitor_test.go`（5） | `application/downloader.aria2` | 治理器判定逻辑 |
| `app/watch/internal_downloader_test.go`（11） | `application/downloader.local` | 断点续传、中断重排队、目录回退 |
| `pkg/kv/kv_test.go` | `bsw/mcal/boltdb` | **`Get` 返回自有拷贝**、并发写不丢 key |
| `app/runtime/manager_test.go`（3） | `bsw/services/bswm` | 陈旧配置不误停、SWC 隔离（需扩充） |
| `app/updater/updater_test.go` | `application/update.self` | 版本比较、资产选择（**去掉 docker 用例**） |
| `app/webui/server_test.go`（27） | `bsw/ecual/hmiif` + 各 SWC | 认证、路由、配置页（需按新结构拆分） |
| `app/login/bot_auth_test.go` | `application/account.telegram` | 临时会话提交（**断言改为凭据指纹**） |
| `core/dcpool/dcpool_test.go` | `bsw/cdd/tgauth` | 池化与 takeout（补 Close 竞态用例） |

### 11.3 待你确认的剩余事项

| # | 事项 | 说明 |
| --- | --- | --- |
| 1 | `/internal_*` 命令是否保留一个版本周期的别名 | 重命名为 `/local_*` 后，老用户的命令习惯会失效 |
| 2 | 前端是否引入构建链 | 倾向先不引入；把 33 字段全局 `state` 分片与重复轮询收敛做完再评估 |
| 3 | `bot/kv_commands.go` 的 `/clean_kv` 与 KV 链接管理页 | 建议删除（KV 成为框架内部实现），或降级为 `dcm` 的一个清理动作 |
| 4 | SWC 状态展示是否需要「实例」维度 | 单账号下不需要，多账号（M6）下必须有；现在可在 `Status` 里预留 |

> 已确认并固化：内置凭据的提示**只在登录 SWC 的配置页给出，不做强制弹窗**（见 §6.2）；改凭据后**不删除旧会话数据、只标记需重登**，允许改回内置预设继续使用。

### 11.4 建议的下一步

先做 **P0**。它只写 `interfaces/` 模块 + `rte` 的 registry/eventbus/schedule 最小集 + 2 个假 SWC + `application/swc.go` 集成点，**不改任何现有 tdl 代码**。成本最低，但能把契约设计的问题提前暴露 —— 因为 `interfaces/` 的接口签名是整个项目最难改的部分。

如果你想先看契约长什么样，我可以直接把 `interfaces/` 的接口草案写出来（比本文的摘录更完整），改起来还没有成本。

