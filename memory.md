# Project Memory

> 本文件保存经过当前代码状态验证的长期项目上下文。处理项目任务前应先核验，任务完成后应同步更新。

## 基本信息

- 项目名称：tdl
- 项目路径：`D:\gitrepo\tdl`
- 项目类型：Go 应用，包含 Telegram 下载、机器人、监听转发、HTTP 下载代理和 Web 管理功能
- 主要技术栈：Go 1.25.8、Cobra、gotd/td、GitHub Actions、GoReleaser、Docker
- Go 模块：根模块 `github.com/snakexgc/tdl`，本地替换并引用 `core/` 子模块
- 构建方式：`go build`；发布构建由 `.goreleaser.yaml` 和 GitHub Actions 完成
- 测试方式：`go test -v $(go list ./... | grep -v /test)`；CI 另运行 golangci-lint
- 最后更新时间：2026-08-11

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
- `core/`
  - 独立 Go 子模块，封装 Telegram 核心能力。
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

- 当前分支：`master`，基于提交 `05be125`；当前存在本次未提交的依赖升级修改。
- 最近修改：将根模块和 `core/` 的 `github.com/go-faster/errors` 升级到 v0.8.0，并将 `core/` 直接依赖及根模块间接依赖 `github.com/gabriel-vasile/mimetype` 升级到 v1.4.15。
- 当前已知问题：未发现由本次依赖升级引起的编译或测试问题。
- 已验证内容：根模块与 `core/` 的完整测试、构建和模块校验均通过。
- 上一次统一依赖升级已进入 master，仓库存在按新规则生成的 `3.12.3-prerelease.58.1.sha05be125` tag。

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

- [ ] 提交并推送依赖升级后，在 GitHub Actions 中验证 lint、测试和 release job。

## 最近一次任务摘要

- 任务：升级根模块与 `core/` 的 `go-faster/errors` 和 `mimetype`。
- 完成内容：`errors` 统一升级到 v0.8.0；冲突的 `mimetype` 目标采用更高的 v1.4.15，并同步根模块间接依赖。
- 修改文件：两个模块的 `go.mod`/`go.sum`、`memory.md`。
- 验证结果：两个模块的测试、构建、模块校验和最终版本解析均通过。
- 下一步：提交并推送修改，然后观察 GitHub Actions。
