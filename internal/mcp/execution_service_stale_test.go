package mcp

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestExecutionServiceEvictsStaleNonTerminalEntries 验证内存泄漏修复 D：
// worker 阻塞在不受 ctx 控制 I/O 时 entry 永远 non-terminal，旧行为下
// cleanupOldEntriesLocked 只淘汰 terminal entry，僵死 entry 无上界累积。
// 修复后超龄（>24h）非终态 entry 在 cleanup 时被发取消信号并逐出。
func TestExecutionServiceEvictsStaleNonTerminalEntries(t *testing.T) {
	s := NewExecutionService(nil, zap.NewNop())

	// 构造一个永远阻塞的 run（不响应 ctx 取消）
	blockForever := make(chan struct{})
	defer close(blockForever)
	handle, err := s.Submit(context.Background(), ExecutionRequest{
		ID:       "stale-1",
		ToolName: "stuck-tool",
		Run: func(ctx context.Context) (*ToolResult, error) {
			<-blockForever
			return &ToolResult{Content: []Content{{Type: "text", Text: "never"}}}, nil
		},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if handle.ID != "stale-1" {
		t.Fatalf("unexpected handle id %q", handle.ID)
	}

	// 正常新鲜 entry：不应被淘汰
	if _, err := s.Submit(context.Background(), ExecutionRequest{
		ID:       "fresh-1",
		ToolName: "fresh-tool",
		Run: func(ctx context.Context) (*ToolResult, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}); err != nil {
		t.Fatalf("Submit fresh: %v", err)
	}

	// 把 stale-1 拨回 25h 前，模拟僵死超龄
	s.mu.Lock()
	if entry, ok := s.entries["stale-1"]; ok {
		entry.exec.StartTime = time.Now().Add(-25 * time.Hour)
	} else {
		s.mu.Unlock()
		t.Fatal("stale-1 entry missing before cleanup")
	}
	// 触发 cleanup（不依赖 maxInMemory 淘汰路径）
	s.cleanupOldEntriesLocked()
	_, staleExists := s.entries["stale-1"]
	_, freshExists := s.entries["fresh-1"]
	s.mu.Unlock()

	if staleExists {
		t.Fatal("stale non-terminal entry should be evicted after 24h")
	}
	if !freshExists {
		t.Fatal("fresh entry must not be evicted")
	}
}
