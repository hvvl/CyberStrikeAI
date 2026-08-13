package handler

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"cyberstrike-ai/internal/agent"
	"cyberstrike-ai/internal/config"
	"cyberstrike-ai/internal/database"
	"cyberstrike-ai/internal/multiagent"

	"go.uber.org/zap"
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

// ---------- 审计 P1-1 / P1-2 回归测试：角色通道 failover 不得拖走会话主通道，
// 且 cheap → 当前会话通道是合法降级 ----------

func newFailoverTestHandler(t *testing.T, cfg *config.Config) *AgentHandler {
	t.Helper()
	db, err := database.NewDB(filepath.Join(t.TempDir(), "failover.db"), zap.NewNop())
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &AgentHandler{config: cfg, logger: zap.NewNop(), db: db}
}

// 场景 2（reviewer）：session=strong, role=cheap, cheap.fallback=cheap-backup
// → failover 成功，主通道仍 strong，只有 cheap 别名到 cheap-backup。
func TestTryChannelFailoverRoleChannelKeepsSessionChannel(t *testing.T) {
	cfg := testCfgWithChannels()
	h := newFailoverTestHandler(t, cfg)
	runCfg, resolved, err := h.configForAIChannel("strong")
	if err != nil || resolved != "strong" {
		t.Fatalf("configForAIChannel: err=%v resolved=%q", err, resolved)
	}
	state := newChannelFailoverState()
	hist := []agent.ChatMessage{}
	ok := h.tryChannelFailover(multiagent.ErrRetryExhausted, "cheap", &runCfg, state, "conv-role-keeps-session", &hist, nil)
	if !ok {
		t.Fatal("role channel failover should succeed")
	}
	if got := runCfg.AI.DefaultChannel; got != "strong" {
		t.Fatalf("session channel must stay strong, got %q", got)
	}
	if got := runCfg.OpenAI.Model; got != "m1" {
		t.Fatalf("session base model must stay m1, got %q", got)
	}
	if got := runCfg.AI.ChannelAliases["cheap"]; got != "cheap-backup" {
		t.Fatalf("cheap alias must point to cheap-backup, got %q", got)
	}
	if !state.switched {
		t.Fatal("state.switched must be true after a successful failover")
	}
}

// 场景 1（reviewer）：session=strong, role=cheap, cheap.fallback=strong
// → 必须允许降级到当前会话通道（此前被 visited 预置的会话通道错误拦截）。
func TestTryChannelFailoverRoleChannelToSessionChannel(t *testing.T) {
	cfg := testCfgWithChannels()
	cfg.AI.Channels["cheap"] = config.AIChannelConfig{Model: "c1", FallbackChannel: "strong"}
	h := newFailoverTestHandler(t, cfg)
	runCfg, _, err := h.configForAIChannel("strong")
	if err != nil {
		t.Fatalf("configForAIChannel: %v", err)
	}
	state := newChannelFailoverState()
	hist := []agent.ChatMessage{}
	ok := h.tryChannelFailover(multiagent.ErrRetryExhausted, "cheap", &runCfg, state, "conv-role-to-session", &hist, nil)
	if !ok {
		t.Fatal("cheap → session channel fallback must be allowed")
	}
	if got := runCfg.AI.DefaultChannel; got != "strong" {
		t.Fatalf("session channel must stay strong, got %q", got)
	}
	if got := runCfg.AI.ChannelAliases["cheap"]; got != "strong" {
		t.Fatalf("cheap alias must point to strong, got %q", got)
	}
}

// 会话通道自身失败 → 整个会话切换（模型/密钥/端点全部换成备用通道）。
func TestTryChannelFailoverSessionChannelSwitchesWholeSession(t *testing.T) {
	cfg := testCfgWithChannels()
	h := newFailoverTestHandler(t, cfg)
	runCfg, _, err := h.configForAIChannel("strong")
	if err != nil {
		t.Fatalf("configForAIChannel: %v", err)
	}
	state := newChannelFailoverState()
	hist := []agent.ChatMessage{}
	ok := h.tryChannelFailover(multiagent.ErrRetryExhausted, "strong", &runCfg, state, "conv-session-switch", &hist, nil)
	if !ok {
		t.Fatal("session channel failover should succeed")
	}
	if got := runCfg.AI.DefaultChannel; got != "strong-backup" {
		t.Fatalf("session must switch to strong-backup, got %q", got)
	}
	if got := runCfg.OpenAI.Model; got != "m2" {
		t.Fatalf("session base model must switch to strong-backup model m2, got %q", got)
	}
	if got := runCfg.AI.ChannelAliases["strong"]; got != "strong-backup" {
		t.Fatalf("session alias must point to strong-backup, got %q", got)
	}
}

