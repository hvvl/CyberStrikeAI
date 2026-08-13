package multiagent

// Retry-After 支持：服务端限流响应（429/503/529）常带 Retry-After 头
// （秒数或 HTTP 日期），指示客户端最早何时可再请求。Eino 的 APIError 不携带
// 响应头，因此必须在 HTTP Transport 层截获：
//
//   - 收到限流响应 → 按通道注册 cooldown（所有指向同一通道的请求共享，
//     避免多会话同时撞限流墙各自烧掉退避次数）；
//   - 发出请求前 → 若通道仍在 cooldown，阻塞等待（尊重 ctx 取消）。
//
// cooldown transport 包在并发限流器外层：等待期间不占用通道并发额度。

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// retryAfterMaxCooldown 截断服务端给出的超长 Retry-After，防止病态值
// （如代理错误返回几天后的时间）导致通道长时间不可用。
const retryAfterMaxCooldown = 120 * time.Second

// failedChannelCollector 一次 run 内的失败通道收集器（请求级，挂在 ctx 上）。
// 相比进程级「最近 5 秒失败」启发式，它天然按 run 隔离：并发会话 A/B 各自
// 携带自己的 collector，A 的 429 不会被 B 的 500 顶掉（审计 P1-3）。
type failedChannelCollector struct {
	mu        sync.Mutex
	channelID string
	at        time.Time
}

func (c *failedChannelCollector) note(channelID string) {
	if c == nil || channelID == "" {
		return
	}
	c.mu.Lock()
	c.channelID = channelID
	c.at = time.Now()
	c.mu.Unlock()
}

// take 返回本 run 内最近登记的失败通道 ID（无则空）。
func (c *failedChannelCollector) take() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.channelID
}

type failedChannelCollectorKey struct{}

// withFailedChannelCollector 把新的 collector 挂到 ctx（派生 ctx 天然继承）。
func withFailedChannelCollector(ctx context.Context) (context.Context, *failedChannelCollector) {
	c := &failedChannelCollector{}
	return context.WithValue(ctx, failedChannelCollectorKey{}, c), c
}

func failedChannelCollectorFromContext(ctx context.Context) *failedChannelCollector {
	if ctx == nil {
		return nil
	}
	if c, ok := ctx.Value(failedChannelCollectorKey{}).(*failedChannelCollector); ok {
		return c
	}
	return nil
}

// channelCooldownRegistry 按通道 ID 记录「最早可再发请求」的时刻。
// 进程级单例：同一通道的主代理/子代理/summarization/不同会话共享。
type channelCooldownRegistry struct {
	mu    sync.Mutex
	until map[string]time.Time
}

var channelCooldowns = &channelCooldownRegistry{until: make(map[string]time.Time)}

// channelFailedAt 记录每个通道最近一次「临时失败」的时间戳（进程级兜底，仅在
// ctx 未携带 collector 的路径使用）。主归属路径是 run 级 collector。
var channelFailedAt sync.Map // channelID -> time.Time

// takeRecentFailedChannel 返回 5s 窗口内最近失败的通道 ID（无则空）。仅作兜底。
func takeRecentFailedChannel() string {
	deadline := time.Now().Add(-5 * time.Second)
	var bestID string
	var bestTime time.Time
	channelFailedAt.Range(func(key, value interface{}) bool {
		id, ok := key.(string)
		if !ok {
			return true
		}
		t, ok := value.(time.Time)
		if !ok || t.Before(deadline) {
			return true
		}
		if bestID == "" || t.After(bestTime) {
			bestID = id
			bestTime = t
		}
		return true
	})
	return bestID
}

// set 注册通道 cooldown；nowOrAfter 早于现值时不覆盖（多请求并发 429 时取最晚）。
func (r *channelCooldownRegistry) set(channelID string, until time.Time) {
	if channelID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.until[channelID]; ok && !until.After(cur) {
		return
	}
	r.until[channelID] = until
}

