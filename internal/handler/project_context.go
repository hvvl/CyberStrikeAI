package handler

import (
	"strings"

	"cyberstrike-ai/internal/project"
	"go.uber.org/zap"
)

// agentSessionContextBlock 注入会话工作目录与项目测试范围（用于 system prompt 追加块）。
// 用户输入由 message history 承载；压缩后由 summarization 摘要指令保留关键约束。
func (h *AgentHandler) agentSessionContextBlock(conversationID string) string {
	var parts []string
	if ws := h.buildWorkspaceBlock(conversationID); ws != "" {
		parts = append(parts, ws)
	}
	if sb := h.projectScopeBlock(conversationID); sb != "" {
		parts = append(parts, sb)
	}
	return strings.Join(parts, "\n\n")
}

func (h *AgentHandler) buildWorkspaceBlock(conversationID string) string {
	if h == nil || h.config == nil {
		return ""
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ""
	}
	projectID := h.conversationProjectID(conversationID)
	rel := project.WorkspaceRootDir(h.config.Agent.WorkspaceRootDir, projectID, conversationID)
	abs, err := project.EnsureWorkspace(rel)
	if err != nil {
		if h.logger != nil {
			h.logger.Warn("创建会话工作目录失败",
				zap.String("conversationId", conversationID),
				zap.String("projectId", projectID),
				zap.String("path", rel),
				zap.Error(err))
		}
		return ""
	}
	return project.BuildWorkspaceBlock(abs)
}

// projectScopeBlock 根据对话 ID 构建项目测试范围块（用于注入 system prompt；黑板机制已移除）。
func (h *AgentHandler) projectScopeBlock(conversationID string) string {
	if h == nil || h.db == nil || h.config == nil {
		return ""
	}
	if !h.config.Project.Enabled {
		return ""
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ""
	}
	projectID, err := h.db.GetConversationProjectID(conversationID)
	if err != nil || projectID == "" {
		return ""
	}
	block, err := project.BuildProjectScopeContextBlock(h.db, projectID)
	if err != nil {
		h.logger.Warn("构建项目测试范围块失败", zap.String("conversationId", conversationID), zap.Error(err))
		return ""
	}
	return strings.TrimSpace(block)
}

// conversationProjectID 返回对话绑定的项目 ID；未绑定或查询失败时返回空字符串。
func (h *AgentHandler) conversationProjectID(conversationID string) string {
	if h == nil || h.db == nil {
		return ""
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ""
	}
	projectID, err := h.db.GetConversationProjectID(conversationID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(projectID)
}
