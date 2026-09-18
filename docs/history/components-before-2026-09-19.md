# 组件开发与迁移说明

任务存储已迁入 `bsw/cdd/taskhub`：本地任务、HTTP 链接、aria2、转发记录及其索引通过统一仓库写入，保持原键名和 JSON 格式。HTTP 的到期判断、活动时间刷新和已发送区间合并在同一事务内完成，不再依赖各实例的缓存锁。WebUI 更新状态只修改其负责的字段，延迟的状态报告不能复活已删除任务，元数据刷新不会回退活动时钟。清理操作检查当前记录，不使用旧快照覆盖索引。

`bsw/services/nvm` 提供数据集注册和读写能力：重复名称、重叠前缀/精确键和错误写入者被拒绝，只读句柄不能转成写句柄，事务内也执行作用域检查。taskhub 封装四个兼容任务数据集；tgauth 封装 `session`、`app`、`account.credentials.fingerprint` 三个精确键，不授权相似前缀。客户端会话写回、成功登录提交和 WebUI 删除会话均使用受限句柄；提交和删除在同一事务中处理三个键。peer、update-state 及 access hash 也已通过独立受限句柄接入监听。Bot 的旧清理命令通过 taskhub 的维护入口处理任务记录，保留空任务索引，并跳过快照后发生变化的数据。

转发队列不再使用全局单例：运行宿主创建一个 namespace 队列并注入 Bot、watch 和 WebUI，通知回调和 worker 也绑定该实例。独立启动的服务创建自己的队列，组合启动必须注入同一实例以保持单 worker。队列状态机已迁入 SWC，Telegram 传输实现保留在协议适配层。

`bsw/ecual/comif` 接管原 HTTP transfer 包的全局文件数和按 DC 分片配额调度器及测试，HTTP 和本地下载共用相同实现；生产本地下载沿用代理持有的同一调度实例。调度器不依赖业务包，配额支持热更新；统一账号资源宿主仍需继续迁移。

`application/account.telegram` 提供凭据端口。兼容配置中的 `telegram.api_id` 与 `telegram.api_hash` 必须成对填写；`builtin_preset` 为空时沿用会话标记，新登录仍默认 desktop；`use_builtin` 可临时恢复内置凭据而保留自有值。WebUI 不返回 API Hash，留空保存表示保留原值。兼容配置文件按 0600 创建；新的组件配置将 API Hash 和可能包含认证信息的更新代理地址分离到 secret 文件。

`bsw/cdd/tgauth` 校验凭据指纹，并在成功登录时通过存储事务一起提交 session、app 标记和指纹。配置变化不会删除已有会话；下一次创建客户端时，指纹不匹配会要求重新登录或恢复原配置。正在运行的客户端不会因保存配置立即重建。旧会话缺少指纹时按原 app 标记推导，仅在成功登录时写入新元数据；这属于本程序的身份一致性策略，不代表 Telegram 协议要求。面板登录交互已迁入账号组件；Bot 登录交互、Telegram 协议调用与连接持有仍在兼容层。

底层新增可选 `storage.Transactional`：Bolt 和 legacy 驱动在同一 bbolt 事务内提交，错误或提交前取消会回滚；file 驱动在同一引擎锁下修改内存快照，回调成功后一次写回文件，但沿用原有文件写入方式，尚不保证断电原子性。自定义旧 Storage 通过进程内统一锁兼容，仅串行化调用，不承诺回滚，也不协调绕过该入口的写入。事务回调只能使用传入的 Storage，不能嵌套事务。

回归覆盖三种真实驱动、同一 namespace 多实例并发写入、账号隔离、悬空索引修复、损坏索引拒绝写入、事务失败及取消回滚，以及多个 HTTP 存储实例并发上报区间。尚未完成跨执行器统一任务模型、任务状态转换规则和全部 NvM 数据集迁移。

