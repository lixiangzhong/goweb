package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
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

// Mode 是站点渲染模式，模板里用 dev / prod 判断。
// serve 默认 dev、gen 默认 prod，可用 --mode 显式覆盖。
type Mode string

const (
	// ModeDev 是开发模式：本地预览、可以包含调试标记。
	ModeDev Mode = "dev"
	// ModeProd 是生产模式：产物会发布出去的形态。
	ModeProd Mode = "prod"
)

// SiteOptions 描述站点目录约定，serve 与 gen 共用。
//
// 三个根（页面 / 模板 / 资源）彼此独立：
//   - Dir 是页面根，递归出现的 *.html 都是页面，页面 URL 与产物路径以它为基准；
//   - TemplateDir 是模板根，放局部模板与 data.json，**不受 Dir 影响**；
//   - AssetsRoot 是资源根，其余文件按它映射到产物。
//
// 所有相对路径都以 ProjectRoot 为基准；ProjectRoot 为空时退化为 Dir
// （即旧版单根行为：页面、模板、资源都在同一个目录下）。
type SiteOptions struct {
	// ProjectRoot 是项目根；所有相对路径的解析基准。置空表示用 Dir。
	ProjectRoot string
	// Dir 是页面根（相对 ProjectRoot，也可以是绝对路径）。
	Dir string
	// TemplateDir 是模板根（相对 ProjectRoot，也可以是绝对路径）；
	// 其中内容只参与模板解析、不产出页面，也不可被访问。置空表示没有独立模板根。
	TemplateDir string
	// AssetsRoot 是资源根（相对 ProjectRoot，也可以是绝对路径）；置空表示用 ProjectRoot。
	AssetsRoot string
	// ExcludeDirs 是不参与扫描的目录（相对 ProjectRoot），例如 gen 的产物目录。
	ExcludeDirs []string
	// IgnorePatterns 是额外的忽略模式（与 .gitignore 同语法），叠加在 .gitignore 之上。
	IgnorePatterns []string
	// UseGitignore 表示是否读取 ProjectRoot 下的 .gitignore。
	UseGitignore bool
	// Publishing 表示当前在为产物工作（gen）：忽略规则生效，且引用被忽略的资源会报错。
	// serve 下为 false：被忽略的文件本地依然可见，方便预览草稿。
	Publishing bool
	// Mode 是渲染模式；置空按 ModeDev 处理。
	Mode Mode
	// BuildTime 是模板里的构建时间；为零值时取站点内容里最新的文件修改时间，
	// 保证「同样的输入产出同样的结果」。
	BuildTime time.Time
	// Fingerprint 表示是否给页面里的站内静态资源引用自动补 ?v=<内容哈希>。
	// 库层默认关闭（单元测试要能断言逐字节输出），CLI 默认开启。
	Fingerprint bool
}

// Site 是站点的渲染入口，持有页面清单与共享模板命名空间。
// 所有页面与局部模板解析进同一个 html/template 命名空间，因此局部模板的
// {{define "x"}} 能被任意页面 {{template "x" .}} 引用；
// 模板数据来自模板目录下的 data.json，页面与局部模板共用同一份数据。
type Site struct {
	// root 是项目根的绝对路径（所有相对路径的基准）。
	root string
	// pagesRoot、templateRoot、assetsRoot 是三个根的绝对路径。
	pagesRoot    string
	templateRoot string
	assetsRoot   string
	// realRoots 是三个根解析软链接后的实际路径，用于判断软链接目标是否越出站点。
	// macOS 的临时目录、/var 等本身就是软链接，按字面路径比较容易误判。
	realRoots []string
	// excludes 是排除目录的绝对路径。
	excludes []string
	// mode 是渲染模式（dev / prod）。
	mode Mode
	// buildTimeOverride 非零时作为模板里的构建时间。
	buildTimeOverride time.Time
	// fingerprint 表示是否自动给资源引用补版本号。
	fingerprint bool
	// publishing 表示当前在为产物工作（gen）：忽略规则生效。
	publishing bool
	// ignore 是忽略规则；只在 publishing 为真时生效。
	ignore *ignoreMatcher

	mu        sync.RWMutex
	tmpl      *template.Template
	pages     map[string]string // 页面模板名 -> 源文件绝对路径
	data      map[string]any    // 模板数据，来自模板目录下的 data.json
	buildTime time.Time         // 本次加载使用的构建时间
	// rewrite 是对页面输出做资源版本号改写的函数；未开启时为 nil。
	rewrite func(pageName string, body []byte) ([]byte, error)
}

