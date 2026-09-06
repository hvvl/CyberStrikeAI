package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestExampleYamlParses 数据文件完整性回归：config.example.yaml 必须可被
// Load 正常解析。防护 2026-09 P0 部署阻断问题的复发——fc6b2ee 合并在
// strong 通道下引入重复的 reasoning: 键，gopkg.in/yaml.v3 严格模式下
// unmarshal 直接报错，全新部署（run.sh 首启从示例复制 config.yaml）
// 在 config.Load 即崩溃。存量部署因沿用旧 config.yaml 未暴露。
// 该文件是用户可见文档与首启模板，损坏即部署阻断，必须 CI 卡住。
func TestExampleYamlParses(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile))) // internal/config/example_yaml_test.go → repo
	example := filepath.Join(repoRoot, "config.example.yaml")
	if _, err := os.Stat(example); err != nil {
		t.Fatalf("config.example.yaml 不存在: %v", err)
	}
	cfg, err := Load(example)
	if err != nil {
		t.Fatalf("Load(config.example.yaml) 失败（部署阻断级）: %v", err)
	}
	if cfg == nil || cfg.AI.Channels == nil {
		t.Fatal("解析成功但 channels 为空")
	}
	// 附带完整性断言：strong 通道 reasoning 四参数具全（fc6b2ee 曾在此区丢键）
	ch, ok := cfg.AI.Channels["strong"]
	if !ok {
		t.Fatal("strong 通道缺失")
	}
	if ch.Reasoning.Mode == "" || ch.Reasoning.Effort == "" || ch.Reasoning.Profile == "" || ch.Reasoning.AllowClientReasoning == nil {
		t.Fatalf("strong 通道 reasoning 参数不完整: %+v", ch.Reasoning)
	}
}
