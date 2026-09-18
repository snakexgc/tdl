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
	downloadcontrol "github.com/snakexgc/tdl/application/download.control"
	aria2 "github.com/snakexgc/tdl/application/downloader.aria2"
	local "github.com/snakexgc/tdl/application/downloader.local"
	"github.com/snakexgc/tdl/application/forwarder"
	proxy "github.com/snakexgc/tdl/application/proxy.range"
	"github.com/snakexgc/tdl/interfaces/types"
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
	registry, err := application.Registry()
	if err != nil {
		return err
	}
	host, err := registry.Build(types.DefaultAccount, nil, nil)
	if err != nil {
		return err
	}
	var buffer bytes.Buffer
	fmt.Fprint(&buffer, "# 组件配置参考\n\n")
	fmt.Fprint(&buffer, "由 `go run ./cmd/swc-docs` 根据已接入配置存储的组件 Manifest 生成。不要手工修改字段表。\n\n")
	fmt.Fprint(&buffer, "使用 `--component-config <目录>` 启用组件配置；文件名为 `swc-<组件 ID>.json`，结构为 `version/enabled/values`。敏感字段通过配置存储分离到 `secrets/` 文件，不在组件查询响应中返回。\n\n")
	fmt.Fprint(&buffer, "本表包含八个静态组件，以及本地下载器、aria2 治理、转发队列、Range 和下载路由的配置。动态组件运行时可热更新并保存配置；aria2 RPC 连接地址、共享并发配额及其他未拆分设置仍使用兼容配置，本表不表示整个配置迁移已完成。\n\n")
	fmt.Fprint(&buffer, "`download.control.executors` 可设置为 `[\"aria2\",\"local\",\"http\"]`，选择本地执行器时必须同时提供本机绝对路径 `local_root`。只有明确未接受任务的错误允许降级；超时、响应丢失或已有任务 ID 时停止提交。aria2 仍需启用对应模块和自动下载。空列表沿用旧 downloader.mode；更改仅影响新提交，不迁移既有任务。\n\n")
	components := host.Configurations()
	localManifest := local.Manifest()
	aria2Manifest := aria2.Manifest()
	components = append(components, rte.Configuration{ID: aria2Manifest.ID, Title: aria2Manifest.Title, Fields: aria2Manifest.Config})
	components = append(components, rte.Configuration{ID: localManifest.ID, Title: localManifest.Title, Fields: localManifest.Config})
	forwardManifest := forwarder.Manifest()
	components = append(components, rte.Configuration{ID: forwardManifest.ID, Title: forwardManifest.Title, Fields: forwardManifest.Config})
	rangeManifest := proxy.Manifest()
	components = append(components, rte.Configuration{ID: rangeManifest.ID, Title: rangeManifest.Title, Fields: rangeManifest.Config})
	routingManifest := downloadcontrol.Manifest()
	components = append(components, rte.Configuration{ID: routingManifest.ID, Title: routingManifest.Title, Fields: routingManifest.Config})
	for _, component := range components {
		fmt.Fprintf(&buffer, "## %s — %s\n\n", component.ID, cell(component.Title))
		if len(component.Fields) == 0 {
			fmt.Fprint(&buffer, "无配置字段。\n\n")
			continue
		}
		fmt.Fprintln(&buffer, "| 字段 | 类型 | 默认值 | 范围 | 敏感 |\n| --- | --- | --- | --- | --- |")
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
			fmt.Fprintf(&buffer, "| `%s` | `%s` | `%s` | %s | %s |\n", cell(field.Name), field.Type, cell(string(value)), bounds, secret)
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
