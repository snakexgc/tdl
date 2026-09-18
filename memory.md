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
# 2026-09-17 通知事件与组件诊断

- Notifications 新增 Enqueue，notify.telegram 声明并订阅 notification.requested，普通 Bot 通知走容量 64 的有界队列；进度消息 Send/Edit 保持同步。入队失败直接报告，异步发送错误记录日志及诊断；停止取消未处理事件与在途请求。新增背压、跨账号、停机等待测试，旧 Bot 完成通知测试改为等待异步交付。
- bsw/services/dem 提供每宿主最近 128 条诊断；RTE 自动收集生命周期、事件和 Runnable 错误。schedule.Group 暴露执行时间/次数/最近错误及周期超期状态，不自动强杀或恢复。Manager 汇总当前宿主诊断，/api/components/health 鉴权，组件页可手动刷新。
- 修复 Registry.Register 的 ConfigField Min/Max/Default 浅拷贝泄漏，注册后修改调用者原始数据不再影响 schema。新加 schema 所有权回归。
- 剩余整体编排、账号连接、执行器、转发与触发意图等迁移仍未完成；禁止据此声称清单全部完成。

# 2026-09-17 审查问题修复

- RTE 停机先取消并等待全部事件处理器、Runnable，再反序释放组件；等待超时或组件清理失败记录诊断，保留资源和待清理状态，允许后续 Stop 重试。Init/Start 失败后的清理也不再跳过等待结果而释放资源。
- Health 使用每组件独立发布的原子快照，生命周期钩子阻塞不影响健康查询；增加 starting/stopping 状态。组件 Stop 返回错误后可能被重试，需保留未完成清理的状态。
- notify.telegram 的 Send/Edit 改为每次传输独立超时，首个接收者超时不阻止后续接收者；调用方取消和宿主停机仍取消整批请求。
- 新增事件/Runnable 连续停机超时与重试、依赖资源保留、清理钩子失败重试、启动/停止期间健康查询、发送/编辑超时隔离及整批取消回归。go test -race ./...、go build ./... 通过；golangci-lint run ./... 为 0 issues。未执行真实 Telegram/aria2 端到端验证。

# 2026-09-17 已注册组件的生产接入与账号数据集

- 核对 docs 下两个 Markdown 文件，确认整体迁移仍未完成；修正组件数量 7/8、配置页和迁移工具尚未接入等过时描述。README 增加组件迁移文档入口。
- Manager 常驻宿主扩展为 account/filter/naming/trigger.reaction/trigger.messagelink/update 六个组件；Bot 持有 console/notify，八个已注册组件现在全部支持生产文件配置。未启用目录时六组件批量应用兼容配置；启用后以组件文件为准。禁用生产必需组件返回明确错误，端口不可用不回退旧凭据。
- Watch 复用主宿主表情、消息链接和凭据端口；表情配置保存即时影响既有监听器。登录、会话检查、spam 检查和监听客户端通过显式 Options 注入凭据；保留会话指纹策略和当前连接。Bot/WebUI 更新检查与下载复用更新端口，其生命周期取消并等待在途请求。
- 旧 WebUI 配置入口拒绝覆盖账号/表情设置（含整体对象补丁）；空 schema 返回空数组，组件页不会因消息链接组件无字段而中断渲染。敏感字段继续脱敏且空值保存保留原值。
- NvM 新增精确键作用域，校验与前缀的双向重叠、重复键及调用方切片所有权。tgauth 统一 session/app/fingerprint 数据集；登录提交、客户端 session 写回和 WebUI 删除经受限句柄，事务型驱动的删除失败回滚，旧键名与数据格式不变。
- 新增生产配置重启恢复、凭据端口优先级/失败不回退、旧配置覆盖拒绝、更新端口注入及取消等待、NvM 精确键越界/事务回滚、会话删除回滚测试。全仓 go test -race ./... 通过；go build ./...、golangci-lint run ./...（0 issues）、swc-check（8/0）、JS 语法检查通过。
- 仍未完成：完整生产编排、下载器/Range/转发 SWC、统一任务状态、账号连接/登录所有权、触发意图、面板导航、其余 NvM 数据集及多账号。真实 Telegram/aria2 端到端和新页视觉验收未做，不能据本轮宣称整体迁移完成；未提交或推送。

