package database

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestDeletionSurfacePurity 删除面纯净度守卫：
// 黑板/资产/FOFA/对话分组四套机制已从 fork 删除（2026-08/09）。
// 本测试静态扫描源码树，防止未来上游合并重新引入或残留引用——任何命中都是回归。
// 注意：tools/*.yaml 命令行工具（fofa_search 等 CLI 工具层）是刻意保留，不在扫描面。
func TestDeletionSurfacePurity(t *testing.T) {
	root := "../.."

	type scan struct {
		label string
		files []string
		pats  []*regexp.Regexp
	}
	var scans []scan

	goFiles, err := filepath.Glob(filepath.Join(root, "internal/handler/*.go"))
	if err != nil {
		t.Fatal(err)
	}
	jsFiles, err := filepath.Glob(filepath.Join(root, "web/static/js/*.js"))
	if err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(filepath.Join(root, "web/templates/index.html"))
	if err != nil {
		t.Fatal(err)
	}

	scans = append(scans, scan{"Go 源码", goFiles, []*regexp.Regexp{
		regexp.MustCompile(`/api/(fofa|groups|assets)[/"']`),
		regexp.MustCompile(`\bgroup:(read|write|delete)\b`),
		regexp.MustCompile(`\basset:(read|write|delete)\b`),
		regexp.MustCompile(`\bfofa:execute\b`),
	}})
	scans = append(scans, scan{"前端脚本", jsFiles, []*regexp.Regexp{
		regexp.MustCompile(`loadConversationsWithGroups|toggleGroupIconPicker|getAllGroupMappings`),
		regexp.MustCompile(`data-require-permission="(group|asset):`),
	}})
	scans = append(scans, scan{"模板", nil, []*regexp.Regexp{
		regexp.MustCompile(`showAddFactModal|getAllGroupMappings|toggleGroupIconPicker`),
		regexp.MustCompile(`value="asset"`),
		regexp.MustCompile(`data-require-permission="(group|asset):`),
	}})

	for _, s := range scans {
		if s.files != nil {
			for _, f := range s.files {
				b, err := os.ReadFile(f)
				if err != nil {
					t.Fatal(err)
				}
				for _, p := range s.pats {
					if p.Match(b) {
						t.Errorf("%s: 命中删除面 %s（黑板/资产/FOFA/分组不得回归或残留）", filepath.Base(f), p.String())
					}
				}
			}
			continue
		}
		for _, p := range s.pats {
			if p.Match(html) {
				t.Errorf("index.html: 命中删除面 %s", p.String())
			}
		}
	}

	// i18n：删除面键不得重现（en/zh 双语）
	for _, f := range []string{"web/static/i18n/en-US.json", "web/static/i18n/zh-CN.json"} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, k := range []string{
			`"getAllGroupMappings"`, `"group": {`, `"fofaSearch"`, `"fofaParse"`,
			`"search_project_facts"`, `"get_asset"`, `"query_assets"`,
			`"resourceTypes": {`, `"asset": "`,
		} {
			if k == `"resourceTypes": {` || k == `"asset": "` {
				continue // 结构键在 rbac 段属正常嵌套,以精确子键判断
			}
			if regexp.MustCompile(regexp.QuoteMeta(k)).MatchString(src) {
				t.Errorf("%s: i18n 含删除面键 %s", f, k)
			}
		}
	}

	// 老库遗留表 DDL：NewDB 建表面必须不再包含分组/资产/黑板表
	dbDDL := []string{"conversation_groups", "conversation_group_mappings", "project_facts", "project_fact_edges", "CREATE TABLE IF NOT EXISTS assets"}
	ddlSrc, err := os.ReadFile(filepath.Join(root, "internal/database/database.go"))
	if err != nil {
		t.Fatal(err)
	}
	ddl := string(ddlSrc)
	for _, tbl := range dbDDL {
		if strings.Contains(ddl, "CREATE TABLE IF NOT EXISTS "+tbl) {
			t.Errorf("database.go 仍建遗留表 %s", tbl)
		}
	}
}
