package handler

import (
	"context"

	"cyberstrike-ai/internal/database"

	"go.uber.org/zap"
)

// traceRunIDContextKey 轨迹代次 ID 的 ctx key（包内私有）。
// 每次任务运行开始时由 DB 分配（ClaimAgentTraceRun），随 ctx 穿透
// detachedAgentContext（WithoutCancel 保留 values）与「中断并继续」的
// 重建 ctx；任何轨迹写回都带上它做条件更新，删除轮次/新运行换代后即失效。
type traceRunIDContextKey struct{}

// claimAgentTraceRun 为本次运行声明新的轨迹代次并挂到 ctx。
// 声明失败不阻塞运行（老库迁移前无该列）：返回原 ctx，写回退化为无条件写。
func claimAgentTraceRun(ctx context.Context, h *AgentHandler, conversationID string) context.Context {
	if h == nil || h.db == nil || conversationID == "" {
		return ctx
	}
	runID, err := h.db.ClaimAgentTraceRun(conversationID)
	if err != nil {
		if h.logger != nil {
			h.logger.Warn("声明代理轨迹代次失败，本运行轨迹写回退化为无条件写",
				zap.String("conversationId", conversationID), zap.Error(err))
		}
		return ctx
	}
	return context.WithValue(ctx, traceRunIDContextKey{}, runID)
}

// traceRunIDFromContext 提取当前运行的轨迹代次 ID（未声明返回空串）。
func traceRunIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(traceRunIDContextKey{}).(string); ok {
		return v
	}
	return ""
}

// saveAgentTraceForContext 以 ctx 携带的代次条件写回代理轨迹。
// 命中 ErrTraceRunStale 说明会话在本次运行期间被删除轮次或已有新运行接管：
// 该写回必须放弃（上下文已按用户意图重建），只记日志不上抛。
func (h *AgentHandler) saveAgentTraceForContext(ctx context.Context, conversationID string, traceInputJSON, assistantOutput string) error {
	if h == nil || h.db == nil {
		return nil
	}
	err := h.db.SaveAgentTraceForRun(conversationID, traceRunIDFromContext(ctx), traceInputJSON, assistantOutput)
	if err != nil && database.IsTraceRunStale(err) {
		if h.logger != nil {
			h.logger.Info("代理轨迹写回已按代次拦截（会话轮次被删除或已被新运行接管）",
				zap.String("conversationId", conversationID))
		}
		return nil
	}
	return err
}