# 2026-09-18 继续迁移

- 转发队列业务迁入 application/forwarder，公共 ForwardTasks/ForwardRepository/ForwardTransport 位于 interfaces；Bot 转发处理器和 WebUI 转发控制通过业务端口调用。app/forward 保留 Telegram 传输与兼容入口；记录类型和 JSON 保持兼容，仓库实现迁入 bsw/cdd/taskhub。
- 连接期间转发工作线程由独立 RTE 宿主的 forward.queue Runnable 管理，等待发送退出后释放连接，Manager 健康查询汇总宿主。注册需要真实队列与传输；默认 swc-check 仍为八组件，不用空组件充数。
- 修复转发消息/相册错误被吞掉、暂停后旧快照继续启动、删除后进度/完成回调复活记录、人工重试未重置预算。控制与领取串行化，仓库 Update 在事务中只更新已有记录；任务通知不再脱离停机 context。
- NvM WriterSet 支持同 owner 多数据集联合事务，拒绝跨 owner 或未知数据集。tgauth 增加 peer、更新偏移和 access hash 受限存储，生产更新管理器接入；状态更新通过 storage.Update 保护跨句柄并发；KV 清理保留 access hash。
- comif 配额原地热更新：降额不回收在途租约，升额唤醒等待请求；HTTP Service 配置应用调用 Reconfigure，本地与 Range 仍共用实例。
- 清单仍有完整生产编排、下载器/Range SWC、统一任务状态、账号连接/登录所有权、触发意图、动态导航和多账号等，不能声称全部完成。保留现有未提交改动，未提交/推送。
- 本轮验证：全量 `go test -race ./...`、`go build ./...` 通过；`golangci-lint run ./...` 为 0 issues；默认/空装配检查为 8/0；组件页 JS 语法和 `git diff --check` 通过。没有执行真实 Telegram/aria2 网络端到端及页面视觉验收。

# 2026-09-18 下载器、Range 与文档续迁

- 本地下载执行、恢复、控制、进度和部分文件处理迁入 application/downloader.local，源元数据/配额/字节流通过接口注入；本地仓库迁入 taskhub。幂等创建保留原状态，更新在事务内只修改现存记录；迟到报告不能覆盖暂停/删除/重新创建或重新执行的任务，短流不能误标完成。连接期间使用真实 RTE 宿主。
- aria2 提交、查询、控制、恢复、重试及两类治理迁入 application/downloader.aria2；仓库迁入 taskhub；类型化 RPC 客户端和测试迁入 bsw/ecual/aria2rpc。Bot 通过 Aria2Tasks 调用。治理任务由 RTE 管理，退出等待子任务实际返回，不因内部超时而提前释放资源。
- HTTP Range、条件请求、HEAD、ETag、单区间/多区间传输迁入 application/proxy.range；HTTP 服务绑定实际传输和 RTE 宿主，停机取消并等待在途请求。Manager 健康及组件查询汇总 Range 和下载器宿主。
- watch 停机先等待 dispatcher 停止提交，再等待提交组、更新管理器和转发任务退出。原 HTTP 和下载回归测试继续通过兼容入口覆盖生产适配链路。
- 新增 swc-docs，从静态 Manifest 生成 docs/configuration.md；新增 docs/TODO-multi-account.md，修正组件和迁移清单的过时状态及前次追加的乱码。动态资源组件尚未独立配置，默认装配检查仍为 8/0。
- 全量竞态测试、构建和 lint 已通过；真实 Telegram/aria2 网络端到端、容器发布与页面视觉验收没有执行。整体生产编排、账号连接/登录所有权、触发意图、统一执行器状态和独立配置、页面资源归属、恢复策略与构建参数统一等仍未全部完成，不能宣称完成全部迁移。保留既有改动，未提交或推送。
- 后续补齐构建 linker 参数统一：internal/buildflags/ldflags.txt 由 Docker 的 cmd/buildflags 与 GoReleaser 的 mustReadFile/printf 共同读取，保持版本原值；修正 Makefile 旧参数，增加多架构模板一致性回归。CI 增加装配检查及生成配置文档差异检查。构建参数统一条目已从剩余清单移除，跨平台实际发布仍未验收。实际构建二进制的 version 命令已验证版本、提交和日期注入。

