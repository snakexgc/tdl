# AUTOSAR 重构检查与修复记录

> 本文是当时的检查记录，其中兼容层和迁移状态不代表当前实现。当前配置行为见 [统一配置参考](configuration.md)。

本次检查基于 `9e4c2d7`，日期为 2026-09-19。检查范围包括分层依赖、组件装配、资源生命周期、端口归属、配置与文档生成，以及现有 Go / WebUI 测试和 CI。

项目采用了 AUTOSAR 的应用组件、RTE、BSW 分层思想。审查依据是 [AUTOSAR Classic Platform 官方架构说明](https://www.autosar.org/standards/classic-platform)中关于三层划分与组件端口通信的原则。本项目是 Go 服务，以下结论是代码架构与行为检查，不代表完整 AUTOSAR Classic / Adaptive 规范符合性认证。

## 当前结构

| 目录 | 当前职责 |
| --- | --- |
| `application/<component>` | 业务组件及 Manifest、配置 schema、页面、命令 |
| `interfaces` | 端口接口、跨组件 DTO、Manifest 定义 |
| `rte` | 依赖图装配、生命周期、端口绑定、事件、调度、组件配置目录 |
| `bsw` | 存储、诊断、账户连接、任务仓库及协议适配等基础服务 |
| `app/runtime`、其他 `app` 包 | 生产资源装配与保留的兼容适配层 |
| `internal/architecture` | 依赖方向及任务存储键所有权的静态检查 |

统一目录声明了 18 个业务组件。`swc-check` 默认装载其中 9 个具有静态工厂的组件；依赖生产资源的其他组件由各资源宿主装配。因此，静态装配检查通过不能单独证明完整生产系统可运行。

## 已确认并修复的问题

| 编号 | 问题与影响 | 修复位置与行为 | 回归覆盖 |
| --- | --- | --- | --- |
| 1 | 资源进程异常退出时，协调器直接清除 active 标记，跳过旧资源清理，也不重启持有旧端口的消费者。 | `rte/reconciler.go`：保留旧资源所有权并标记待停止；先停止消费者，再清理旧资源，成功后才重启。使用旧资源的 Running 回调观察其状态。 | `TestProductionGraphRestartsConsumersAfterProviderExits`，含清理超时与重试。 |
| 2 | 操作上下文已取消时，资源协调仍可能调用 Stop；组件协调仍会取消运行上下文、改为 stopping，影响本来正常工作的组件。 | `rte/reconciler.go`、`rte/reconcile_components.go`：取得锁后立即检查取消状态，在修改运行状态前返回错误。 | 两个 `Canceled…PreservesRunning…` 回归测试。 |
| 3 | Runtime 关闭成功后，从未完成初始化的 failed / blocked 组件不会转为 stopped，监控状态与实际生命周期不一致。 | `rte/rte.go`：无待清理资源的实例同样发布 stopped 状态，诊断历史保留。 | `TestStopMarksFailedAndBlockedComponentsStopped`。 |
| 4 | 关闭未完成时仍可解析出提供者端口；目录查询只检查组件存在，未检查当前端口归属，且状态与解析分两次加锁。 | `rte/rte.go`、`rte/directory.go`：在同一锁内核对实际提供者、running 状态和端口值。 | `TestDirectoryChecksLivePortOwnership`，以及停止失败期间拒绝新端口解析的测试。 |
| 5 | BSW 架构检查只禁止 `app/`、`application/` 子包，遗漏包根路径，也完全遗漏对 RTE 的反向依赖限制。 | `internal/architecture/ownership_test.go`：同时禁止 `app`、`application`、`rte` 的根包与子包，并保留允许的接口及基础设施依赖。 | `TestBSWImportBoundary` 及全库架构扫描。本次未发现现有 BSW 代码实际违反该规则。 |
| 6 | 仓库未包含生成文档，空目录不会随 Git 检出；文档生成器不创建父目录，导致干净检出中的 CI 生成步骤失败。原 `git diff` 也检查不到未跟踪文档。 | `cmd/swc-docs/main.go`：创建输出目录；补充 `docs/configuration.md`；CI 增加跟踪文件存在性检查。 | `TestRunCreatesOutputDirectory`，文档重复生成的一致性检查。 |
| 7 | 已有组件前端测试没有纳入 CI，页面挂载、设置草稿和事件订阅回归无法阻止合并。 | `.github/workflows/master.yml`：安装 Node.js 24，并使用必要的 `--experimental-vm-modules` 参数运行现有测试。 | `internal/integration/*.test.mjs` 的 4 个测试。 |

上述运行时问题和缺失输出目录的问题均先用回归测试确认修复前失败，再验证修复后的行为。

## 验证命令

最终代码的全量竞态测试通过，静态检查为 0 issues，程序构建成功；默认及空注册表装配检查、4 个前端测试、文档重复生成一致性检查均通过。初始版本的普通 Go 全量测试也通过，说明新增回归测试覆盖了原测试未发现的边界。

```sh
go test ./...
go test -race -p 2 ./...
golangci-lint run --timeout 5m
go build -o .tdl/autosar-review/tdl.exe .
go run ./cmd/swc-check
go run ./cmd/swc-check -empty
go run ./cmd/swc-docs
node --experimental-vm-modules --test internal/integration/*.test.mjs
git diff --check
```

测试使用 Windows/amd64、Go 1.26.7、Node.js 24.18.0。没有启动真实 Telegram 账户、aria2 服务或执行发布操作；自动化验证覆盖不到这些外部服务的实际连通性。生产兼容层仍存在，本次没有将“目录分层完成”推断为所有平台服务抽象均已迁移完成。
