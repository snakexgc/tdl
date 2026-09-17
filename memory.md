# Project Memory

> 本文件保存经过当前代码状态验证的长期项目上下文。处理项目任务前应先核验，任务完成后应同步更新。

## 基本信息

- 项目名称：tdl
- 项目路径：`D:\gitrepo\tdl`
- 项目类型：Go 应用，包含 Telegram 下载、机器人、监听转发、HTTP 下载代理和 Web 管理功能
- 主要技术栈：Go 1.26.7、Cobra、gotd/td、GitHub Actions、GoReleaser、Docker
- Go 模块：单模块 `github.com/snakexgc/tdl`；原 core 已迁入 `internal/core/`，不再使用 replace
- 构建方式：`go build`；发布构建由 `.goreleaser.yaml` 和 GitHub Actions 完成
- 测试方式：`go test ./...`、`go test -race ./...`；CI 另运行 golangci-lint，根目录测试包含底层包和架构规则
- 最后更新时间：2026-09-16

## 项目概览

tdl 是围绕 Telegram 文件获取与自动化处理构建的 Go 应用。当前仓库还包含 aria2 集成、HTTP 文件流代理、Web 管理界面、机器人控制、消息监听下载和转发等功能，并通过 GitHub Actions 构建二进制、预发布版本和容器镜像。

## 项目结构与文件职责

### 根目录

- `main.go`
  - 应用入口。
- `app/`
  - 应用组装与运行相关代码。
- `cmd/`
  - Cobra 命令及命令行入口逻辑。
- `pkg/`
  - 下载、配置、HTTP、机器人、转发等主要功能包。
- `internal/core/`
  - 封装 Telegram 底层能力，使用 Go internal 可见性约束。
- `scripts/`、`hack/`
  - 项目辅助脚本与开发工具。
- `README.md`
  - 配置、部署和使用说明。
- `go.mod`、`go.sum`
  - 根 Go 模块和依赖锁定信息。
- `.goreleaser.yaml`
  - 多平台二进制、归档、校验和与 changelog 的 GoReleaser 配置。
- `Dockerfile`、`docker-compose.yml`
  - 容器构建和部署配置。
- `memory.md`
  - 经验证的长期项目上下文和修改记录。

### GitHub Actions

- `.github/workflows/master.yml`
  - master 分支和 PR 的 lint、构建、单元测试流程。
- `.github/workflows/release.yml`
  - 正式 `v*` tag 发布以及 master/workflow_dispatch 的 snapshot 预发布流程。
- `.github/workflows/docker.yml`
  - master、正式 `v*` tag 和手动触发的多架构容器发布流程。
- `.github/workflows/dependabot-fix.yml`
  - Dependabot Go 模块分支的 tidy 与自动提交流程。

## 架构与关键流程

- 正式发布：推送 `vX.Y.Z` tag → GoReleaser 正式构建并发布。
- 预发布：master 推送或手动触发 → GoReleaser snapshot 构建 → `gh release create` 上传构建产物并标记 prerelease。
- 容器发布：`docker.yml` 生成镜像元数据并构建 linux/amd64、linux/arm64、linux/arm/v7、linux/arm/v6 镜像。

## 特殊事项与项目约束

- GoReleaser 固定为 `v1.18.2`；该版本即使在 snapshot 模式也会解析当前 checkout 中距 HEAD 最近的 tag，因此所有可见 tag 必须能被解析为 SemVer。
- 历史 tag `prerelease-master-55-1-edc6b69` 不符合 SemVer。预发布流程只在本地 checkout 删除 `prerelease-*` 旧格式 tag，不删除或改写远端 tag/release。
- 新预发布 tag 使用 `<next-patch>-prerelease.<run>.<attempt>.sha<commit>`，例如 `3.12.3-prerelease.56.1.sha4ec6caf`。它符合 SemVer，但不以 `v` 开头，避免触发只面向正式版本的 `v*` release/docker workflow。
- 正式版本 tag 保持 `vX.Y.Z` 格式。
- 当前环境没有 `gh` CLI；无法在本机直接重新查询或重跑 GitHub Actions。