`rte/eventbus` 提供有界队列、订阅者异常隔离、账号隔离及发布时的队列背压；队列满时整次发布不入队，不做部分扇出。组件只能发布或订阅 Manifest 声明的主题，停止时取消订阅并等待处理器退出。请求响应沿用同步端口，文件字节流不经过总线。`rte/schedule` 提供命名的一次性和周期 Runnable，禁止重名，同一 Runnable 不重叠执行；组件退出时统一取消并等待。处理器必须响应 context，框架不能强杀卡住的 Go 函数。

消息链接校验已迁入 `application/trigger.messagelink`；生产主宿主持有端口并注入 watch 控制器和下载分发，消息获取和下载意图排队仍在旧 watch，Bot 的命令预校验仍保留临时适配。`application/trigger.reaction` 接管表情归属匹配、下载/转发表情配置和并发去重窗口，生产监听器复用主宿主端口；组件文件和配置页热更新立即作用于运行中的监听器，无需重连。watch 负责把 Telegram reaction/chosen-order 转成普通端口数据，保留移除后可重触发、部分更新不清除窗口、队列满时释放占位及取消时静默跳过的行为。监听连接和意图投递仍需后续迁移。

更新实现及原回归测试已迁入 `application/update.self`，生产 Bot/WebUI 的检查和下载注入主宿主更新端口，代理配置从组件文件读取并可独立保存；宿主停止会取消并等待在途更新。进程替换辅助入口及独立调用保留兼容适配。`bsw/ecual/aria2rpc` 统一执行器、WebUI 控制请求和 AriaNg 代理的类型化 RPC 传输；治理器已迁入下载器 SWC，AriaNg 页面仍需拆分。目录创建由显式启动步骤执行，加载组件不再因目录创建失败而 panic。

`rte/config.Store` 支持独立的 `swc-<id>.json` 文档，包含 `version/enabled/values`。敏感字段按 Manifest 的 `Secret` 声明写入 `secrets/` 中的独立 0600 文件，公开文档只保存文件引用。先写并同步新的敏感配置，再原子替换公开文档；失败会清理未发布的敏感文件，旧文档与旧敏感值保持一致。已发布的旧版本保留，以支持并发读者和备份，不自动清理；这些文件是权限受限的明文存储，并非加密。旧的内嵌值仍可读取，下次保存时分离。

`BuildStored` 从组件文件装配；`ReconfigureSaved` 校验、准备后先保存，再发布运行配置。`swc-check -config-dir <目录>` 可验证导出结果。主程序通过 `--component-config <目录>` 将八个已注册组件切换到组件文件；尚未拆分的其他业务继续读取兼容配置。

`swc-migrate` 当前转换已注册的八个组件配置，默认只校验并预览组件 ID 和账号，不输出凭据：

```powershell
go run ./cmd/swc-migrate -source ./config.json
go run ./cmd/swc-migrate -source ./config.json -out ./component-config -write
go run ./cmd/swc-check -config-dir ./component-config
```

目标目录必须不存在且父目录已存在；命令拒绝覆盖旧目录，失败移除本次创建的输出，成功后最后写入 `migration.json` 标记。输入大小上限为 1 MiB，拒绝未知字段与多份 JSON，同时兼容旧 `file_size_mb` 和下载模式 `internal`。源配置、数据库和会话不被修改。尚未迁移的业务继续使用原配置，因此必须保留源文件。

验证导出后，可用 `tdl --component-config ./component-config` 启动。主宿主持有 account、filter、naming、两个 trigger 和 update 六个组件；Bot 运行时另持有 console/notify。独立文件是这八个组件的权威配置，兼容配置热更新不会覆盖它们。省略开关即恢复原配置来源，组件文件仍保留。缺少组件文件使用 schema 默认值，因此未迁入白名单时控制台默认拒绝全部用户；文件中的 `enabled=false` 会明确阻止需要该组件的宿主装配，不静默回退旧配置。账号设置通过端口注入监听连接、Bot/WebUI 登录和会话检查，变化只影响后续创建的客户端，不强制销毁当前连接。更新代理独立于兼容配置的其他网络代理设置。

