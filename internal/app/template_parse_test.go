package app

import (
	"html/template"
	"io"
	"path/filepath"
	"testing"
)

// TestWebTemplatesParseAndExecute 回归测试（2026-08 线上事故）：
// index.html 是 Go html/template（内含合法的 {{.Version}}），任何写进 HTML 的
// 字面 {{...}} 文案（如 CSV 派发弹窗的 {{target}}/{{note}}/{{name}} 占位符示例）
// 都会被模板解析器当成函数调用，导致 gin LoadHTMLGlob 启动即 panic：
//
//	panic: template: index.html:6187: function "target" not defined
//
// 前端文案里的占位符示例必须用 HTML 实体 &#123;&#123;...&#125;&#125; 转义。
// 本测试与 gin 的加载路径一致（ParseGlob 全部模板 + 执行 index.html），
// 任何新增的模板语法错误/未定义函数都会在此失败。
func TestWebTemplatesParseAndExecute(t *testing.T) {
	pattern := filepath.Join("..", "..", "web", "templates", "*")
	tpl, err := template.New("").ParseGlob(pattern)
	if err != nil {
		t.Fatalf("web 模板解析失败（等价于服务启动 panic）: %v", err)
	}
	if tpl.Lookup("index.html") == nil {
		t.Fatal("index.html 模板缺失")
	}
	if err := tpl.ExecuteTemplate(io.Discard, "index.html", map[string]string{"Version": "test"}); err != nil {
		t.Fatalf("index.html 执行失败（未注册函数/字段或模板动作错误）: %v", err)
	}
}
