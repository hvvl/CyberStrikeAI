//go:build windows

package security

import "io"

// forceUnblockPipeRead Windows 下 exec 管道读端不支持 SetReadDeadline，退化为无操作
// （TerminateJobObject 已覆盖整组终止，孤儿写端场景由 drainer 兜底）。
func forceUnblockPipeRead(readers ...io.Reader) {}