// waitUntil 返回通道的 cooldown 截止时刻（无 cooldown 时 ok=false）。
func (r *channelCooldownRegistry) waitUntil(channelID string) (time.Time, bool) {
	if channelID == "" {
		return time.Time{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.until[channelID]
	if ok && !time.Now().Before(t) {
		delete(r.until, channelID)
		return time.Time{}, false
	}
	return t, ok
}

// parseRetryAfterHeader 解析 Retry-After 头（delta-seconds 或 HTTP-date），
// 返回应等待的时长；无效或超过上限时返回 0。
func parseRetryAfterHeader(v string) time.Duration {
	v = trimHTTPHeaderValue(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		d := time.Duration(secs) * time.Second
		if d > retryAfterMaxCooldown {
			return retryAfterMaxCooldown
		}
		return d
	}
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d <= 0 {
			return 0
		}
		if d > retryAfterMaxCooldown {
			return retryAfterMaxCooldown
		}
		return d
	}
	return 0
}

func trimHTTPHeaderValue(v string) string {
	start := 0
	end := len(v)
	for start < end && (v[start] == ' ' || v[start] == '\t') {
		start++
	}
	for end > start && (v[end-1] == ' ' || v[end-1] == '\t') {
		end--
	}
	return v[start:end]
}

// retryAfterTransport 在发出请求前等待通道 cooldown，并截获限流响应的
// Retry-After 头注册新的 cooldown。channelID 为空时直通。
type retryAfterTransport struct {
	channelID string
	next      http.RoundTripper
}

func (t *retryAfterTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.channelID != "" {
		if err := t.waitCooldown(req.Context()); err != nil {
			return nil, err
		}
	}
	resp, err := t.next.RoundTrip(req)
	if t.channelID != "" {
		if err == nil && resp != nil {
			// 限流响应：按 Retry-After 注册 cooldown；同时记录为失败通道供 failover 归属判定。
			t.observeRateLimitResponse(resp)
			switch resp.StatusCode {
			case http.StatusTooManyRequests, http.StatusServiceUnavailable, 529:
				noteFailedChannelCtx(req.Context(), t.channelID)
			default:
				if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
					noteFailedChannelCtx(req.Context(), t.channelID)
				}
			}
		} else if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			// 网络类临时错误（connection reset/refused、dial tcp、TLS 超时、EOF、DNS 等）
			// 同样登记失败通道：run loop 把它们当作可重试，重试耗尽时必须能归到
			// 实际出错的角色通道，否则 FailedChannelID 为空会错退回会话通道。
			noteFailedChannelCtx(req.Context(), t.channelID)
		}
	}
	return resp, err
}

// noteFailedChannelCtx 登记通道失败：优先写入 run 级 collector（按 run 隔离），
// ctx 无 collector 时退回进程级 map（5s 窗口兜底）。
func noteFailedChannelCtx(ctx context.Context, channelID string) {
	if channelID == "" {
		return
	}
	if c := failedChannelCollectorFromContext(ctx); c != nil {
		c.note(channelID)
		return
	}
	noteFailedChannel(channelID)
}

// noteFailedChannel 登记通道的最近一次临时失败（进程级兜底路径）。
func noteFailedChannel(channelID string) {
	if channelID == "" {
		return
	}
	channelFailedAt.Store(channelID, time.Now())
}

// waitCooldown 阻塞至通道 cooldown 结束；ctx 取消立即返回。
func (t *retryAfterTransport) waitCooldown(ctx context.Context) error {
	for {
		until, ok := channelCooldowns.waitUntil(t.channelID)
		if !ok {
			return nil
		}
		wait := time.Until(until)
		if wait <= 0 {
			continue
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			// 未消费完的 cooldown 保留给其它请求，重新注册（取剩余部分）。
			channelCooldowns.set(t.channelID, until)
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// observeRateLimitResponse 从限流响应读取 Retry-After 并注册通道 cooldown。
// 429（限流）/503（过载）/529（Anthropic overloaded）生效；无头时不注册
// （退避交给 run loop 层）。
func (t *retryAfterTransport) observeRateLimitResponse(resp *http.Response) {
	if resp == nil {
		return
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusServiceUnavailable, 529:
	default:
		return
	}
	d := parseRetryAfterHeader(resp.Header.Get("Retry-After"))
	if d <= 0 {
		return
	}
	channelCooldowns.set(t.channelID, time.Now().Add(d))
}
