# 组件配置迁移与回退

导出覆盖 17 个组件及对应模块的启用状态。旧八组件导出不是完整迁移；建议从原配置导出到新目录，再合并旧组件目录中的显式自定义值。

## 预览与导出

```powershell
tdl migrate-config --source ./config.json
tdl migrate-config --source ./config.json --out ./component-config --write
# 开发环境及保留的工具入口：
go run . migrate-config --source ./config.json
go run ./cmd/swc-migrate -source ./config.json -out ./another-directory -write
```

预览只读取、校验并输出账号和组件 ID，不初始化守护进程、日志、数据库、NTP 或会话，不输出字段值。输入上限 1 MiB，拒绝未知字段及多份 JSON，兼容 file_size_mb 和 internal 下载模式。

目标目录必须不存在且父目录已存在。失败只清理本次新建输出，migration.json 最后写入。组件文件包含 version/enabled/values，秘密分离到 secrets 下的版本文件；备份须包含整个目录。

## 启动

```powershell
go run ./cmd/swc-check -config-dir ./component-config
tdl --component-config ./component-config
```

静态检查只验证八组件注册表；迁移命令内部校验全部目录 schema 和离线语义。两者都不代替实际启动。

组件文件是权威来源，缺文件/字段使用 schema 默认值。缺省白名单为空，Bot 拒绝全部用户。组件模式下 ntp 为空使用系统时钟，不自动改写旧配置。

模块页保留旧进程开关，组件页可保存所有组件的启用状态并触发宿主协调。常驻策略目前共用宿主，启停会重建该宿主及消费者；连接内组件随连接宿主重新装配，尚未做到每个 SWC 单独隔离。旧配置保留账号命名空间、调试等 bootstrap 设置及回退来源；会话和原存储路径不变，旧业务字段不用于回填组件默认值。

## 保存及恢复

离线和停用组件可以编辑。秘密留空保留，枚举、URL、范围及组件语义在保存前验证。热更新保存后发布，重启字段显示提示；协调失败保留诊断和待生效状态。

HTTP 409 表示其他窗口已保存，重新加载并合并，不直接重试旧表单。API 不提供跨进程或外部编辑器文件锁，不让多个进程共用可写目录。

变更面板地址后使用新地址连接。连接设置修改前保留完整备份；恢复失败时正常停止进程、恢复备份再启动，旧实例未退出前不得启动第二份。

## 回退

1. 正常停止进程，等待任务和共享连接退出。
2. 保留完整组件目录及 migration.json。
3. 使用原配置、原会话目录，省略 --component-config 启动。
4. 在独立测试任务中检查账号、下载模式、目录、端口和权限。
5. 重新迁移时写入新的目录，不覆盖旧输出。

工具不改源配置和会话。组件页后续修改不会反向写回原配置；若回退也要保留这些修改，应停止服务后明确同步并校验。

真实后端、视觉和发布验收见 [当前清单](migration-status.md)。
