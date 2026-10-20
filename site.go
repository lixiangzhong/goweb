package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	// pageExt 是页面与局部模板唯一的扩展名。
	pageExt = ".html"
	// indexName 是目录默认页的模板名。
	indexName = "index"
	// nodeModulesDir 是扫描时固定跳过的前端依赖目录。
	nodeModulesDir = "node_modules"
	// dataFileName 是模板目录下的数据文件，其顶层 JSON 对象即模板的渲染数据。
	dataFileName = "data.json"
)

// SiteOptions 描述站点目录约定，serve 与 gen 共用。
type SiteOptions struct {
	// Dir 是页面目录，其中递归出现的 *.html 都视为页面。
	Dir string
	// TemplateDir 是局部模板目录（相对 Dir）；其中内容只参与模板解析，不产出页面。
	// 置空表示没有独立的局部模板目录。
	TemplateDir string
	// ExcludeDirs 是不参与扫描的目录（相对 Dir），例如 gen 的产物目录。
	ExcludeDirs []string
}

// Site 是站点的渲染入口，持有页面清单与共享模板命名空间。
// 所有页面与局部模板解析进同一个 html/template 命名空间，因此局部模板的
// {{define "x"}} 能被任意页面 {{template "x" .}} 引用；
// 模板数据来自模板目录下的 data.json，页面与局部模板共用同一份数据。
type Site struct {
	// root 是页面目录的绝对路径。
	root string
	// excludes 是排除目录的绝对路径。
	excludes []string
	// templatePrefix 是归一化后的局部模板目录（相对 root，斜杠分隔）。
	templatePrefix string

	mu    sync.RWMutex
	tmpl  *template.Template
	pages map[string]string // 页面模板名 -> 源文件绝对路径
	data  map[string]any    // 模板数据，来自模板目录下的 data.json
}

// siteSnapshot 是一次加载的完整结果，保证模板、页面清单与数据同时生效。
type siteSnapshot struct {
	tmpl  *template.Template
	pages map[string]string
	data  map[string]any
}

// LoadSite 扫描页面目录并编译全部模板。
func LoadSite(opts SiteOptions) (*Site, error) {
	root, err := filepath.Abs(opts.Dir)
	if err != nil {
		return nil, fmt.Errorf("解析页面目录 %q: %w", opts.Dir, err)
	}
	site := &Site{
		root:           root,
		excludes:       absoluteDirs(root, opts.ExcludeDirs),
		templatePrefix: cleanRelativeDir(opts.TemplateDir),
	}
	if err := site.Reload(); err != nil {
		return nil, err
	}
	return site, nil
}

// Reload 重新扫描并编译模板；失败时保留原有模板集合，因此调用方可以自行决定
// 是致命退出（首次加载）还是继续用上一份可用模板（开发期热重编译）。
func (s *Site) Reload() error {
	snapshot, err := s.compile()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tmpl, s.pages, s.data = snapshot.tmpl, snapshot.pages, snapshot.data
	return nil
}

// Root 返回页面目录的绝对路径。
func (s *Site) Root() string {
	return s.root
}

// Pages 返回排序后的页面模板名，例如 index、about、help/74。
func (s *Site) Pages() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.pages))
	for name := range s.pages {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DataFile 返回数据文件路径；没有配置局部模板目录时返回空字符串。
func (s *Site) DataFile() string {
	if s.templatePrefix == "" {
		return ""
	}
	return filepath.Join(s.root, filepath.FromSlash(s.templatePrefix), dataFileName)
}

// Render 渲染指定模板并返回 HTML 内容，渲染数据为 data.json 的顶层对象。
//
// 数据是普通 map，缺键按 Go 模板原生语义渲染成空字符串（例如页面写错
// {{.site.titl}} 不会报错，只是留白），拼写错误需要自行核对。
func (s *Site) Render(name string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tmpl := s.tmpl.Lookup(name)
	if tmpl == nil {
		return nil, fmt.Errorf("模板 %q 不存在", name)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, s.data); err != nil {
		return nil, fmt.Errorf("渲染 %q: %w", name, err)
	}
	return buf.Bytes(), nil
}

// WithinTemplateDir 判断绝对路径是否落在局部模板目录内。
func (s *Site) WithinTemplateDir(absPath string) bool {
	if s.templatePrefix == "" {
		return false
	}
	rel, err := filepath.Rel(s.root, absPath)
	if err != nil {
		return false
	}
	return s.isTemplateFile(rel)
}

// Ignored 判断路径是否属于扫描与监听都忽略的范围，用于过滤文件系统事件。
func (s *Site) Ignored(filePath string) bool {
	rel, err := filepath.Rel(s.root, filePath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return true
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if strings.HasPrefix(part, ".") || part == nodeModulesDir {
			return true
		}
	}
	return s.isExcluded(filePath)
}

// WatchPaths 返回需要监听的文件系统路径（目录与文件）。
//
// darwin 上 fsnotify 走 kqueue 后端，监听目录不会收到目录内文件内容变更，
// 因此文件与目录都要单独挂监听。
func (s *Site) WatchPaths() ([]string, error) {
	var paths []string
	err := filepath.WalkDir(s.root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath != s.root && s.skipPath(filePath, entry.IsDir()) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		paths = append(paths, filePath)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("收集监听路径: %w", err)
	}
	return paths, nil
}

// compile 构造一次完整快照：模板命名空间、页面清单与 data.json 数据，
// 任一步失败即整体失败。
func (s *Site) compile() (siteSnapshot, error) {
	var snapshot siteSnapshot
	snapshot.tmpl = template.New(s.root)
	snapshot.pages = make(map[string]string)

	err := filepath.WalkDir(s.root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath != s.root && s.skipPath(filePath, entry.IsDir()) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		ext := filepath.Ext(filePath)
		if !strings.EqualFold(ext, pageExt) {
			return nil
		}
		rel, err := filepath.Rel(s.root, filePath)
		if err != nil {
			return fmt.Errorf("计算相对路径 %s: %w", filePath, err)
		}
		name := strings.TrimSuffix(filepath.ToSlash(rel), ext)
		body, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("读取模板 %s: %w", rel, err)
		}
		if _, err := snapshot.tmpl.New(name).Funcs(templateFuncs()).Parse(string(body)); err != nil {
			return fmt.Errorf("解析模板 %s: %w", rel, err)
		}
		if !s.isTemplateFile(rel) {
			snapshot.pages[name] = filePath
		}
		return nil
	})
	if err != nil {
		return snapshot, err
	}

	data, err := s.loadData()
	if err != nil {
		return snapshot, err
	}
	snapshot.data = data
	return snapshot, nil
}

