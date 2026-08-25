//go:build !windows

package security

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
)

// TestEinoStreamingShell_OrphanPipeHolderUnblocksOnCancel 复现内存泄漏修复 C 的场景：
// `setsid -f sleep 60` 会二次 fork 出逃逸进程组的孙进程，它继承 shell 的 stdout
// 管道写端。cancel 后 kill(-pgid) 杀不到它，旧实现里 readFn 永久阻塞在 Read →
// chunks 永不关闭 → ExecuteStreaming 返回的流永不收口（每条卡死 execute 泄漏 4 goroutine）。
// 修复后 terminateShellCmdSessionAndPipes 用过期 ReadDeadline 强制唤醒阻塞读，
// 流必须在 5s 内收口（修复前会阻塞到 60s 孤儿进程自然退出）。
func TestEinoStreamingShell_OrphanPipeHolderUnblocksOnCancel(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid not available")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	shell := NewEinoStreamingShell()
	sr, err := shell.ExecuteStreaming(ctx, &filesystem.ExecuteRequest{
		// setsid -f：孙进程脱离进程组且不退出，持有 stdout 写端
		Command: "echo start; setsid -f sleep 60; echo done",
	})
	if err != nil {
		t.Fatalf("ExecuteStreaming: %v", err)
	}

	// 读取首个 chunk 确认流已启动（echo start）
	firstDeadline := time.Now().Add(5 * time.Second)
	gotFirst := false
	for !gotFirst {
		if time.Now().After(firstDeadline) {
			t.Fatal("no first chunk within 5s")
		}
		resp, err := sr.Recv()
		if err != nil {
			t.Fatalf("recv first chunk: %v", err)
		}
		if resp != nil && strings.Contains(resp.Output, "start") {
			gotFirst = true
		}
	}

	// cancel：触发 stopWatch 的 terminateShellCmdSessionAndPipes
	cancel()

	// 流必须在 5s 内收口（Recv 返回 EOF/error），否则视为泄漏
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			if _, err := sr.Recv(); err != nil {
				return
			}
		}
	}()
	select {
	case <-closed:
		// ok：流已收口
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not close within 5s after cancel — orphan pipe holder still blocking readFn (memory leak regression)")
	}
}

// TestForceUnblockPipeReadDeadlinesOsFile 单测唤醒机制本身：
// 对 os.Pipe 读端设置过期 deadline 后，阻塞中的 Read 必须立刻返回错误。
func TestForceUnblockPipeReadDeadlinesOsFile(t *testing.T) {
	r, w, err := osPipe()
	if err != nil {
		t.Fatalf("osPipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	readReturned := make(chan error, 1)
	go func() {
		buf := make([]byte, 16)
		_, err := r.Read(buf)
		readReturned <- err
	}()

	// 等 Read 真正阻塞
	time.Sleep(100 * time.Millisecond)
	forceUnblockPipeRead(r)

	select {
	case err := <-readReturned:
		if err == nil {
			t.Fatal("expected error from unblocked read, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Read still blocked after forceUnblockPipeRead")
	}
}
