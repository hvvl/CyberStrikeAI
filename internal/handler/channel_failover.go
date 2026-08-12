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

// tryChannelFailover 在重试耗尽时尝试切换 fallback 通道。
// 成功时替换 *runCfg 并更新 *curHistory（从代理轨迹），返回 true，调用方应 continue 循环；
// 否则返回 false，调用方走普通失败路径。
func (h *AgentHandler) tryChannelFailover(
	runErr error,
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

	fallback := strings.TrimSpace(cur.OpenAI.FallbackChannel)
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

	// 从已持久化的代理轨迹重建历史，保留切换前的上下文。
	if hist, herr := h.loadHistoryFromAgentTrace(conversationID); herr == nil && len(hist) > 0 {
		if curHistory != nil {
			*curHistory = hist
		}
	}

	fromChannel := cur.AI.DefaultChannel
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

// rebindForChannelFailover 复用 rebindEinoRunningTask 重建 ctx（同 empty_response continue），
// 供流式 handler 在 failover 后续跑。
func (h *AgentHandler) rebindForChannelFailover(baseCtx context.Context, conversationID string, timeoutCancel context.CancelFunc) (context.Context, context.CancelCauseFunc, context.Context, context.CancelFunc) {
	return h.rebindEinoRunningTask(baseCtx, conversationID, timeoutCancel)
}
