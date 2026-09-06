package database

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// TestLegacySchemaUpgradeCompat 老库升级兼容回归：
// 现网库存在 5d04805/分组删除前的遗留表（conversation_groups/mappings、assets、
// project_facts、project_fact_edges）且 conversations 缺新列（ai_channel_id/
// trace_run_id）时，新二进制 NewDB 升级路径必须：
//  1. 不报错（遗留表残留不触发清理/报错路径）；
//  2. 迁移补齐新列；
//  3. 遗留表数据原样保留（不做破坏性清理）；
//  4. 新功能（通道列 + 轨迹代次）可正常写入读取。
func TestLegacySchemaUpgradeCompat(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	// 构造"更老的库"：先建缺新列的 conversations 与遗留表（含数据）
	raw, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	scripts := []string{
		`CREATE TABLE conversations (
			id TEXT PRIMARY KEY,
			title TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			owner_user_id TEXT DEFAULT '',
			summary TEXT,
			summary_updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			pinned INTEGER NOT NULL DEFAULT 0
		)`,
		`INSERT INTO conversations (id, title, pinned) VALUES ('c1', '旧会话', 1)`,
		`CREATE TABLE conversation_groups (id TEXT PRIMARY KEY, name TEXT)`,
		`INSERT INTO conversation_groups VALUES ('g1', '遗留分组')`,
		`CREATE TABLE conversation_group_mappings (conversation_id TEXT, group_id TEXT)`,
		`INSERT INTO conversation_group_mappings VALUES ('c1', 'g1')`,
		`CREATE TABLE assets (id TEXT PRIMARY KEY, target TEXT)`,
		`INSERT INTO assets VALUES ('a1', '1.2.3.4')`,
		`CREATE TABLE project_facts (id TEXT PRIMARY KEY, content TEXT)`,
		`INSERT INTO project_facts VALUES ('f1', '遗留事实')`,
		`CREATE TABLE project_fact_edges (id TEXT PRIMARY KEY)`,
		`INSERT INTO project_fact_edges VALUES ('e1')`,
	}
	for _, s := range scripts {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("seed legacy schema (%s…): %v", s[:40], err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	// 用新二进制路径重新打开：走真实迁移
	db, err := NewDB(dbPath, zap.NewNop())
	if err != nil {
		t.Fatalf("NewDB upgrade on legacy db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// 1. 新列补齐且可写新功能值（老库升级后通道/轨迹功能立即可用）
	if _, err := db.Exec(`UPDATE conversations SET ai_channel_id='ch1', trace_run_id=7 WHERE id='c1'`); err != nil {
		t.Fatalf("write upgraded columns: %v", err)
	}
	var chID string
	var runID int
	var pinned int
	if err := db.QueryRow(`SELECT ai_channel_id, trace_run_id, pinned FROM conversations WHERE id='c1'`).
		Scan(&chID, &runID, &pinned); err != nil {
		t.Fatalf("read upgraded columns: %v", err)
	}
	if chID != "ch1" || runID != 7 || pinned != 1 {
		t.Fatalf("upgraded values mismatch: ch=%q run=%d pinned=%d", chID, runID, pinned)
	}

	// 2. 遗留表数据原样保留（不清理、不报错）
	for _, q := range []struct {
		sql  string
		want int
		hint string
	}{
		{`SELECT COUNT(*) FROM conversation_groups`, 1, "conversation_groups"},
		{`SELECT COUNT(*) FROM conversation_group_mappings`, 1, "conversation_group_mappings"},
		{`SELECT COUNT(*) FROM assets`, 1, "assets"},
		{`SELECT COUNT(*) FROM project_facts`, 1, "project_facts"},
		{`SELECT COUNT(*) FROM project_fact_edges`, 1, "project_fact_edges"},
	} {
		var n int
		if err := db.QueryRow(q.sql).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", q.hint, err)
		}
		if n != q.want {
			t.Fatalf("%s legacy rows: got %d want %d (遗留数据必须原样保留)", q.hint, n, q.want)
		}
	}
}
