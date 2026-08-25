package database

import (
	"database/sql"
	"fmt"
	"strings"
)

// ProjectStats 项目聚合统计（黑板已移除，仅漏洞与对话计数）。
type ProjectStats struct {
	VulnCount         int `json:"vuln_count"`
	ConversationCount int `json:"conversation_count"`
}

// GetProjectStatsCounts 统计项目下漏洞与对话数量。
func (db *DB) GetProjectStatsCounts(projectID string) (*ProjectStats, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, fmt.Errorf("project_id 不能为空")
	}
	if _, err := db.GetProject(projectID); err != nil {
		return nil, err
	}
	stats := &ProjectStats{}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM vulnerabilities WHERE project_id = ?`,
		projectID,
	).Scan(&stats.VulnCount); err != nil {
		return nil, fmt.Errorf("统计漏洞失败: %w", err)
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM conversations WHERE project_id = ?`,
		projectID,
	).Scan(&stats.ConversationCount); err != nil {
		return nil, fmt.Errorf("统计对话失败: %w", err)
	}
	return stats, nil
}

func (db *DB) ListConversationsByProjectID(projectID string, limit, offset int) ([]*Conversation, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.Query(
		`SELECT id, title, COALESCE(pinned, 0), created_at, updated_at, project_id, role_name
		 FROM conversations WHERE project_id = ? ORDER BY updated_at DESC LIMIT ? OFFSET ?`,
		projectID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("查询项目对话失败: %w", err)
	}
	defer rows.Close()

	var conversations []*Conversation
	for rows.Next() {
		var conv Conversation
		var createdAt, updatedAt string
		var pinned int
		var pid sql.NullString
		var roleName sql.NullString
		if err := rows.Scan(&conv.ID, &conv.Title, &pinned, &createdAt, &updatedAt, &pid, &roleName); err != nil {
			return nil, err
		}
		if pid.Valid {
			conv.ProjectID = strings.TrimSpace(pid.String)
		}
		if roleName.Valid {
			conv.RoleName = normalizeConversationRoleName(roleName.String)
		}
		conv.CreatedAt = parseDBTime(createdAt)
		conv.UpdatedAt = parseDBTime(updatedAt)
		conv.Pinned = pinned != 0
		conversations = append(conversations, &conv)
	}
	return conversations, rows.Err()
}

// CountConversationsByProjectID 统计项目绑定对话数。
func (db *DB) CountConversationsByProjectID(projectID string) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE project_id = ?`, projectID).Scan(&n)
	return n, err
}