// loadData 读取模板目录下的 data.json 作为模板数据。
// 文件不存在表示没有数据（不报错）；JSON 语法错误或顶层不是对象则报错。
func (s *Site) loadData() (map[string]any, error) {
	dataPath := s.DataFile()
	if dataPath == "" {
		return nil, nil
	}
	body, err := os.ReadFile(dataPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取数据文件 %s: %w", dataPath, err)
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("解析数据文件 %s（顶层必须是 JSON 对象）: %w", dataPath, err)
	}
	return data, nil
}

// skipPath 判断扫描阶段是否跳过某个路径：点号开头的隐藏项、node_modules 与显式排除目录。
func (s *Site) skipPath(filePath string, isDir bool) bool {
	base := filepath.Base(filePath)
	if strings.HasPrefix(base, ".") {
		return true
	}
	if isDir && base == nodeModulesDir {
		return true
	}
	return s.isExcluded(filePath)
}

// isExcluded 判断路径是否落在显式排除目录内。
func (s *Site) isExcluded(filePath string) bool {
	for _, exclude := range s.excludes {
		if filePath == exclude || strings.HasPrefix(filePath, exclude+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// isTemplateFile 判断相对路径是否位于局部模板目录内。
func (s *Site) isTemplateFile(rel string) bool {
	if s.templatePrefix == "" {
		return false
	}
	slashed := filepath.ToSlash(rel)
	return slashed == s.templatePrefix || strings.HasPrefix(slashed, s.templatePrefix+"/")
}

// hasPage 判断模板名是否为页面（局部模板不算页面）。
func (s *Site) hasPage(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.pages[name]
	return ok
}

// RouteKind 是 URL 到页面/文件的映射结果类型。
type RouteKind int

const (
	// RoutePage 表示命中页面模板。
	RoutePage RouteKind = iota
	// RouteFile 表示命中磁盘上的静态文件。
	RouteFile
	// RouteNotFound 表示既没有页面也没有文件。
	RouteNotFound
)

// Route 描述一次 URL 映射的结果。
type Route struct {
	Kind RouteKind
	Name string // RoutePage 时的模板名
	File string // RouteFile 时的磁盘路径
}

// routeCandidate 是 try_files 的一个候选项：磁盘相对路径，以及命中该文件时
// 应渲染的页面模板名（为空表示该文件只能按静态资源直出）。
type routeCandidate struct {
	rel  string
	page string
}

// Resolve 把 URL 路径映射为页面或静态文件，等效于 nginx 的
//
//	try_files $uri $uri.html $uri/index.html =404;
//
// 因此不做任何重定向，URL 原样保留：
//
//	/about.html -> $uri 命中真实文件        -> 渲染页面 about
//	/about      -> $uri.html 命中           -> 渲染页面 about
//	/about/     -> 目录不存在               -> 404
//	/help       -> $uri/index.html 命中     -> 渲染页面 help/index
//	/help/      -> 目录存在，由 index 提供   -> 渲染页面 help/index
//	/static/a.css、/robots.txt -> $uri 命中 -> 静态文件直出
//
// 非规范路径按 nginx（merge_slashes 开 + remove_dot_segments）的方式静默归一化，
// 同样不重定向：//about// 归一化后是 /about/（404），/a/../about 归一化后命中 about。
// 局部模板目录、产物目录与扫描忽略项一律视为不存在，保证与 gen 产物部署后的
// 线上行为一致。
func (s *Site) Resolve(urlPath string) Route {
	for _, candidate := range s.candidates(normalizePath(urlPath)) {
		if route, ok := s.match(candidate); ok {
			return route
		}
	}
	return Route{Kind: RouteNotFound}
}

// candidates 按 try_files 的顺序给出候选文件。
//
// urlPath 以 / 结尾时，$uri 命中的是目录，由托管端的 index 规则提供
// <目录>/index.html，因此只有这一个候选。
func (s *Site) candidates(urlPath string) []routeCandidate {
	trimmed := strings.Trim(urlPath, "/")

	if strings.HasSuffix(urlPath, "/") {
		if trimmed == "" {
			return []routeCandidate{{rel: indexName + pageExt, page: indexName}}
		}
		return []routeCandidate{{
			rel:  path.Join(trimmed, indexName) + pageExt,
			page: path.Join(trimmed, indexName),
		}}
	}

	return []routeCandidate{
		{rel: trimmed, page: pageNameOf(trimmed)},
		{rel: trimmed + pageExt, page: trimmed},
		{rel: path.Join(trimmed, indexName) + pageExt, page: path.Join(trimmed, indexName)},
	}
}

// match 判断候选文件是否可用：命中页面时返回页面路由，否则按静态文件直出。
func (s *Site) match(candidate routeCandidate) (Route, bool) {
	file, ok := s.servableFile(candidate.rel)
	if !ok {
		return Route{}, false
	}
	if candidate.page == "" {
		return Route{Kind: RouteFile, File: file}, true
	}
	if !s.hasPage(candidate.page) {
		// 页面目录内可被提供的 html 都是页面；出现没注册成页面的 html 时按不存在处理，
		// 避免把未渲染的模板源码直接吐给浏览器。
		return Route{}, false
	}
	return Route{Kind: RoutePage, Name: candidate.page}, true
}

// pageNameOf 返回文件相对路径对应的页面模板名；非 html 文件返回空。
func pageNameOf(rel string) string {
	ext := path.Ext(rel)
	if ext == "" || !strings.EqualFold(ext, pageExt) {
		return ""
	}
	return strings.TrimSuffix(rel, ext)
}

// servableFile 把 URL 相对路径解析为可对外提供的常规文件。
// 局部模板目录、产物目录与扫描忽略项（隐藏项、node_modules）一律视为不存在，
// 与 gen 产物部署后的行为保持一致。
func (s *Site) servableFile(rel string) (string, bool) {
	if rel == "" {
		return "", false
	}
	full := filepath.Join(s.root, filepath.FromSlash(rel))
	// 归一化已经消除了 .. 段，这里再做一次边界校验，避免拼接结果逃出页面目录。
	back, err := filepath.Rel(s.root, full)
	if err != nil || back == ".." || strings.HasPrefix(back, ".."+string(filepath.Separator)) {
		return "", false
	}
	if s.Ignored(full) || s.WithinTemplateDir(full) {
		return "", false
	}
	info, err := os.Stat(full)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return full, true
}

// normalizePath 按 nginx 的方式归一化 URL 路径：折叠重复斜杠、解析 . 与 .. 段，
// 并保留尾斜杠（尾斜杠表示目录）。
//
// 与 path.Clean 的区别在于 "/about/." 这类写法：RFC 3986 的 remove_dot_segments
// 会把它视为目录("/about/")，而 path.Clean 会得到 "/about"（nginx 实测为 404）。
func normalizePath(urlPath string) string {
	if !strings.HasPrefix(urlPath, "/") {
		urlPath = "/" + urlPath
	}
	trailing := strings.HasSuffix(urlPath, "/") ||
		strings.HasSuffix(urlPath, "/.") ||
		strings.HasSuffix(urlPath, "/..")

	segments := make([]string, 0, 8)
	for _, segment := range strings.Split(urlPath, "/") {
		switch segment {
		case "", ".":
			continue
		case "..":
			if len(segments) > 0 {
				segments = segments[:len(segments)-1]
			}
		default:
			segments = append(segments, segment)
		}
	}

	normalized := "/" + strings.Join(segments, "/")
	if trailing && normalized != "/" {
		normalized += "/"
	}
	return normalized
}

// absoluteDirs 把相对目录列表解析为相对 root 的绝对路径。
func absoluteDirs(root string, dirs []string) []string {
	absolute := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		trimmed := strings.TrimSpace(dir)
		if trimmed == "" {
			continue
		}
		if !filepath.IsAbs(trimmed) {
			trimmed = filepath.Join(root, trimmed)
		}
		if resolved, err := filepath.Abs(trimmed); err == nil {
			trimmed = resolved
		}
		absolute = append(absolute, filepath.Clean(trimmed))
	}
	return absolute
}

// cleanRelativeDir 归一化相对目录：统一成斜杠分隔、去掉 ./ 与前后的 /。
func cleanRelativeDir(dir string) string {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return ""
	}
	cleaned := filepath.ToSlash(filepath.Clean(trimmed))
	cleaned = strings.TrimPrefix(cleaned, "./")
	cleaned = strings.Trim(cleaned, "/")
	if cleaned == "." {
		return ""
	}
	return cleaned
}
