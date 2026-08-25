package handler

import (
	"net/http"
	"strconv"
	"strings"

	"cyberstrike-ai/internal/database"
	"cyberstrike-ai/internal/security"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const maxProjectDescriptionRunes = 4000

func clampProjectDescription(s string) string {
	r := []rune(s)
	if len(r) <= maxProjectDescriptionRunes {
		return s
	}
	return string(r[:maxProjectDescriptionRunes])
}

// ProjectHandler 项目管理处理器。
type ProjectHandler struct {
	db     *database.DB
	logger *zap.Logger
}

// NewProjectHandler 创建项目管理处理器。
func NewProjectHandler(db *database.DB, logger *zap.Logger) *ProjectHandler {
	return &ProjectHandler{db: db, logger: logger}
}

type createProjectRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	ScopeJSON   string `json:"scope_json"`
	Status      string `json:"status"`
}

// updateProjectRequest 部分更新：字段省略表示不修改；传 null 或 "" 可清空字符串字段。
type updateProjectRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	ScopeJSON   *string `json:"scope_json"`
	Status      *string `json:"status"`
	Pinned      *bool   `json:"pinned"`
}

// CreateProject POST /api/projects
func (h *ProjectHandler) CreateProject(c *gin.Context) {
	var req createProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p := &database.Project{
		Name:        strings.TrimSpace(req.Name),
		Description: clampProjectDescription(req.Description),
		ScopeJSON:   req.ScopeJSON,
		Status:      strings.TrimSpace(req.Status),
	}
	created, err := h.db.CreateProject(p)
	if err != nil {
		h.logger.Error("创建项目失败", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if session, ok := security.CurrentSession(c); ok {
		_ = h.db.SetResourceOwner("project", created.ID, session.UserID)
		_ = h.db.AssignResourceToUser(session.UserID, "project", created.ID)
	}
	c.JSON(http.StatusOK, created)
}

// GetDashboardSummary GET /api/projects/dashboard-summary
func (h *ProjectHandler) GetDashboardSummary(c *gin.Context) {
	limit, _ := strconv.Atoi(strings.TrimSpace(c.DefaultQuery("fact_limit", "5")))
	if limit <= 0 {
		limit = 5
	}
	if limit > 50 {
		limit = 50
	}
	session, _ := security.CurrentSession(c)
	summary, err := h.db.GetProjectDashboardSummaryForAccess(limit, session.UserID, session.Scope)
	if err != nil {
		h.logger.Error("获取项目仪表盘摘要失败", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, summary)
}

// ListProjects GET /api/projects
func (h *ProjectHandler) ListProjects(c *gin.Context) {
	status := c.Query("status")
	search := c.Query("search")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	session, _ := security.CurrentSession(c)
	list, err := h.db.ListProjectsForAccess(status, search, limit, offset, session.UserID, session.Scope)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if list == nil {
		list = []*database.Project{}
	}
	total, err := h.db.CountProjectsForAccess(status, search, session.UserID, session.Scope)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"projects": list,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
	})
}

// GetProjectStats GET /api/projects/:id/stats（黑板已移除，返回漏洞与会话统计）
func (h *ProjectHandler) GetProjectStats(c *gin.Context) {
	stats, err := h.db.GetProjectStatsCounts(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"vuln_count":         stats.VulnCount,
		"conversation_count": stats.ConversationCount,
	})
}

// ListProjectConversations GET /api/projects/:id/conversations
func (h *ProjectHandler) ListProjectConversations(c *gin.Context) {
	projectID := c.Param("id")
	if _, err := h.db.GetProject(projectID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "项目不存在"})
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	list, err := h.db.ListConversationsByProjectID(projectID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if list == nil {
		list = []*database.Conversation{}
	}
	total, _ := h.db.CountConversationsByProjectID(projectID)
	c.JSON(http.StatusOK, gin.H{
		"conversations": list,
		"total":         total,
		"limit":         limit,
		"offset":        offset,
	})
}

// GetProject GET /api/projects/:id
func (h *ProjectHandler) GetProject(c *gin.Context) {
	p, err := h.db.GetProject(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "项目不存在"})
		return
	}
	c.JSON(http.StatusOK, p)
}

// UpdateProject PUT /api/projects/:id
func (h *ProjectHandler) UpdateProject(c *gin.Context) {
	id := c.Param("id")
	p, err := h.db.GetProject(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "项目不存在"})
		return
	}
	var req updateProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Name != nil {
		if s := strings.TrimSpace(*req.Name); s != "" {
			p.Name = s
		}
	}
	if req.Description != nil {
		p.Description = clampProjectDescription(*req.Description)
	}
	if req.ScopeJSON != nil {
		p.ScopeJSON = *req.ScopeJSON
	}
	if req.Status != nil {
		if s := strings.TrimSpace(*req.Status); s != "" {
			p.Status = s
		}
	}
	if req.Pinned != nil {
		p.Pinned = *req.Pinned
	}
	if err := h.db.UpdateProject(p); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, p)
}

// DeleteProject DELETE /api/projects/:id
func (h *ProjectHandler) DeleteProject(c *gin.Context) {
	if err := h.db.DeleteProject(c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