// 防环：通道 fallback 指回自己必须被拒绝。
func TestTryChannelFailoverSelfReferencingFallbackRejected(t *testing.T) {
	cfg := testCfgWithChannels()
	cfg.AI.Channels["loop"] = config.AIChannelConfig{Model: "l1", FallbackChannel: "loop"}
	h := newFailoverTestHandler(t, cfg)
	runCfg, _, _ := h.configForAIChannel("strong")
	state := newChannelFailoverState()
	hist := []agent.ChatMessage{}
	if ok := h.tryChannelFailover(multiagent.ErrRetryExhausted, "loop", &runCfg, state, "conv-self-loop", &hist, nil); ok {
		t.Fatal("self-referencing fallback must be rejected")
	}
}

// 角色通道的 fallback 目标不存在 → 拒绝切换，不得静默落到别的通道。
func TestTryChannelFailoverRoleChannelUnknownFallbackRejected(t *testing.T) {
	cfg := testCfgWithChannels()
	cfg.AI.Channels["ghost"] = config.AIChannelConfig{Model: "g1", FallbackChannel: "nowhere"}
	h := newFailoverTestHandler(t, cfg)
	runCfg, _, _ := h.configForAIChannel("strong")
	state := newChannelFailoverState()
	hist := []agent.ChatMessage{}
	if ok := h.tryChannelFailover(multiagent.ErrRetryExhausted, "ghost", &runCfg, state, "conv-ghost-fallback", &hist, nil); ok {
		t.Fatal("unknown fallback target must be rejected")
	}
	if got := runCfg.AI.DefaultChannel; got != "strong" {
		t.Fatalf("session channel must stay strong on rejected failover, got %q", got)
	}
}

// 整次请求最多降级一次：switched 后再次耗尽不再切换。
func TestTryChannelFailoverOnlySwitchesOnce(t *testing.T) {
	cfg := testCfgWithChannels()
	h := newFailoverTestHandler(t, cfg)
	runCfg, _, _ := h.configForAIChannel("strong")
	state := newChannelFailoverState()
	hist := []agent.ChatMessage{}
	if !h.tryChannelFailover(multiagent.ErrRetryExhausted, "cheap", &runCfg, state, "conv-once", &hist, nil) {
		t.Fatal("first failover should succeed")
	}
	if h.tryChannelFailover(multiagent.ErrRetryExhausted, "cheap-backup", &runCfg, state, "conv-once", &hist, nil) {
		t.Fatal("second failover in the same request must be rejected (switched)")
	}
}

// ---------- 审计 P2-5 / P2-6 辅助逻辑 ----------

func TestValidateAIChannelExists(t *testing.T) {
	h := &AgentHandler{config: testCfgWithChannels(), logger: zap.NewNop()}
	if err := h.validateAIChannelExists("cheap"); err != nil {
		t.Fatalf("cheap should exist: %v", err)
	}
	if err := h.validateAIChannelExists(""); err != nil {
		t.Fatalf("empty channel means follow default and must pass: %v", err)
	}
	if err := h.validateAIChannelExists("nope"); err == nil {
		t.Fatal("nonexistent channel must be rejected")
	}
	if err := h.validateAIChannelExists("cheap_backup"); err != nil {
		t.Fatalf("normalized id cheap_backup should resolve to cheap-backup: %v", err)
	}
}

func TestConversationAIChannelPersistRoundtrip(t *testing.T) {
	db, err := database.NewDB(filepath.Join(t.TempDir(), "conv-channel.db"), zap.NewNop())
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	conv, err := db.CreateConversation("channel persist", database.ConversationCreateMeta{})
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if got, err := db.GetConversationAIChannel(conv.ID); err != nil || got != "" {
		t.Fatalf("fresh conversation should have empty channel: %q, %v", got, err)
	}
	if err := db.SetConversationAIChannel(conv.ID, "cheap"); err != nil {
		t.Fatalf("SetConversationAIChannel: %v", err)
	}
	got, err := db.GetConversationAIChannel(conv.ID)
	if err != nil {
		t.Fatalf("GetConversationAIChannel: %v", err)
	}
	if got != "cheap" {
		t.Fatalf("expected cheap, got %q", got)
	}
	if _, err := db.GetConversationAIChannel("no-such-conv"); err == nil {
		t.Fatal("missing conversation must error")
	}
}