// siteSnapshot 是一次加载的完整结果，保证模板、页面清单与数据同时生效。
type siteSnapshot struct {
	tmpl      *template.Template
	pages     map[string]string
	data      map[string]any
	buildTime time.Time
	rewrite   func(pageName string, body []byte) ([]byte, error)
}

// LoadSite 扫描页面根并编译全部模板。
func LoadSite(opts SiteOptions) (*Site, error) {
	// 项目根是相对路径的基准；未指定时退化为页面根，保持旧版单根语义。
	projectRootInput := opts.ProjectRoot
	if strings.TrimSpace(projectRootInput) == "" {
		projectRootInput = opts.Dir
	}
	projectRoot, err := filepath.Abs(projectRootInput)
	if err != nil {
		return nil, fmt.Errorf("解析项目根 %q: %w", projectRootInput, err)
	}

	mode := opts.Mode
	if mode == "" {
		mode = ModeDev
	}

	site := &Site{
		root:              projectRoot,
		excludes:          absoluteDirs(projectRoot, opts.ExcludeDirs),
		mode:              mode,
		buildTimeOverride: opts.BuildTime,
		fingerprint:       opts.Fingerprint,
		publishing:        opts.Publishing,
	}
	if site.pagesRoot, err = resolveRoot(projectRoot, opts.Dir, "."); err != nil {
		return nil, fmt.Errorf("解析页面根: %w", err)
	}
	if site.templateRoot, err = resolveRoot(projectRoot, opts.TemplateDir, ""); err != nil {
		return nil, fmt.Errorf("解析模板根: %w", err)
	}
	if site.assetsRoot, err = resolveRoot(projectRoot, opts.AssetsRoot, "."); err != nil {
		return nil, fmt.Errorf("解析资源根: %w", err)
	}
	// 模板根是「只读、不产出、不可访问」的子树；它一旦覆盖页面根或资源根，
	// 页面发现与资源发布会被整体清空（而且产物是空的却照样成功），必须在加载期拦住。
	if err := validateTemplateRoot(site); err != nil {
		return nil, err
	}
	site.realRoots = canonicalRoots(site.pagesRoot, site.templateRoot, site.assetsRoot)
	// 忽略规则只在产出产物时生效（serve 仍可见被忽略的文件）。
	if site.publishing {
		matcher, err := loadIgnoreMatcher(projectRoot, opts.UseGitignore, opts.IgnorePatterns)
		if err != nil {
			return nil, err
		}
		site.ignore = matcher
	}

	if err := site.Reload(); err != nil {
		return nil, err
	}
	return site, nil
}

// resolveRoot 把配置里的根解析成绝对路径：绝对路径原样使用，
// 相对路径相对项目根；值为空时用 fallback（fallback 为空表示没有这个根）。
func resolveRoot(projectRoot, value, fallback string) (string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		raw = fallback
	}
	if raw == "" {
		return "", nil
	}
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(projectRoot, raw)
	}
	return filepath.Abs(raw)
}

// validateTemplateRoot 拒绝「模板根覆盖页面根或资源根」的配置。
//
// 模板根里的内容只参与模板解析：既不产出页面，也不进入产物、不可访问。
// 一旦它是页面根或资源根的祖先（含相等），整棵子树都会被排除，站点会变成
// 「产物为空但 gen/check 都成功」的静默故障，因此在加载期直接报错。
func validateTemplateRoot(site *Site) error {
	if site.templateRoot == "" {
		return nil
	}
	if site.templateRoot == site.pagesRoot || isAncestor(site.templateRoot, site.pagesRoot) {
		return fmt.Errorf("模板根不能是页面根或其祖先（会排除全部页面）：模板根 %s，页面根 %s",
			site.templateRoot, site.pagesRoot)
	}
	if site.templateRoot == site.assetsRoot || isAncestor(site.templateRoot, site.assetsRoot) {
		return fmt.Errorf("模板根不能是资源根或其祖先（会跳过全部资源）：模板根 %s，资源根 %s",
			site.templateRoot, site.assetsRoot)
	}
	return nil
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
	s.tmpl, s.pages, s.data, s.buildTime, s.rewrite =
		snapshot.tmpl, snapshot.pages, snapshot.data, snapshot.buildTime, snapshot.rewrite
	return nil
}

