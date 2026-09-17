# 组件开发与迁移说明

任务存储已迁入 `bsw/cdd/taskhub`：本地任务、HTTP 链接、aria2、转发记录及其索引通过统一仓库写入，保持原键名和 JSON 格式。HTTP 的到期判断、活动时间刷新和已发送区间合并在同一事务内完成，不再依赖各实例的缓存锁。WebUI 更新状态只修改其负责的字段，延迟的状态报告不能复活已删除任务，元数据刷新不会回退活动时钟。清理操作检查当前记录，不使用旧快照覆盖索引。

`bsw/services/nvm` 提供数据集注册和读写能力：重复名称、重叠前缀和错误写入者被拒绝，只读句柄不能转成写句柄，事务内也执行作用域检查。taskhub 使用该能力封装四个兼容数据集；账号会话等其他数据集仍未全部接入 NvM。Bot 的旧清理命令通过 taskhub 的维护入口处理任务记录，保留空任务索引，并跳过快照后发生变化的数据。

转发队列不再使用全局单例：运行宿主创建一个 namespace 队列并注入 Bot、watch 和 WebUI，通知回调和 worker 也绑定该实例。独立启动的服务创建自己的队列，组合启动必须注入同一实例以保持单 worker。任务状态机及转发执行代码尚未全部迁入 SWC。

`bsw/ecual/comif` 接管原 HTTP transfer 包的全局文件数和按 DC 分片配额调度器及测试，HTTP 和本地下载共用相同实现；生产本地下载沿用代理持有的同一调度实例。调度器不依赖业务包。账号资源宿主和配额热更新仍需继续迁移。

`application/account.telegram` 提供凭据端口。兼容配置中的 `telegram.api_id` 与 `telegram.api_hash` 必须成对填写；`builtin_preset` 为空时沿用会话标记，新登录仍默认 desktop；`use_builtin` 可临时恢复内置凭据而保留自有值。WebUI 不返回 API Hash，留空保存表示保留原值。兼容配置文件按 0600 创建；新的组件配置将 API Hash 和可能包含认证信息的更新代理地址分离到 secret 文件。

`bsw/cdd/tgauth` 校验凭据指纹，并在成功登录时通过存储事务一起提交 session、app 标记和指纹。配置变化不会删除已有会话；下一次创建客户端时，指纹不匹配会要求重新登录或恢复原配置。正在运行的客户端不会因保存配置立即重建。旧会话缺少指纹时按原 app 标记推导，仅在成功登录时写入新元数据；这属于本程序的身份一致性策略，不代表 Telegram 协议要求。登录交互与连接持有仍在兼容层。

底层新增可选 `storage.Transactional`：Bolt 和 legacy 驱动在同一 bbolt 事务内提交，错误或提交前取消会回滚；file 驱动在同一引擎锁下修改内存快照，回调成功后一次写回文件，但沿用原有文件写入方式，尚不保证断电原子性。自定义旧 Storage 通过进程内统一锁兼容，仅串行化调用，不承诺回滚，也不协调绕过该入口的写入。事务回调只能使用传入的 Storage，不能嵌套事务。

回归覆盖三种真实驱动、同一 namespace 多实例并发写入、账号隔离、悬空索引修复、损坏索引拒绝写入、事务失败及取消回滚，以及多个 HTTP 存储实例并发上报区间。尚未完成跨执行器统一任务模型、任务状态转换规则和全部 NvM 数据集迁移。

`rte/eventbus` 提供有界队列、订阅者异常隔离、账号隔离及发布时的队列背压；队列满时整次发布不入队，不做部分扇出。组件只能发布或订阅 Manifest 声明的主题，停止时取消订阅并等待处理器退出。请求响应沿用同步端口，文件字节流不经过总线。`rte/schedule` 提供命名的一次性和周期 Runnable，禁止重名，同一 Runnable 不重叠执行；组件退出时统一取消并等待。处理器必须响应 context，框架不能强杀卡住的 Go 函数。

消息链接校验已迁入 `application/trigger.messagelink`，原命令与监听入口通过账号端口适配，消息获取和下载意图排队仍在旧 watch。`application/trigger.reaction` 接管表情归属匹配、下载/转发表情配置和并发去重窗口；watch 负责把 Telegram reaction/chosen-order 转成普通端口数据，保留移除后可重触发、部分更新不清除窗口、队列满时释放占位及取消时静默跳过的行为。监听连接和意图投递仍需后续迁移。

