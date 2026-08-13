package multiagent

// 通道路由接线：Agent（orchestrator / 子代理）只在配置里引用语义化通道 ID
// （如 strong / mid / cheap），真实模型、密钥、端点与并发额度全部由后台
// ai.channels 配置决定，运行时可改。Agent 未指定通道时跟随所在会话的通道
// （handler 层 configForAIChannel 已把会话通道解析进 appCfg.OpenAI，并把
// 实际通道 ID 写回 appCfg.AI.DefaultChannel）。

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"cyberstrike-ai/internal/config"
	"cyberstrike-ai/internal/openai"
	"cyberstrike-ai/internal/reasoning"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"go.uber.org/zap"
)

// buildLLMHTTPClient 为一个 LLM 通道构建 HTTP 客户端：
// base transport → 通道并发限流器 → claude bridge（若启用）→ summarization 诊断。
// 限流器按通道 ID 全局共享 gate，主代理/子代理/summarization 指向同一通道时
// 并发额度整体生效。
func buildLLMHTTPClient(oa *config.OpenAIConfig, channelID string, logger *zap.Logger) *http.Client {
	base := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   300 * time.Second,
			KeepAlive: 300 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 60 * time.Minute,
	}
	client := &http.Client{
		Timeout: 30 * time.Minute,
		// Retry-After transport 在并发限流器外层：cooldown 等待期间不占用通道并发额度。
		Transport: &retryAfterTransport{channelID: channelID, next: channelLimiterTransport(channelID, oa.MaxConcurrency, base)},
	}
	// 若配置为 Claude provider，注入自动桥接 transport，对 Eino 透明走 Anthropic Messages API
	client = openai.NewEinoHTTPClient(oa, client)
	openai.AttachSummarizationDiagTransport(client, logger)
	return client
}

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

// chatModelConfigFor 依据 OpenAIConfig 构建 Eino ChatModelConfig。
func chatModelConfigFor(oa *config.OpenAIConfig, httpClient *http.Client) *einoopenai.ChatModelConfig {
	mct := oa.MaxCompletionTokensEffective()
	cfg := &einoopenai.ChatModelConfig{
		APIKey:              oa.APIKey,
		BaseURL:             strings.TrimSuffix(oa.BaseURL, "/"),
		Model:               oa.Model,
		HTTPClient:          httpClient,
		MaxCompletionTokens: &mct,
	}
	return cfg
}

// cloneChatModelConfig 浅拷贝 ChatModelConfig（供 plan_execute planner 基于
// 主模型配置派生，避免重复书写字面量）。
func cloneChatModelConfig(src *einoopenai.ChatModelConfig) *einoopenai.ChatModelConfig {
	if src == nil {
		return nil
	}
	tmp := *src
	return &tmp
}

// modelForAgentChannel 按通道 ID 为 Agent 构建独立模型配置。
// channelID 为空 → 返回 (nil, ..., nil)，调用方沿用会话默认配置（baseModelCfg）。
func modelForAgentChannel(appCfg *config.Config, channelID string, reasoningClient *reasoning.ClientIntent, logger *zap.Logger) (*einoopenai.ChatModelConfig, config.OpenAIConfig, string, error) {
	oa, resolvedID, ok, err := resolveAgentChannel(appCfg, channelID)
	if err != nil || !ok {
		return nil, config.OpenAIConfig{}, "", err
	}
	client := buildLLMHTTPClient(&oa, resolvedID, logger)
	cfg := chatModelConfigFor(&oa, client)
	reasoning.ApplyToEinoChatModelConfig(cfg, &oa, reasoningClient)
	if logger != nil {
		logger.Info("agent 使用独立 AI 通道",
			zap.String("channel", resolvedID),
			zap.String("model", oa.Model))
	}
	return cfg, oa, resolvedID, nil
}
