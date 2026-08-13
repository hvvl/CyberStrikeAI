package handler

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"cyberstrike-ai/internal/config"
	"cyberstrike-ai/internal/multiagent"
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

func TestAgentRunErrorMsgTagging(t *testing.T) {
	// 重试耗尽错误 → 打 [api_failure:*] 分类标签
	exhausted := fmt.Errorf("exhausted: %w: %w", multiagent.ErrRetryExhausted, errors.New("boom"))
	got := agentRunErrorMsg(exhausted)
	if !strings.HasPrefix(got, apiFailureTagPrefix) {
		t.Fatalf("exhausted error should be tagged, got %q", got)
	}
	if !strings.Contains(got, "执行失败") || !strings.Contains(got, "boom") {
		t.Fatalf("tagged message should keep context, got %q", got)
	}
	// 普通错误 → 不打标签
	plain := agentRunErrorMsg(errors.New("auth failed"))
	if strings.HasPrefix(plain, apiFailureTagPrefix) {
		t.Fatalf("plain error should not be tagged, got %q", plain)
	}
	if !strings.HasPrefix(plain, "执行失败: ") {
		t.Fatalf("plain error format wrong: %q", plain)
	}
	// nil → 不打标签
	if got := agentRunErrorMsg(nil); strings.HasPrefix(got, apiFailureTagPrefix) {
		t.Fatalf("nil error should not be tagged, got %q", got)
	}
	// errors.Is 语义：标签判定依赖 ErrRetryExhausted 的包裹链
	wrapped := fmt.Errorf("outer: %w", exhausted)
	if !errors.Is(wrapped, multiagent.ErrRetryExhausted) {
		t.Fatal("test fixture should preserve ErrRetryExhausted via %w chain")
	}
	if got := agentRunErrorMsg(wrapped); !strings.HasPrefix(got, apiFailureTagPrefix) {
		t.Fatalf("wrapped exhausted error should be tagged, got %q", got)
	}
}