## 当前项目状态

- 本次改造基于提交 `0415395`，修改尚未提交或推送。
- 单模块迁移完成：原 core 的实现完整保留在 `internal/core`；clone 转发仍依赖其中 downloader/uploader，不能按计划的零引用假设删除。
- 新增 `interfaces`、`rte`、`application` 组件边界；`filter.rules` 和 `naming.rules` 已接入真实监听链路，并可独立热更新。其余业务仍由旧 runtime 编排。
- 计划分析、开发约定和未迁移范围以 `docs/components.md` 为准；这不是 M0–M5 的全面替代。
- `downloader.mode=local` 为规范值，兼容读取旧值 `internal`。旧 KV 数据和会话继续使用。
- 容器内不再原地自更新；镜像和二进制使用相同版本号，保留旧版本字符串的比较兼容。
- 修复 DC 池关闭竞态、更新状态读改写竞争与频道进度被清空、HTTPS 代理跳过证书校验、读取应用凭据丢失 context/吞掉存储错误。
- 验证：完整 `go test -race ./...`、`go build ./...` 通过；最终 lint 为 0 issues；格式/测试常量调整后的相关包测试通过。装配检查分别验证 2 个真实组件与 0 个组件。
- 未运行真实 Telegram/aria2 端到端下载、Docker 构建或远端 GitHub Actions。
## 需求与修改记录

### 2026-07-21：修复 GoReleaser snapshot 无法解析 prerelease tag

#### 用户需求

修复 GitHub Actions 中 GoReleaser `release --snapshot` 报错：`failed to parse tag 'prerelease-master-55-1-edc6b69' as semver`。

#### 需求分析

- 失败发生在 GoReleaser 构建前的 tag 解析阶段。
- 历史预发布流程生成了非 SemVer tag，并且该 tag 是当前 HEAD 最近的 tag。
- 合并提交对应的 PR #44 只修改 `go.mod` 和 `go.sum`，与失败根因无关。
- 修复必须兼容已经存在的旧 tag，并防止后续预发布继续制造非 SemVer tag。

#### 修改内容

- 在 prerelease metadata 阶段从 runner 的本地 checkout 删除 `prerelease-*` 旧格式 tag。
- 从最近的稳定 `vX.Y.Z` tag 计算下一个 patch 版本。
- 将新预发布 tag 改为有效 SemVer，且不使用 `v` 前缀。

#### 涉及文件

- `.github/workflows/release.yml`
- `memory.md`

#### 修改结果

历史坏 tag 不再进入 GoReleaser 1.18.2 的当前 tag 解析；后续生成的预发布 tag 可被同一 SemVer 库解析，并且不会匹配正式发布 workflow 的 `v*` tag 规则。

#### 验证情况

- 从 workflow 中提取实际 Bash 脚本，在独立临时 clone 中使用 `GITHUB_RUN_NUMBER=56`、`GITHUB_RUN_ATTEMPT=1` 执行通过。
- 临时 clone 中旧 tag 被删除，`git describe --tags --abbrev=0 HEAD` 返回 `v3.12.2`。
- 输出 tag 为 `3.12.3-prerelease.56.1.sha4ec6caf`。
- 源工作区的历史 tag 仍保留，验证过程未修改远端或源仓库 tag。
- 使用 PyYAML 6.0.3 成功解析 `.github/workflows/release.yml`。
- 未重新运行 GitHub Actions；当前环境缺少 `gh` CLI，且本地修改尚未推送。

#### 遗留事项

- 推送修改后重新运行 release workflow，确认 GoReleaser 完整多平台构建和 prerelease 上传成功。

### 2026-07-21：统一升级 Go 与 GitHub Actions 依赖

#### 用户需求

升级 `github.com/mymmrac/telego`、`github.com/gotd/contrib`、`github.com/gotd/td`、`golang.org/x/net`、`golang.org/x/sync`、`golang.org/x/mod`，并将 `actions/setup-go` 从 v6 升级到 v7；涉及根模块及 `core/` 子模块。

