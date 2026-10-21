package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/css"
	"github.com/tdewolff/minify/v2/html"
	"github.com/tdewolff/minify/v2/js"
	"github.com/tdewolff/minify/v2/json"
	"github.com/tdewolff/minify/v2/svg"
	"github.com/tdewolff/minify/v2/xml"
)

// 压缩用的 MIME 类型；只有这些类型会被压缩，其余文件逐字节原样拷贝。
const (
	mediaTypeHTML = "text/html"
	mediaTypeCSS  = "text/css"
	mediaTypeJS   = "application/javascript"
	mediaTypeJSON = "application/json"
	mediaTypeSVG  = "image/svg+xml"
	mediaTypeXML  = "text/xml"
)

// newMinifier 构造产物压缩器。
//
// 定位是「去空白与注释」，不是 uglify/terser 那种改名压缩：
// JS 关闭局部变量改名（KeepVarNames），因此内联 <script> 里依赖 eval、函数源码文本或
// 调试符号的代码不会被改坏。CSS 与 JS 里的 `/*! 许可声明 */` 由库默认保留；
// HTML 注释会删除（要在产物里保留版权注释就别依赖 HTML 注释）。
func newMinifier() *minify.M {
	minifier := minify.New()
	minifier.Add("text/html", &html.Minifier{})
	minifier.Add("text/css", &css.Minifier{})
	minifier.Add("application/javascript", &js.Minifier{KeepVarNames: true})
	minifier.Add("application/json", &json.Minifier{})
	minifier.Add("image/svg+xml", &svg.Minifier{})
	minifier.Add("text/xml", &xml.Minifier{})
	return minifier
}

// minifyMediaType 按扩展名给出压缩用的 MIME 类型；不在名单里的返回空字符串（不压缩）。
func minifyMediaType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".html", ".htm":
		return mediaTypeHTML
	case ".css":
		return mediaTypeCSS
	case ".js", ".mjs":
		return mediaTypeJS
	case ".json":
		return mediaTypeJSON
	case ".svg":
		return mediaTypeSVG
	case ".xml":
		return mediaTypeXML
	default:
		return ""
	}
}

// minifyBytes 压缩一段内存内容；mediaType 为空或压缩器为 nil 时原样返回。
func minifyBytes(minifier *minify.M, mediaType string, body []byte) ([]byte, error) {
	if minifier == nil || mediaType == "" {
		return body, nil
	}
	var out bytes.Buffer
	if err := minifier.Minify(mediaType, &out, bytes.NewReader(body)); err != nil {
		return nil, fmt.Errorf("压缩 %s 内容: %w", mediaType, err)
	}
	return out.Bytes(), nil
}