更新实现及原回归测试已迁入 `application/update.self`，常规检查和下载通过 RTE 端口装配，进程替换辅助入口保留兼容适配。`bsw/ecual/aria2rpc` 统一执行器、WebUI 控制请求和 AriaNg 代理的 RPC 传输；AriaNg 页面及治理器组件归属仍需继续拆分。目录创建由显式启动步骤执行，加载组件不再因目录创建失败而 panic。

`rte/config.Store` 支持独立的 `swc-<id>.json` 文档，包含 `version/enabled/values`。敏感字段按 Manifest 的 `Secret` 声明写入 `secrets/` 中的独立 0600 文件，公开文档只保存文件引用。先写并同步新的敏感配置，再原子替换公开文档；失败会清理未发布的敏感文件，旧文档与旧敏感值保持一致。已发布的旧版本保留，以支持并发读者和备份，不自动清理；这些文件是权限受限的明文存储，并非加密。旧的内嵌值仍可读取，下次保存时分离。

`BuildStored` 从组件文件装配；`ReconfigureSaved` 校验、准备后先保存，再发布运行配置。`swc-check -config-dir <目录>` 可验证导出结果。主程序可通过 `--component-config <目录>` 将过滤、命名、Bot 权限及通知切换到组件文件；其他模块继续读取兼容配置。

`swc-migrate` 当前转换已注册的八个组件配置，默认只校验并预览组件 ID 和账号，不输出凭据：

```powershell
go run ./cmd/swc-migrate -source ./config.json
go run ./cmd/swc-migrate -source ./config.json -out ./component-config -write
go run ./cmd/swc-check -config-dir ./component-config
```

目标目录必须不存在且父目录已存在；命令拒绝覆盖旧目录，失败移除本次创建的输出，成功后最后写入 `migration.json` 标记。输入大小上限为 1 MiB，拒绝未知字段与多份 JSON，同时兼容旧 `file_size_mb` 和下载模式 `internal`。源配置、数据库和会话不被修改。尚未迁移的业务继续使用原配置，因此必须保留源文件。

验证导出后，可用 `tdl --component-config ./component-config` 启动。该开关切换生产过滤、命名及 Bot 的 console/notify 宿主：独立文件是这四个组件的权威配置，兼容配置热更新不会覆盖它们。省略开关即恢复原配置来源，组件文件仍保留。其他四个已注册组件不会因导出文件存在而自动切换。缺少组件文件使用 schema 默认值，因此未迁入白名单时控制台默认拒绝全部用户。

WebUI 的“组件配置”入口提供 `/components.html`，从宿主 Manifest 生成文本、整数（含范围）、布尔、字符串列表和密码控件。`/api/components` 沿用会话鉴权；保存通过 `PatchSaved` 在同一 Runtime 锁内合并、校验、落盘并应用。敏感字段不返回，省略或空字符串保持旧值。未指定组件目录时为只读预览；指定后原配置页面拒绝修改过滤和命名字段，防止两个页面争用。页面不控制尚未注册为生产宿主的功能。API、语法和持久化回归已验证；环境没有可用浏览器，新页视觉验收仍待完成。

下载提交契约已迁至 `interfaces/ports.DownloadExecutor` 和普通数据类型 `interfaces/types.DownloadSubmission/DownloadResult`。watch 向 local 与 aria2 都传账号标识；local 从本账号任务仓库取源任务，aria2 在 RPC 前拒绝其他账号。旧 `app/download` 仅保留类型别名。aria2 的两个治理后台循环已纳入 RTE Runnable Group，管理器退出时先取消并等待，再执行停机暂停；完整下载控制、统一报告和 SWC 执行器拆分仍待完成。

`application/notify.telegram` 接管通知分发、接收者去重与配置快照、消息引用和进度编辑。公共端口只传账号、聊天/消息 ID 与文本；`app/bot/notifier.go` 保留 telego 传输适配。每次调用有超时，Bot 停止会取消在途请求并等待退出；单个接收者失败不影响后续接收者，编辑前会校验整批引用的账号和接收者范围。接收者修改先验证再发布，旧引用不能用于编辑已移除接收者的消息。