#### 需求分析

- 根模块和 `core/` 是两个独立 Go 模块，需要分别升级、tidy、构建和测试。
- `github.com/gotd/td` 与相关 `golang.org/x/*` 升级会带动一组必要的间接依赖更新。
- `actions/setup-go` 在 3 个 workflow 中共有 5 处引用，需要保持一致。

#### 修改内容

- 根模块升级 `telego` 到 v1.11.1、`gotd/td` 到 v0.161.0、`x/mod` 到 v0.38.0，并同步模块图中的 `gotd/contrib` v0.25.0、`x/net` v0.57.0 等间接版本。
- `core/` 升级 `gotd/contrib` 到 v0.25.0、`gotd/td` 到 v0.161.0、`x/net` 到 v0.57.0、`x/sync` 到 v0.22.0、`x/mod` 到 v0.38.0。
- 两个模块均执行 `go mod tidy`，同步 `go.sum` 和必要的间接依赖。
- 将 `.github/workflows/dependabot-fix.yml`、`master.yml`、`release.yml` 中的 `actions/setup-go` 全部升级到 v7。
- 新依赖与现有源码 API 兼容，不需要修改 Go 源代码。

#### 涉及文件

- `go.mod`
- `go.sum`
- `core/go.mod`
- `core/go.sum`
- `.github/workflows/dependabot-fix.yml`
- `.github/workflows/master.yml`
- `.github/workflows/release.yml`
- `memory.md`

#### 修改结果

用户列出的依赖版本均已被两个模块的最终模块图选中，旧的目标版本不再残留；所有 `setup-go` 引用均为 v7。

#### 验证情况

- 根模块：`go test ./...` 通过，`go build ./...` 通过，`go mod verify` 返回 `all modules verified`。
- `core/`：`go test ./...` 通过，`go build ./...` 通过，`go mod verify` 返回 `all modules verified`。
- 使用 `go list -m` 确认最终选中版本与用户要求一致。
- 使用 PyYAML 6.0.3 成功解析全部 `.github/workflows/*.yml`。
- `git diff --check` 通过。
- 未运行远端 GitHub Actions；当前修改尚未提交或推送。

#### 特殊事项

直接依赖升级同时更新了 `gotd/ige`、`ogen`、`fasthttp`、`goldmark`、`x/crypto`、`x/sys`、`x/text`、`x/tools` 等由新模块图要求的间接依赖。

#### 遗留事项

- 提交并推送后观察 GitHub Actions 的 lint、测试和 release job。

### 2026-07-27：升级 errors 与 mimetype

#### 用户需求

将根模块和 `core/` 的 `github.com/go-faster/errors` 从 v0.7.1 升级到 v0.8.0，并升级 `core/` 的 `github.com/gabriel-vasile/mimetype`。用户同时给出了 v1.4.14 和 v1.4.15 两个目标。

#### 需求分析

- 根模块和 `core/` 是两个独立 Go 模块，需要分别更新和验证。
- `mimetype` 的两个目标互相覆盖，因此采用更高的 v1.4.15。
- 根模块通过本地替换引用 `core/`，其模块图中的间接 `mimetype` 也需要同步到 v1.4.15。

#### 修改内容

- 根模块和 `core/` 的 `github.com/go-faster/errors` 均升级到 v0.8.0。
- `core/` 的直接依赖和根模块的间接依赖 `github.com/gabriel-vasile/mimetype` 均升级到 v1.4.15。
- 两个模块分别执行 `go mod tidy`，刷新相应校验和。

#### 涉及文件

- `go.mod`
- `go.sum`
- `core/go.mod`
- `core/go.sum`
- `memory.md`

#### 修改结果

两个模块最终均选中 `errors` v0.8.0 和 `mimetype` v1.4.15，旧版本不再残留，无需修改 Go 源代码。

#### 验证情况

