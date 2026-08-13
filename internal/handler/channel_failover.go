package handler

// 跨通道 failover：当某 AI 通道的临时错误重试耗尽（ErrRetryExhausted）时，
// 若该通道配置了 fallback_channel，则切换到备用通道继续本轮任务。
//
// 实现复用「分段续跑」模式（与 interrupt_continue / empty_response continue 同款）：
//   - 重试耗尽前 run loop 已把 ModelFacingTrace 持久化（persistEinoAgentTraceForResume）；
//   - failover 时从代理轨迹重建历史（loadHistoryFromAgentTrace），保留已有上下文；
//   - 只替换 runCfg（模型/密钥/端点/并发/重试策略），不重建 600 行的 DA；
//   - visited set 防止 fallback 链成环（A→B→A）。

import (
	"context"
	"errors"
	"strings"

	"cyberstrike-ai/internal/agent"
	"cyberstrike-ai/internal/config"
	"cyberstrike-ai/internal/multiagent"

	"go.uber.org/zap"
)

// channelFailoverState 跟踪一次请求内已尝试过的通道，防止 fallback 链成环，
// 并用 switched 保证整次请求最多跨通道降级一次（与文档承诺一致）。
type channelFailoverState struct {
	visited  map[string]struct{}
	switched bool
}

func newChannelFailoverState(initialChannelID string) *channelFailoverState {
	s := &channelFailoverState{visited: make(map[string]struct{})}
	if id := strings.TrimSpace(initialChannelID); id != "" {
		s.visited[config.NormalizeAIChannelID(id)] = struct{}{}
	}
	return s
}

// resolveChannelFailoverTarget 计算失败通道对应的 fallback 通道 ID（空=不降级）。
// 失败通道非会话通道时优先用该通道自己的 fallback_channel，缺省回退会话通道的 fallback；
// 失败通道为空或等于会话通道时维持原有「会话通道 fallback」语义。
func resolveChannelFailoverTarget(cur *config.Config, failedChannelID string) string {
	if cur == nil {
		return ""
	}
	failedID := config.NormalizeAIChannelID(failedChannelID)
	sessionID := config.NormalizeAIChannelID(cur.AI.DefaultChannel)
	if failedID == "" {
		failedID = sessionID
	}
	fallback := ""
	if failedID != sessionID {
		if cur.AI.Channels != nil {
			if ch, ok := cur.AI.Channels[failedID]; ok {
				fallback = strings.TrimSpace(ch.FallbackChannel)
			}
		}
		if fallback == "" {
			fallback = strings.TrimSpace(cur.OpenAI.FallbackChannel)
		}
	} else {
		fallback = strings.TrimSpace(cur.OpenAI.FallbackChannel)
	}
	return fallback
}

