package database

import (
	"errors"
	"testing"

	"go.uber.org/zap"
)

// TestAgentTraceRunGenerationStaleWritesRejected 核心竞态回归：
// 用户停止任务 → 删除轮次 → 滞留的 Run 协程退场时回写轨迹。
// 删除轮次在事务内换代 trace_run_id，持有旧 runID 的写回必须被拒绝，
// 否则已删除的 prompt 会通过 last_react_input 复活。
func TestAgentTraceRunGenerationStaleWritesRejected(t *testing.T) {
	db, err := NewDB(t.TempDir()+"/trace-run.db", zap.NewNop())
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	conv, err := db.CreateConversation("race", ConversationCreateMeta{})
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	// 1. 运行开始：声明本次轨迹代次
	runID, err := db.ClaimAgentTraceRun(conv.ID)
	if err != nil {
		t.Fatalf("ClaimAgentTraceRun: %v", err)
	}
	if runID == "" {
		t.Fatal("ClaimAgentTraceRun returned empty runID")
	}

	// 2. 正常运行中的轨迹写入应成功
	if err := db.SaveAgentTraceForRun(conv.ID, runID, `[{"role":"user","content":"旧prompt"}]`, "ok"); err != nil {
		t.Fatalf("SaveAgentTraceForRun during run: %v", err)
	}
	gotIn, gotOut, err := db.GetAgentTrace(conv.ID)
	if err != nil || gotIn == "" || gotOut != "ok" {
		t.Fatalf("trace after in-run save: in=%q out=%q err=%v", gotIn, gotOut, err)
	}

	// 3. 用户停止任务并删除轮次：事务内删除消息 + 清轨迹 + 换代
	if _, err := db.AddMessage(conv.ID, "user", "旧prompt", nil); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	msgs, err := db.GetMessages(conv.ID)
	if err != nil || len(msgs) == 0 {
		t.Fatalf("GetMessages: %v (n=%d)", err, len(msgs))
	}
	if _, err := db.DeleteConversationTurn(conv.ID, msgs[0].ID); err != nil {
		t.Fatalf("DeleteConversationTurn: %v", err)
	}
	gotIn, _, err = db.GetAgentTrace(conv.ID)
	if err != nil || gotIn != "" {
		t.Fatalf("trace must be cleared after delete-turn: in=%q err=%v", gotIn, err)
	}

	// 4. 滞留协程（旧 runID）迟到写回：必须被拒绝（ErrTraceRunStale）
	err = db.SaveAgentTraceForRun(conv.ID, runID, `[{"role":"user","content":"旧prompt"}]`, "stale write")
	if !errors.Is(err, ErrTraceRunStale) {
		t.Fatalf("stale write must be rejected with ErrTraceRunStale, got: %v", err)
	}
	gotIn, gotOut, err = db.GetAgentTrace(conv.ID)
	if err != nil || gotIn != "" || gotOut != "" {
		t.Fatalf("trace must stay cleared after stale write: in=%q out=%q err=%v", gotIn, gotOut, err)
	}

	// 5. 新运行声明新代次后可正常写入（不能被旧墓碑卡死）
	newRunID, err := db.ClaimAgentTraceRun(conv.ID)
	if err != nil {
		t.Fatalf("ClaimAgentTraceRun (new run): %v", err)
	}
	if newRunID == runID {
		t.Fatal("new run must claim a different runID")
	}
	if err := db.SaveAgentTraceForRun(conv.ID, newRunID, `[{"role":"user","content":"新prompt"}]`, "new ok"); err != nil {
		t.Fatalf("SaveAgentTraceForRun with new runID: %v", err)
	}
	gotIn, gotOut, err = db.GetAgentTrace(conv.ID)
	if err != nil || gotOut != "new ok" {
		t.Fatalf("trace after new-run save: in=%q out=%q err=%v", gotIn, gotOut, err)
	}

	// 6. 旧 runID 在新运行接管后写回仍被拒绝（僵尸任务迟到写不污染新上下文）
	if err := db.SaveAgentTraceForRun(conv.ID, runID, `[{"role":"user","content":"旧prompt"}]`, "zombie write"); !errors.Is(err, ErrTraceRunStale) {
		t.Fatalf("zombie write after new run must be rejected, got: %v", err)
	}
}

// TestAgentTraceRunUnconditionalWriteCompat 兼容性：runID 为空退化为旧行为（无条件写）。
func TestAgentTraceRunUnconditionalWriteCompat(t *testing.T) {
	db, err := NewDB(t.TempDir()+"/trace-run-compat.db", zap.NewNop())
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	conv, err := db.CreateConversation("compat", ConversationCreateMeta{})
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if _, err := db.ClaimAgentTraceRun(conv.ID); err != nil {
		t.Fatalf("ClaimAgentTraceRun: %v", err)
	}
	// 旧调用方（未参与代次机制）无条件写必须成功
	if err := db.SaveAgentTrace(conv.ID, `[{"role":"user"}]`, "legacy"); err != nil {
		t.Fatalf("legacy SaveAgentTrace must succeed: %v", err)
	}
	if _, _, err := db.GetAgentTrace(conv.ID); err != nil {
		t.Fatalf("GetAgentTrace: %v", err)
	}
}
