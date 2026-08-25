package database

import (
	"fmt"
	"strings"
)

// ProjectDashboardTotals 仪表盘项目汇总计数（黑板已移除，仅活跃项目数）。
type ProjectDashboardTotals struct {
	ActiveProjects int `json:"active_projects"`
}

// ProjectDashboardSummary 仪表盘项目情报摘要。
type ProjectDashboardSummary struct {
	Totals ProjectDashboardTotals `json:"totals"`
}

// GetProjectDashboardSummary 聚合项目汇总（黑板事实流已移除）。
func (db *DB) GetProjectDashboardSummary(factLimit int) (*ProjectDashboardSummary, error) {
	return db.GetProjectDashboardSummaryForAccess(factLimit, "", "")
}

func (db *DB) GetProjectDashboardSummaryForAccess(factLimit int, userID, scope string) (*ProjectDashboardSummary, error) {
	out := &ProjectDashboardSummary{}

	projectAccess := ""
	args := []interface{}{}
	userID = strings.TrimSpace(userID)
	if userID != "" && scope != RBACScopeAll {
		projectAccess = ` AND (
			p.owner_user_id = ?
			OR EXISTS (
				SELECT 1 FROM rbac_resource_assignments ra
				WHERE ra.user_id = ? AND ra.resource_type = 'project' AND ra.resource_id = p.id
			)
		)`
		args = append(args, userID, userID)
	}

	if err := db.QueryRow(`SELECT COUNT(*) FROM projects p WHERE p.status = 'active'`+projectAccess, args...).Scan(&out.Totals.ActiveProjects); err != nil {
		return nil, fmt.Errorf("统计活跃项目失败: %w", err)
	}
	return out, nil
}