- 根模块：`go test ./...`、`go build ./...` 和 `go mod verify` 均通过。
- `core/`：`go test ./...`、`go build ./...` 和 `go mod verify` 均通过。
- `go list -m` 确认两个模块最终选中 `errors` v0.8.0 和 `mimetype` v1.4.15。

#### 遗留事项

- 未运行远端 GitHub Actions；当前修改尚未提交或推送。

## 待处理事项

- [ ] 后续迁移任务中枢/存储唯一写入者、其余业务组件、配置落盘与自动配置页，具体范围见 `docs/components.md`。
- [ ] 在有账号、aria2 和容器的环境验收真实下载、转发、镜像发布。

## 最近一次任务摘要

- 任务：继续迁移命名策略。
- 完成：`naming.rules` 拥有目录模板、文件名模板、UTF-8 长度限制、同批冲突处理和迁入的模板函数；通过端口接入监听下载和已有链接加入本地队列。`rte/targetpath` 提供无文件系统访问的通用目标路径操作。
- 热更新：Runtime 新增准备后发布的批量配置更新；过滤与命名全部校验成功后才更新。每次命名渲染使用单个配置快照，已生成任务保留自身长度上限。
- 修复：极短文件名上限无法容纳冲突后缀时返回错误，避免重复生成相同名称而无限循环；已有链接不会重复套用文件名模板。
- 验证：完整 `go test -race ./...`、`go build ./...` 通过；`golangci-lint run ./...` 0 issues；装配检查输出 2 个及 0 个组件；`git diff --check` 通过。
- 保留：旧配置、会话和持久化任务路径不迁移。尚未执行真实 Telegram/aria2 端到端验收。
- 后续：统一 taskhub/NvM 所有权、其余 11 个 SWC、配置落盘与自动配置页等，范围见 `docs/components.md`。尚未提交或推送。
# 2026-09-16 任务存储迁移补充

- 新增 internal/taskhub.Collection，本地下载任务保存、删除、列表索引修复已接入；保持旧键和 JSON，watch 仅保留业务字段适配。
- storage.Update / Transactional 支持原生事务：legacy/bolt 使用 bbolt 事务；file 使用引擎锁与内存快照，一次写回，尚无断电原子性。旧自定义 Storage 仅兼容串行化，无失败回滚保证。
- 测试覆盖真实三驱动、多实例并发、namespace 隔离、损坏索引、悬空索引、失败与取消回滚。
- 未完成：HTTP/aria2/WebUI/Bot 的所有直接写入统一、状态冲突控制、完整 taskhub/NvM。
# 当前继续迁移状态（2026-09-16）

- 用户最新要求完成所有剩余 AUTOSAR 重构；整体尚未完成，不能把本轮注册的五个组件当作全量替代。详见 docs/migration-status.md。
- 已新增 trigger.messagelink（校验）、trigger.reaction（表情归属、下载/转发匹配及去重）、update.self（实现和测试迁移）；共五个注册组件。旧命令、watch 与更新调用通过 application 集成适配器接入，监听和意图投递仍在 watch。
- internal/taskhub 已迁入 bsw/cdd/taskhub。HTTP 链接、aria2、本地任务及 WebUI 状态/清理写入已统一，HTTP Get 不再依赖跨实例不一致缓存；区间完成态、刷新和过期判断用同一事务；元数据不回退 last_active_at；延迟状态不复活删除任务。
- bsw/services/nvm 数据集注册验证重复/前缀冲突、错误 writer 与作用域；taskhub 通过受限句柄写入三个兼容数据集。其余账号/转发数据集尚未迁移。Bot 清理保留任务索引并跳过快照后变化的数据。
- rte/eventbus 有界、账号隔离、Manifest 声明限制、异常隔离；RTE 停止和 Init 失败先取消并等待相关事件处理器。rte/schedule 命名周期/一次性 Runnable，统一取消等待、不重叠执行。
- rte/config.Store、BuildStored、ReconfigureSaved 支持版本化单组件文件和保存失败不发布；仅 swc-check -config-dir 接入，生产 config 和自动配置页仍未切换。
- bsw/ecual/aria2rpc 供 app/aria2、WebUI 请求和 AriaNg 代理共用传输；页面和治理器仍待迁成组件。pkg/consts 不再在 init 创建目录或 panic，cmd.New 显式初始化并通过启动错误返回。
- 本轮未提交/推送。保留之前所有单模块、并发修复、filter/naming 和本地任务迁移改动。

