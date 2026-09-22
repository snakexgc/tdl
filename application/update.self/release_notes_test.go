package updater

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/html"
)

func TestReleaseNotesRenderMarkdown(t *testing.T) {
	source := "# 发布说明\n\n## 改进\n\n**加粗**、*强调*、~~删除~~和 `tdl version`。\n\n" +
		"- 下载管理\n  - 嵌套说明\n\n1. 检查更新\n2. 选择版本\n\n" +
		"> 更新后自动重启。\n\n" +
		"```sh\ntdl version\necho '<script>example</script>'\n```\n\n" +
		"| 版本 | 状态 |\n| --- | --- |\n| 正式版 | 可用 |\n\n" +
		"- [x] 已完成\n- [ ] 待处理\n\n" +
		"[发布页](https://github.com/snakexgc/tdl/releases)\n\nhttps://github.com/snakexgc/tdl\n\n" +
		"![示例图片](https://example.com/release.png)\n"
	rendered := renderReleaseNotes(source)
	for _, want := range []string{
		"<h1>发布说明</h1>", "<h2>改进</h2>", "<strong>加粗</strong>", "<em>强调</em>", "<del>删除</del>",
		"<code>tdl version</code>", "<ul>", "<ol>", "<li>嵌套说明</li>", "<blockquote>",
		`<pre><code class="language-sh">`, "&lt;script&gt;example&lt;/script&gt;", "<table>", "<th>版本</th>",
		`checked="" disabled="" type="checkbox"`, `disabled="" type="checkbox"`,
		`href="https://github.com/snakexgc/tdl/releases"`, `href="https://github.com/snakexgc/tdl"`,
		`<img src="https://example.com/release.png" alt="示例图片">`,
	} {
		require.Contains(t, rendered, want)
	}
}

func TestReleaseNotesDoNotRenderActiveHTMLOrDangerousLinks(t *testing.T) {
	source := `<script>window.bad = true</script>

<img src=x onerror="window.bad = true">

<iframe srcdoc="<script>alert(1)</script>"></iframe>

<svg onload="alert(1)"></svg>

[script](javascript:alert(1))
[mixed](JaVaScRiPt:alert(1))
[entity](java&#x73;cript:alert(1))
[data](data:text/html;base64,PHNjcmlwdD4=)
[file](file:///C:/Windows/system.ini)
[vb](vbscript:msgbox(1))
![svg](data:image/svg+xml;base64,PHN2Zz4=)

## Heading {#update-notes onclick="alert(1)"}

[quoted](https://example.com "&quot; onmouseover=&quot;alert(1)")
`
	rendered := renderReleaseNotes(source)
	document, err := html.Parse(strings.NewReader(rendered))
	require.NoError(t, err)
	var inspect func(*html.Node)
	inspect = func(node *html.Node) {
		if node.Type == html.ElementNode {
			require.NotContains(t, []string{"script", "iframe", "svg", "style", "form", "object", "embed"}, node.Data)
			for _, attribute := range node.Attr {
				require.False(t, strings.HasPrefix(attribute.Key, "on"), "event attribute in %s", rendered)
				require.NotEqual(t, "id", attribute.Key, "release content must not shadow application element IDs")
				if attribute.Key == "href" || attribute.Key == "src" {
					for _, scheme := range []string{"javascript:", "vbscript:", "data:", "file:"} {
						require.False(t, strings.HasPrefix(strings.ToLower(attribute.Val), scheme), "unsafe URL in %s", rendered)
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			inspect(child)
		}
	}
	inspect(document)
}

func TestReleaseChoicesIncludeRenderedNotesForBothChannels(t *testing.T) {
	for _, preview := range []bool{false, true} {
		release := testRelease("markdown-release", preview, 1)
		release.Body = "## 本次更新\n\n- **修复**下载问题\n"
		choice := releaseChoice(releaseTestInfo(), release)
		require.Equal(t, release.Body, choice.ReleaseNotes)
		require.Contains(t, choice.ReleaseNotesHTML, "<h2>本次更新</h2>")
		require.Contains(t, choice.ReleaseNotesHTML, "<strong>修复</strong>")
	}
	require.Empty(t, renderReleaseNotes(""))
	require.Empty(t, renderReleaseNotes(" \n\t\n"))
}
