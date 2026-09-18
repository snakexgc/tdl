// swc-docs renders the registered configuration schema without opening user
// configuration, storage, sessions or network connections.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
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
	fmt.Fprint(&buffer, "使用 `--component-config <目录>` 启用组件配置；文件名为 `swc-<组件 ID>.json`，结构为 `version/enabled/values`。敏感字段通过配置存储分离到 `secrets/` 文件，不在组件查询响应中返回。\n\n")
	fmt.Fprintf(&buffer, "本表覆盖统一目录中的 %d 个业务组件（八个静态组件和九个按资源装配的组件）。组件文件是权威来源；缺文件或缺字段使用 schema 默认值，不回退旧业务配置。生产兼容适配器仍使用普通配置 DTO；这不代表旧编排及真实环境验收已经全部完成。\n\n", len(catalog.Definitions()))
	fmt.Fprint(&buffer, "未启动或已停用组件仍可编辑配置。标注需要重启的字段先保存，再协调所属服务；失败时运行状态保留错误，配置页显示等待重启。其他字段经准备、校验和持久化后发布。秘密字段省略或留空表示保留已保存的值。组件模式下 ntp 留空使用系统时钟，不再自动改写旧配置文件。\n\n")
	fmt.Fprint(&buffer, "`download.control.executors` 可设置为 `[\"aria2\",\"local\",\"http\"]`，选择本地执行器时必须同时提供本机绝对路径 `local_root`。只有明确未接受任务的错误允许降级；超时、响应丢失或已有任务 ID 时停止提交。aria2 仍需启用对应模块和自动下载。空列表沿用旧 downloader.mode；更改仅影响新提交，不迁移既有任务。\n\n")
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
	return os.WriteFile(path, buffer.Bytes(), 0o644)
}

func cell(value string) string {
	return strings.NewReplacer("|", "&#124;", "\n", " ", "\r", " ", "`", "&#96;").Replace(value)
}
