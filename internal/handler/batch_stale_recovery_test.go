package handler

import (
	"path/filepath"
	"testing"
	"time"

	"cyberstrike-ai/internal/database"

	"go.uber.org/zap"
)

// TestTaskStatusHelper 验证只读 TaskStatus 辅助（内存修复 H1 兜底 defer 依赖）：
// 已加载任务返回当前状态；不存在返回空串。
func TestTaskStatusHelper(t *testing.T) {
	t.Parallel()

	db, err := database.NewDB(filepath.Join(t.TempDir(), "task-status.db"), zap.NewNop())
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}

	m := NewBatchTaskManager(zap.NewNop())
	m.SetDB(db)
	queue, err := m.CreateBatchQueue("status helper", "", "eino_single", "manual", "", "", "", nil, 1,
		[]string{"t1", "t2"})
	if err != nil {
		t.Fatalf("CreateBatchQueue: %v", err)
	}

	// 未认领：pending
	if got := m.TaskStatus(queue.ID, queue.Tasks[0].ID); got != BatchTaskStatusPending {
		t.Fatalf("expected pending, got %q", got)
	}
	// 认领后：running
	claimed, ok := m.ClaimNextPendingTask(queue.ID)
	if !ok || claimed == nil {
		t.Fatal("claim should succeed")
	}
	if got := m.TaskStatus(queue.ID, claimed.ID); got != BatchTaskStatusRunning {
		t.Fatalf("expected running, got %q", got)
	}
	// 落终态后：completed
	m.UpdateTaskStatus(queue.ID, claimed.ID, BatchTaskStatusCompleted, "done", "")
	if got := m.TaskStatus(queue.ID, claimed.ID); got != BatchTaskStatusCompleted {
		t.Fatalf("expected completed, got %q", got)
	}
	// 不存在的任务/队列：空串
	if got := m.TaskStatus(queue.ID, "no-such-task"); got != "" {
		t.Fatalf("expected empty for missing task, got %q", got)
	}
	if got := m.TaskStatus("no-such-queue", claimed.ID); got != "" {
		t.Fatalf("expected empty for missing queue, got %q", got)
	}
}

// TestRecoverStaleRunningTasks 验证运行期自愈（内存修复 H3）：
// 超龄 running 任务回滚 pending → 可被 ClaimNextPendingTask 重新认领；
// 未超龄的任务不动；无执行器的 running 队列置回 paused。
// 注意时间余量：回滚判定是严格大于（started_at < now-maxAge），
// 构造「未超龄」样本时 started_at 与 maxAge 要留足间隔。
func TestRecoverStaleRunningTasks(t *testing.T) {
	t.Parallel()

	db, err := database.NewDB(filepath.Join(t.TempDir(), "recover-stale.db"), zap.NewNop())
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}

	m := NewBatchTaskManager(zap.NewNop())
	m.SetDB(db)
	queue, err := m.CreateBatchQueue("超龄自愈", "", "eino_single", "manual", "", "", "", nil, 1,
		[]string{"stale", "fresh"})
	if err != nil {
		t.Fatalf("CreateBatchQueue: %v", err)
	}
	m.UpdateQueueStatus(queue.ID, BatchQueueStatusRunning)

	staleTask := queue.Tasks[0]
	freshTask := queue.Tasks[1]

	// 模拟崩溃/卡死现场：两个任务均 running（内存+DB 同步置 running，
	// 再把 started_at 回填为历史时刻——真实场景由 LoadFromDB 恢复内存 running）。
	// stale：started_at 在 25h 前（阈值 12h，留足间隔防执行耗时越界）；
	// fresh：started_at 在 1h 前（远小于阈值，不会被回滚）。
	staleStarted := time.Now().Add(-25 * time.Hour)
	freshStarted := time.Now().Add(-1 * time.Hour)
	m.UpdateTaskStatusWithConversationID(queue.ID, staleTask.ID, BatchTaskStatusRunning, "", "", "")
	m.UpdateTaskStatusWithConversationID(queue.ID, freshTask.ID, BatchTaskStatusRunning, "", "", "")
	if _, err := db.Exec(
		"UPDATE batch_tasks SET started_at = ? WHERE queue_id = ? AND id = ?",
		staleStarted, queue.ID, staleTask.ID,
	); err != nil {
		t.Fatalf("backfill stale started_at: %v", err)
	}
	if _, err := db.Exec(
		"UPDATE batch_tasks SET started_at = ? WHERE queue_id = ? AND id = ?",
		freshStarted, queue.ID, freshTask.ID,
	); err != nil {
		t.Fatalf("backfill fresh started_at: %v", err)
	}
	staleTask.StartedAt = &staleStarted
	freshTask.StartedAt = &freshStarted

	rolled := m.RecoverStaleRunningTasks(staleRunningTaskMaxAge)
	if rolled != 1 {
		t.Fatalf("expected 1 rolled task, got %d", rolled)
	}

	// 超龄任务回滚 pending；未超龄任务不动
	if got := m.TaskStatus(queue.ID, staleTask.ID); got != BatchTaskStatusPending {
		t.Fatalf("stale task should be pending, got %q", got)
	}
	if got := m.TaskStatus(queue.ID, freshTask.ID); got != BatchTaskStatusRunning {
		t.Fatalf("fresh task should stay running, got %q", got)
	}

	// 无执行器的 running 队列被置回 paused，恢复到可「继续执行」的状态
	q, ok := m.GetBatchQueue(queue.ID)
	if !ok || q.Status != BatchQueueStatusPaused {
		t.Fatalf("queue should be paused after stale recovery, got %+v", q)
	}
	if q.LastRunError == "" {
		t.Fatal("queue should carry recovery hint in last_run_error")
	}

	// 重启后持久化仍生效：新 manager 加载，stale pending、fresh running、队列 paused
	m2 := NewBatchTaskManager(zap.NewNop())
	m2.SetDB(db)
	if err := m2.LoadFromDB(); err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	q2, ok := m2.GetBatchQueue(queue.ID)
	if !ok {
		t.Fatal("queue should survive restart")
	}
	if q2.Status != BatchQueueStatusPaused {
		t.Fatalf("queue should stay paused after restart, got %q", q2.Status)
	}
	for _, task := range q2.Tasks {
		switch task.ID {
		case staleTask.ID:
			if task.Status != BatchTaskStatusPending {
				t.Fatalf("stale task should be pending after restart, got %q", task.Status)
			}
		case freshTask.ID:
			if task.Status != BatchTaskStatusRunning {
				t.Fatalf("fresh task should stay running after restart, got %q", task.Status)
			}
		}
	}

	// 回滚后的任务可被重新认领：模拟用户点「继续执行」（队列回 running 后 claim）。
	// 放在重启校验之后——UpdateQueueStatus 会把 running 持久化进 DB。
	m.UpdateQueueStatus(queue.ID, BatchQueueStatusRunning)
	reclaimed, ok := m.ClaimNextPendingTask(queue.ID)
	if !ok || reclaimed == nil || reclaimed.ID != staleTask.ID {
		t.Fatalf("stale task should be reclaimable after resume, got %v %v", reclaimed, ok)
	}
}
