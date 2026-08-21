package multiagent

// 通道路由接线：Agent（orchestrator / 子代理）只在配置里引用语义化通道 ID
// （如 strong / mid / cheap），真实模型、密钥、端点与并发额度全部由后台
// ai.channels 配置决定，运行时可改。Agent 未指定通道时跟随所在会话的通道
// （handler 层 configForAIChannel 已把会话通道解析进 appCfg.OpenAI，并把
// 实际通道 ID 写回 appCfg.AI.DefaultChannel）。

import (
	"fmt"
	"strings"

	"cyberstrike-ai/internal/config"
)

// resolveAgentChannel 解析 Agent 显式指定的通道。
// channelID 为空 → ok=false 且无错误，表示"跟随会话通道"；
// 显式指定但 ai.channels 中不存在 → 返回错误（fail fast，便于发现后台配置错误）。
func resolveAgentChannel(appCfg *config.Config, channelID string) (config.OpenAIConfig, string, bool, error) {
	id := strings.TrimSpace(channelID)
	if id == "" {
		return config.OpenAIConfig{}, "", false, nil
	}
	nid := config.NormalizeAIChannelID(id)
	// 运行时别名（failover 后失败通道 → 备用通道）只应用一次，防别名链。
	if appCfg != nil && appCfg.AI.ChannelAliases != nil {
		if target, ok := appCfg.AI.ChannelAliases[nid]; ok && strings.TrimSpace(target) != "" {
			nid = config.NormalizeAIChannelID(target)
		}
	}
	if appCfg != nil && appCfg.AI.Channels != nil {
		if ch, ok := appCfg.AI.Channels[nid]; ok {
			return ch.ToOpenAIConfig(), nid, true, nil
		}
	}
	return config.OpenAIConfig{}, nid, false, fmt.Errorf("AI 通道 %q 未在 ai.channels 中配置（请在后台通道配置中添加）", channelID)
}
