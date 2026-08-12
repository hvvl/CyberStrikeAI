package multiagent

// 通道并发限流：为适配各家 LLM 套餐（token plan / agent plan）的并发额度，
// 每个 AI 通道可配置 max_concurrency。限流器挂在 HTTP Transport 层：
//   - 信号量在 RoundTrip 前获取，在响应 Body 关闭时释放；
//   - SSE 流式响应在整个流存续期间持有槽位，天然覆盖流式场景；
//   - 对 Eino / claude bridge / summarization 诊断层完全透明。
//
// 信号量按通道 ID 全局注册：指向同一通道的多个 http.Client（主代理、
// 各子代理、summarization）共享同一个 gate，保证并发额度按通道整体生效。

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/sync/semaphore"
)

// channelGate 一个通道的并发闸门。cap 变化（后台改配置）时整体重建，
// 旧闸门上在途请求正常释放、自然退场。
type channelGate struct {
	cap int64
	sem *semaphore.Weighted
}

var (
	channelGatesMu sync.Mutex
	channelGates   = make(map[string]*channelGate)
)

// channelSemaphoreFor 返回通道对应的信号量；容量与注册值不一致时重建。
func channelSemaphoreFor(channelID string, maxConcurrency int64) *semaphore.Weighted {
	key := strings.ToLower(strings.TrimSpace(channelID))
	if key == "" {
		key = "default"
	}
	channelGatesMu.Lock()
	defer channelGatesMu.Unlock()
	g, ok := channelGates[key]
	if !ok || g.cap != maxConcurrency {
		g = &channelGate{cap: maxConcurrency, sem: semaphore.NewWeighted(maxConcurrency)}
		channelGates[key] = g
	}
	return g.sem
}

// resetChannelGatesForTest 仅供单元测试清理全局注册表。
func resetChannelGatesForTest() {
	channelGatesMu.Lock()
	defer channelGatesMu.Unlock()
	channelGates = make(map[string]*channelGate)
}

// channelLimiterTransport 用并发信号量包装 base。maxConcurrency <= 0 时原样返回（不限制）。
func channelLimiterTransport(channelID string, maxConcurrency int, base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if maxConcurrency <= 0 {
		return base
	}
	return &limitedRoundTripper{
		channelID:      channelID,
		maxConcurrency: int64(maxConcurrency),
		base:           base,
	}
}

type limitedRoundTripper struct {
	channelID      string
	maxConcurrency int64
	base           http.RoundTripper
}

func (rt *limitedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	sem := channelSemaphoreFor(rt.channelID, rt.maxConcurrency)
	ctx := req.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if err := sem.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	resp, err := rt.base.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		sem.Release(1)
		return resp, err
	}
	resp.Body = &releasingReadCloser{ReadCloser: resp.Body, sem: sem}
	return resp, nil
}

// releasingReadCloser 在 Body 关闭时恰好释放一次信号量槽位。
type releasingReadCloser struct {
	io.ReadCloser
	sem  *semaphore.Weighted
	once sync.Once
}

func (b *releasingReadCloser) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() { b.sem.Release(1) })
	return err
}
