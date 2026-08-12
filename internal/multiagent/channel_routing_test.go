package multiagent

import (
	"testing"

	"cyberstrike-ai/internal/config"
)

func testChannelConfig() *config.Config {
	cfg := &config.Config{}
	cfg.AI.Channels = map[string]config.AIChannelConfig{
		"strong": {
			Name:           "强模型",
			Model:          "glm-5.2",
			APIKey:       "test-key",
			BaseURL:        "https://example.com/v1",
			MaxConcurrency: 2,
		},
	}
	cfg.OpenAI.Model = "glm-5.2"
	cfg.OpenAI.BaseURL = "https://example.com/v1"
	return cfg
}

// TestResolveAgentChannelEmpty 空通道 → 跟随会话通道（ok=false，无错误）。
func TestResolveAgentChannelEmpty(t *testing.T) {
	cfg := testChannelConfig()
	oa, id, ok, err := resolveAgentChannel(cfg, "")
	if err != nil || ok {
		t.Fatalf("空通道应跟随会话: ok=%v err=%v", ok, err)
	}
	_ = oa
	_ = id
}

// TestResolveAgentChannelMissing 显式指定但未配置 → 报错（fail fast）。
func TestResolveAgentChannelMissing(t *testing.T) {
	cfg := testChannelConfig()
	_, _, _, err := resolveAgentChannel(cfg, "ghost")
	if err == nil {
		t.Fatal("未配置的通道应返回错误")
	}
}

// TestResolveAgentChannelHit 命中通道 → 返回通道配置与归一化 ID。
func TestResolveAgentChannelHit(t *testing.T) {
	cfg := testChannelConfig()
	oa, id, ok, err := resolveAgentChannel(cfg, "STRONG")
	if err != nil || !ok {
		t.Fatalf("已配置通道应命中: err=%v ok=%v", err, ok)
	}
	if id != "strong" {
		t.Fatalf("通道 ID 应归一化为 strong, got %q", id)
	}
	if oa.Model != "glm-5.2" || oa.MaxConcurrency != 2 {
		t.Fatalf("通道配置透传异常: model=%q maxConcurrency=%d", oa.Model, oa.MaxConcurrency)
	}
}

// TestModelForAgentChannelFallback nil cfg 返回（跟随会话），已配置通道返回独立配置。
func TestModelForAgentChannelFallback(t *testing.T) {
	cfg := testChannelConfig()
	mcfg, _, _, err := modelForAgentChannel(cfg, "", nil, nil)
	if err != nil || mcfg != nil {
		t.Fatalf("空通道应返回 nil 配置: err=%v", err)
	}
	mcfg2, oa2, id2, err := modelForAgentChannel(cfg, "strong", nil, nil)
	if err != nil || mcfg2 == nil {
		t.Fatalf("strong 通道应返回独立模型配置: err=%v", err)
	}
	if mcfg2.Model != "glm-5.2" || mcfg2.HTTPClient == nil {
		t.Fatalf("独立模型配置异常: model=%q client=%v", mcfg2.Model, mcfg2.HTTPClient)
	}
	if id2 != "strong" || oa2.MaxConcurrency != 2 {
		t.Fatalf("通道元信息异常: id=%q maxConcurrency=%d", id2, oa2.MaxConcurrency)
	}
}

// TestChannelIDNormalizedInGate 通道 ID 大小写归一化共享 gate（与 limiter 测试互补）。
func TestChannelIDNormalizedInGate(t *testing.T) {
	resetChannelGatesForTest()
	a := channelSemaphoreFor("Cheap", 3)
	b := channelSemaphoreFor("cheap", 3)
	if a != b {
		t.Fatal("同一通道不同大小写应共享 gate")
	}
}
