package handler

import (
	"testing"

	"cyberstrike-ai/internal/config"
)

func testCfgWithChannels() *config.Config {
	return &config.Config{
		AI: config.AIConfig{
			DefaultChannel: "strong",
			Channels: map[string]config.AIChannelConfig{
				"strong":        {Model: "m1", FallbackChannel: "strong-backup"},
				"strong-backup": {Model: "m2"},
				"cheap":         {Model: "c1", FallbackChannel: "cheap-backup"},
				"cheap-backup":  {Model: "c2"},
				"lonely":        {Model: "l1"}, // 无 fallback
			},
		},
		OpenAI: config.OpenAIConfig{Model: "m1", FallbackChannel: "strong-backup"},
	}
}

func TestResolveChannelFailoverTargetSessionChannel(t *testing.T) {
	cfg := testCfgWithChannels()
	// 失败通道 == 会话通道 → 会话通道 fallback
	if got := resolveChannelFailoverTarget(cfg, "strong"); got != "strong-backup" {
		t.Fatalf("session channel fallback wrong: %q", got)
	}
	// 失败通道为空 → 归一到会话通道
	if got := resolveChannelFailoverTarget(cfg, ""); got != "strong-backup" {
		t.Fatalf("empty failed channel fallback wrong: %q", got)
	}
}

func TestResolveChannelFailoverTargetRoleChannel(t *testing.T) {
	cfg := testCfgWithChannels()
	// 角色通道失败 → 优先该通道自己的 fallback_channel
	if got := resolveChannelFailoverTarget(cfg, "cheap"); got != "cheap-backup" {
		t.Fatalf("role channel fallback wrong: %q", got)
	}
}

func TestResolveChannelFailoverTargetRoleChannelNoFallback(t *testing.T) {
	cfg := testCfgWithChannels()
	// 角色通道未配置 fallback → 回退会话通道 fallback
	if got := resolveChannelFailoverTarget(cfg, "lonely"); got != "strong-backup" {
		t.Fatalf("role channel without fallback wrong: %q", got)
	}
}

func TestResolveChannelFailoverTargetUnknownChannel(t *testing.T) {
	cfg := testCfgWithChannels()
	// 未知通道 → 无通道级 fallback，回退会话通道 fallback
	if got := resolveChannelFailoverTarget(cfg, "nope"); got != "strong-backup" {
		t.Fatalf("unknown channel fallback wrong: %q", got)
	}
}

func TestResolveChannelFailoverTargetNoFallbackAnywhere(t *testing.T) {
	cfg := &config.Config{
		AI: config.AIConfig{
			DefaultChannel: "default",
			Channels:       map[string]config.AIChannelConfig{"default": {Model: "m"}},
		},
		OpenAI: config.OpenAIConfig{Model: "m"}, // 无 FallbackChannel
	}
	if got := resolveChannelFailoverTarget(cfg, "default"); got != "" {
		t.Fatalf("expected no fallback, got %q", got)
	}
}

func TestResolveChannelFailoverTargetNilCfg(t *testing.T) {
	if got := resolveChannelFailoverTarget(nil, "x"); got != "" {
		t.Fatalf("nil cfg should return empty, got %q", got)
	}
}