WebUI 的“组件配置”入口提供 `/components.html`，从宿主 Manifest 生成文本、整数（含范围）、布尔、字符串列表和密码控件；无配置字段的组件也可正常展示。`/api/components` 沿用会话鉴权；保存通过 `PatchSaved` 在同一 Runtime 锁内合并、校验、落盘并应用。敏感字段不返回，省略或空字符串保持旧值。未指定组件目录时为只读预览；指定后原配置页面拒绝修改过滤、命名、Bot 白名单、账号凭据和表情配置，包括整体对象补丁，防止两个页面争用。页面不控制尚未注册为生产宿主的功能。API、语法和持久化回归已验证；新页视觉验收仍待完成。

下载提交契约已迁至 `interfaces/ports.DownloadExecutor` 和普通数据类型 `interfaces/types.DownloadSubmission/DownloadResult`。watch 向 local 与 aria2 都传账号标识；local 从本账号任务仓库取源任务，aria2 在 RPC 前拒绝其他账号。旧 `app/download` 仅保留类型别名。两个下载器的业务控制和执行已迁入 SWC。aria2 治理后台循环由 Runnable Group 管理，退出先取消并等待实际调用返回，再执行停机暂停；统一报告、执行器选择策略及独立配置仍待完成。

`application/notify.telegram` 接管通知分发、接收者去重与配置快照、消息引用和进度编辑。公共端口只传账号、聊天/消息 ID 与文本；`app/bot/notifier.go` 保留 telego 传输适配。每次调用有超时，Bot 停止会取消在途请求并等待退出；单个接收者失败不影响后续接收者，编辑前会校验整批引用的账号和接收者范围。接收者修改先验证再发布，旧引用不能用于编辑已移除接收者的消息。

该组件在未装配传输时仍可校验配置，但发送返回明确的不可用错误。生产 Bot 创建拥有实际传输的 console/notify 共用宿主，随 Bot 启停并向 Manager 注册；Bot 运行时，组件页展示这两个业务组件，隐藏内部传输提供者。转换命令导出 `notify.telegram.recipients` 与 `console.bot.allowed_users`，配置页保存后立即生效，重启从组件文件恢复。旧配置页拒绝覆盖权限，包含整体 Bot 对象补丁。

普通 Bot 通知通过 `Notifications.Enqueue` 发布 `notification.requested` 事件，订阅队列容量为 64；入队成功不代表发送成功，队列满明确返回背压错误并记录日志。消费时使用当前接收者配置。停机取消在途请求和未消费事件，不提供持久投递保证；需要消息 ID 的进度通知与编辑仍使用同步端口。发送失败进入日志和宿主诊断记录。

`bsw/services/dem` 为每个 RTE 宿主保存最近 128 条错误，包含账号、组件、操作和时间。组件启动/停止失败、事件处理错误和 Runnable 错误自动进入记录，调用者未传 reporter 也可追踪。`Runtime.Health` 返回状态、Runnable 执行次数/时间/最近错误；周期调用超过配置周期时标记 `overdue`，不强杀或重复启动任务。`/api/components/health` 沿用会话鉴权，组件页提供手动刷新。当前只有接入 RTE 的宿主受此观测，历史不跨进程保留，自动恢复策略仍待实现。

`application/console.bot` 接管菜单目录、兼容命令的私聊限制和账号隔离的权限判定。白名单使用原子配置快照，非法用户 ID 更新失败时保留旧权限，组件停止后拒绝访问。Bot 的消息与回调都通过该权限端口；telego 命令解析及下载、登录、转发等业务处理器仍在协议适配层，尚未全部迁为独立业务端口。

本次采用兼容式组件化改造，现有程序入口、配置文件、KV 数据和登录会话继续使用。没有把计划中的全部 AUTOSAR 目录一次性建立出来。`rte` 是组件装配与生命周期的唯一管理器，暂不重复引入 BSW 的第二套生命周期管理。

生产 `app/runtime.Manager` 持有六个常驻组件的 RTE 宿主，创建监听控制器时注入策略、表情、链接与凭据端口；停止或重启监听器不销毁这些组件。无组件目录时配置先通过宿主批量准备，再更新监听选项，失败保留已有策略。初始化失败不终止 WebUI 进程，受影响端口返回不可用；修正配置文件后重试装配。独立 watch 入口仍可创建并管理自己的策略宿主。其余业务的生命周期尚未全部移交 RTE。

