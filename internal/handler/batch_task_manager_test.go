package handler

import (
	"errors"
	"path/filepath"
	"testing"

	"cyberstrike-ai/internal/database"

	"go.uber.org/zap"
)

func TestNormalizeBatchQueueConcurrency(t *testing.T) {
	if got := normalizeBatchQueueConcurrency(0); got != DefaultBatchQueueConcurrency {
		t.Fatalf("expected default %d, got %d", DefaultBatchQueueConcurrency, got)
	}
	if got := normalizeBatchQueueConcurrency(99); got != MaxBatchQueueConcurrency {
		t.Fatalf("expected max %d, got %d", MaxBatchQueueConcurrency, got)
	}
}

func TestClaimNextPendingTaskParallel(t *testing.T) {
	m := NewBatchTaskManager(zap.NewNop())
	queue, err := m.CreateBatchQueue("test", "", "eino_single", "manual", "", "", "", nil, 3, []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("CreateBatchQueue: %v", err)
	}
	m.UpdateQueueStatus(queue.ID, BatchQueueStatusRunning)

	t1, ok1 := m.ClaimNextPendingTask(queue.ID)
	t2, ok2 := m.ClaimNextPendingTask(queue.ID)
	if !ok1 || !ok2 || t1.ID == t2.ID {
		t.Fatalf("expected two distinct claims, got ok1=%v ok2=%v t1=%v t2=%v", ok1, ok2, t1, t2)
	}
	if t1.Status != BatchTaskStatusRunning || t2.Status != BatchTaskStatusRunning {
		t.Fatalf("claimed tasks should be running")
	}
	t3, ok3 := m.ClaimNextPendingTask(queue.ID)
	if !ok3 {
		t.Fatal("expected third claim")
	}
	_, ok4 := m.ClaimNextPendingTask(queue.ID)
	if ok4 {
		t.Fatal("expected no fourth pending task")
	}
	_ = t3
}

func TestBatchQueueExecutionShouldStop(t *testing.T) {
	t.Parallel()
	if !batchQueueExecutionShouldStop(nil, false) {
		t.Fatal("expected stop when queue missing")
	}
	if !batchQueueExecutionShouldStop(nil, true) {
		t.Fatal("expected stop when queue is nil but exists=true")
	}
	q := &BatchTaskQueue{Status: BatchQueueStatusRunning}
	if batchQueueExecutionShouldStop(q, true) {
		t.Fatal("expected continue when running")
	}
	q.Status = BatchQueueStatusCancelled
	if !batchQueueExecutionShouldStop(q, true) {
		t.Fatal("expected stop when cancelled")
	}
}

func TestBatchSubTaskConversationMetaKeepsQueueRole(t *testing.T) {
	t.Parallel()

	meta := batchSubTaskConversationMeta(nil, &BatchTaskQueue{Role: " 渗透测试 "})
	if meta.Source != "batch_task" {
		t.Fatalf("expected batch_task source, got %q", meta.Source)
	}
	if meta.RoleName != "渗透测试" {
		t.Fatalf("expected queue role to be stored on child conversation, got %q", meta.RoleName)
	}
}

func TestDeleteQueueBlockedWhileExecutorActive(t *testing.T) {
	t.Parallel()
	m := NewBatchTaskManager(zap.NewNop())
	queue, err := m.CreateBatchQueue("test", "", "eino_single", "manual", "", "", "", nil, 1, []string{"hello"})
	if err != nil {
		t.Fatalf("CreateBatchQueue: %v", err)
	}
	if !m.TryMarkQueueExecutor(queue.ID) {
		t.Fatal("expected to mark executor")
	}
	m.UpdateQueueStatus(queue.ID, BatchQueueStatusCancelled)

	err = m.DeleteQueue(queue.ID)
	if !errors.Is(err, ErrBatchQueueExecutorActive) {
		t.Fatalf("expected ErrBatchQueueExecutorActive, got %v", err)
	}
	if _, ok := m.GetBatchQueue(queue.ID); !ok {
		t.Fatal("queue should still exist while executor active")
	}

	m.UnmarkQueueExecutor(queue.ID)
	if err := m.DeleteQueue(queue.ID); err != nil {
		t.Fatalf("expected delete after executor unmarked, got %v", err)
	}
	if _, ok := m.GetBatchQueue(queue.ID); ok {
		t.Fatal("queue should be deleted")
	}
}

func TestDeleteQueueBlockedWhileRunning(t *testing.T) {
	t.Parallel()
	m := NewBatchTaskManager(zap.NewNop())
	queue, err := m.CreateBatchQueue("test", "", "eino_single", "manual", "", "", "", nil, 1, []string{"hello"})
	if err != nil {
		t.Fatalf("CreateBatchQueue: %v", err)
	}
	m.UpdateQueueStatus(queue.ID, BatchQueueStatusRunning)

	err = m.DeleteQueue(queue.ID)
	if !errors.Is(err, ErrBatchQueueStillRunning) {
		t.Fatalf("expected ErrBatchQueueStillRunning, got %v", err)
	}
}

func TestTryMarkQueueExecutorDedupes(t *testing.T) {
	t.Parallel()
	m := NewBatchTaskManager(zap.NewNop())
	if !m.TryMarkQueueExecutor("q-1") {
		t.Fatal("first mark should succeed")
	}
	if m.TryMarkQueueExecutor("q-1") {
		t.Fatal("second mark should fail")
	}
	m.UnmarkQueueExecutor("q-1")
	if !m.TryMarkQueueExecutor("q-1") {
		t.Fatal("mark after unmark should succeed")
	}
}

// TestRecoverInterruptedQueues 构造崩溃现场（running 队列 + running/pending 子任务），
// 断言 RecoverInterruptedQueues 回滚为 paused/pending，且重启 LoadFromDB 后持久化仍在。
func TestRecoverInterruptedQueues(t *testing.T) {
	t.Parallel()

	db, err := database.NewDB(filepath.Join(t.TempDir(), "recover-interrupted.db"), zap.NewNop())
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}

	m := NewBatchTaskManager(zap.NewNop())
	m.SetDB(db)
	queue, err := m.CreateBatchQueue("崩溃现场", "", "eino_single", "manual", "", "", "", nil, 1,
		[]string{"t1", "t2", "t3"})
	if err != nil {
		t.Fatalf("CreateBatchQueue: %v", err)
	}
	// 模拟崩溃瞬间的状态：队列 running，t1/t2 已认领（running），t3 待执行
	m.UpdateQueueStatus(queue.ID, BatchQueueStatusRunning)
	m.ClaimNextPendingTask(queue.ID)
	m.ClaimNextPendingTask(queue.ID)

	recovered := m.RecoverInterruptedQueues()
	if recovered != 1 {
		t.Fatalf("expected 1 recovered queue, got %d", recovered)
	}
	q, ok := m.GetBatchQueue(queue.ID)
	if !ok || q.Status != BatchQueueStatusPaused {
		t.Fatalf("queue should be paused after recovery, got %+v", q)
	}
	for _, task := range q.Tasks {
		if task.Status != BatchTaskStatusPending {
			t.Fatalf("task %s should be pending after recovery, got %s", task.ID, task.Status)
		}
	}

	// 模拟重启：新建 manager 从 DB 加载，验证持久化生效（队列 paused、任务 pending）
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
		t.Fatalf("queue should stay paused after restart, got %s", q2.Status)
	}
	for _, task := range q2.Tasks {
		if task.Status != BatchTaskStatusPending {
			t.Fatalf("task %s should stay pending after restart, got %s", task.ID, task.Status)
		}
	}
}