// tryChannelFailover 在重试耗尽时尝试切换 fallback 通道。
// failedChannelID 由 RunDeepAgent 判定（transport 层 429/5xx 响应登记的失败通道）：
// 非空且非会话通道时，优先使用该通道自己的 fallback_channel；
// 为空或等于会话通道时，维持原有「会话通道 fallback」语义。
// 成功时替换 *runCfg 并更新 *curHistory（从代理轨迹），同时把失败通道→备用通道
// 写入 runCfg.AI.ChannelAliases，使 Agent Markdown 中显式写死的 channel 在后续
// 分段续跑（RunDeepAgent 重建 DA 时）解析到备用通道。返回 true，调用方应 continue。
func (h *AgentHandler) tryChannelFailover(
	runErr error,
	failedChannelID string,
	runCfg **config.Config,
	state *channelFailoverState,
	conversationID string,
	curHistory *[]agent.ChatMessage,
	notify func(eventType, message string, data interface{}),
) bool {
	if runErr == nil || !errors.Is(runErr, multiagent.ErrRetryExhausted) {
		return false
	}
	if runCfg == nil || *runCfg == nil {
		return false
	}
	cur := *runCfg

	// 最多跨通道降级一次：已切换过则不再继续降级（防环之外的数量上限）。
	if state != nil && state.switched {
		return false
	}

	// 失败通道判定：优先 transport 登记的失败角色通道，回退会话默认通道。
	failedID := config.NormalizeAIChannelID(failedChannelID)
	sessionID := config.NormalizeAIChannelID(cur.AI.DefaultChannel)
	if failedID == "" {
		failedID = sessionID
	}

	fallback := resolveChannelFailoverTarget(cur, failedChannelID)
	if fallback == "" {
		return false
	}
	fallbackID := config.NormalizeAIChannelID(fallback)
	if state != nil {
		if _, seen := state.visited[fallbackID]; seen {
			if h.logger != nil {
				h.logger.Warn("fallback 通道已尝试过，终止降级链",
					zap.String("conversationId", conversationID),
					zap.String("fallbackChannel", fallbackID))
			}
			return false
		}
		state.visited[fallbackID] = struct{}{}
	}

	newCfg, resolvedID, err := h.configForAIChannel(fallbackID)
	if err != nil {
		if h.logger != nil {
			h.logger.Warn("解析 fallback 通道失败",
				zap.String("conversationId", conversationID),
				zap.String("fallbackChannel", fallbackID),
				zap.Error(err))
		}
		return false
	}

	// 注入运行时通道别名：失败通道 → 备用通道。
	// 会话通道本身已通过 runCfg.OpenAI 替换完成切换；别名主要服务
	// Agent Markdown 中显式绑定失败通道的子代理/主代理。
	if failedID != resolvedID {
		if newCfg.AI.ChannelAliases == nil {
			newCfg.AI.ChannelAliases = make(map[string]string)
		}
		newCfg.AI.ChannelAliases[failedID] = resolvedID
	}
	// 保留既有别名（理论上 switched 限一次，但防御性合并）。
	if len(cur.AI.ChannelAliases) > 0 {
		for k, v := range cur.AI.ChannelAliases {
			if _, exists := newCfg.AI.ChannelAliases[k]; !exists {
				newCfg.AI.ChannelAliases[k] = v
			}
		}
	}

	// 从已持久化的代理轨迹重建历史，保留切换前的上下文。
	if hist, herr := h.loadHistoryFromAgentTrace(conversationID); herr == nil && len(hist) > 0 {
		if curHistory != nil {
			*curHistory = hist
		}
	}

	fromChannel := cur.AI.DefaultChannel
	if failedID != sessionID {
		fromChannel = failedID
	}
	*runCfg = newCfg
	if state != nil {
		state.switched = true
	}
	if notify != nil {
		notify("channel_failover",
			"通道 "+fromChannel+" 重试已用尽，自动切换到备用通道 "+resolvedID+" 继续执行…",
			map[string]interface{}{
				"conversationId": conversationID,
				"fromChannel":    fromChannel,
				"toChannel":      resolvedID,
				"source":         "channel_failover",
			})
	}
	if h.logger != nil {
		h.logger.Warn("临时错误重试耗尽，切换 fallback 通道",
			zap.String("conversationId", conversationID),
			zap.String("fromChannel", fromChannel),
			zap.String("toChannel", resolvedID),
			zap.Error(runErr))
	}
	return true
}

// failedChannelIDFromResult 安全提取 RunResult 中 transport 层判定的失败通道 ID。
func failedChannelIDFromResult(result *multiagent.RunResult) string {
	if result == nil {
		return ""
	}
	return strings.TrimSpace(result.FailedChannelID)
}

// rebindForChannelFailover 复用 rebindEinoRunningTask 重建 ctx（同 empty_response continue），
// 供流式 handler 在 failover 后续跑。
func (h *AgentHandler) rebindForChannelFailover(baseCtx context.Context, conversationID string, timeoutCancel context.CancelFunc) (context.Context, context.CancelCauseFunc, context.Context, context.CancelFunc) {
	return h.rebindEinoRunningTask(baseCtx, conversationID, timeoutCancel)
}