// Root 返回项目根的绝对路径（所有相对路径的基准）。
func (s *Site) Root() string {
	return s.root
}

// PagesRoot 返回页面根的绝对路径。
func (s *Site) PagesRoot() string {
	return s.pagesRoot
}

// TemplateRoot 返回模板根的绝对路径；没有模板根时返回空字符串。
func (s *Site) TemplateRoot() string {
	return s.templateRoot
}

// AssetsRoot 返回资源根的绝对路径。
func (s *Site) AssetsRoot() string {
	return s.assetsRoot
}

// ignoredForOutput 判断绝对路径在产出产物时是否被忽略规则排除。
// serve（非 publishing）下恒为 false：被忽略的文件本地依然可见，方便预览草稿。
func (s *Site) ignoredForOutput(absPath string, isDir bool) bool {
	if !s.publishing || s.ignore == nil {
		return false
	}
	rel, err := filepath.Rel(s.root, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return s.ignore.Match(filepath.ToSlash(rel), isDir)
}

// templateName 给出模板根里某个文件的模板名：
// 落在项目根内时用「相对项目根的路径」，这样默认布局下仍是 templates/header 这种旧写法；
// 模板根在项目根之外时退回「相对模板根的路径」。
func (s *Site) templateName(filePath string) (string, error) {
	ext := filepath.Ext(filePath)

	base := s.root
	rel, err := filepath.Rel(base, filePath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		base = s.templateRoot
		rel, err = filepath.Rel(base, filePath)
		if err != nil {
			return "", fmt.Errorf("计算模板相对路径 %s: %w", filePath, err)
		}
	}
	return strings.TrimSuffix(filepath.ToSlash(rel), ext), nil
}

// pageFile 返回页面模板名对应的源文件路径；不存在时返回空字符串。
func (s *Site) pageFile(name string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pages[name]
}

// Mode 返回渲染模式（dev / prod）。
func (s *Site) Mode() Mode {
	return s.mode
}

// BuildTime 返回本次加载使用的构建时间。
func (s *Site) BuildTime() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.buildTime
}

// assetFile 校验并返回可用于 asset 的静态资源文件。
// 页面（渲染产物与源文件内容不一致，哈希源文件没有意义）、局部模板目录、
// 产物目录与扫描忽略项一律拒绝，避免产出指向不存在资源的链接。
func (s *Site) assetFile(urlPath string) (string, error) {
	rel := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(urlPath)), "/")
	if rel == "" {
		return "", errors.New("asset 需要资源路径，例如 {{asset \"/static/css/site.css\"}}")
	}
	if decoded, err := url.PathUnescape(rel); err == nil {
		rel = decoded
	}
	full, ok := s.resolveURL(rel)
	if !ok {
		// 文件存在但不发布在这个 URL 上时，给出产物路径提示，避免作者按源文件位置写链接。
		if source, found := s.findSourceFile(rel); found {
			if artifact, skip, artifactErr := s.artifactRel(source); artifactErr == nil && !skip {
				return "", fmt.Errorf("资源 %s 的产物路径是 /%s，请按产物路径引用",
					urlPath, filepath.ToSlash(artifact))
			}
		}
		return "", fmt.Errorf("asset 只接受会进入产物的静态资源：%s", urlPath)
	}
	ext := filepath.Ext(rel)
	// 页面根里的 html 是渲染产物，不参与内容哈希。这里按「路径是否落在页面根内」判断，
	// 而不是拿 URL 去比对页面名：页面根不是项目根时（例如 dir: pages），URL 里会多出
	// 页面根那一层（/pages/x.html），按名字比对会漏判，于是给一个根本不会发布的文件
	// 加上 ?v=<哈希>，把坏链接伪装成正常资源链接。
	if strings.EqualFold(ext, pageExt) && withinDir(s.pagesRoot, full) {
		return "", fmt.Errorf("asset 不接受页面（页面是渲染产物，请直接写路径）：%s", urlPath)
	}
	if s.ignoredForOutput(full, false) {
		return "", fmt.Errorf("资源被忽略规则排除，产物里不会有它：%s", urlPath)
	}
	info, err := os.Stat(full)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("asset 找不到静态资源：%s", urlPath)
	}
	return full, nil
}

