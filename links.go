package main

import (
	"bytes"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// linkChecker 校验渲染结果里的站内链接：目标必须要么是页面、要么是会进产物的静态文件。
//
// 同一个坏 URL 常由共享模板带到所有页面，因此按 URL 合并成一条、附上出现过的页面，
// 避免一次校验刷出几十行同样的问题。
type linkChecker struct {
	site *Site
	// problemPages 记录 URL -> 出现过的页面集合。
	problemPages map[string]map[string]bool
	// checked 统计校验过的站内链接数量。
	checked int
}

// newLinkChecker 构造链接校验器。
func newLinkChecker(site *Site) *linkChecker {
	return &linkChecker{site: site, problemPages: make(map[string]map[string]bool)}
}

// checkPage 收集并校验一页渲染结果里的站内链接，签名与 GenOptions.OnPage 一致。
func (c *linkChecker) checkPage(name string, body []byte) error {
	document, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("解析 HTML: %w", err)
	}

	// 页面 URL 与其源文件同目录，因此相对链接按页面所在目录解析。
	pageDir := path.Dir(name)
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			for _, attr := range node.Attr {
				switch key := strings.ToLower(attr.Key); {
				case key == "srcset":
					for candidate := range strings.SplitSeq(attr.Val, ",") {
						if fields := strings.Fields(candidate); len(fields) > 0 {
							c.checkURL(pageDir, name, fields[0])
						}
					}
				case fingerprintedAttributes[key]:
					c.checkURL(pageDir, name, attr.Val)
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	return nil
}

// checkURL 校验单个链接值；不是站内路径的写法（外链、data:、锚点等）直接跳过。
func (c *linkChecker) checkURL(pageDir, pageName, raw string) {
	value := strings.TrimSpace(raw)
	if value == "" || strings.HasPrefix(value, "#") {
		return
	}
	// 外链、协议相对 URL 与 data:/mailto:/tel: 这类带 scheme 的值不校验。
	if strings.Contains(value, "://") || strings.HasPrefix(value, "//") || hasScheme(value) {
		return
	}
	// 查询串与锚点不影响浏览器找文件，去掉后再解析。
	urlPath := value
	if cut := strings.IndexAny(urlPath, "?#"); cut >= 0 {
		urlPath = urlPath[:cut]
	}
	if urlPath == "" {
		return
	}
	relative := strings.TrimPrefix(urlPath, "/")
	if !strings.HasPrefix(value, "/") && pageDir != "" && pageDir != "." {
		relative = path.Join(pageDir, relative)
	}

	c.checked++
	if c.site.urlIsPublished("/" + relative) {
		return
	}
	if c.problemPages[value] == nil {
		c.problemPages[value] = make(map[string]bool)
	}
	c.problemPages[value][pageName] = true
}

// issues 返回排序后的问题清单，每条包含坏 URL 与出现过的页面。
func (c *linkChecker) issues() []string {
	urls := make([]string, 0, len(c.problemPages))
	for url := range c.problemPages {
		urls = append(urls, url)
	}
	sort.Strings(urls)

	issues := make([]string, 0, len(urls))
	for _, url := range urls {
		pages := make([]string, 0, len(c.problemPages[url]))
		for page := range c.problemPages[url] {
			pages = append(pages, page)
		}
		sort.Strings(pages)
		issues = append(issues, fmt.Sprintf("链接在产物里不存在：%s（出现在 %s）",
			url, strings.Join(pages, "、")))
	}
	return issues
}

// urlIsPublished 判断站内 URL 在产物里是否有对应内容。
//
// 页面：命中且未被忽略规则排除即可（URL 由页面名推导，天然一致）。
// 静态文件：除了要进产物，还必须**正好发布在这个 URL 上**——产物路径与 URL 必须相等，
// 否则线上取不到（例如资源在页面根内时，它的产物路径相对页面根，而带页面根目录名的
// URL 就会落空）。这条正是本项目「URL 与产物路径始终一致」的不变式。
//
// 走的是与 serve 完全相同的 try_files 规则（Site.Resolve），因此不会把「本地能开、
// 线上没有」误判为正常。
func (s *Site) urlIsPublished(urlPath string) bool {
	route := s.Resolve(urlPath)
	switch route.Kind {
	case RoutePage:
		// 页面只有 Name 没有 File，按模板名反查源文件才能判断是否被忽略。
		return !s.ignoredForOutput(s.pageFile(route.Name), false)
	case RouteFile:
		if s.ignoredForOutput(route.File, false) {
			return false
		}
		rel, skip, err := assetRelPath(s, route.File)
		if err != nil || skip {
			// 页面根里的 html 不会作为静态文件发布。
			return false
		}
		return filepath.ToSlash(rel) == strings.TrimPrefix(urlPath, "/")
	default:
		return false
	}
}
