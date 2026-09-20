// swc-docs renders the registered configuration schema without opening user
// configuration, storage, sessions or network connections.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/rte"
)

func main() {
	out := flag.String("out", "docs/configuration.md", "output Markdown file; - writes to stdout")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(path string) error {
	catalog, err := application.Catalog()
	if err != nil {
		return err
	}
	var buffer bytes.Buffer
	fmt.Fprint(&buffer, "# 组件配置参考\n\n")
	fmt.Fprint(&buffer, "由 `go run ./cmd/swc-docs` 根据已接入配置存储的组件 Manifest 生成。不要手工修改字段表。\n\n")
	fmt.Fprint(&buffer, "正常启动默认使用组件配置，首次启动将旧配置导入应用目录的 `components/<账号的 Base64URL 编码>/`，保留原文件。`--component-config <目录>` 可指定已有目录。文件名为 `swc-<组件 ID>.json`，结构为 `version/enabled/values`。敏感字段分离到 `secrets/` 文件，不在查询响应中返回。\n\n")
	fmt.Fprintf(&buffer, "本表覆盖统一目录中的 %d 个业务组件。组件文件是业务配置的权威来源；缺文件或缺字段使用 schema 默认值，不回退旧业务配置。协议适配器通过集中映射获取兼容 DTO，SWC 不读取旧全局配置。\n\n", len(catalog.Definitions()))
	fmt.Fprint(&buffer, "未启动或已停用组件仍可编辑配置。标注需要重启的字段先保存，再协调所属服务；失败时运行状态保留错误，配置页显示等待重启。其他字段经准备、校验和持久化后发布。秘密字段省略或留空表示保留已保存的值。组件模式下 ntp 留空使用系统时钟，不再自动改写旧配置文件。\n\n")
	fmt.Fprint(&buffer, "网络代理统一在 WebUI 的“网络配置 → 网络代理”中配置；网络配置位于设置标签首位并默认打开，选择协议后填写 IP 或域名加端口，需要认证时展开填写用户名和密码。由 account.telegram.proxy 保存，Telegram、机器人和软件更新共用。console.bot.proxy 与 update.self.proxy 仅兼容读取旧文件，不再生效或提供编辑入口。模块启停位于独立“模块管理”；运行日志、组件健康及诊断事件位于“检查更新”之前的独立“日志管理”，支持按全部功能、功能分组和细分模块查看。软件升级位于独立“检查更新”。\n\n")
	fmt.Fprint(&buffer, "`download.control.executors` 可设置为 `[\"aria2\",\"local\",\"http\"]`，选择本地执行器时必须同时提供本机绝对路径 `local_root`。只有明确未接受任务的错误允许降级；超时、响应丢失或已有任务 ID 时停止提交。aria2 仍需启用对应模块和自动下载。空列表沿用旧 downloader.mode；更改仅影响新提交，不迁移既有任务。\n\n")
	fmt.Fprint(&buffer, "下载模式、本地根目录、并发文件数、DC 连接数、过滤/命名、表情触发、HTTP 公网地址/TTL 和分组转发规则支持热更新。表中“需要重启”指自动协调对应服务及必要依赖，不等于整个进程重启。账号命名空间、存储位置和程序升级需要进程重启。\n\n")
	fmt.Fprint(&buffer, "WebUI 的“系统设置 → 一键完全重置”须再次确认后执行。重置会停止服务、关闭数据库与日志，再清空应用目录内 `.tdl/`、`components/` 的内容，删除 `config.json` 及其写入临时文件，包含所有账号的登录凭据、配置和历史密钥。自定义组件目录仅清理 TDL 的组件配置文件、密钥和迁移记录。目录本身保留以兼容挂载；完成后程序退出，下次启动需重新配置与登录，容器遵循自身重启策略。清理结果显示在程序控制台。\n\n")
	fmt.Fprint(&buffer, "新配置的 Telegram 内置预设默认为 `desktop`，WebUI 提供下拉选择并提示不建议修改；“强制使用内部预设”默认勾选。已有配置保留其预设及自定义凭据选择，旧版 `config.json` 省略开关时仍沿用历史自动选择。API ID 未设置时显示空输入框，API Hash 不回显，两者留空均保留原值。断线重试参数位于“网络配置 → 高级选项”，默认折叠；原有重试行为保持不变。\n\n")
	fmt.Fprint(&buffer, "日志管理默认显示当前账号和系统公共记录，支持级别、诊断类型、时间、关键词筛选及 JSON 导出；每页 200 条，最新页每 5 秒刷新，历史页与离开页面时停止自动刷新。面板查询最近 5000 条结构化记录，写入 `.tdl/log/events.jsonl`；原有 `latest.log` 继续保留。两类文件每 10 MB 轮转，备份最多 3 份且最长保留 7 天。重启恢复当前结构化日志文件，轮转备份需在服务器文件系统查看；旧版本只有文本日志的记录不自动转换。详细日志开关位于系统设置，保存后对后续日志立即生效。密码、Token 及常见敏感字段在结构化日志中脱敏；日志写入故障在页面提示，已有内存记录仍可查看。\n\n")
	components := []rte.Configuration{}
	for _, definition := range catalog.Definitions() {
		m := definition.Manifest
		components = append(components, rte.Configuration{ID: m.ID, Title: m.Title, Fields: m.Config, Scope: definition.Scope})
	}
	for _, component := range components {
		fmt.Fprintf(&buffer, "## %s — %s\n\n", component.ID, cell(component.Title))
		fmt.Fprintf(&buffer, "运行作用域：`%s`。\n\n", component.Scope)
		if len(component.Fields) == 0 {
			fmt.Fprint(&buffer, "无配置字段。\n\n")
			continue
		}
		fmt.Fprintln(&buffer, "| 字段 | 类型 | 默认值 | 范围 / 格式 | 敏感 | 需要重启 |\n| --- | --- | --- | --- | --- | --- |")
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
			if field.ReplacedBy != "" {
				bounds = "已停用，仅兼容读取旧文件；统一使用 " + field.ReplacedBy
			}
			restart := "否"
			if field.RestartRequired {
				restart = "是"
			}
			fmt.Fprintf(&buffer, "| `%s` | `%s` | `%s` | %s | %s | %s |\n", cell(field.Name), field.Type, cell(string(value)), cell(bounds), secret, restart)
		}
		fmt.Fprintln(&buffer)
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