# 2026-09-18 动态生命周期、页面与意图续迁

- 新增 rte.Process；Bot、aria2、面板、HTTP、watch 控制器实际使用同一动态启停实现。停止超时保留实例、禁止重叠启动，退出错误和 panic 进入账号隔离的 Dem，支持有限恢复预算；Bot 仅对 net.Error 最多恢复 3 次。配置重启检查停止错误，主进程停机等待适配器真正退出后释放策略。
- 新增 application/panel.webui，持有 HTTP 监听及状态同步 Runnable；停机取消并等待请求。WebUI 登录关闭准入、取消并等待认证，避免后台认证仍访问资源时宿主已返回。Range 任务到期和源缓存清理也纳入 Runnable。
- 页面资源从 app/webui 移入 application 下对应组件 assets；公共壳归 panel.webui，账号/下载/转发/更新/AriaNg 资源各归组件。application.WebAssets 保持旧 URL；组件声明路由，WebUI 绑定处理器并统一鉴权。新增资源唯一归属、原 URL 和权限回归。静态源码路径已变化，检查 JS 应使用新 assets 路径。
- 新增 trigger.download，表情触发通过带账号的有界下载意图事件进入实际处理器，保持 peer 引用及背压；停机先等待事件消费者再等待提交组。队列失败不会同时回退旧通道，去重占位仍释放；无需回执的 watch 通知复用已有事件通知端口，去掉脱离 context 的 goroutine。
- 本地 ListTasks/ChangeTasks 适配迁入本地 SWC。统一查询增加 state 字段，waiting/queued 均映射 queued，保留原 status；尚未完成持久状态机与执行器降级。动态宿主不加入缺少实际资源的默认八组件检查。
- 完整配置协调、账号唯一连接/完整登录端口、消息获取及转发意图、独立配置、统一执行器状态机、其余控制面端口和多账号等仍有待办；不能把本次代码迁移描述成全部迁移完成。没有提交、推送或触发真实消息发送和发布。
- 本轮最终检查：`go test -race ./...`、`go build ./...`、`golangci-lint run ./...`（0 issues）、`swc-check`（8/0）及 `git diff --check` 通过；17 个迁移后的 JS 文件语法检查通过。真实 Telegram/aria2、浏览器视觉及跨平台发布未验收。

2026-09-18 再续迁：
- trigger.forward 接管表情/新消息转发的有界事件消费，与下载意图共用 IntentHost；停止排空后释放连接。去掉反应转发游离 goroutine，入队失败释放去重标记。
- account.telegram.Login 接管 WebUI 登录状态机，经 AccountLogin/LoginChallenge 公共端口调用；panel 显式依赖 account.telegram.login。验证码/密码阶段校验、重复提交、密码空格、取消后不提交切换、快照隔离都有测试。旧 WebUI 状态机测试迁入账号组件，WebUI 保留协议/存储/账号配置适配。
- download.control.Router 接入提交，有序 aria2→HTTP 链接降级只接受 ErrDownloadNotAccepted 且无任务 ID；未知 RPC 结果不降级。本地仍单一执行器，优先级不可配置。
- downloader.local 独立扫描间隔/停机记录超时 schema、生产文件加载、Manager 保存、热更新实际扫描及重启持久化接入。swc-docs 增加动态本地 schema；默认静态注册数仍 8。
- migration-status.md 已更新真实剩余项；账号唯一连接、Bot 完整登录端口、统一持久状态转换、其他动态配置、完整宿主装配和多账号仍未完成。

