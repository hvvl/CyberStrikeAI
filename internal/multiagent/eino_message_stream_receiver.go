package multiagent

import (
	"context"
	"errors"
	"io"

	"github.com/cloudwego/eino/schema"
)

// recvEinoSchemaMessageStreamWithContext consumes an Eino schema.Message stream
// and stops promptly when ctx is canceled. EOF and nil chunks are treated as a
// normal stream boundary.
func recvEinoSchemaMessageStreamWithContext(
	ctx context.Context,
	stream *schema.StreamReader[*schema.Message],
	buffer int,
	onChunk func(*schema.Message),
) error {
	if stream == nil {
		return nil
	}
	if buffer <= 0 {
		buffer = 1
	}
	type streamMsg struct {
		chunk *schema.Message
		err   error
	}
	recvCh := make(chan streamMsg, buffer)
	go func() {
		defer close(recvCh)
		for {
			ch, rerr := stream.Recv()
			// 发送必须带 ctx 逃生：消费方因 ctx 取消提前返回后，泵阻塞在向满缓冲
			// recvCh 的裸发送上，永久持有 stream 及其上游链（pprof 实测：1 个残留泵
			// 持有 118MB peek 缓冲）。内存修复 F。
			select {
			case recvCh <- streamMsg{chunk: ch, err: rerr}:
			case <-ctx.Done():
				return
			}
			if rerr != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case sm, ok := <-recvCh:
			if !ok {
				return nil
			}
			if errors.Is(sm.err, io.EOF) {
				return nil
			}
			if sm.err != nil {
				return sm.err
			}
			if sm.chunk == nil || onChunk == nil {
				continue
			}
			onChunk(sm.chunk)
		}
	}
}