# 继续迁移状态（2026-09-17）

- account.telegram 新增凭据端口及原子配置快照，注册组件现为六个。兼容 config.Telegram 支持 api_id/api_hash、builtin_preset、use_builtin；API Hash 在 WebUI 响应中隐藏，空白保存保留原值，配置文件使用 0600。
- tgauth 提供只读凭据指纹检查及 session/app/fingerprint 事务提交。成功登录使用登录开始时捕获的凭据；配置改变不删除旧会话，下次创建客户端要求重新登录或恢复原配置。正在运行的客户端不自动重建。登录流程和连接所有权尚未迁入完整账号组件。
- forward 数据集迁入 taskhub/NvM，保留 forward.job.* 和 forward.index，记录/索引事务一致；新增跨存储实例并发与旧记录维护测试。taskhub 现在封装四个数据集。
- 移除 forward 的全局 Jobs/ConfigureQueue，Manager 持有 NewQueue 并注入 Bot、watch、WebUI；独立入口显式创建实例。转发执行代码及业务端口仍待组件化。
- 原 app/http/transfer 调度器及测试迁入 bsw/ecual/comif，HTTP/本地下载调用已更新，生产共用实例关系保留。
- 修复 RTE 停止后相同配置绕过状态校验并重新落盘 enabled=true 的问题，补充回归。
- 最终验证：go test -race ./...、go build ./...、golangci-lint run ./...（0 issues）、swc-check（6/0）、git diff --check 全部通过。未做真实 Telegram/aria2 网络端到端测试，未提交或推送。
- 整体 AUTOSAR 重构仍未完成。剩余主宿主切换、下载执行器、完整账号/转发/通知/面板组件、配置迁移命令与自动页面等，以 docs/migration-status.md 为准。

## 生产策略宿主接入补充

- Manager 统一持有 filter.rules/naming.rules 的 RTE 宿主与端口，watch Controller 对外部注入的端口不再重复装配或停止；独立启动继续管理自己的宿主，拒绝只注入一个策略端口。
- Manager ApplyConfig 先批量更新策略，再调整其他模块。无效热更新保留旧策略并记录错误。首次策略装配失败不终止 WebUI 等入口，仅阻止监听启动；后续配置应用重试装配。Shutdown 在停止消费者后停止策略宿主。
- 新增生产宿主端口注入、失败保留旧配置、同端口热更新，以及监听反复启停不关闭外部组件的测试。
- 全量 go test -race ./...、go build ./...、golangci-lint run ./...（0 issues）、git diff --check 通过。其余业务生命周期和配置主存储尚未全部切换，未提交或推送。

## 配置迁移与执行器提交接入补充