## 计划核对结论

- 采用单模块、声明依赖、配置隔离、独立失败状态、账号维度预留和本地下载器命名。
- 采用显式 `Register`，不用全局 `init` 注册；可创建独立注册表，测试不会相互污染。
- `core/downloader`、`core/uploader` 并非无用代码：`forwarder/clone.go` 调用它们完成 clone 转发。已迁入 `internal/core`，不能删除。
- 保留旧 KV 驱动和迁移路径。计划中“新项目无历史数据”的假设不适用于本仓库。
- 保留 `_docker` 和 `-origin-` 旧版本号的只读比较兼容；不再用版本号判断容器，也不再生成 `_docker` 后缀或读取 `TDL_DOCKER`。
- 缺失 `app` 键且没有显式凭据配置时回退为 `builtin`，而不是计划 §6.2 所写的 `desktop`。存储错误和取消不会被当成“没有凭据”。
- 文档 §0.4 的 Telegram 条款、风控和会话绑定推断未作为本次修改的事实依据；没有因为这些推断改动用户会话。

## 已实现的边界

```text
application/swc.go                 唯一组件集成点
application/filter.rules/         过滤规则：schema、算法、生命周期
application/naming.rules/         目录/文件名模板、长度限制、同批去重、模板函数
application/trigger.messagelink/  消息链接校验与账号端口
application/trigger.reaction/     表情归属匹配、触发配置和去重窗口
application/update.self/          版本检查、下载、进程替换与容器限制
application/forwarder/            持久队列策略、控制端口和 RTE 工作线程
application/downloader.local/     本地下载执行、恢复与事务状态控制
application/downloader.aria2/     aria2 提交、任务控制、重连与治理
application/proxy.range/          HTTP Range、条件请求与传输生命周期
interfaces/manifest/              配置字段、端口类型和兼容版本声明
interfaces/ports/                 同步业务契约
interfaces/types/                 AccountID
rte/                              装配校验、拓扑启停、错误隔离、配置分发
rte/config/                       不可变配置视图及校验
rte/eventbus/                     有界事件订阅、账号与异常隔离
rte/schedule/                     可取消的命名 Runnable
bsw/cdd/taskhub/                  兼容任务仓库与事务维护
bsw/cdd/tgauth/                   会话、peer、更新状态及 access hash 的受限存储
bsw/services/nvm/                 数据集所有权及作用域能力
bsw/ecual/aria2rpc/               aria2 共用 RPC 传输
rte/targetpath/                   跨平台目标路径处理，不访问本地文件系统
internal/core/                    原底层模块，使用 Go internal 可见性约束
app/watch/policies.go              旧配置到新组件的过渡适配器
internal/architecture/            新分层的导入规则测试
```

真实监听链路已使用 `filter.rules` 端口。通过控制器更新过滤配置时，仅调用过滤组件的 `Reconfigure`，不会重启监听连接。运行中的过滤器用原子快照切换配置；扩展名判断仍先于大小判断，大小边界仍含端点，超大 MB 数值仍饱和为 int64 上限。

`naming.rules` 也已接入真实监听及已有链接加入本地队列的入口。组件只接收普通元数据，不访问 Telegram、数据库或文件系统；同一次 `Render` 的目录、文件名和长度上限使用同一个不可变快照。`watch` 只负责收集消息/peer 信息、调用端口和执行文件操作。原 `pkg/tplfunc` 已迁入该组件。

保留原有目录别名、模板函数、F/I 拼接、优先缩短消息标题、UTF-8 字节上限和扩展名行为。已有下载链接通过 `RenderedName` 复用已命名的文件，避免套用两次文件名模板。只有新任务使用更新后的命名；已经持久化的任务路径不做迁移。

同批本地文件名冲突通过 `NamingRules.Unique` 处理，沿用不区分大小写的冲突判断及 ` (2)` 后缀，并使用每个待提交任务生成时的字节上限。若上限无法同时容纳区分后缀和扩展名，返回明确错误且不提交这一批任务，避免旧实现不断产生相同截断结果。

