package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// assetHashLength 是 asset 输出到 URL 上的内容哈希长度（sha256 前 N 个十六进制字符）。
const assetHashLength = 8

// renderContext 是每次加载（含热重载）生成一次的模板函数上下文。
// 模板函数只依赖它与入参，因此 serve 与 gen 对同一份站点给出同样的输出。
type renderContext struct {
	site      *Site
	mode      Mode
	buildTime time.Time

	mu     sync.Mutex
	assets map[string]string // 资源绝对路径 -> 内容哈希（懒计算并缓存到本次加载）
}

// templateFuncs 返回注入到所有页面与局部模板的内置函数。
//
// 命名约定：产出「不转义安全类型」的函数用大写（HTML、JS），普通工具用小写驼峰，
// 这样模板里一眼能看出哪些是信任边界的出口。
func templateFuncs(ctx *renderContext) template.FuncMap {
	return template.FuncMap{
		// 信任边界出口
		"HTML": func(s string) template.HTML { return template.HTML(s) },
		"JS": func(v any) (template.JS, error) {
			encoded, err := json.Marshal(v)
			if err != nil {
				return "", err
			}
			return template.JS(encoded), nil
		},

		// 资源与缓存失效
		"asset": ctx.assetURL,

		// 数据兜底与显式断言
		"default": defaultValue,
		"fail":    failf,

		// 集合构造（给局部模板传多个参数）
		"dict": makeDict,
		"list": func(items ...any) []any { return items },

		// 文本（一律按字符而非字节处理）
		"upper":      strings.ToUpper,
		"lower":      strings.ToLower,
		"trimPrefix": func(prefix, s string) string { return strings.TrimPrefix(s, prefix) },
		"trimSuffix": func(suffix, s string) string { return strings.TrimSuffix(s, suffix) },
		"replace":    func(old, new, s string) string { return strings.ReplaceAll(s, old, new) },
		"join":       joinValues,
		"truncate":   truncateRunes,
		"charCount":  utf8.RuneCountInString,

		// 模式与时间
		"dev":       func() bool { return ctx.mode == ModeDev },
		"prod":      func() bool { return ctx.mode == ModeProd },
		"buildTime": func() time.Time { return ctx.buildTime },
		"year":      ctx.year,
		"date":      ctx.formatDate,
	}
}

// assetURL 把站点内的静态资源路径转成带内容哈希的 URL：
//
//	{{asset "/static/css/site.css"}} -> /static/css/site.css?v=1a2b3c4d
//
// 文件不存在、落在局部模板目录或忽略项里、或指向页面都会直接报错，
// 不静默产出坏链接。
func (c *renderContext) assetURL(urlPath string) (string, error) {
	if strings.ContainsAny(urlPath, "?#") {
		return "", fmt.Errorf("asset 只接受路径，不要带查询串或锚点：%s", urlPath)
	}
	hash, err := c.assetHash(urlPath)
	if err != nil {
		return "", err
	}
	return "/" + strings.TrimPrefix(filepath.ToSlash(urlPath), "/") + "?v=" + hash, nil
}

// assetHash 计算资源内容哈希（同一次加载内缓存）。
func (c *renderContext) assetHash(urlPath string) (string, error) {
	full, err := c.site.assetFile(filepath.ToSlash(urlPath))
	if err != nil {
		return "", err
	}

	c.mu.Lock()
	cached, ok := c.assets[full]
	c.mu.Unlock()
	if ok {
		return cached, nil
	}

	body, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("读取资源 %s: %w", urlPath, err)
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])[:assetHashLength]

	c.mu.Lock()
	if c.assets == nil {
		c.assets = make(map[string]string, 8)
	}
	c.assets[full] = hash
	c.mu.Unlock()
	return hash, nil
}