- rte/config.View 记录 schema Secret 标记，Store.Save 将敏感字段写入 secrets/ 下的不可变 0600 文件，公开文档增加 secrets 文件引用；先同步敏感文件，再原子替换公开文档。失败清理未发布文件，已发布旧代保留以支持并发读者。支持读取旧内嵌配置，不是加密存储。
- 新增 internal/migration 和 cmd/swc-migrate。默认只校验、预览账号与六个受支持组件 ID，不输出值；-write -out 必须是不存在且父目录存在的目录，拒绝覆盖，失败清理本次新建输出，最后写 migration.json。严格拒绝未知字段、超 1 MiB 和多 JSON，兼容 file_size_mb/internal。源配置和会话不改。导出后可用 swc-check -config-dir 检查。
- cmd 新增 --component-config，Manager 对生产过滤/命名从该目录装配，后续兼容配置 Apply 不再覆盖两者；其余四个已注册组件尚未切换。无开关仍是原路径。新增保存、重启保留和无效配置不覆盖回归。
- RTE Configurations 提供脱敏且复制的 schema/值，PatchSaved 在同一锁内合并、校验、保存、发布，空敏感字段保留旧值。WebUI 新增 /api/components（会话鉴权）和 /components.html（Manifest 生成字符串/整数/布尔/字符串列表/密码控件）；无组件目录时只读，启用后可保存生产的两个组件。旧 WebUI 配置入口阻止编辑已切换的过滤/命名字段。其他入口的旧字段尚需整体移除。
- 公共 interfaces/ports.DownloadExecutor 和 types.DownloadSubmission/DownloadResult 携带账号、纯元数据。aria2 和 local 提交已接入，跨账号在 RPC/任务读取前拒绝（aria2 保留空账号旧调用兼容）。app/download 仅类型别名。local 源任务从账号仓库解析，不访问外部 URL；新测试验证持久排队和不提前创建输出文件。
- aria2 Manager 两个后台治理器已用 rte/schedule.Group 管理，取消后等待退出再执行停机暂停；异常会记录。完整 Executor 控制/报告和 downloader SWC 拆分尚未完成。
- 最终全量 go test -race ./...、go build ./...、golangci-lint run ./...（0 issues）、node --check components.js、git diff --check 通过。CUA 报告 No browser is available，无法视觉验收组件页；临时 localhost:8765 预览服务已通过执行会话 Ctrl-C 停止，未留后台预览服务。
- 清单仍有大量未完成范围：完整主宿主、通知/控制台/账号/forward/proxy/downloader/panel SWC、事件与统一任务状态/执行器控制等。不能声称全部迁移完成。所有未提交改动保留，未提交/推送。

## notify.telegram 组件迁移补充

- 新增 application/notify.telegram，接管接收者去重、分发、跟踪消息引用、进度编辑、配置快照、发送超时、账号和编辑引用校验；interfaces/ports.NotificationTransport 隔离 telego 协议。注册组件现为七个。
- application.NotificationHost 显式注册宿主传输提供者，并装配通知组件。app/bot/notifier.go 改为传输与旧调用适配；bot.Run 创建通知宿主并在退出时 Close。任务取消后的最终通知仍可发送，但受通知超时和 Bot 生命周期约束，不再无限脱离取消。
- 组件通过互斥锁协调请求进入和 Stop，取消并等待在途请求完成；整体引用校验在任何编辑 RPC 之前执行。部分接收者失败继续发送其他接收者。配置解析失败保留原接收者。
- swc-migrate 增加从 bot.allowed_users 转换 notify.telegram.recipients；转换支持七个组件。通知配置尚未接入主宿主的文件配置页，业务通知事件路由及 console 拆分仍未完成。
- 新增重复接收者、部分失败、跟踪编辑、跨账号/引用拒绝、无效配置保留、停止取消并等待请求测试；原 Bot/aria2 通知测试同步适配并通过。
- 全量 go test -race ./... 通过，golangci-lint run ./... 为 0 issues。构建与装配检查见本轮工具输出；未提交或推送。
# 2026-09-17 console 与 Bot 组件配置接入

- 新增 application/console.bot，迁移菜单目录、兼容私聊命令识别和账号隔离白名单；移除 app/bot/allowed_users.go。消息与 callback 使用 Console 端口，非法权限配置不替换旧快照，停止后拒绝访问。
- application.BotHost 统一 console/notify 与传输适配；Bot 向 Manager 注册宿主，--component-config 接管两者配置，运行期间可从组件页热更新并持久化。默认空白名单，缺少文件不回退旧权限。旧 WebUI 配置拒绝覆盖白名单，包含整体 bot 对象和带空格路径。
- 注册及转换组件数增至 8；生产组件文件接入数为 4（filter/naming/console/notify）。任务和登录等命令处理器仍未完成端口迁移，业务事件与整体生命周期编排仍待完成，不能声称清单全部完成。
- 验证：go test -race ./... 通过，最终 WebUI 调整单独 race 回归通过；golangci-lint run ./... 为 0 issues；go build ./... 通过；swc-check 默认 8、empty 0。
