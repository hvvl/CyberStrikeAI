package handler

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"cyberstrike-ai/internal/database"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// TestResumeInterruptedEndpoint 一键续跑端点 HTTP 级回归（内存修复 H3 链路）：
//   - 超龄僵尸 running 队列被回滚 paused 后由本端点自动纳入启动（不用人工干预）；
//   - 等待 Cron 定时触发的 pending 队列不提前启动；
//   - completed 终态队列不受影响。
// 审计结论「resume-interrupted 端点无测试覆盖」以此补齐。
func TestResumeInterruptedEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db, err := database.NewDB(filepath.Join(t.TempDir(), "resume-endpoint.db"), zap.NewNop())
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}

	m := NewBatchTaskManager(zap.NewNop())
	m.SetDB(db)

	// 队列 1：伪僵尸现场（running、started_at 在 25h 前，阈值 12h）
	qStale, err := m.CreateBatchQueue("僵尸队列", "", "eino_single", "manual", "", "", "", nil, 1,
		[]string{"t-stale"})
	if err != nil {
		t.Fatalf("CreateBatchQueue: %v", err)
	}
	m.UpdateQueueStatus(qStale.ID, BatchQueueStatusRunning)
	staleTask := qStale.Tasks[0]
	m.UpdateTaskStatusWithConversationID(qStale.ID, staleTask.ID, BatchTaskStatusRunning, "", "", "")
	staleStarted := time.Now().Add(-25 * time.Hour)
	if _, err := db.Exec(
		"UPDATE batch_tasks SET started_at = ? WHERE queue_id = ? AND id = ?",
		staleStarted, qStale.ID, staleTask.ID,
	); err != nil {
		t.Fatalf("backfill stale started_at: %v", err)
	}
	staleTask.StartedAt = &staleStarted

	// 队列 2：等待 Cron 定时触发的 pending 队列
	qCron, err := m.CreateBatchQueue("定时队列", "", "eino_single", "cron", "", "", "", nil, 1,
		[]string{"t-cron"})
	if err != nil {
		t.Fatalf("CreateBatchQueue(cron): %v", err)
	}
	// nextRunAt 传未来时刻：等待 Cron 定时触发的 pending 队列
	soon := time.Now().Add(5 * time.Minute)
	m.UpdateQueueSchedule(qCron.ID, "cron", "*/5 * * * *", &soon)
	if !m.SetScheduleEnabled(qCron.ID, true) {
		t.Fatal("SetScheduleEnabled should succeed")
	}

	// 队列 3：completed 终态
	qDone, err := m.CreateBatchQueue("完成队列", "", "eino_single", "manual", "", "", "", nil, 1,
		[]string{"t-done"})
	if err != nil {
		t.Fatalf("CreateBatchQueue(done): %v", err)
	}
	m.UpdateQueueStatus(qDone.ID, BatchQueueStatusCompleted)

	h := &AgentHandler{
		logger:           zap.NewNop(),
		db:               db,
		batchTaskManager: m,
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/batch-tasks/resume-interrupted", nil)
	h.ResumeInterruptedBatchQueues(c)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Started int `json:"started"`
		Skipped int `json:"skipped"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// 断言 1：僵尸队列被端点纳入启动（Started 计数为直接证据）。
	// 启动后 executor goroutine 与断言赛跑（无 RBAC owner → 子任务走"队列所有者不存在"
	// 失败支路 → tryFinalizeBatchQueue 收尾），故此处轮询到静默态再断言终值，
	// 避免与 TestTaskStatusHelper 同款的偶发竞态。
	if resp.Started < 1 {
		t.Fatalf("stale queue should be resume-started, got %+v", resp)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		q1, ok := m.GetBatchQueue(qStale.ID)
		if !ok {
			t.Fatal("stale queue missing after resume")
		}
		if q1.Status != BatchQueueStatusRunning {
			if q1.Status != BatchQueueStatusCompleted {
				t.Fatalf("stale queue should settle completed, got %q", q1.Status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stale queue executor did not settle in 5s, still running: %+v", q1)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 断言 2：cron pending 不被提前触发（计入 skipped）
	q2, ok := m.GetBatchQueue(qCron.ID)
	if !ok {
		t.Fatal("cron queue missing")
	}
	if q2.Status == BatchQueueStatusRunning {
		t.Fatalf("cron pending queue must not be started early, got %+v", q2.Status)
	}

	// 断言 3：completed 终态不动
	q3, ok := m.GetBatchQueue(qDone.ID)
	if !ok || q3.Status != BatchQueueStatusCompleted {
		t.Fatalf("completed queue must stay completed, got %+v", q3)
	}
}