`downloader.mode=local` 是新规范值，旧文件中的 `internal` 会归一化为 `local`。已有任务键、API 路径、HTML 元素 ID 和内部 Go 类型名暂时保留，避免把命名调整变成数据迁移。

## 新增组件

1. 在 `interfaces/ports` 定义实际需要的 Go 接口；涉及账号的输入携带 `AccountID`。
2. 在 `application/<id>` 实现 `rte.Component`，声明 `Manifest`，实现 `Register(*rte.Registry) error`。
3. 在 `application/swc.go` 加入注册函数。
4. 在业务宿主装配边界注入该端口。业务组件不得直接导入其他组件或旧的 `app/pkg/internal` 包。

`Manifest.Provides` 和 `Requires` 用 `manifest.PortOf[T](name, major, minor)` 声明接口类型。装配要求类型完全一致、主版本一致、提供方次版本不小于所需版本。缺少必需端口、重复提供、类型或版本不兼容、循环依赖会在任何工厂执行前返回错误。可选依赖缺失时可启动，通过 `Resolve` 返回的错误降级。

生命周期约定：

- `Init` 通过 `Kernel.Provide` 提供已声明的端口，只能在初始化期间绑定。通过 `Kernel.Resolve` 访问声明过的依赖。
- `Start` 应迅速返回，组件自己的 goroutine 必须响应传入 context。
- `Stop` 负责释放资源，也会在 `Init/Start` 失败后调用，因而必须支持部分初始化。清理失败后可能重试，组件需保留完成清理所需的状态；RTE 先等待全部事件处理器和 Runnable 退出，再按依赖反序释放资源。超时或清理失败时保留资源、记录诊断并允许再次 `Stop`，仅全部完成后标记宿主已停止。
- 生命周期 panic 转为该组件的错误。必需依赖失败时其消费者显示 `blocked`，不影响无关组件。
- `Reconfigure` 接收完整的新配置视图；失败时组件必须保留旧配置。等值配置不重复通知。
- 多个策略一起更新时使用 `ReconfigureBatch`。组件实现 `PreparedConfig`，先构造完整的新快照并返回不会失败的提交函数；所有校验成功后才依次发布。模板错误、范围错误或取消不会导致只更新部分策略。每个组件的发布是原子的，但这不保证跨端口读取事务。
- Runtime 当前是一轮生命周期；停止后应重新 Build，不能复用旧实例启动。
- 生命周期钩子必须遵守 context；同进程框架不能强杀不响应取消的 goroutine。

健康查询使用独立的生命周期快照，不等待启动、停止或配置更新持有的运行时锁；启动和停止期间分别显示 `starting`、`stopping`。通知发送和编辑为每个接收者的传输请求独立设置超时，单个请求超时后继续尝试后续接收者；调用方取消或 Bot 停机仍会终止整批操作。

当前 schema 实现 `string/int/bool/strings`，验证未知字段、类型、整数范围，按默认值补齐。视图只含本组件字段，越界读取返回错误；读取数组不会泄露底层可变内存。后续控件应随着真实组件需求增加。

## 校验

```sh
go test ./...
go test -race ./...
go build ./...
golangci-lint run ./...
go run ./cmd/swc-check
go run ./cmd/swc-check -empty
```

根目录命令已覆盖原 `core` 测试，无需进入子模块。CI 使用相同模块，并运行竞态检测和架构规则测试。回归测试覆盖装配失败、失败隔离、反序清理、配置越界、配置热更新、并发状态写入、幂等连接池关闭和 HTTPS 代理证书校验。命名迁移额外验证了原有模板行为、配置失败保留原策略、并发渲染快照、已命名链接复用，以及无法容纳冲突后缀时的退出行为。`swc-check` 不打开旧配置或数据库、不连接 Telegram、不监听端口；两种模式分别输出装载 8 个和 0 个 SWC。

## 尚未迁移的范围

