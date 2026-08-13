package multiagent

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// 审计 P1-3/P1-4 回归测试：
// 1) 并发会话互不串扰（run 级 collector 隔离）；
// 2) HTTP 5xx 归属本 run 的失败通道；
// 3) 网络类临时错误（connection refused / dial tcp 等）同样登记失败通道；
// 4) 用户取消不登记；ctx 无 collector 时退回进程级 map 兜底。

type fakeRoundTripper struct {
	fn func(*http.Request) (*http.Response, error)
}

func (f fakeRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f.fn(r)
}

func TestFailedChannelCollectorPerRunIsolation(t *testing.T) {
	ctxA, cA := withFailedChannelCollector(context.Background())
	ctxB, cB := withFailedChannelCollector(context.Background())
	// 会话 A 的 cheap 429 与 会话 B 的 strong 500 并发发生，互不覆盖。
	noteFailedChannelCtx(ctxA, "cheap")
	noteFailedChannelCtx(ctxB, "strong")
	if got := cA.take(); got != "cheap" {
		t.Fatalf("collector A must attribute cheap, got %q", got)
	}
	if got := cB.take(); got != "strong" {
		t.Fatalf("collector B must attribute strong, got %q", got)
	}
	if got := failedChannelCollectorFromContext(context.Background()); got != nil {
		t.Fatal("bare context should carry no collector")
	}
}

func TestFailedChannelCollectorLastWriteWins(t *testing.T) {
	ctx, c := withFailedChannelCollector(context.Background())
	noteFailedChannelCtx(ctx, "cheap")
	noteFailedChannelCtx(ctx, "cheap")
	noteFailedChannelCtx(ctx, "cheap")
	if got := c.take(); got != "cheap" {
		t.Fatalf("expected cheap, got %q", got)
	}
}

func TestRetryAfterTransportRecordsHTTPFailureToCollector(t *testing.T) {
	ctx, collector := withFailedChannelCollector(context.Background())
	tr := &retryAfterTransport{channelID: "cheap", next: fakeRoundTripper{fn: func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError}, nil
	}}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://example.invalid/v1/chat", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got := collector.take(); got != "cheap" {
		t.Fatalf("500 must be attributed to cheap, got %q", got)
	}
}

func TestRetryAfterTransportRecordsRateLimitToCollector(t *testing.T) {
	ctx, collector := withFailedChannelCollector(context.Background())
	// 独立通道 ID：避免 Retry-After cooldown 串到同包其他测试。
	tr := &retryAfterTransport{channelID: "cheap-429", next: fakeRoundTripper{fn: func(_ *http.Request) (*http.Response, error) {
		h := http.Header{}
		h.Set("Retry-After", "1")
		return &http.Response{StatusCode: http.StatusTooManyRequests, Header: h}, nil
	}}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://example.invalid/v1/chat", nil)
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got := collector.take(); got != "cheap-429" {
		t.Fatalf("429 must be attributed to cheap-429, got %q", got)
	}
}

func TestRetryAfterTransportRecordsNetworkErrorToCollector(t *testing.T) {
	ctx, collector := withFailedChannelCollector(context.Background())
	tr := &retryAfterTransport{channelID: "cheap", next: fakeRoundTripper{fn: func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("Post \"https://api.example.com/v1/chat\": dial tcp 10.0.0.1:443: connect: connection refused")
	}}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://example.invalid/v1/chat", nil)
	_, _ = tr.RoundTrip(req)
	if got := collector.take(); got != "cheap" {
		t.Fatalf("network error must be attributed to cheap, got %q", got)
	}
}

func TestRetryAfterTransportIgnoresContextCanceled(t *testing.T) {
	ctx, collector := withFailedChannelCollector(context.Background())
	tr := &retryAfterTransport{channelID: "cheap", next: fakeRoundTripper{fn: func(_ *http.Request) (*http.Response, error) {
		return nil, context.Canceled
	}}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://example.invalid/v1/chat", nil)
	_, _ = tr.RoundTrip(req)
	if got := collector.take(); got != "" {
		t.Fatalf("context.Canceled must not be recorded as channel failure, got %q", got)
	}
}

func TestNoteFailedChannelCtxFallsBackToGlobalMap(t *testing.T) {
	// ctx 未携带 collector 的路径（如早期直连调用）退回进程级 map。
	noteFailedChannelCtx(context.Background(), "strong")
	defer channelFailedAt.Delete("strong") // 清理：不得串到 channel_routing_test 的「空表」断言
	if got := takeRecentFailedChannel(); got != "strong" {
		t.Fatalf("global fallback must record strong, got %q", got)
	}
}
