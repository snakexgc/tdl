// swc-docs renders the registered configuration schema without opening user
// configuration, storage, sessions or network connections.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/snakexgc/tdl/application"
	configuration "github.com/snakexgc/tdl/application/configuration.manager"
	"github.com/snakexgc/tdl/rte"
)

func main() {
	out := flag.String("out", "docs/configuration.md", "output Markdown file; - writes to stdout")
	configOut := flag.String("config-out", "", "also generate the complete default tdl_config.json template")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *configOut != "" {
		if err := writeDefaultConfig(*configOut); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func writeDefaultConfig(path string) error {
	catalog, err := application.Catalog()
	if err != nil {
		return err
	}
	doc, err := configuration.New(nil, catalog).Defaults(context.Background())
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func run(path string) error {
	catalog, err := application.Catalog()
	if err != nil {
		return err
	}
	var buffer bytes.Buffer
	fmt.Fprint(&buffer, "# TDL 统一配置参考\n\n")
	fmt.Fprint(&buffer, "由 `go run ./cmd/swc-docs` 根据已接入配置存储的组件 Manifest 生成。不要手工修改字段表。\n\n")
	fmt.Fprint(&buffer, "正常启动只使用应用目录的 `tdl_config.json`。`configuration.manager` SWC 集中负责全局配置的默认值补全、校验、版本检查和原子保存；业务组件通过 RTE 获取各自的配置视图。完整默认模板见 [examples/tdl_config.json](../examples/tdl_config.json)。模板由 `go run ./cmd/swc-docs -config-out examples/tdl_config.json` 生成，不含用户密钥。\n\n")
	fmt.Fprintf(&buffer, "本表覆盖 %d 个业务组件及统一配置管理 SWC。字段 schema 由业务组件声明，配置管理 SWC 通过统一 Catalog 收集；不再各自写配置文件。缺组件或缺字段使用 schema 默认值，不回退旧配置；未知组件、字段、错误类型或无效组合会阻止加载，停用组件同样校验。\n\n", len(catalog.Definitions())-1)
	fmt.Fprint(&buffer, "## 文件结构与系统设置\n\n`version` 当前为 `1`。`system.namespace` 默认 `default`，只允许英文字母，选择启动时使用的登录会话与历史任务；WebUI 通过账号管理切换并重启进程，同一进程只运行一个 Telegram 账号。`system.debug` 默认 `false`，控制详细日志，保存并手动重启后生效。`components` 的键为下面列出的组件 ID，每个组件由 `enabled` 和 `values` 组成。`trigger.forward` 默认停用，其余组件默认启用；配置管理 SWC 固定启用，不需要在 components 下声明。HTTP 下载服务也固定启用，旧 proxy.range.enabled=false 不再关闭监听。所有设置全局共用，切换命名空间不会重置端口、密码、代理或其他组件设置。\n\n手工编辑的路径示例：`components[\"downloader.aria2\"].values.monitor_stall_seconds`。组件 ID 中的点属于键名，不代表嵌套对象。JSON 不支持注释。建议停止程序后编辑文件，再启动生效；运行中使用 WebUI 保存只写入统一文件，点击设置页顶部“保存并重启”后统一生效。\n\n")
	fmt.Fprint(&buffer, "## 首次启动与配置校验\n\n程序只读取应用目录中的 `tdl_config.json`。文件不存在时生成当前版本的完整默认配置；文件损坏、版本不支持或含有未知字段时直接报错。不导入 `config.json`、`components/` 或旧存储驱动，也不提供迁移命令。可以执行 `tdl config-init --home <应用目录>` 离线创建或校验配置，无需启动 Telegram、aria2 或 WebUI。\n\n敏感值与其他配置一起保存在统一文件，写入权限为 0600（Windows 使用文件系统 ACL）；WebUI 查询不回显密钥。根目录实际 `tdl_config.json` 已加入 Git 和 Docker 构建忽略规则。分享配置时使用默认模板。\n\n")
	fmt.Fprint(&buffer, "运行时快照仅由组件 schema 和统一仓库生成，缺少仓库时直接报错；不存在平面配置覆盖或第二套宽松校验。嵌套规则对象的未知字段也会被拒绝。历史兼容清理范围和开发约定见 [开发基准](baseline.md)。\n\n")
	fmt.Fprint(&buffer, "## 配置范围与启动参数\n\n`TDL_HOME` 环境变量指定应用目录，未设置时使用可执行文件所在目录；它在读取配置前确定，不能放进同一个 JSON。统一文件固定为 `<应用目录>/tdl_config.json`，会话与任务数据库位于 `.tdl/`，日志位于 `.tdl/log/`。`config-init --home` 仅选择离线工具操作的目录；普通启动仍使用 `TDL_HOME`。没有其他业务配置环境变量覆盖。数据库记录、登录会话、任务状态、浏览器筛选条件与外部 aria2/Docker 的配置不属于 TDL 设置。日志容量/轮转、HTTP 内部缓冲等代码常量当前不是用户配置项。所有现有用户设置及可调重试/超时/清理周期都列在下面。\n\n")
	fmt.Fprint(&buffer, "未启动或已停用组件仍可编辑配置。所有设置经校验后仅保存到文件，不自动重启或修改运行中的服务。启动配置保持不变，断线重连也继续使用启动值。WebUI 中秘密字段省略或留空表示保留已保存的值；直接编辑 JSON 时，空值表示清空，省略则使用默认值。启动顺序为本地配置与资源装配、HTTP 下载服务监听、WebUI 监听就绪，然后启动时间同步 SWC 和各后台模块；网络异常不会阻塞面板。完整模块依赖见 [启动顺序与网络故障隔离](startup.md)。\n\n时间校准由独立的 `time.sync` SWC 负责，默认每 300 秒同步一次，可将 `sync_interval_seconds` 设为 60。首选服务器配置为 `components[\"time.sync\"].values.server`；留空或不可达时并发探测内置候选，选择最快的有效响应。首次同步之前使用系统时间，后续失败保留最近成功的偏移并继续重试。其他组件通过 RTE 时间端口读取，不等待网络，也不重复查询 NTP。同步结果只更新运行状态，不改写配置或操作系统时间。旧 `account.telegram.ntp` 在加载时兼容迁移，新字段显式填写时优先；下次保存写入新结构。详细配置、状态和接口见 [时间同步 SWC](time.md)。\n\n")
	fmt.Fprint(&buffer, "网络代理统一在 WebUI 的“网络连接 → 网络代理与连接”中配置；网络连接位于设置标签首位并默认打开，选择协议后填写 IP 或域名加端口，需要认证时展开填写用户名和密码。由 account.telegram.proxy 保存，Telegram、机器人和软件更新共用。模块启停位于独立“模块管理”；运行日志、组件健康及诊断事件位于“检查更新”之前的独立“日志管理”，支持按全部功能、功能分组和细分模块查看。软件升级位于独立“检查更新”。\n\n")
	fmt.Fprint(&buffer, "WebUI 设置覆盖全部 75 个组件字段及两个系统设置。全部字段直接展开，支持按中文名称、说明或字段名搜索并定位；搜索不索引实际配置值。分类和区块显示未保存数量。顶部仅保留“刷新”，从 tdl_config.json 重新读取配置，替换当前未保存输入。下载方式单选本地下载器或 aria2，HTTP 服务始终启动供两种方式和复制直链使用；本地下载目录与 aria2 保存目录分别说明所属机器。字段显示默认值、范围和生效方式，输入错误在字段旁提示。账号数据空间只展示，切换入口在账号管理；完全重置位于系统的应用维护区。\n\n有修改时，顶部显示“尚未保存的修改”框，逐项对比当前运行值和修改后的值，并区分尚未保存与已保存待重启；没有修改时隐藏整个框及按钮。框内“保存并重启”一次校验并保存所有分类的草稿，然后请求重启；也可用区块按钮先保存文件。校验或保存失败不重启，未保存输入保留，已成功写入文件的区块会单独标记。普通字段及允许操作机器人的用户列表直接填入输入框，代理和 aria2 地址也将去掉认证信息后的地址填入输入框，不在下方重复显示已保存值；地址未修改时保留完整配置，修改地址时需同时填写所需的认证信息；API Hash、Token、密码和 RPC 密钥不回显。刷新页面仍可看到文件与运行值的差异，改回启动值并保存后移除对应项。重启会暂时中断连接和任务，新配置生效后清单清空；如修改了面板地址或凭据，请使用新地址或凭据重新登录。\n\n")
	fmt.Fprint(&buffer, "`download.control.executors` 设置为 `[\"local\",\"http\"]` 或 `[\"aria2\",\"http\"]`，`local_root` 留空时使用 TDL 可执行文件所在目录下的 `download` 文件夹，提交本地下载时自动创建；与当前工作目录和 `TDL_HOME` 无关。自定义目录须填写本机绝对路径。只有明确未接受任务的错误允许降级；超时、响应丢失或已有任务 ID 时停止提交。aria2 仍需启用对应模块和自动下载。列表不能为空，默认使用 aria2 并保留 HTTP 链接。旧多下载器数组仅采用第一个下载器，保留末尾 HTTP；旧 http 单项配置继续仅生成链接。保存并重启后切换下载器。本地模式不启动 aria2 管理，下载管理页显示本地磁盘剩余空间和保存目录内文件的数量、逻辑大小；目录未创建时查询已有父目录所在磁盘，统计不跟随符号链接，排除已知未完成任务，最多缓存 30 秒。只接受 local、aria2、http，不接受 internal 或 mode 字段；本地任务只使用 download.local 键空间，任务记录必须具有当前状态与版本字段，不自动补齐历史记录。\n\n")
	fmt.Fprint(&buffer, "链接记录中的“打开下载链接”将文件下载到浏览器；“加入队列”则提交给 TDL 的本地下载器。HTTP 链接优先读取关联任务已经完整下载的本地文件，要求任务来源、文件大小和完成状态一致；这种传输无需 Telegram 连接，也不占用 Telegram 下载名额。文件缺失、不可读或未完成时仍从 Telegram 读取。两种来源都支持完整下载、HEAD、单段及多段 Range，并统一记录 HTTP 传输进度。\n\n")
	fmt.Fprint(&buffer, "所有配置字段及模块启停均需用户手动重启进程后生效，包括下载执行器、过滤/命名、转发规则和详细日志。配置保存不触发服务协调或自动重启。账号切换、程序升级与完全重置继续由各自明确的操作入口执行。\n\n")
	fmt.Fprint(&buffer, "HTTP 下载记录必须包含有效的 `last_active_at`；缺失或损坏时读取、续期和清理会报错，不回退到创建时间或索引时间，也不自动补写。软件更新只接受与当前操作系统、架构和 ARM 版本完全匹配的发布包，以及包内根目录的 `tdl` / `tdl.exe`。\n\n")
	fmt.Fprint(&buffer, "WebUI 的“系统 → 应用维护 → 完全重置”须再次确认后执行。重置会停止服务、关闭数据库与日志，再清空应用目录内 `.tdl/` 的内容，删除 `tdl_config.json` 及其写入临时文件，包含全局配置、所有账号的登录凭据和任务记录。数据目录本身保留以支持挂载；完成后程序退出，下次启动需重新配置与登录，容器遵循自身重启策略。清理结果显示在程序控制台。\n\n")
	fmt.Fprint(&buffer, "新配置的 Telegram 内置预设默认为 `desktop`，WebUI 提供下拉选择并提示不建议修改；“使用内置 API 凭据”默认勾选，位于账号设置的常用选项。API ID 未设置时显示空输入框，API Hash 不回显，两者留空均保留原值。内置预设必须明确选择；已有会话缺少凭据指纹时需要重新登录，不再猜测原应用。断线重试和时间校准直接显示在“网络连接”中；重连间隔必须为 1–86400 秒，默认 3 秒，0 不再作为另一套默认值的别名。\n\n")
	fmt.Fprint(&buffer, "日志管理默认显示当前账号和系统公共记录，支持级别、诊断类型、时间、关键词筛选及 JSON 导出；每页 200 条，最新页每 5 秒刷新，历史页与离开页面时停止自动刷新。面板查询最近 2000 条结构化记录，写入 `.tdl/log/events.jsonl`；原有 `latest.log` 继续保留。两类文件每 10 MB 轮转，备份最多 3 份且最长保留 7 天。重启恢复当前结构化日志文件，轮转备份需在服务器文件系统查看；旧版本只有文本日志的记录不自动转换。详细日志开关位于系统设置，保存并手动重启后生效。CMD／终端仅保留启动、停止和 WebUI 地址等简短提示，详细运行日志在 WebUI 查看；文本日志和结构化日志统一脱敏，日志写入故障按输出端在页面提示，已有内存记录仍可查看。沿用 DEBUG（高频细节与正常取消）、INFO（关键结果）、WARN（可恢复异常）、ERROR（最终失败）四级；默认 INFO，详细日志开启后记录 DEBUG。分级、字段和开发约定见 [日志规范](logging.md)。\n\n")
	fmt.Fprint(&buffer, "HTTP 下载的上游并发由 `components[\"account.telegram\"].values.dc_pool_size` 控制，默认 8。即使外部下载器发来 32 路 Range 请求，同一 DC 同时执行的 Telegram 分片请求也不超过此值；不同 DC 分别计数，同一 DC 的不同文件及本地下载器共享额度。当前调度优先处理先激活文件的待取分片，空余额度自动供其他文件使用，并非按 HTTP 连接平均分配。`file_limit` 另行限制全局同时下载的不同 Telegram 文件数，同一文件的多个 Range 只占一个文件名额；默认 1，需要同时读取不同文件时可调大。单次 Telegram 请求结束即释放 DC 额度，HTTP 慢速接收及重试间隔不占用该额度；取消请求会退出排队。已完成本地文件的 HTTP 直读绕过这两项 Telegram 限制。修改配置后需重启生效。\n\n")
	fmt.Fprint(&buffer, "HTTP Range 可以从任意字节开始，服务按请求位置读取并裁切后返回。Telegram 开启 `precise` 时要求偏移和长度均为 1 KiB 的倍数、单次最多 1 MiB，且不能跨越从文件起点计算的 1 MiB 边界；不要求每次读满 1 MiB。文件末尾按实际长度返回，不向 HTTP 响应填充对齐字节；本地文件直接按 Range 读取，无需 Telegram 分片对齐。协议依据见 [Telegram 文件下载文档](https://core.telegram.org/api/files#downloading-files)。HEAD 始终返回完整文件长度且无响应体，即使客户端附带 Range 也不申请下载额度。\n\n")
	components := []rte.Configuration{}
	for _, definition := range catalog.Definitions() {
		m := definition.Manifest
		components = append(components, rte.Configuration{ID: m.ID, Title: m.Title, Fields: m.Config, Scope: definition.Scope})
	}
	for _, component := range components {
		fmt.Fprintf(&buffer, "## %s — %s\n\n", component.ID, cell(component.Title))
		fmt.Fprintf(&buffer, "运行作用域：`%s`。\n\n", component.Scope)
		if len(component.Fields) == 0 {
			if component.ID == configuration.ID {
				fmt.Fprint(&buffer, "固定启用，统一管理顶层 system 设置及全局组件设置；自身不另建组件配置。\n\n")
				continue
			}
			fmt.Fprint(&buffer, "无配置字段。\n\n")
			continue
		}
		fmt.Fprintln(&buffer, "| 字段 | 含义 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 | WebUI 分类 / 区块 | 填写说明 |\n| --- | --- | --- | --- | --- | --- | --- | --- | --- |")
		for _, field := range component.Fields {
			value, err := json.Marshal(field.Default)
			if err != nil {
				return err
			}
			bounds := "—"
			if field.Min != nil {
				bounds = fmt.Sprintf("≥ %d", *field.Min)
			}
			if field.Max != nil {
				if field.Min != nil {
					bounds += fmt.Sprintf("，≤ %d", *field.Max)
				} else {
					bounds = fmt.Sprintf("≤ %d", *field.Max)
				}
			}
			secret := "否"
			if field.Secret {
				secret = "是"
			}
			if len(field.Choices) > 0 {
				bounds = strings.Join(field.Choices, " / ")
			}
			if field.Format != "" {
				bounds = field.Format
			}
			restart := "是"
			location := field.SettingsTab + " / " + field.SettingsSection
			fmt.Fprintf(&buffer, "| `%s` | %s | `%s` | `%s` | %s | %s | %s | %s | %s |\n", cell(field.Name), cell(field.Title), field.Type, cell(string(value)), cell(bounds), secret, restart, cell(location), cell(field.Help))
		}
		fmt.Fprintln(&buffer)
		if component.ID == "forward.rules" {
			fmt.Fprint(&buffer, "`rules` 每项包含 `id`（非空且唯一）、`name`（最多 100 字）、`enabled`、`sources`（来源数组）、`targets`（目标数组）、`mode`（default/clone）、`silent`。来源/目标格式为 `user:123`、`chat:123`、`channel:123`；目标也可为 `self`。最多 200 条规则，每条最多 500 个来源和 100 个目标，不允许转发环路。示例：`{\"id\":\"saved\",\"name\":\"收藏\",\"enabled\":true,\"sources\":[\"channel:123\"],\"targets\":[\"self\"],\"mode\":\"default\",\"silent\":false}`。\n\n")
		}
	}
	if path == "-" {
		_, err = os.Stdout.Write(buffer.Bytes())
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buffer.Bytes(), 0o644)
}

func cell(value string) string {
	return strings.NewReplacer("|", "&#124;", "\n", " ", "\r", " ", "`", "&#96;").Replace(value)
}