2026-09-18 aria2 治理配置续迁：`downloader.aria2` 的状态同步、连接重试上下限、零速检测与 Telegram 错误调节参数已进入 Manifest。生产宿主读取 `--component-config` 指定的配置目录，配置页面通过 `PatchSaved` 保存并热更新；旧配置触发管理器重建时也会重新加载。参数使用不可变快照，变更会唤醒重连等待、状态同步和零速扫描，已开始的单次治理操作使用自己的参数快照。RPC 连接地址和凭据仍由兼容配置适配，不代表整个 aria2 配置迁移完成。

2026-09-18 最新接入：Bot 登录状态机已从 `app/bot` 迁入 `account.telegram`，通过 `BotLogin`、`LoginRunner`、`LoginMessenger` 使用纯数据接口，实际 Bot 宿主构建登录 RTE 并等待停止。`AccountSession` 为主宿主、Bot 和 WebUI 提供会话检查，并声明对 `AccountResources` 的依赖；停机先排空检查请求再释放资源。`SessionCatalog` 持有列表筛选和删除规则；tgauth 的会话维护与登录互斥，防止删除后被迟到认证重新写回。

面板配置规则经 `Configuration` 端口执行，持久化适配器使用 `CompareAndSet` 拒绝过期配置快照。配置 DTO 移到 `interfaces/types`，原类型保留别名及旧 JSON 解码兼容；反射修改、敏感字段脱敏和已迁移字段保护位于面板组件。以下历史段落中的对应待办以本段及迁移状态表为准。

目前仍不是 M0–M5 的全面替代：主程序的配置协调和完整依赖图仍由 `app/runtime` 编排。八个静态组件的配置切换、迁移工具和 schema 页面已接入生产；转发、本地下载、aria2 和 Range 组件由持有实际资源的宿主显式装配。执行器路由、持久任务状态、账号共享连接与登录端口、触发意图有界排队、面板目录和提交端口已接入。未完成的是整体生产装配、其余业务端口与独立配置、剩余 NvM 数据集核对，以及面板重试关联发现、同步和其他处理器的完整拆分；具体以 [迁移状态表](migration-status.md) 为准。多账号仍为 TODO，按用户要求排除在本轮范围外，验收要求见 [多账号清单](TODO-multi-account.md)。

KV 清理由 `storage.maintenance` 的 `KVMaintenance` 端口编排，实际 Bot 宿主持有其 RTE 生命周期。taskhub 维护仓库在输出快照前过滤会话、应用凭据及协议状态，并独立拒绝删除这些键；普通记录只在当前值与快照一致时删除。组件停止会取消活动清理并等待退出，超时后可再次等待。

面板链接删除经 `DownloadLinks` 端口进入 `download.control`，本地控制返回错误时保留元数据；本地删除成功后，taskhub 在同一事务中删除链接、aria2 关联和索引。兼容旧版无索引关联，扫描候选后在事务内重新读取关联归属。

链接目录与重新提交现经 `DownloadCatalog` 端口进入 `download.control`。目录 DTO 归 `interfaces/types`；组件持有排序、滑动过期、完成判定及完成状态保存规则，保存失败作为状态错误返回。批量提交验证账号、取消与任务 ID，批次内去重；aria2 使用 BSW 类型化客户端和 taskhub 仓库。远端接受后保存失败会返回已接受数量及 GID，不再报告为未提交。存储快照只输出链接与 aria2 记录，保留无索引旧数据并隔离协议凭据。现有远端重试关联发现、同步和本地媒体恢复仍经兼容适配器接入，尚需继续收拢其业务归属。

### 转发、协议存储与配额续迁（2026-09-18）

`ForwardTasks` 隔离转发命令和查询；`ForwardRepository` 隔离持久化；`ForwardTransport` 只负责一次真实发送及进度回调。队列的排队、暂停、恢复、删除、退避重试和历史清理由 `application/forwarder` 实现。`app/forward` 保留 Telegram 适配与旧入口类型别名，JSON 和旧 KV 键保持兼容。转发仓库由 BSW taskhub 提供，更新已有记录使用事务，不允许进度回调复活删除任务。

