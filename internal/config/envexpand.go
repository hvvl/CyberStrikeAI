package config

import (
	"os"
	"strings"
)

// expandEnvVar 展开字符串中的 ${VAR} 和 ${VAR:-default} 环境变量引用。
// 与官方 MCP 配置格式一致（Claude Desktop / Cursor / VS Code 均支持此语法）。
func expandEnvVar(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		// 查找 ${
		idx := strings.Index(s[i:], "${")
		if idx < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+idx])
		i += idx + 2 // skip ${

		// 查找对应的 }
		end := strings.IndexByte(s[i:], '}')
		if end < 0 {
			// 没有 }，原样保留
			b.WriteString("${")
			continue
		}
		expr := s[i : i+end]
		i += end + 1 // skip }

		// 解析 VAR:-default
		varName := expr
		defaultVal := ""
		hasDefault := false
		if colonIdx := strings.Index(expr, ":-"); colonIdx >= 0 {
			varName = expr[:colonIdx]
			defaultVal = expr[colonIdx+2:]
			hasDefault = true
		}

		val := os.Getenv(varName)
		if val == "" && hasDefault {
			val = defaultVal
		}
		b.WriteString(val)
	}
	return b.String()
}

// ExpandConfigEnv 展开 ExternalMCPServerConfig 中所有支持环境变量的字段。
// 展开范围：Command、Args、Env values、URL、Headers values。
// 注意：copy-on-write——Args/Env/Headers 全部重建为新的底层容器后再写，
// 不原地改写元素。调用方可能在存储本配置的同时已有懒加载 SDK 协程并发读
// 同一底层数组（exec.Command 会 slicecopy），原地写会触发数据竞争（-race 实测）。
func ExpandConfigEnv(cfg *ExternalMCPServerConfig) {
	cfg.Command = expandEnvVar(cfg.Command)
	if len(cfg.Args) > 0 {
		args := make([]string, len(cfg.Args))
		for i, arg := range cfg.Args {
			args[i] = expandEnvVar(arg)
		}
		cfg.Args = args
	}
	if len(cfg.Env) > 0 {
		env := make(map[string]string, len(cfg.Env))
		for k, v := range cfg.Env {
			env[k] = expandEnvVar(v)
		}
		cfg.Env = env
	}
	cfg.URL = expandEnvVar(cfg.URL)
	if len(cfg.Headers) > 0 {
		headers := make(map[string]string, len(cfg.Headers))
		for k, v := range cfg.Headers {
			headers[k] = expandEnvVar(v)
		}
		cfg.Headers = headers
	}
}
