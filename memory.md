# Project Memory

> 本文件保存经过当前代码状态验证的长期项目上下文。处理项目任务前应先核验，任务完成后应同步更新。

## 基本信息

- 项目名称：tdl
- 项目路径：`D:\gitrepo\tdl`
- 项目类型：Go 应用，包含 Telegram 下载、机器人、监听转发、HTTP 下载代理和 Web 管理功能
- 主要技术栈：Go 1.25.8、Cobra、gotd/td、GitHub Actions、GoReleaser、Docker
- Go 模块：根模块 `github.com/iyear/tdl`，本地替换并引用 `core/` 子模块
- 构建方式：`go build`；发布构建由 `.goreleaser.yaml` 和 GitHub Actions 完成
- 测试方式：`go test -v $(go list ./... | grep -v /test)`；CI 另运行 golangci-lint
- 最后更新时间：2026-07-21

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

- 当前分支：`master`，基于提交 `4ec6caf`。
- 最近修改：修复 GoReleaser snapshot 因历史非 SemVer prerelease tag 而失败的问题。
- 当前已知问题：尚未在 GitHub Actions runner 上重新运行修复后的 release workflow。
- 已验证内容：release metadata Bash 脚本在带有历史坏 tag 的独立临时 clone 中执行成功；workflow YAML 可解析。

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

## 待处理事项

- [ ] 推送本次 workflow 修复后，在 GitHub Actions 中验证完整 release job。

## 最近一次任务摘要

- 任务：修复 GoReleaser snapshot 的非 SemVer tag 解析失败。
- 完成内容：兼容清理历史坏 tag，并改用 SemVer 预发布 tag。
- 修改文件：`.github/workflows/release.yml`、`memory.md`。
- 验证结果：本地临时 clone 的实际 metadata 脚本与 YAML 解析均通过；远端 workflow 尚未重跑。
- 下一步：提交并推送修改，然后观察 release workflow。