2026-09-18 单账号资源与下载链路续迁（用户明确排除多账户 TODO）：
- tgauth.Connections 统一生产主连接，账号资源由 account.telegram.resources 的 RTE 生命周期持有；监听、会话检查和面板协议请求共享。登录临时传输隔离，Bot/WebUI 登录准入互斥；Replace 阻止新准入，等待旧使用者与传输退出后提交会话。Stop 同时等待临时认证和会话替换。底层传输错误传回 watcher，不吞成普通取消；最后使用者正常返回不会因清理取消而误报失败。连接测试连续 30 轮 race 通过。恢复中间件为每次 Invoke 创建独立 backoff。
- taskhub 为 local/aria2 写入规范 state 和 revision，保护终态，aria2 Report 使用读取版本比较更新并保留其他字段；旧记录读取兼容，迟到状态不覆盖控制结果、不复活删除任务。aria2.status Runnable 独立于面板同步。
- MessageLinks.Submit 接管校验、源解析端口与有界 DownloadRequests 编排。请求关联回执处理取消、停止和 panic；Bot 预校验不再临时创建 RTE。
- forwarder 的扫描、重试和保留策略，proxy.range 的等待/保存超时与任务/源清理周期均接入生产组件配置文件及 Manager 保存。RTE RunDynamic 支持周期变化、诊断和失败后继续调度。配置回滚和重启恢复测试通过。
- download.control 增加 DownloadRouting 端口，executors/local_root 配置校验、持久化和快照隔离。生产 watcher 按顺序选择 aria2/local/http，明确拒绝才降级；local_root 必须是独立绝对路径，远端路径不会产生本地 IO。提交前准备的路由快照保持一致，本地相册目标仍经 Unique 去冲突。aria2 模块和 AutoDownload 开关仍有效。空优先级保留旧模式。
- go test -race ./... 通过；最后改动相关包再次 race 通过；go build ./...、golangci-lint（0 issues）、swc-check（8/0）、生成配置文档及 git diff --check 通过。Linux amd64/arm64、macOS arm64、Windows amd64 交叉构建已验证（在最后的动态配置/路由补充之前）；CI 增加发布目标构建矩阵。Docker daemon 未运行，真实 Telegram/aria2、更新替换和视觉验收未执行。
- 剩余代码任务仍见 docs/migration-status.md：完整生产配置协调/依赖图、aria2 和其他业务配置、Bot/面板完整业务端口等；不能声称除多账户之外全部已完成。未提交、推送或执行真实消息发送/发布。

2026-09-18 控制面与账号端口续迁：
- Bot 登录状态机及测试迁至 application/account.telegram/bot_login.go，接口为 ports.BotLogin/LoginRunner/LoginMessenger/BotLoginChallenge，不再依赖 Telegram/Bot SDK。app/bot/login_manager.go 只保留协议适配；BotLoginHost 构建实际 RTE，命令处理器使用解析出的端口，退出等待登录完成。Stop 超时保留活动流程、拒绝再准入，可再次等待；2FA 密码不再 TrimSpace。Bot 登录其他命名会话时 SessionOptions.Account 改为目标命名空间，保证实际锁定目标账号。
- account.telegram.session 提供 AccountSession，显式依赖 AccountResources；主宿主、Bot 启动和 WebUI 检查均注入该端口。会话检查绑定组件取消、校验账号与用户 ID，停止排空检查后释放资源。app/login.SessionProbe 负责 SDK 与纯身份数据转换；独立入口仍保留直接协议适配。
- SessionCatalog 规则迁入账号组件，过滤/去重/排序会话，拒绝删除当前会话及非法名称；BSW tgauth.SessionRepository 负责存储。Connections.ReplaceIdle 与 BeginLogin 双向互斥，防止删除时认证重新提交会话；已有 WebUI 会话测试及新增账号、并发测试通过。面板公开实际 Configuration/SessionCatalog 端口。
- 配置 DTO 与旧字段 JSON 解码迁入 interfaces/types/configuration.go；pkg/config 原类型为兼容别名，默认值和文件访问继续归适配器。panel.webui.ConfigurationService 持有配置编辑、反射字段访问、保护迁移字段和脱敏规则；app/webui 处理器调用 ports.Configuration，configurationStore 只负责配置持久化/校验/通知。新增 config.CompareAndSet，旧快照不能覆盖并发更改；规范化路径不能绕过 namespace 及已迁移字段保护。
- 完整 go test -race ./... 通过（会话目录补充前）；之后账号、tgauth、面板、Bot、runtime、配置相关包重复 race 验证。go build ./...、swc-check 8/0、swc-docs、git diff --check 通过；golangci-lint 最后为 0 issues。未运行真实 Telegram、发送消息、发布或提交代码。
- 仍有完整生产依赖图/配置协调、KV 目录与命令、其余组件独立配置及真实部署/视觉验收任务；详见 docs/migration-status.md。多账户 TODO 按用户要求排除。

