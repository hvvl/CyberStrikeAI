package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"cyberstrike-ai/internal/agent"
	"cyberstrike-ai/internal/agentfinalizer"
	"cyberstrike-ai/internal/authctx"
	"cyberstrike-ai/internal/config"
	"cyberstrike-ai/internal/mcp"
	"cyberstrike-ai/internal/multiagent"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// FailedConversationItem 最近一次执行以 error 结束的会话（API 调用失败等）。
type FailedConversationItem struct {
	ConversationID string    `json:"conversationId"`
	Title          string    `json:"title"`
	AgentMode      string    `json:"agentMode"`
	FailedAt       time.Time `json:"failedAt"`
	ErrorPreview   string    `json:"errorPreview"`
}

// ListAPIFailedConversations 列出最近一次过程事件为 error 的会话。
// 识别依据：process_details 中该会话 rowid 最大的事件 event_type='error'
// （执行失败时 handler 会写入 AddProcessDetail(..., "error", ...)；
// 用户取消=cancelled、服务端超时=timeout，均不视为 API 调用失败）。
func (h *AgentHandler) ListAPIFailedConversations(limit int) ([]*FailedConversationItem, error) {
	if h.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	// limit <= 0 表示不限制（用于"继续全部"）；limit > 0 时按条数截断。
	// 分类过滤：仅收录带 [api_failure:*] 标签的重试耗尽错误（agentRunErrorMsg 打标签），
	// 非重试型错误（鉴权失败/程序缺陷等）不入列，避免一键续跑重复执行必然失败的任务。
	base := `
SELECT c.id, c.title, c.agent_mode, pd.message, pd.created_at
FROM conversations c
JOIN process_details pd ON pd.conversation_id = c.id
WHERE pd.event_type = 'error'
  AND pd.message LIKE '` + apiFailureTagPrefix + `%'
  AND pd.rowid = (SELECT MAX(pd2.rowid) FROM process_details pd2 WHERE pd2.conversation_id = c.id)
ORDER BY pd.created_at DESC`
	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = h.db.Query(base+` LIMIT ?`, limit)
	} else {
		rows, err = h.db.Query(base)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]*FailedConversationItem, 0, limit)
	for rows.Next() {
		var it FailedConversationItem
		var createdAt string
		if err := rows.Scan(&it.ConversationID, &it.Title, &it.AgentMode, &it.ErrorPreview, &createdAt); err != nil {
			h.logger.Warn("扫描失败会话行失败", zap.Error(err))
			continue
		}
		it.FailedAt = parseFlexibleTime(createdAt)
		// 按 rune 截断，避免在 300 字节边界切断多字节 UTF-8 字符（中文错误消息）。
		if r := []rune(it.ErrorPreview); len(r) > 300 {
			it.ErrorPreview = string(r[:300]) + "…"
		}
		items = append(items, &it)
	}
	return items, rows.Err()
}

// ListAPIFailedConversationsByIDs 按显式会话 ID 查询失败会话（不受最近 N 条上限约束）。
// 仅返回「最近一次过程事件为 error」的会话；传入不存在或非失败的 ID 自然不出现在结果里，
// 由调用方据此在 skipped 中给出明确原因。
func (h *AgentHandler) ListAPIFailedConversationsByIDs(ids []string) ([]*FailedConversationItem, error) {
	if h.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, 0, len(ids))
	args := make([]interface{}, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	if len(placeholders) == 0 {
		return nil, nil
	}
	q := `
SELECT c.id, c.title, c.agent_mode, pd.message, pd.created_at
FROM conversations c
JOIN process_details pd ON pd.conversation_id = c.id
WHERE pd.event_type = 'error'
  AND pd.message LIKE '` + apiFailureTagPrefix + `%'
  AND pd.rowid = (SELECT MAX(pd2.rowid) FROM process_details pd2 WHERE pd2.conversation_id = c.id)
  AND c.id IN (` + strings.Join(placeholders, ",") + `)
ORDER BY pd.created_at DESC`
	rows, err := h.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]*FailedConversationItem, 0, len(placeholders))
	for rows.Next() {
		var it FailedConversationItem
		var createdAt string
		if err := rows.Scan(&it.ConversationID, &it.Title, &it.AgentMode, &it.ErrorPreview, &createdAt); err != nil {
			h.logger.Warn("扫描失败会话行失败", zap.Error(err))
			continue
		}
		it.FailedAt = parseFlexibleTime(createdAt)
		if r := []rune(it.ErrorPreview); len(r) > 300 {
			it.ErrorPreview = string(r[:300]) + "…"
		}
		items = append(items, &it)
	}
	return items, rows.Err()
}