// year 返回年份：默认取构建时间，也可以传入一个时间值（管道友好：{{.since | year}}）。
func (c *renderContext) year(values ...any) (int, error) {
	switch len(values) {
	case 0:
		return c.buildTime.Year(), nil
	case 1:
		t, err := parseTimeValue(values[0])
		if err != nil {
			return 0, err
		}
		return t.Year(), nil
	default:
		return 0, errors.New("year 最多接受一个时间参数")
	}
}

// formatDate 按 Go 的时间布局格式化：{{buildTime | date "2006-01-02"}}。
func (c *renderContext) formatDate(layout string, value any) (string, error) {
	t, err := parseTimeValue(value)
	if err != nil {
		return "", err
	}
	return t.Format(layout), nil
}

// acceptedTimeLayouts 是模板时间参数与 --build-time 共同接受的格式，单一真相源。
// time.DateTime 即 "2006-01-02 15:04:05"，time.DateOnly 即 "2006-01-02"。
var acceptedTimeLayouts = []string{time.RFC3339, time.DateTime, time.DateOnly}

// timeLayoutsHint 是给用户看的格式提示，与 acceptedTimeLayouts 一一对应。
const timeLayoutsHint = "RFC3339 / 2006-01-02 15:04:05 / 2006-01-02"

// parseTimeValue 接受 time.Time、Unix 秒（int/int64/float64）与常见时间字符串。
func parseTimeValue(value any) (time.Time, error) {
	switch v := value.(type) {
	case time.Time:
		return v, nil
	case int:
		return time.Unix(int64(v), 0), nil
	case int64:
		return time.Unix(v, 0), nil
	case float64:
		return time.Unix(int64(v), 0), nil
	case string:
		for _, layout := range acceptedTimeLayouts {
			if parsed, err := time.Parse(layout, v); err == nil {
				return parsed, nil
			}
		}
		return time.Time{}, fmt.Errorf("无法解析时间 %q（支持 %s）", v, timeLayoutsHint)
	default:
		return time.Time{}, fmt.Errorf("不支持的时间类型 %T", value)
	}
}

// defaultValue 在 value 为空值时返回 fallback（管道友好：{{.site.title | default "未命名"}}）。
func defaultValue(fallback, value any) any {
	if isEmptyValue(value) {
		return fallback
	}
	return value
}

// isEmptyValue 判断 nil、空串、零值数字、false、空切片/数组/映射与 nil 指针。
func isEmptyValue(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.String, reflect.Slice, reflect.Map, reflect.Array:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Ptr, reflect.Interface:
		return v.IsNil()
	default:
		return false
	}
}

// failf 让渲染直接失败：gen 退出非零、serve 回 500，并把消息带出来。
func failf(format string, args ...any) (string, error) {
	if len(args) == 0 {
		return "", errors.New(format)
	}
	return "", fmt.Errorf(format, args...)
}

// makeDict 用「键 值 键 值」构造 map，便于给局部模板传多个参数：
//
//	{{template "card" dict "title" .title "url" .url}}
func makeDict(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, fmt.Errorf("dict 需要偶数个参数（键值成对），实际收到 %d 个", len(pairs))
	}
	dict := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict 第 %d 个参数应是 string 键，实际是 %T", i+1, pairs[i])
		}
		dict[key] = pairs[i+1]
	}
	return dict, nil
}

// joinValues 用分隔符连接切片，支持 []string 与 []any（元素用 fmt.Sprint 渲染）。
func joinValues(sep string, value any) (string, error) {
	switch items := value.(type) {
	case nil:
		return "", nil
	case []string:
		return strings.Join(items, sep), nil
	case []any:
		parts := make([]string, 0, len(items))
		for _, item := range items {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, sep), nil
	default:
		return "", fmt.Errorf("join 只支持字符串切片或任意值切片，实际是 %T", value)
	}
}

// truncateRunes 按字符截断并补省略号：中文不会被字节截半（内置 slice/len 是字节语义）。
func truncateRunes(n int, s string) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