// hasSymlinkDir 判断 filePath 相对 root 的路径里是否有中间目录是符号链接。
func hasSymlinkDir(root, filePath string) bool {
	rel, err := filepath.Rel(root, filePath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	dir := root
	for _, part := range parts[:len(parts)-1] {
		dir = filepath.Join(dir, part)
		info, err := os.Lstat(dir)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

// findSourceFile 在页面根/资源根里查找 URL 相对路径对应的磁盘文件（页面根优先）。
//
// 模板根、排除目录、隐藏项与 node_modules 视为不存在；软链接指向三个根之外时同样
// 视为不存在，避免 serve 与 gen 通过软链接把站点根之外的文件暴露出去。
func (s *Site) findSourceFile(rel string) (string, bool) {
	if rel == "" {
		return "", false
	}
	for _, root := range []string{s.pagesRoot, s.assetsRoot} {
		if root == "" {
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(rel))
		if back, err := filepath.Rel(root, full); err != nil ||
			back == ".." || strings.HasPrefix(back, ".."+string(filepath.Separator)) {
			continue
		}
		if s.ignoredPath(full) || s.withinTemplateRoot(full) {
			continue
		}
		info, err := os.Stat(full)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		// gen 遍历不跟随目录软链接，serve 若跟随就会造出「本地能开、产物里没有」的 URL。
		if hasSymlinkDir(root, full) {
			continue
		}
		resolved, err := filepath.EvalSymlinks(full)
		if err != nil || !s.withinAnyRootResolved(resolved) {
			continue
		}
		return full, true
	}
	return "", false
}

// resolveURL 把 URL 相对路径解析成「部署后仍然存在」的磁盘文件。
//
// 不变式：URL 必须与文件的产物路径相等。文件虽然存在于某个根里，但按产物映射会落到
// 另一个 URL 时，它在该 URL 上就应当是 404（例如页面根外的资源按资源根映射）。
// serve、自动指纹与链接校验共用这条规则，保证「本地能打开 == 线上存在」。
func (s *Site) resolveURL(rel string) (string, bool) {
	full, ok := s.findSourceFile(rel)
	if !ok {
		return "", false
	}
	artifact, skip, err := s.artifactRel(full)
	if err != nil || skip {
		return "", false
	}
	if filepath.ToSlash(artifact) != rel {
		return "", false
	}
	return full, true
}

// artifactRel 给出文件在产物里的相对路径：页面根内的文件按页面根映射
// （保证与页面 URL 一致），其余按资源根映射；页面根里的 html 由模板渲染产出，skip 为真。
func (s *Site) artifactRel(filePath string) (rel string, skip bool, err error) {
	rel, err = filepath.Rel(s.assetsRoot, filePath)
	if err != nil {
		return "", false, fmt.Errorf("计算相对路径 %s: %w", filePath, err)
	}
	if !withinDir(s.pagesRoot, filePath) {
		return rel, false, nil
	}
	if strings.EqualFold(filepath.Ext(filePath), pageExt) {
		return "", true, nil
	}
	if pageRel, pageErr := filepath.Rel(s.pagesRoot, filePath); pageErr == nil {
		rel = pageRel
	}
	return rel, false, nil
}

// sourceRoots 返回需要遍历发布的根（页面根 ∪ 资源根），并去掉被另一个根包含的根，
// 避免默认布局（两个根都是项目根）下整棵树走两遍。
func (s *Site) sourceRoots() []string {
	var roots []string
	for _, root := range []string{s.pagesRoot, s.assetsRoot} {
		if root == "" {
			continue
		}
		covered := false
		for _, kept := range roots {
			if withinDir(kept, root) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		kept := make([]string, 0, len(roots))
		for _, old := range roots {
			if !withinDir(root, old) {
				kept = append(kept, old)
			}
		}
		roots = append(kept, root)
	}
	return roots
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

// DataFile 返回数据文件路径（模板根下的 data.json）；没有模板根时返回空字符串。
func (s *Site) DataFile() string {
	if s.templateRoot == "" {
		return ""
	}
	return filepath.Join(s.templateRoot, dataFileName)
}

// Render 渲染指定模板并返回 HTML 内容，渲染数据为 data.json 的顶层对象。
//
// 数据是普通 map，缺键按 Go 模板原生语义渲染成空字符串（例如页面写错
// {{.site.titl}} 不会报错，只是留白），拼写错误需要自行核对。
func (s *Site) Render(name string) ([]byte, error) {
	// 只在取快照时持锁：模板执行（可能读大量文件）不阻塞热重载替换快照。
	s.mu.RLock()
	tmpl := s.tmpl.Lookup(name)
	data := s.data
	rewrite := s.rewrite
	s.mu.RUnlock()
	if tmpl == nil {
		return nil, fmt.Errorf("模板 %q 不存在", name)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("渲染 %q: %w", name, err)
	}
	// 开启后给站内静态资源引用补 ?v=<内容哈希>；serve 与 gen 共用这条路径，
	// 保证本地看到的 URL 与产物一致。
	if rewrite != nil {
		body, err := rewrite(name, buf.Bytes())
		if err != nil {
			return nil, err
		}
		return body, nil
	}
	return buf.Bytes(), nil
}

// withinTemplateRoot 判断绝对路径是否落在模板根内（模板不产出页面、也不可访问）。
func (s *Site) withinTemplateRoot(absPath string) bool {
	return withinDir(s.templateRoot, absPath)
}

// withinDir 判断 absPath 是否位于 dir 内（dir 为空表示没有这个根）。
func withinDir(dir, absPath string) bool {
	if dir == "" {
		return false
	}
	if absPath == dir {
		return true
	}
	return strings.HasPrefix(absPath, dir+string(filepath.Separator))
}

// ignoredName 判断单个路径组件是否属于「永远不参与扫描、也不对外提供」的名字：
// 点号开头的隐藏项（.git、.DS_Store 等）与 node_modules。
//
// 这里刻意不区分文件与目录：名为 node_modules 的普通文件同样忽略。serve 与 gen 必须
// 对同一个路径给出同一答案，否则会出现「gen 把它拷进产物、serve 却拒绝提供」的分叉。
//
// "." 与 ".." 是相对路径里的路径记号，不是隐藏文件名：三个根允许位于项目根之外，
// 此时相对项目根的路径必然带 ".." 组件，不能被误判成隐藏项而整站忽略。
func ignoredName(name string) bool {
	if name == "." || name == ".." {
		return false
	}
	return strings.HasPrefix(name, ".") || name == nodeModulesDir
}

// ignoredPath 判断绝对路径是否属于扫描与监听都忽略的范围：任一路径组件命中 ignoredName，
// 或落在显式排除目录（产物目录等）内。用于过滤文件系统事件与判断资源能否对外提供。
func (s *Site) ignoredPath(filePath string) bool {
	if !s.withinAnyRoot(filePath) {
		return true
	}
	rel, err := filepath.Rel(s.root, filePath)
	if err != nil {
		return true
	}
	for part := range strings.SplitSeq(filepath.ToSlash(rel), "/") {
		if ignoredName(part) {
			return true
		}
	}
	return s.isExcluded(filePath)
}

// withinAnyRoot 判断路径是否落在三个根之内（按字面路径比较）。
func (s *Site) withinAnyRoot(filePath string) bool {
	for _, root := range []string{s.pagesRoot, s.templateRoot, s.assetsRoot} {
		if withinDir(root, filePath) {
			return true
		}
	}
	return false
}

// withinAnyRootResolved 判断「解析软链接后的真实路径」是否落在三个根的真实路径之内。
// 用于校验软链接目标：站点根自身可能位于软链接下（macOS 的 /var、临时目录），
// 按字面路径比较会把合法目标误判成越界。
func (s *Site) withinAnyRootResolved(realPath string) bool {
	for _, root := range s.realRoots {
		if withinDir(root, realPath) {
			return true
		}
	}
	return false
}

// canonicalRoots 返回去空、去重后的真实根路径。
func canonicalRoots(roots ...string) []string {
	seen := make(map[string]bool, len(roots))
	canonical := make([]string, 0, len(roots))
	for _, root := range roots {
		if root == "" {
			continue
		}
		if real, err := filepath.EvalSymlinks(root); err == nil {
			root = real
		}
		root = filepath.Clean(root)
		if seen[root] {
			continue
		}
		seen[root] = true
		canonical = append(canonical, root)
	}
	return canonical
}

// allRoots 返回站点内容涉及的全部根：页面根、资源根，以及不在前两者之内的模板根。
// 用来遍历「站点内容」时避免同一棵子树被重复走（默认布局下页面根与资源根都是项目根）。
func (s *Site) allRoots() []string {
	roots := s.sourceRoots()
	if s.templateRoot != "" {
		covered := false
		for _, root := range roots {
			if withinDir(root, s.templateRoot) {
				covered = true
				break
			}
		}
		if !covered {
			roots = append(roots, s.templateRoot)
		}
	}
	return roots
}

// walkRoot 以统一的扫描规则遍历 root：跳过隐藏项、node_modules 与排除目录（整棵子树）。
// 遍历错误（含根不存在）交给调用方处理；只想尽力而为请用 walkRootLenient。
func (s *Site) walkRoot(root string, fn func(filePath string, entry fs.DirEntry) error) error {
	return s.walkRootMode(root, false, fn)
}

// walkRootLenient 与 walkRoot 相同，但忽略遍历错误：监听集合、构建时间这类场景
// 失败也不该拖垮启动或渲染。
func (s *Site) walkRootLenient(root string, fn func(filePath string, entry fs.DirEntry) error) error {
	return s.walkRootMode(root, true, fn)
}

func (s *Site) walkRootMode(root string, lenient bool, fn func(filePath string, entry fs.DirEntry) error) error {
	if root == "" {
		return nil
	}
	return filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if lenient {
				return nil //nolint:nilerr // 根不存在或权限不足都属于尽力而为的场景
			}
			return walkErr
		}
		if filePath == root {
			return nil
		}
		if s.skipInWalk(filePath) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		return fn(filePath, entry)
	})
}

// WatchPaths 返回需要监听的文件系统路径（目录与文件）。
//
// darwin 上 fsnotify 走 kqueue 后端，监听目录不会收到目录内文件内容变更，
// 因此文件与目录都要单独挂监听。
func (s *Site) WatchPaths() ([]string, error) {
	seen := make(map[string]bool)
	var paths []string

	for _, root := range s.allRoots() {
		if info, statErr := os.Stat(root); statErr == nil && info.IsDir() && !seen[root] {
			seen[root] = true
			paths = append(paths, root)
		}
		err := s.walkRootLenient(root, func(filePath string, _ fs.DirEntry) error {
			if !seen[filePath] {
				seen[filePath] = true
				paths = append(paths, filePath)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("收集监听路径: %w", err)
		}
	}
	return paths, nil
}

// compile 构造一次完整快照：模板命名空间、页面清单、data.json 数据与构建时间，
// 任一步失败即整体失败。
//
// 页面来自页面根；局部模板来自模板根（两处独立解析，模板文件不会成为页面）。
// 模板名沿用「相对项目根的路径去扩展名」，页面名是「相对页面根的路径去扩展名」，
// 因此默认布局下模板名仍是 templates/header 这类旧写法。
func (s *Site) compile() (siteSnapshot, error) {
	snapshot := siteSnapshot{
		tmpl:  template.New(s.pagesRoot),
		pages: make(map[string]string, 16),
	}

	// 模板函数绑定在本次加载的上下文上：模式、构建时间与资源哈希都随快照一起生效。
	ctx := &renderContext{site: s, mode: s.mode}
	funcs := templateFuncs(ctx)

	pageFiles, err := s.pageFiles()
	if err != nil {
		return snapshot, err
	}
	for _, filePath := range pageFiles {
		if err := s.parsePage(&snapshot, funcs, filePath); err != nil {
			return snapshot, err
		}
	}

	partialFiles, err := s.partialFiles()
	if err != nil {
		return snapshot, err
	}
	for _, filePath := range partialFiles {
		if err := s.parsePartial(&snapshot, funcs, filePath); err != nil {
			return snapshot, err
		}
	}

	data, err := s.loadData()
	if err != nil {
		return snapshot, err
	}
	snapshot.data = data
	// 构建时间要同时写进快照与渲染上下文：快照供 Site.BuildTime() 查询，
	// 上下文供模板里的 buildTime/year/date 使用（少了它模板会拿到零值时间）。
	snapshot.buildTime = s.resolveBuildTime()
	ctx.buildTime = snapshot.buildTime
	if s.fingerprint {
		snapshot.rewrite = ctx.fingerprintHTML
	}
	return snapshot, nil
}

// pageFiles 返回页面根里的全部页面文件；模板根落在页面根内时，其子树交给 partialFiles。
func (s *Site) pageFiles() ([]string, error) {
	separateTemplateRoot := s.hasSeparateTemplateRoot()
	var files []string

	err := s.walkRoot(s.pagesRoot, func(filePath string, entry fs.DirEntry) error {
		if separateTemplateRoot && s.withinTemplateRoot(filePath) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.IsDir() && isPageFile(filePath) {
			files = append(files, filePath)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// partialFiles 返回模板根里的全部局部模板；没有独立模板根（或它不存在）时返回空。
func (s *Site) partialFiles() ([]string, error) {
	if !s.hasSeparateTemplateRoot() {
		return nil, nil
	}

	var files []string
	err := s.walkRoot(s.templateRoot, func(filePath string, entry fs.DirEntry) error {
		if !entry.IsDir() && isPageFile(filePath) {
			files = append(files, filePath)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// hasSeparateTemplateRoot 判断是否存在「独立于页面根且真实存在」的模板根。
// 模板根与页面根重合（或未配置）时，页面根里的 html 全是页面；模板根不存在则说明
// 站点没有局部模板——两种情况都不需要单独扫描模板根。
func (s *Site) hasSeparateTemplateRoot() bool {
	if s.templateRoot == "" || s.templateRoot == s.pagesRoot {
		return false
	}
	_, err := os.Stat(s.templateRoot)
	return err == nil
}

// isPageFile 判断文件是否为页面/局部模板（*.html，扩展名大小写不敏感）。
func isPageFile(filePath string) bool {
	return strings.EqualFold(filepath.Ext(filePath), pageExt)
}

// parsePage 读取并解析一个页面模板，并登记进页面清单（页面名相对页面根）。
func (s *Site) parsePage(snapshot *siteSnapshot, funcs template.FuncMap, filePath string) error {
	rel, err := filepath.Rel(s.pagesRoot, filePath)
	if err != nil {
		return fmt.Errorf("计算相对路径 %s: %w", filePath, err)
	}
	name := strings.TrimSuffix(filepath.ToSlash(rel), filepath.Ext(filePath))
	if err := parseTemplate(snapshot, funcs, name, filePath); err != nil {
		return err
	}
	snapshot.pages[name] = filePath
	return nil
}

// parsePartial 读取并解析一个局部模板；局部模板不登记为页面。
func (s *Site) parsePartial(snapshot *siteSnapshot, funcs template.FuncMap, filePath string) error {
	name, err := s.templateName(filePath)
	if err != nil {
		return err
	}
	return parseTemplate(snapshot, funcs, name, filePath)
}

// parseTemplate 读取文件并解析进共享的模板命名空间。
func parseTemplate(snapshot *siteSnapshot, funcs template.FuncMap, name, filePath string) error {
	body, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("读取模板 %s: %w", filePath, err)
	}
	if _, err := snapshot.tmpl.New(name).Funcs(funcs).Parse(string(body)); err != nil {
		return fmt.Errorf("解析模板 %s: %w", filePath, err)
	}
	return nil
}

// resolveBuildTime 给出本次快照使用的构建时间：显式覆盖优先，否则取站点内容里最新的
// 文件修改时间（数据文件也算在内），保证「同样的输入产出同样的结果」。
func (s *Site) resolveBuildTime() time.Time {
	if !s.buildTimeOverride.IsZero() {
		return s.buildTimeOverride
	}

	newest := s.newestModTime()
	// 数据文件的修改时间也要算进去，否则只改 data.json 时构建时间不会变。
	if dataPath := s.DataFile(); dataPath != "" {
		if info, err := os.Stat(dataPath); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	if newest.IsZero() {
		// 空站点没有可推导的时间，退回到当前时间（这种站点本来也没有内容可复现）。
		newest = time.Now()
	}
	return newest.Truncate(time.Second)
}

// newestModTime 返回站点内容（忽略项之外）最新的文件修改时间。
func (s *Site) newestModTime() time.Time {
	var newest time.Time
	for _, root := range s.allRoots() {
		_ = s.walkRootLenient(root, func(_ string, entry fs.DirEntry) error {
			if entry.IsDir() {
				return nil
			}
			if info, err := entry.Info(); err == nil && info.ModTime().After(newest) {
				newest = info.ModTime()
			}
			return nil
		})
	}
	return newest
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

// skipInWalk 判断 walk 阶段是否跳过某个路径。
// 只按 basename 判断：父目录一旦跳过就不会再往下走，因此不必逐组件检查；
// 逐组件版本是 ignoredPath，用于扫描之外的单点判断。
func (s *Site) skipInWalk(filePath string) bool {
	if ignoredName(filepath.Base(filePath)) {
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
	if candidate.page != "" && s.hasPage(candidate.page) {
		return Route{Kind: RoutePage, Name: candidate.page}, true
	}
	if candidate.page != "" && withinDir(s.pagesRoot, file) {
		// 页面根里的 html 都是页面；没有被登记成页面的（例如被忽略的模板源码）
		// 按不存在处理，避免把未渲染的模板直接吐给浏览器。
		return Route{}, false
	}
	// 页面根之外的 html（例如资源根里的 .html 文件）会原样进产物，
	// 与 nginx 一致按静态文件直出。
	return Route{Kind: RouteFile, File: file}, true
}

// pageNameOf 返回文件相对路径对应的页面模板名；非 html 文件返回空。
func pageNameOf(rel string) string {
	ext := path.Ext(rel)
	if ext == "" || !strings.EqualFold(ext, pageExt) {
		return ""
	}
	return strings.TrimSuffix(rel, ext)
}

// servableFile 把 URL 相对路径解析为可对外提供的常规文件：
// 先查页面根（页面根空间优先），再查资源根；模板根、产物目录与扫描忽略项
// （隐藏项、node_modules）一律视为不存在。
func (s *Site) servableFile(rel string) (string, bool) {
	if rel == "" {
		return "", false
	}
	full, ok := s.findSourceFile(rel)
	if !ok {
		return "", false
	}
	artifact, skip, err := s.artifactRel(full)
	if err != nil {
		return "", false
	}
	if skip {
		// 页面根里的 html 是页面源文件：它不会被逐字节发布，但会渲染成一个同路径的
		// 产物页面（about.html -> about 页 -> 产物 about.html）。这里只确认「确实有页面」，
		// URL 最终是否以页面身份出货由 match() 决定。
		return full, s.hasPage(pageNameOf(rel))
	}
	// 不变式：URL 必须与文件在产物里的路径相等。文件虽然存在于某个根里，
	// 但按产物映射会落到另一个 URL 时，它在这个 URL 上应当是 404
	// （例如页面根外的 html 按资源根映射；多写一层页面根路径的 URL 也是 404）。
	if filepath.ToSlash(artifact) != rel {
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

	var segments []string
	for segment := range strings.SplitSeq(urlPath, "/") {
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
