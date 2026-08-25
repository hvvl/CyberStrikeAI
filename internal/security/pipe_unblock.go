//go:build !windows

package security

import (
	"io"
	"time"
)

// forceUnblockPipeRead 强制解除阻塞在 Read 上的 exec 管道读端。
//
// 背景：kill(-pgid) 杀不掉逃逸进程组的孙进程（setsid/nohup/二次 fork，渗透场景
// 高发——被测目标拉起的后台进程）；它们继承的管道写端使 readFn 永久阻塞在 Read，
// 上游 chunks channel 永不关闭，整条 goroutine 链泄漏。
//
// 原理：os/exec 管道读端是 *os.File，已注册 runtime poller，支持 SetReadDeadline。
// 对阻塞中的 Read 设置一个已过期的 deadline，内核 poller 会唤醒阻塞的读并返回
// os.ErrDeadlineExceeded，从而让 readFn 走到 return，链路得以收口。
func forceUnblockPipeRead(readers ...io.Reader) {
	const grace = 1 * time.Millisecond
	for _, r := range readers {
		f, ok := r.(interface {
			SetReadDeadline(time.Time) error
		})
		if !ok {
			continue
		}
		// 设为过去时刻：立即唤醒阻塞读（即使 Read 当前并未阻塞也无副作用）。
		_ = f.SetReadDeadline(time.Now().Add(-grace))
	}
}