`application.ServeForwardQueue` 为每次连接构建单次使用的 RTE 宿主，运行命名 Runnable `forward.queue`，退出前等待传输返回；持久队列跨连接保留。Manager 健康接口汇总该宿主。它需要实际队列及传输，不加入无外部资源的默认 `swc-check`；转发组件现支持扫描间隔、失败预算、退避和历史保留配置。整个生产依赖图尚未合并。

账号协议存储通过 NvM 分别授予 `peers:`、`state:`、`chan:` 与 `access_hash:` 的写权限。更新偏移使用事务修改，多个句柄不会相互覆盖频道偏移；更新管理器同时持久化频道及用户 access hash。旧 session、peer 和偏移键不重命名。普通 KV 清理保留协议数据。

共享 `comif.Scheduler.Reconfigure` 原地调整文件和 DC 配额。降额保留已有租约，等待在途任务释放到新限制以下再准入；升额唤醒等待者。HTTP Service 的生产配置更新已接入该方法，HTTP Range 与本地下载继续共用同一调度器。

后续建议按“任务存储权威 → 下载器端口 → 触发 → 控制面”迁移，每一步保留行为回归测试。任务存储合并必须先确定兼容的数据模型，不能靠移动目录或删除旧表完成。

### 下载器与 Range 续迁（2026-09-18）

本地 worker、恢复、暂停/启动/删除和部分文件处理归 `application/downloader.local`。源文件元数据、配额租约与字节流通过端口注入，组件不访问 Telegram 类型。taskhub 仓库提供幂等创建和事务内更新：重复提交保留已有状态；任务删除或暂停后，迟到进度与完成报告不能恢复旧状态；短流返回错误。旧 KV 键及 JSON 字段保持不变。

`application/downloader.aria2` 持有提交、查询、批量控制、重连、错误治理和零速治理策略；`bsw/ecual/aria2rpc` 持有类型化 RPC。Bot 控制依赖 `Aria2Tasks`，兼容入口只装配客户端、仓库和配置。治理任务不会在传输尚未退出时假报停机完成。

`application/proxy.range` 处理 HEAD、ETag、条件请求、单区间和多区间响应。HTTP 适配器提供绑定源记录的传输端口，文件字节不经过事件总线。真实 HTTP 服务构建 Range 宿主；停止取消请求、关闭准入，并等待在途流退出。健康页面汇总实际运行的下载和 Range 宿主。

运行 `go run ./cmd/swc-docs` 可重新生成 [组件配置参考](configuration.md)，无需打开用户配置、数据库或网络。现包含本地下载器、转发、Range 和下载路由的动态 schema，其他动态配置仍需迁移。

### 构建参数与文档检查