func parseFlexibleTime(s string) time.Time {
	layouts := []string{
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05",
		time.RFC3339,
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// ListFailedConversations GET /api/agent-loop/failed —— 列出 API 调用失败的会话。
func (h *AgentHandler) ListFailedConversations(c *gin.Context) {
	// 取全部失败会话（不限条数），统一 RBAC/运行中过滤后计算真实 total，
	// 列表只返回最近 100 条，避免「界面看 100 条、继续全部却入队几百条」的错位。
	items, err := h.ListAPIFailedConversations(0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	visible := make([]*FailedConversationItem, 0, len(items))
	for _, it := range items {
		if h.tasks != nil && h.tasks.GetTask(it.ConversationID) != nil {
			continue
		}
		if !h.agentConversationAllowed(c, it.ConversationID) {
			continue
		}
		visible = append(visible, it)
	}
	total := len(visible)
	const listCap = 100
	truncated := total > listCap
	if truncated {
		visible = visible[:listCap]
	}
	c.JSON(http.StatusOK, gin.H{"items": visible, "count": len(visible), "total": total, "truncated": truncated})
}

// ContinueFailedConversationsRequest 一键继续失败会话的请求体。
type ContinueFailedConversationsRequest struct {
	// ConversationIDs 为空时表示继续全部失败会话；非空则只继续指定会话。
	ConversationIDs []string `json:"conversationIds"`
}

// continueFailedState 防止重复入队的内存状态。
type continueFailedState struct {
	mu      sync.Mutex
	queued  map[string]bool // 已入队/执行中的会话
	running bool            // 串行 worker 是否在跑
	queue   []string
}

var globalContinueFailedState = &continueFailedState{queued: make(map[string]bool)}

// ContinueFailedConversations POST /api/agent-loop/continue-failed —— 一键继续所有 API 调用失败的会话。
func (h *AgentHandler) ContinueFailedConversations(c *gin.Context) {
	var req ContinueFailedConversationsRequest
	_ = c.ShouldBindJSON(&req) // 允许空 body（= 继续全部）

	var candidates []*FailedConversationItem
	var err error
	if len(req.ConversationIDs) > 0 {
		// 显式 ID：不受列表上限约束，逐一判定（含不存在/未失败的明确原因）。
		candidates, err = h.ListAPIFailedConversationsByIDs(req.ConversationIDs)
	} else {
		// 空 body = 全部失败会话（不限最近 N 条）。
		candidates, err = h.ListAPIFailedConversations(0)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	want := make(map[string]bool, len(req.ConversationIDs))
	for _, id := range req.ConversationIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			want[id] = true
		}
	}

	st := globalContinueFailedState
	st.mu.Lock()
	defer st.mu.Unlock()

	queued := make([]string, 0, len(candidates))
	type skippedItem struct {
		ConversationID string `json:"conversationId"`
		Reason         string `json:"reason"`
	}
	skipped := make([]skippedItem, 0)

	for _, it := range candidates {
		if len(want) > 0 && !want[it.ConversationID] {
			continue
		}
		if !h.agentConversationAllowed(c, it.ConversationID) {
			skipped = append(skipped, skippedItem{it.ConversationID, "无权访问"})
			continue
		}
		if h.tasks != nil && h.tasks.GetTask(it.ConversationID) != nil {
			skipped = append(skipped, skippedItem{it.ConversationID, "会话已有任务正在执行"})
			continue
		}
		if st.queued[it.ConversationID] {
			skipped = append(skipped, skippedItem{it.ConversationID, "已在续跑队列中"})
			continue
		}
		st.queued[it.ConversationID] = true
		st.queue = append(st.queue, it.ConversationID)
		queued = append(queued, it.ConversationID)
	}

	// 显式 ID 请求时，对「不在失败会话候选里」的 ID 给出明确原因（不存在/未处于失败状态），
	// 避免客户端看到 queued=0 却不知原因。
	if len(req.ConversationIDs) > 0 {
		found := make(map[string]bool, len(candidates))
		for _, it := range candidates {
			found[it.ConversationID] = true
		}
		for _, id := range req.ConversationIDs {
			id = strings.TrimSpace(id)
			if id == "" || found[id] {
				continue
			}
			skipped = append(skipped, skippedItem{id, "会话不存在或当前不处于失败状态"})
		}
	}

	if len(queued) > 0 && !st.running {
		st.running = true
		go h.runContinueFailedWorker(st)
	}

	c.JSON(http.StatusOK, gin.H{
		"queued":  len(queued),
		"ids":     queued,
		"skipped": skipped,
	})
}

// runContinueFailedWorker 串行消费续跑队列（避免多会话同时打满通道并发/配额）。
func (h *AgentHandler) runContinueFailedWorker(st *continueFailedState) {
	for {
		st.mu.Lock()
		if len(st.queue) == 0 {
			st.running = false
			st.mu.Unlock()
			return
		}
		id := st.queue[0]
		st.queue = st.queue[1:]
		st.mu.Unlock()

		h.continueFailedConversation(id)

		st.mu.Lock()
		delete(st.queued, id)
		st.mu.Unlock()
	}
}

// continueFailedContinuePrompt 续跑时发给模型的提示（不写入 messages 用户气泡，
// 与 interrupt_continue 同款做法，避免主对话流出现模板文本）。
const continueFailedContinuePrompt = "上一次任务执行因 API 调用失败而中断（系统已自动重试仍未成功）。" +
	"请基于已有上下文，从中断处继续完成原任务；若原任务已无可继续的内容，请总结当前进展。"

// continueFailedConversation 后台续跑单个失败会话（复用 batch_queue_executor 的服务端执行模板）。
func (h *AgentHandler) continueFailedConversation(conversationID string) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" || h.db == nil || h.config == nil {
		return
	}
	log := h.logger.With(zap.String("conversationId", conversationID), zap.String("source", "continue_failed"))

	conv, err := h.db.GetConversation(conversationID)
	if err != nil || conv == nil {
		log.Warn("续跑失败会话：会话不存在", zap.Error(err))
		return
	}
	if h.tasks.GetTask(conversationID) != nil {
		log.Info("续跑失败会话：已有任务在跑，跳过")
		return
	}

	// 以会话所有者身份执行（同 batch_queue_executor）。
	ownerUserID := h.db.GetResourceOwner("conversation", conversationID)
	access, accessErr := h.db.ResolveRBACAccess(ownerUserID)
	if accessErr != nil || access == nil || !access.User.Enabled {
		log.Warn("续跑失败会话：所有者不存在或已禁用", zap.Error(accessErr))
		return
	}
	principal := authctx.NewPrincipalWithScopes(access.User.ID, access.User.Username, access.Scope, access.Permissions, access.PermissionScopes)

	// 历史优先取 model-facing 轨迹（失败时已 persistEinoAgentTraceForResume 保留），否则退化为消息文本历史。
	history, herr := h.loadHistoryFromAgentTrace(conversationID)
	if herr != nil || len(history) == 0 {
		if msgs, gerr := h.db.GetMessages(conversationID); gerr == nil {
			history = dbMessagesToAgentChatMessages(msgs)
		} else {
			history = []agent.ChatMessage{}
		}
	}

	var roleTools []string
	if conv.RoleName != "" && conv.RoleName != "默认" && h.config.Roles != nil {
		if role, ok := h.config.Roles[conv.RoleName]; ok && role.Enabled {
			roleTools = role.Tools
		}
	}

	principalCtx := authctx.WithPrincipal(context.Background(), principal)
	baseCtx, cancelWithCause := context.WithCancelCause(principalCtx)
	taskCtx, timeoutCancel := context.WithTimeout(baseCtx, 6*time.Hour)

	registered := false
	finishStatus := "completed"
	defer func() {
		timeoutCancel()
		if registered {
			h.publishContinueFailedEvent(conversationID, "done", "", map[string]interface{}{"conversationId": conversationID})
			h.tasks.FinishTask(conversationID, finishStatus)
		}
		cancelWithCause(nil)
	}()

	if _, err := h.tasks.StartTask(conversationID, continueFailedContinuePrompt, cancelWithCause); err != nil {
		log.Warn("续跑失败会话：注册任务失败", zap.Error(err))
		return
	}
	registered = true
	h.tasks.UpdateTaskStatus(conversationID, "running")

	// 先抢占任务成功再写"处理中"占位消息：避免 StartTask 与正常聊天请求竞争同一会话时
	// 留下孤立的 assistant 占位消息（M6 竞态修复）。
	assistantMsg, err := h.db.AddMessage(conversationID, "assistant", "处理中...", nil)
	if err != nil {
		log.Error("续跑失败会话：创建助手消息失败", zap.Error(err))
		return
	}
	assistantMessageID := ""
	if assistantMsg != nil {
		assistantMessageID = assistantMsg.ID
	}

	h.publishContinueFailedEvent(conversationID, "continue_failed_start",
		"检测到上一次执行因 API 调用失败，正在自动继续…",
		map[string]interface{}{"conversationId": conversationID, "messageId": assistantMessageID})

	progressCallback := h.createProgressCallback(taskCtx, cancelWithCause, conversationID, assistantMessageID, func(eventType, message string, data interface{}) {
		h.publishContinueFailedEvent(conversationID, eventType, message, data)
	})
	taskCtx = mcp.WithMCPConversationID(taskCtx, conversationID)
	taskCtx = mcp.WithToolRunRegistry(taskCtx, h.tasks)
	taskCtx = mcp.WithEinoExecuteRunRegistry(taskCtx, h.tasks)

	// 按会话的 agent_mode 选择执行器；通道走默认路由，重试耗尽时经 tryChannelFailover 分段续跑。
	mode := strings.TrimSpace(strings.ToLower(conv.AgentMode))
	useMulti := h.config.MultiAgent.Enabled && mode != "" && mode != "eino_single"
	orch := "deep"
	if useMulti {
		orch = config.NormalizeMultiAgentOrchestration(mode)
	}

	// 后台续跑与普通聊天入口一致：重试耗尽 → fallback_channel 分段续跑（复用同一套 failover 循环）。
	runCfg, _, cfgErr := h.configForAIChannel("")
	if cfgErr != nil {
		log.Warn("续跑失败会话：解析通道配置失败", zap.Error(cfgErr))
		runCfg = h.config
	}
	failover := newChannelFailoverState(runCfg.AI.DefaultChannel)
	curHistory := history

	var result *multiagent.RunResult
	var runErr error
	for {
		if useMulti {
			result, runErr = multiagent.RunDeepAgent(taskCtx, runCfg, &runCfg.MultiAgent, h.agent, h.db, h.logger,
				conversationID, h.conversationProjectID(conversationID), continueFailedContinuePrompt, curHistory, roleTools,
				progressCallback, h.agentsMarkdownDir, orch, nil, h.agentSessionContextBlock(conversationID))
		} else {
			result, runErr = multiagent.RunEinoSingleChatModelAgent(taskCtx, runCfg, &runCfg.MultiAgent, h.agent, h.db, h.logger,
				conversationID, h.conversationProjectID(conversationID), continueFailedContinuePrompt, curHistory, roleTools,
				progressCallback, nil, h.agentSessionContextBlock(conversationID))
		}
		if runErr == nil {
			break
		}
		if shouldPersistEinoAgentTraceAfterRunError(baseCtx) {
			h.persistEinoAgentTraceForResume(conversationID, result)
		}
		if h.tryChannelFailover(runErr, failedChannelIDFromResult(result), &runCfg, failover, conversationID, &curHistory, func(eventType, message string, data interface{}) {
			h.publishContinueFailedEvent(conversationID, eventType, message, data)
		}) {
			continue
		}
		break
	}

	if runErr != nil {
		finishStatus = "failed"
		errMsg := agentRunErrorMsg(runErr)
		if assistantMessageID != "" {
			_, _ = h.db.Exec("UPDATE messages SET content = ?, updated_at = ? WHERE id = ?", errMsg, time.Now(), assistantMessageID)
			_ = h.db.AddProcessDetail(assistantMessageID, conversationID, "error", errMsg, nil)
		}
		h.publishContinueFailedEvent(conversationID, "error", errMsg, map[string]interface{}{
			"conversationId": conversationID,
			"messageId":      assistantMessageID,
		})
		log.Warn("续跑失败会话：再次失败", zap.Error(runErr))
		return
	}
	if result == nil {
		finishStatus = "failed"
		log.Error("续跑失败会话：执行成功但无结果对象")
		return
	}

	agentMode := "eino_single"
	if useMulti {
		agentMode = "eino_" + orch
	}
	var decision agentfinalizer.Decision
	if h.config != nil {
		decision = h.finalizeAgentRunForDeliveryWithPolicy(conversationID, assistantMessageID, agentMode, result, result.MCPExecutionIDs, multiagent.AggregatedReasoningFromTraceJSON(result.LastAgentTraceInput), true)
	}
	resText := decision.FinalText
	if !decision.Finalizable {
		resText = finalizationBlockedMessage(decision)
		finishStatus = decision.Status
	}
	h.publishContinueFailedEvent(conversationID, "response", resText, map[string]interface{}{
		"conversationId":  conversationID,
		"messageId":       assistantMessageID,
		"agentMode":       agentMode,
		"mcpExecutionIds": result.MCPExecutionIDs,
	})
	if result.LastAgentTraceInput != "" || result.LastAgentTraceOutput != "" {
		if err := h.db.SaveAgentTrace(conversationID, result.LastAgentTraceInput, result.LastAgentTraceOutput); err != nil {
			log.Warn("续跑失败会话：保存代理轨迹失败", zap.Error(err))
		}
	}
	log.Info("续跑失败会话：完成", zap.String("finishStatus", finishStatus))
}

// publishContinueFailedEvent 通过任务事件总线发布 SSE 事件（前端打开该会话时可实时看到）。
func (h *AgentHandler) publishContinueFailedEvent(conversationID, eventType, message string, data interface{}) {
	if h.taskEventBus == nil {
		return
	}
	ev := StreamEvent{Type: eventType, Message: message, Data: data}
	b, err := json.Marshal(ev)
	if err != nil {
		b = []byte(`{"type":"error","message":"marshal failed"}`)
	}
	line := make([]byte, 0, len(b)+8)
	line = append(line, []byte("data: ")...)
	line = append(line, b...)
	line = append(line, '\n', '\n')
	h.taskEventBus.Publish(conversationID, line)
}
