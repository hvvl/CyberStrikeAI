package multiagent

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

// TestRecvEinoSchemaMessageStreamPumpExitsOnCtxCancel 验证内存修复 F：
// 消费方因 ctx 取消提前返回后，泵 goroutine 不能阻塞在向满缓冲 recvCh 的
// 裸发送上——否则永久持有 stream 及上游链（pprof 实测残留泵持有 118MB peek）。
func TestRecvEinoSchemaMessageStreamPumpExitsOnCtxCancel(t *testing.T) {
	// Pipe 容量 1 + 泵 buffer 8：上游持续推送,泵很快阻塞在 recvCh 发送上。
	sr, sw := schema.Pipe[*schema.Message](1)
	go func() {
		defer func() { _ = recover() }() // sw.Close 后 Send panic 由 recover 吞掉
		for {
			if closed := sw.Send(&schema.Message{Role: schema.Assistant, Content: "x"}, nil); closed {
				return
			}
		}
	}()

	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	consumed := make(chan struct{})
	go func() {
		defer close(consumed)
		n := 0
		_ = recvEinoSchemaMessageStreamWithContext(ctx, sr, 8, func(*schema.Message) {
			n++
			if n >= 2 {
				cancel() // 消费方随即经 select ctx.Done 逃逸返回
			}
		})
	}()

	select {
	case <-consumed:
	case <-time.After(5 * time.Second):
		t.Fatal("consumer did not return after cancel")
	}

	// 断言泵与 sender 在宽限期内退出（旧实现泵阻塞在满 recvCh 裸发送,永不退出）。
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > before+1 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before+1 {
		t.Fatalf("goroutines leaked: before=%d now=%d (pump stuck on full recvCh?)", before, n)
	}
	sr.Close()
}

// TestCloneAnyMapSkipsEinoInternalKeys 验证内存修复 G：
// `_eino` 前缀的库内部元数据 key 不拷贝（纯元数据 map → nil），
// 业务自定义 key 照常拷贝。
func TestCloneAnyMapSkipsEinoInternalKeys(t *testing.T) {
	// 空 map → nil
	if got := cloneAnyMap(nil); got != nil {
		t.Fatalf("nil in → nil out, got %v", got)
	}
	// 纯库内部 key → nil（不再逐 chunk 拷贝元数据,砍掉 ~800MB 级拷贝）
	onlyInternal := map[string]any{
		"_eino_ext_agenticopenai_chat_response_meta_ext": map[string]any{"model": "gpt"},
	}
	if got := cloneAnyMap(onlyInternal); got != nil {
		t.Fatalf("internal-only map should be nil, got %v", got)
	}
	// 业务 key 照常拷贝
	business := map[string]any{
		"cyberstrike_model_facing_trace_version":         "1",
		"_eino_ext_agenticopenai_chat_response_meta_ext": map[string]any{"model": "gpt"},
	}
	got := cloneAnyMap(business)
	if len(got) != 1 {
		t.Fatalf("business keys only, got %v", got)
	}
	if v, ok := got["cyberstrike_model_facing_trace_version"].(string); !ok || v != "1" {
		t.Fatalf("business key value wrong: %v", got)
	}
}