`internal/buildflags/ldflags.txt` 是版本、提交、提交时间和 GOARM 的 linker 参数模板。Docker 通过 `go run ./cmd/buildflags` 渲染；GoReleaser 通过 `printf` 与 `mustReadFile` 读取同一文件，保持正式版本和开发版本原值。模板读取依赖 GoReleaser 2.12 及以上，仓库发布工作流使用 2.18；模板函数说明见 [GoReleaser 官方文档](https://goreleaser.com/customization/general/templates/)。Makefile 使用当前的 `--clean`、`--snapshot` 和 `--skip=publish` 参数，本地版本默认 `dev`，可通过 `RELEASE_VERSION` 覆盖。

回归测试比较 Docker helper 与 GoReleaser 模板在非 ARM 和 ARM 5/6/7 上的输出。CI 运行默认及空注册表装配检查，并重新生成配置文档后检查差异，防止 schema 修改遗漏文档。实际跨平台发布及容器运行仍需发布环境验收。

### 动态宿主、页面与下载意图续迁（2026-09-18）

`rte.Process` 接管 Bot、aria2、面板、HTTP 和监听适配器的动态启停。调用者传入账号和运行函数，RTE 负责取消、等待、异常隔离、状态及诊断。停止超时后旧实例仍保留，直到调用真正退出才允许新实例启动；配置重启不再把停止超时当成功。进程退出会等待这些适配器，再关闭其依赖的策略。`app/runtime` 仍承担配置协调和装配，尚未完全替换。

恢复策略默认关闭，只能重试已经退出且被判定为可恢复的调用，不能复制仍在运行或卡住的任务。Bot 对网络错误最多重试 3 次，间隔 1 秒；认证及其他业务错误不自动重试。恢复等待可取消，失败及停止超时进入按账号隔离的 Dem 历史。健康页面同时展示这些动态进程与 SWC 宿主。

`application/panel.webui` 管理监听端口、HTTP 服务与状态同步 Runnable，停止时取消请求并等待处理器退出。WebUI 适配器还会关闭登录准入、取消并等待已启动的认证流程。Range 的任务到期和源缓存清理由组件的小时/分钟 Runnable 管理，停止先等待清理任务，再执行源清理。

HTML、JS、CSS 已从 `app/webui` 移至对应组件的 `assets`：更新、账号、下载控制、转发及 AriaNg 分别由各自组件拥有，公共面板资源归 `panel.webui`。`application.WebAssets` 在旧 URL 下组合这些资源。组件用 `Routes` 声明 API/页面路由和公开属性，WebUI 只绑定处理器并统一执行鉴权；缺少适配器或未声明的处理器均拒绝装配。资源内容、旧 URL、MIME 类型与鉴权有回归覆盖，API 业务处理器的完整端口化仍未完成。

`application/trigger.download` 为表情下载请求提供有界 `download.requested` 事件。普通数据包含账号、消息 ID 和 peer 引用，不包含 Telegram SDK 对象或文件字节。连接宿主绑定真实处理器，停止先等待事件消费结束，再等待文件提交任务和释放池；容量为 100，队列满返回背压，触发端释放去重占位，不向旧通道重复提交。连接前和独立测试适配器保留本地有界队列；需要即时结果的消息链接命令继续使用同步请求。

下载查询统一返回 `DownloadTask.state`，aria2 的 `waiting` 与本地的 `queued` 都映射为 `queued`；原始 `status` 保留以兼容旧界面。未知状态显示 `unknown`，不猜测完成状态。该查询归一化尚不等于统一持久化状态机。

### 转发意图、登录端口与动态配置续迁（2026-09-18）

`trigger.forward` 接收表情及监听新消息产生的普通数据意图，容量为 100，队列满直接返回错误。消费负责在当前连接内获取消息并写入持久转发队列；与下载意图共用连接宿主，停止等待消费退出后再释放池。去掉表情转发的游离 goroutine；来源解析或持久化入队失败会释放去重标记。事件队列不承诺跨进程投递，成功写入持久队列后才受原转发恢复策略保障。

`account.telegram.Login` 接管面板登录状态机，通过 `ports.AccountLogin` 提供开始、状态、验证码、密码、取消与停止。认证适配器通过不含 SDK 类型的 `LoginChallenge` 请求输入。`panel.webui` 声明对 `account.telegram.login` 的依赖，停机先排空 HTTP，再取消并等待认证。验证码确认发送后才允许提交；密码只在被请求时接受，保留首尾空格；取消后不执行账号切换；状态查询返回独立快照。Telegram 登录协议及安装级凭据到目标命名空间的绑定仍由适配器提供，Bot 登录和统一连接持有尚未迁完。

`download.control.Router` 按显式顺序尝试执行器。生产远端提交按 aria2、HTTP 链接的顺序选择；只有 `ErrDownloadNotAccepted` 且没有返回任务 ID 时允许降级。网络超时、连接重置及普通 RPC 错误不能证明远端未接受，不会触发第二次提交。本地模式仍仅选择本地执行器，未实现跨远端/本地路径的自动降级及可配置优先级。

本地下载器新增独立 `poll_interval_ms` 和 `shutdown_timeout_seconds` 配置，通过同一 Manifest 生成文档、校验并加载生产组件配置。热更新会唤醒实际扫描循环并重置周期；非法值不改变内存或已保存配置。运行中的本地宿主由 Manager 路由保存，重连/重启重新读取组件文件。下载路径及共享传输配额仍属于未完成的配置迁移项。
