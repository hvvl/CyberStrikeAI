package handler

import (
	"path/filepath"
	"strings"
	"testing"

	"cyberstrike-ai/internal/database"

	"go.uber.org/zap"
)

// TestBatchTaskResultPreview 验证内存泄漏修复 E 的截断函数：
// 短文本原样返回；超限按字节截断并回退到 UTF-8 边界（多字节字符不被劈开）。
func TestBatchTaskResultPreview(t *testing.T) {
	// 短文本：原样
	short := "报告正文"
	if got := batchTaskResultPreview(short); got != short {
		t.Fatalf("short text should pass through, got %q", got)
	}

	// 纯 ASCII 超限：精确 2048
	long := strings.Repeat("a", batchTaskResultPreviewBytes+100)
	got := batchTaskResultPreview(long)
	if len(got) != batchTaskResultPreviewBytes {
		t.Fatalf("ascii cut should be exactly %d bytes, got %d", batchTaskResultPreviewBytes, len(got))
	}

	// 多字节截断：以「中」(3 字节) 填充，2048 不在字符边界（2048%3=2），
	// 必须回退到边界且结果仍是合法 UTF-8。
	multi := strings.Repeat("中", 2000)
	gotMulti := batchTaskResultResultPreviewChecked(t, multi)
	if len(gotMulti) >= batchTaskResultPreviewBytes {
		t.Fatalf("multi-byte cut must back off below %d, got %d", batchTaskResultPreviewBytes, len(gotMulti))
	}
	// 预览应保留尽可能多的字节（回退最多 2 字节：2048→2046）
	if len(gotMulti) < batchTaskResultPreviewBytes-3 {
		t.Fatalf("multi-byte cut backed off too much: %d", len(gotMulti))
	}
}

func batchTaskResultResultPreviewChecked(t *testing.T, s string) string {
	t.Helper()
	got := batchTaskResultPreview(s)
	if len(s) > batchTaskResultPreviewBytes && len(got) >= len(s) {
		t.Fatal("preview not truncated")
	}
	return got
}

// TestBatchTaskMemoryPreviewOnComplete 验证运行时路径：子任务完成时写入内存的
// Result 是预览而非全量，且 DB 行保留全量。
func TestBatchTaskMemoryPreviewOnComplete(t *testing.T) {
	db, err := database.NewDB(filepath.Join(t.TempDir(), "preview-complete.db"), zap.NewNop())
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	m := NewBatchTaskManager(zap.NewNop())
	m.SetDB(db)
	queue, err := m.CreateBatchQueue("预览", "", "eino_single", "manual", "", "", "", nil, 1,
		[]string{"only"})
	if err != nil {
		t.Fatalf("CreateBatchQueue: %v", err)
	}
	m.UpdateQueueStatus(queue.ID, BatchQueueStatusRunning)
	task, ok := m.ClaimNextPendingTask(queue.ID)
	if !ok {
		t.Fatal("claim failed")
	}

	full := strings.Repeat("渗透报告-", 2000) // 32KB，远超预览上限
	m.UpdateTaskStatus(queue.ID, task.ID, BatchTaskStatusCompleted, full, "")

	q, _ := m.GetBatchQueue(queue.ID)
	for _, tk := range q.Tasks {
		if tk.ID != task.ID {
			continue
		}
		if len(tk.Result) > batchTaskResultPreviewBytes {
			t.Fatalf("in-memory result should be capped at %d bytes, got %d", batchTaskResultPreviewBytes, len(tk.Result))
		}
	}

	// DB 行保留全量
	rows, err := db.GetBatchTasks(queue.ID)
	if err != nil {
		t.Fatalf("GetBatchTasks: %v", err)
	}
	found := false
	for _, row := range rows {
		if row.ID == task.ID && row.Result.Valid && len(row.Result.String) == len(full) {
			found = true
		}
	}
	if !found {
		t.Fatal("DB row should keep the full result")
	}
}
