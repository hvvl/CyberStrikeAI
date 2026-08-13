package multiagent

import (
	"testing"
	"time"

	"cyberstrike-ai/internal/config"
)

func TestTakeRecentFailedChannelEmpty(t *testing.T) {
	if got := takeRecentFailedChannel(); got != "" {
		t.Fatalf("expected empty with no failures, got %q", got)
	}
}

func TestTakeRecentFailedChannelPicksMostRecent(t *testing.T) {
	noteFailedChannel("cheap")
	time.Sleep(10 * time.Millisecond)
	noteFailedChannel("strong")
	if got := takeRecentFailedChannel(); got != "strong" {
		t.Fatalf("expected most recent channel, got %q", got)
	}
	// 空 channelID 不登记
	noteFailedChannel("")
	if got := takeRecentFailedChannel(); got != "strong" {
		t.Fatalf("empty channel should not override, got %q", got)
	}
}

func TestResolveAgentChannelAlias(t *testing.T) {
	appCfg := &config.Config{
		AI: config.AIConfig{
			DefaultChannel: "default",
			Channels: map[string]config.AIChannelConfig{
				"cheap":        {Model: "c1"},
				"cheap-backup": {Model: "c2"},
			},
			ChannelAliases: map[string]string{"cheap": "cheap-backup"},
		},
	}
	oa, resolvedID, ok, err := resolveAgentChannel(appCfg, "cheap")
	if err != nil || !ok {
		t.Fatalf("resolve: err=%v ok=%v", err, ok)
	}
	if resolvedID != "cheap-backup" {
		t.Fatalf("alias not applied, resolved %q", resolvedID)
	}
	if oa.Model != "c2" {
		t.Fatalf("wrong model after alias: %q", oa.Model)
	}
}

func TestResolveAgentChannelAliasOnlyOnce(t *testing.T) {
	appCfg := &config.Config{
		AI: config.AIConfig{
			DefaultChannel: "default",
			Channels: map[string]config.AIChannelConfig{
				"a": {Model: "ma"},
				"b": {Model: "mb"},
			},
			ChannelAliases: map[string]string{"a": "b", "b": "a"},
		},
	}
	// 别名只应用一次：a → b，不会再从 b 跳回 a
	_, resolvedID, _, err := resolveAgentChannel(appCfg, "a")
	if err != nil {
		t.Fatalf("resolve err: %v", err)
	}
	if resolvedID != "b" {
		t.Fatalf("alias chain should stop after one hop, got %q", resolvedID)
	}
}

func TestResolveAgentChannelNoAlias(t *testing.T) {
	appCfg := &config.Config{
		AI: config.AIConfig{
			DefaultChannel: "default",
			Channels:       map[string]config.AIChannelConfig{"cheap": {Model: "c1"}},
		},
	}
	_, resolvedID, _, err := resolveAgentChannel(appCfg, "cheap")
	if err != nil || resolvedID != "cheap" {
		t.Fatalf("no-alias resolve wrong: id=%q err=%v", resolvedID, err)
	}
	// 空 channelID → 跟随会话通道
	_, _, ok, err := resolveAgentChannel(appCfg, "")
	if err != nil || ok {
		t.Fatalf("empty channel should return ok=false, got ok=%v err=%v", ok, err)
	}
}