该组件在未装配传输时仍可校验配置，但发送返回明确的不可用错误。生产 Bot 创建拥有实际传输的 console/notify 共用宿主，随 Bot 启停并向 Manager 注册；Bot 运行时，组件页展示这两个业务组件，隐藏内部传输提供者。转换命令导出 `notify.telegram.recipients` 与 `console.bot.allowed_users`，配置页保存后立即生效，重启从组件文件恢复。旧配置页拒绝覆盖权限，包含整体 Bot 对象补丁。业务代码目前通过同步通知端口调用，通知事件路由仍待完成。

`application/console.bot` 接管菜单目录、兼容命令的私聊限制和账号隔离的权限判定。白名单使用原子配置快照，非法用户 ID 更新失败时保留旧权限，组件停止后拒绝访问。Bot 的消息与回调都通过该权限端口；telego 命令解析及下载、登录、转发等业务处理器仍在协议适配层，尚未全部迁为独立业务端口。

本次采用兼容式组件化改造，现有程序入口、配置文件、KV 数据和登录会话继续使用。没有把计划中的全部 AUTOSAR 目录一次性建立出来。`rte` 是组件装配与生命周期的唯一管理器，暂不重复引入 BSW 的第二套生命周期管理。

生产 `app/runtime.Manager` 现在持有过滤和命名的 RTE 宿主，创建监听控制器时注入两个端口，停止或重启监听器不销毁这些组件。配置先通过宿主批量准备，再更新监听选项；失败保留已有策略。初始化失败仅阻止监听启动，WebUI 等入口仍可启动，修正配置后重试装配。独立 watch 入口仍可创建并管理自己的策略宿主。其余业务的生命周期尚未全部移交 RTE。

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
interfaces/manifest/              配置字段、端口类型和兼容版本声明
interfaces/ports/                 同步业务契约
interfaces/types/                 AccountID
rte/                              装配校验、拓扑启停、错误隔离、配置分发
rte/config/                       不可变配置视图及校验
rte/eventbus/                     有界事件订阅、账号与异常隔离
rte/schedule/                     可取消的命名 Runnable
bsw/cdd/taskhub/                  兼容任务仓库与事务维护
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
- `Stop` 负责释放资源，也会在 `Init/Start` 失败后调用，因而必须支持部分初始化。
- 生命周期 panic 转为该组件的错误。必需依赖失败时其消费者显示 `blocked`，不影响无关组件。
- `Reconfigure` 接收完整的新配置视图；失败时组件必须保留旧配置。等值配置不重复通知。
- 多个策略一起更新时使用 `ReconfigureBatch`。组件实现 `PreparedConfig`，先构造完整的新快照并返回不会失败的提交函数；所有校验成功后才依次发布。模板错误、范围错误或取消不会导致只更新部分策略。每个组件的发布是原子的，但这不保证跨端口读取事务。
- Runtime 当前是一轮生命周期；停止后应重新 Build，不能复用旧实例启动。
- 生命周期钩子必须遵守 context；同进程框架不能强杀不响应取消的 goroutine。

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

根目录命令已覆盖原 `core` 测试，无需进入子模块。CI 使用相同模块，并运行竞态检测和架构规则测试。回归测试覆盖装配失败、失败隔离、反序清理、配置越界、配置热更新、并发状态写入、幂等连接池关闭和 HTTPS 代理证书校验。命名迁移额外验证了原有模板行为、配置失败保留原策略、并发渲染快照、已命名链接复用，以及无法容纳冲突后缀时的退出行为。`swc-check` 不打开旧配置或数据库、不连接 Telegram、不监听端口；两种模式分别输出装载 7 个和 0 个 SWC。

## 尚未迁移的范围

目前仍不是 M0–M5 的全面替代：主程序大部分业务仍由 `app/runtime` 编排。未完成全部业务端口、统一执行器及任务状态模型、全部 NvM 数据集、主程序配置切换与迁移工具、schema 自动配置页，以及 account、proxy、两个 downloader、forwarder、console、notify、panel 八个 SWC 的完整拆分。两个 trigger 组件还需接管监听或意图排队。多账号仍为 TODO，仅预留契约维度。

后续建议按“任务存储权威 → 下载器端口 → 触发 → 控制面”迁移，每一步保留行为回归测试。任务存储合并必须先确定兼容的数据模型，不能靠移动目录或删除旧表完成。
