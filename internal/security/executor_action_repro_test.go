package security

import (
	"testing"

	"cyberstrike-ai/internal/config"
)

// loadActionTool 加载真实 tools/*.yaml 的单个工具定义。
func loadActionTool(t *testing.T, name string) config.ToolConfig {
	t.Helper()
	tools, err := config.LoadToolsFromDir("../../tools")
	if err != nil {
		t.Fatalf("加载 tools 目录失败: %v", err)
	}
	for _, tc := range tools {
		if tc.Name == name {
			return tc
		}
	}
	t.Fatalf("工具 %s 未找到", name)
	return config.ToolConfig{}
}

// TestActionParamDropped_Reproduction 用真实 YAML + 真实 buildCommandArgs 复现：
// executor 三处硬编码跳过名为 action 的参数（executor.go:391/410/535），
// 导致脚本 sys.argv[1] 收不到 action、二进制工具的子命令丢失。
//
// python3 -c <script> 型工具：期望 argv[1]（即 cmdArgs[2]，紧跟 -c script）是 action。
func TestActionParamDropped_Reproduction(t *testing.T) {
	cases := []struct {
		tool   string
		args   map[string]interface{}
		action string
	}{
		{"search_exploit", map[string]interface{}{
			"action": "view", "query": "45863",
		}, "view"},
		{"interactsh", map[string]interface{}{
			"action": "poll", "server": "oast.fun",
		}, "poll"},
		{"libc-database", map[string]interface{}{
			"action": "find", "symbols": "__libc_start_main", "libc_id": "", "additional_args": "",
		}, "find"},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			toolCfg := loadActionTool(t, tc.tool)
			executor, _ := setupTestExecutor(t)
			cmdArgs := executor.buildCommandArgs(tc.tool, &toolCfg, tc.args)

			if len(cmdArgs) < 3 || cmdArgs[0] != "-c" {
				t.Fatalf("非预期的固定参数结构: %v", cmdArgs[:min(2, len(cmdArgs))])
			}
			t.Logf("tool=%s cmdArgs[2:]=%v", tc.tool, cmdArgs[2:])
			if cmdArgs[2] != tc.action {
				t.Errorf("BUG 复现：脚本 argv[1] 期望 %q，实际 %q（action 被丢弃/错位）",
					tc.action, safeGet(cmdArgs, 2))
			}
		})
	}
}

// TestActionParamDropped_CloudmapperSteghide 二进制命令型工具：
// position 0 的子命令被跳过，命令行首 token 缺失。
// 期望 action 出现在 cmdArgs[0]（所有 flag 之前，作为子命令）。
func TestActionParamDropped_CloudmapperSteghide(t *testing.T) {
	cases := []struct {
		tool   string
		args   map[string]interface{}
		action string
	}{
		{"cloudmapper", map[string]interface{}{
			"action": "find_admins", "account": "prod", "config": "config.json",
		}, "find_admins"},
		{"steghide", map[string]interface{}{
			"action": "info", "cover_file": "/tmp/x.jpg",
		}, "info"},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			toolCfg := loadActionTool(t, tc.tool)
			executor, _ := setupTestExecutor(t)
			cmdArgs := executor.buildCommandArgs(tc.tool, &toolCfg, tc.args)
			t.Logf("tool=%s cmdArgs=%v", tc.tool, cmdArgs)

			if len(cmdArgs) == 0 || cmdArgs[0] != tc.action {
				t.Errorf("BUG 复现：子命令 %q 未出现在 cmdArgs[0]，实际 %q",
					tc.action, safeGet(cmdArgs, 0))
			}
		})
	}
}

func safeGet(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