2026-09-18 KV 维护与链接删除续迁：
- 新增 storage.maintenance 实际组件及 KVMaintenance/CleanupRepository 端口，Bot 使用常驻 MaintenanceHost。清理绑定组件与请求取消，停机等待活动调用，超时可再次等待；taskhub 输出快照前过滤凭据和协议键，删除时再次保护并比较快照，避免覆盖并发写入。
- 面板删除经 DownloadLinks 端口进入 download.control，只有本地删除成功才删除元数据；不再吞掉控制结果中的逐项错误。taskhub.LinkRepository 事务删除链接、aria2 关联与索引，扫描并重新核对无索引旧关联，兼容旧数据且可重试。
- 修复 Bot 消息入口仍 TrimSpace 登录输入的遗漏；增加真实分发入口密码原文回归。新增清理取消/排空/保护密钥/并发写入测试、链接删除顺序及三种 KV 驱动旧记录兼容测试。
- go test -race ./...、go build ./...、golangci-lint（0 issues）、swc-check（8/0）、swc-docs 和 git diff --check 通过。追加的 Bot 分发回归单独通过。文档修正历史待办，并明确 KV 目录/提交、完整装配、其余独立配置及真实环境验收仍未完成；未执行真实消息发送、发布、提交或推送。

2026-09-18 面板目录及重新提交续迁：
- 新增 ports.DownloadCatalog/CatalogSource 和 interfaces/types/download_catalog.go；application/download.control.Catalog 持有目录排序、滑动过期、完成判定、完成标记错误反馈以及批量重新提交。实际面板持有并提供该端口，HTTP 处理器经端口进入业务组件。
- taskhub.LinkRepository.Snapshot 限制输出到链接与 aria2 记录，排除索引和凭据，保留无索引旧数据并复制字节。app/webui/catalogAdapter 负责存储和协议观测转换、本地媒体恢复适配，远端重试关联发现及同步尚待收拢。
- 重新提交使用 BSW aria2rpc 类型化客户端与 taskhub.Aria2Repository；按任务 ID 去重，拒绝路径/保留 ID 和记录 ID 不一致，取消后停止后续提交。远端已接受但持久化失败时保留 Added 并报告 GID，不触发其他执行器重试。目录提交禁用仓库附带 TTL 清理，避免改变旧接口行为或删除无关关联。
- 新增账号/取消准入、批次去重、远端接受后的保存失败、滑动过期及短流完成判定、协议凭据隔离和真实本地 HTTP RPC 模拟回归。go test -race ./...、架构检查、go build ./...、swc-check 8/0、swc-docs 通过；最后相关面板包再次 race 通过，golangci-lint 为 0 issues。未执行真实远端消息/下载、发布、提交或推送。
- docs/migration-status.md 和 docs/components.md 已同步；尚有统一生产装配/配置协调、其余独立配置、重试关联和同步适配、其他处理器及真实环境验收。多账户 TODO 仍排除，不能宣称整体迁移完成。

2026-09-18 aria2 治理配置续迁：
- application/downloader.aria2.Manifest 新增 12 项实际治理参数：状态同步、连接重试起始/上限、零速扫描/阈值/暂停/操作超时，以及 Telegram 错误窗口/阈值/冷却/暂停/操作超时。PrepareConfig 一次发布不可变参数快照，拒绝 retry > maximum；状态周期使用 RTE RunDynamic，重连与零速扫描通过各自通知通道采用新参数。
- Aria2DownloadHost 接入 componentValues 配置读取，app/aria2.Manager 支持注入配置存储；app/runtime 首次构建和兼容配置触发重建均注入同一存储，SaveComponentConfiguration 将 downloader.aria2 路由到实际宿主。配置生成器添加 aria2 Manifest，docs/configuration.md 已重新生成。
- 新增热更新唤醒连接重试/状态同步/零速扫描、非法配置保留旧值、持久化重启恢复及生产宿主读取文件/拒绝禁用必需组件测试。原停机排空测试改为经 schema 配置扫描间隔。全库 go test -race ./...、go build ./...、swc-check 8/0、swc-docs 通过；架构测试曾拒绝根包外部测试自导入，已改同包测试后全部通过。
- 整体迁移尚未完成：RPC 地址/凭据等配置归属、统一生产配置协调和装配、其余业务处理器及真实环境验收仍见迁移状态表。多账户 TODO 继续排除。未运行真实远端操作、发布、提交或推送。
