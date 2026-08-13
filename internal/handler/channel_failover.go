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
	"fmt"
	"strings"

	"cyberstrike-ai/internal/agent"
	"cyberstrike-ai/internal/config"
	"cyberstrike-ai/internal/multiagent"

	"go.uber.org/zap"
)

// channelFailoverState 跟踪一次请求内已尝试过的通道，防止 fallback 链成环，
// 并用 switched 保证整次请求最多跨通道降级一次（与文档承诺一致）。
// 注意：不再在构造时预置会话通道——会话通道本身是合法的降级目标
// （角色通道 cheap.fallback=strong 应允许降级到当前会话通道），
// 防环只针对「失败通道自身的 fallback 指回自己」这一种情形。
type channelFailoverState struct {
	visited  map[string]struct{}
	switched bool
}

func newChannelFailoverState() *channelFailoverState {
	return &channelFailoverState{visited: make(map[string]struct{})}
}

// apiFailureTagPrefix 失败分类标签：LLM 临时错误重试耗尽时在持久化消息前打前缀，
// 使「API 调用失败会话」列表能按类别过滤（审计 M3），避免把非重试型错误
// （鉴权失败、程序缺陷、超时等）误当成可续跑的 API 故障。
const apiFailureTagPrefix = "[api_failure:llm_transient_retry_exhausted]"

// agentRunErrorMsg 构造执行失败消息；重试耗尽类错误打分类标签。
func agentRunErrorMsg(runErr error) string {
	msg := ""
	if runErr != nil {
		msg = runErr.Error()
	}
	if runErr != nil && errors.Is(runErr, multiagent.ErrRetryExhausted) {
		return apiFailureTagPrefix + " 执行失败: " + msg
	}
	return "执行失败: " + msg
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
// failedChannelID 由 RunDeepAgent 判定（transport 层 429/5xx/网络错误登记的失败通道）：
// 非空且非会话通道时，优先使用该通道自己的 fallback_channel；
// 为空或等于会话通道时，维持原有「会话通道 fallback」语义。
// 两种情况的切换范围不同：
//   - 角色通道失败：只写 失败通道→备用通道 别名（runCfg.AI.ChannelAliases），
//     会话主通道（OpenAI/DefaultChannel）保持不变——只有显式绑定失败通道的
//     子代理会迁到备用通道；
//   - 会话通道失败：整个会话（模型/密钥/端点/并发/重试）切到备用通道。
//
// 成功时替换 *runCfg 并更新 *curHistory（从代理轨迹），返回 true，调用方应 continue。
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
		// 防环：fallback 指回本次失败的通道（含自指）即终止；其余通道均允许，
		// 包括当前会话通道（角色通道 cheap.fallback=strong 是合法配置）。
		state.visited[failedID] = struct{}{}
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

	var newCfg *config.Config
	var resolvedID string
	if failedID != sessionID {
		// 角色通道失败：只重定向该角色通道，不动会话主通道。
		// 会话基础模型（appCfg.OpenAI + AI.DefaultChannel）保持不变，
		// 仅写入 失败通道→备用通道 别名，使 Agent Markdown 显式绑定
		// 失败通道的子代理在续跑重建 DA 时解析到备用通道。
		if cur.AI.Channels == nil {
			return false
		}
		if _, ok := cur.AI.Channels[fallbackID]; !ok {
			if h.logger != nil {
				h.logger.Warn("角色通道 fallback 目标不存在",
					zap.String("conversationId", conversationID),
					zap.String("fallbackChannel", fallbackID))
			}
			return false
		}
		newCfg = new(config.Config)
		*newCfg = *cur
		aliases := make(map[string]string, len(cur.AI.ChannelAliases)+1)
		for k, v := range cur.AI.ChannelAliases {
			aliases[k] = v
		}
		aliases[failedID] = fallbackID
		newCfg.AI.ChannelAliases = aliases
		resolvedID = fallbackID
	} else {
		// 会话通道失败：整个会话切换到备用通道（模型/密钥/端点/并发/重试策略一并更换）。
		var err error
		newCfg, resolvedID, err = h.configForAIChannel(fallbackID)
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

// persistConversationAIChannel 把本次请求实际解析出的通道持久化到会话（审计 P2-5：
// 续跑失败会话按原会话通道执行）。失败仅告警：通道持久化失败不影响本次执行。
func (h *AgentHandler) persistConversationAIChannel(conversationID, resolvedChannelID string) {
	if h == nil || h.db == nil || strings.TrimSpace(conversationID) == "" {
		return
	}
	if err := h.db.SetConversationAIChannel(conversationID, resolvedChannelID); err != nil && h.logger != nil {
		h.logger.Warn("持久化会话 AI 通道失败", zap.String("conversationId", conversationID), zap.Error(err))
	}
}

// runSegmentFunc 执行一段 run（由调用方闭包绑定具体执行器与 ctx）。
// 每段执行时传入当前 runCfg（failover 后会变为切换后的配置）。
type runSegmentFunc func(runCfg *config.Config) (*multiagent.RunResult, error)

// runAgentWithChannelFailover 非流式路径共用的 failover 状态机：
// 「执行 → 重试耗尽 → 按失败通道定向切换 fallback → 分段续跑」。
// multi-agent handler、single-agent handler、继续失败会话、批量执行器全部走这一套循环，
// 避免各复制一份循环导致行为漂移（审计 P1/P2 建议）。
// 角色通道失败时只加别名、会话通道失败时整体切换，均由 tryChannelFailover 决定。
func (h *AgentHandler) runAgentWithChannelFailover(
	runCfg *config.Config,
	conversationID string,
	curHistory *[]agent.ChatMessage,
	notify func(eventType, message string, data interface{}),
	baseCtx context.Context,
	runSegment runSegmentFunc,
) (*multiagent.RunResult, error) {
	failover := newChannelFailoverState()
	var result *multiagent.RunResult
	var runErr error
	for {
		result, runErr = runSegment(runCfg)
		if runErr == nil {
			return result, nil
		}
		if shouldPersistEinoAgentTraceAfterRunError(baseCtx) {
			h.persistEinoAgentTraceForResume(conversationID, result)
		}
		if h.tryChannelFailover(runErr, failedChannelIDFromResult(result), &runCfg, failover, conversationID, curHistory, notify) {
			continue
		}
		return result, runErr
	}
}

// validateAIChannelExists 严格校验通道 ID 存在于 ai.channels（空 ID 表示跟随默认通道，直接通过）。
// 注意不能用 config.ResolveAIChannel 的返回值判断：它对未知通道会回退全局 OpenAI 配置并
// 返回 ok=true（兼容旧单通道部署），批量创建/派发需要的是「精确存在」语义（审计 P2-6）。
func (h *AgentHandler) validateAIChannelExists(channelID string) error {
	if strings.TrimSpace(channelID) == "" {
		return nil
	}
	if h == nil || h.config == nil {
		return fmt.Errorf("服务器配置未加载，无法校验 AI 通道")
	}
	nid := config.NormalizeAIChannelID(channelID)
	if h.config.AI.Channels != nil {
		if _, ok := h.config.AI.Channels[nid]; ok {
			return nil
		}
	}
	return fmt.Errorf("AI 通道 %q 不存在（请检查 ai.channels 配置或先创建该通道）", strings.TrimSpace(channelID))
}
