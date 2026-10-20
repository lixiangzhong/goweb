package main

import (
	"encoding/json"
	"html/template"
)

// templateFuncs 返回注入到所有页面与局部模板的内置函数。
//
// 站点没有数据源，模板里唯一的动态来源就是这两个函数：
// HTML 用于输出含标记的片段，JS 用于在内联脚本里输出数据。
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		// HTML 把字符串原样标记为安全 HTML，不再转义。
		"HTML": func(s string) template.HTML {
			return template.HTML(s)
		},
		// JS 把任意值编码为 JSON 字面量，可直接放进 <script>。
		"JS": func(v any) (template.JS, error) {
			encoded, err := json.Marshal(v)
			if err != nil {
				return "", err
			}
			return template.JS(encoded), nil
		},
	}
}
