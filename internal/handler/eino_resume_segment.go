package handler

import (
	"context"

	"cyberstrike-ai/internal/agent"
	"cyberstrike-ai/internal/multiagent"
)

// applyEinoTraceResumeSegment 中断并继续：persist last_react_* → loadHistory，可选替换下一段 user 文案。
// persist 使用调用方 ctx 携带的轨迹代次，删除轮次后的过期写回会被静默拦截。
func (h *AgentHandler) applyEinoTraceResumeSegment(
	ctx context.Context,
	conversationID string,
	result *multiagent.RunResult,
	curHistory *[]agent.ChatMessage,
	curFinalMessage *string,
	segmentUserMessage string,
) {
	if shouldPersistEinoAgentTraceAfterRunError(ctx) {
		h.persistEinoAgentTraceForResume(ctx, conversationID, result)
	}
	if hist, err := h.loadHistoryFromAgentTrace(conversationID); err == nil && len(hist) > 0 {
		*curHistory = hist
	}
	if segmentUserMessage != "" {
		*curFinalMessage = segmentUserMessage
	}
}
