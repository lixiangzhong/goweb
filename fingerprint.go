package main

import (
	"bytes"
	"fmt"
	"path"
	"strings"

	"golang.org/x/net/html"
)

// fingerprintedAttributes 是需要自动补版本号的属性白名单。
// srcset 单独处理（一个属性里有多个 URL），style/内联 CSS 不处理。
var fingerprintedAttributes = map[string]bool{
	"href":     true,
	"src":      true,
	"poster":   true,
	"data-src": true,
}

// fingerprintHTML 给页面输出里的站内静态资源引用补上 ?v=<内容哈希>，
// 这样作者写普通的 /static/css/site.css 也能拿到缓存失效。
//
// 只有真的改动了才会重新渲染整份文档：HTML 解析会规范化格式（补齐 head、
// 统一引号与自闭合、实体解码、不合法嵌套会被修正），没有改动时逐字节原样返回，
// 把「解析器路线」的副作用限制在确实需要改写的页面上。
func (c *renderContext) fingerprintHTML(pageName string, body []byte) ([]byte, error) {
	document, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("解析页面 %s 的 HTML: %w", pageName, err)
	}

	// 页面 URL 与其源文件同目录，因此相对路径按页面所在目录解析。
	pageDir := path.Dir(pageName)
	if !c.rewriteAssets(document, pageDir) {
		return body, nil
	}

	var out bytes.Buffer
	if err := html.Render(&out, document); err != nil {
		return nil, fmt.Errorf("重新渲染页面 %s: %w", pageName, err)
	}
	return out.Bytes(), nil
}

// rewriteAssets 递归改写元素属性，返回是否有改动。
func (c *renderContext) rewriteAssets(node *html.Node, pageDir string) bool {
	changed := false

	if node.Type == html.ElementNode {
		for i := range node.Attr {
			attribute := &node.Attr[i]
			switch key := strings.ToLower(attribute.Key); {
			case key == "srcset":
				if rewritten, ok := c.fingerprintSrcset(pageDir, attribute.Val); ok {
					attribute.Val = rewritten
					changed = true
				}
			case fingerprintedAttributes[key]:
				if rewritten, ok := c.fingerprintURL(pageDir, attribute.Val); ok {
					attribute.Val = rewritten
					changed = true
				}
			}
		}
	}

	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if c.rewriteAssets(child, pageDir) {
			changed = true
		}
	}
	return changed
}

// fingerprintURL 判断 URL 是否指向站点内的静态资源，是则返回带版本号的 URL。
// 不是可发布的静态资源（页面、局部模板、忽略项、不存在的文件）或本来就该原样保留的
// 写法（外链、data:、锚点、已带查询串）都返回 false，由调用方保持原值。
func (c *renderContext) fingerprintURL(pageDir, raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" || strings.HasPrefix(value, "#") {
		return "", false
	}
	// 已带查询串或锚点时交给作者自己控制：追加参数会牵涉 & 的转义形态。
	if strings.ContainsAny(value, "?#") {
		return "", false
	}
	// 外链、协议相对 URL 与 data:/mailto:/tel: 这类带 scheme 的值一律不动。
	if strings.Contains(value, "://") || strings.HasPrefix(value, "//") || hasScheme(value) {
		return "", false
	}

	relative := strings.TrimPrefix(value, "/")
	if !strings.HasPrefix(value, "/") && pageDir != "" && pageDir != "." {
		relative = path.Join(pageDir, relative)
	}

	hash, err := c.assetHash("/" + relative)
	if err != nil {
		return "", false
	}
	return value + "?v=" + hash, true
}

// fingerprintSrcset 处理 srcset 里的多个候选：逗号分隔，每个候选是「URL [描述符]」。
func (c *renderContext) fingerprintSrcset(pageDir, raw string) (string, bool) {
	// data: URL 自身含逗号，拆分不可靠，整条跳过。
	if strings.HasPrefix(strings.TrimSpace(raw), "data:") {
		return "", false
	}

	candidates := strings.Split(raw, ",")
	changed := false
	for i, candidate := range candidates {
		fields := strings.Fields(candidate)
		if len(fields) == 0 {
			continue
		}
		if rewritten, ok := c.fingerprintURL(pageDir, fields[0]); ok {
			fields[0] = rewritten
			candidates[i] = strings.Join(fields, " ")
			changed = true
		}
	}
	if !changed {
		return "", false
	}
	return strings.Join(candidates, ", "), true
}

// hasScheme 识别 data:、mailto:、tel:、javascript: 这类带 scheme 的值。
func hasScheme(value string) bool {
	colon := strings.Index(value, ":")
	if colon <= 0 {
		return false
	}
	slash := strings.Index(value, "/")
	return slash == -1 || colon < slash
}
