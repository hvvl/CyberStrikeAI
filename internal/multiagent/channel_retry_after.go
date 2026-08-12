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
	"net/http"
	"strconv"
	"sync"
	"time"
)

// retryAfterMaxCooldown 截断服务端给出的超长 Retry-After，防止病态值
// （如代理错误返回几天后的时间）导致通道长时间不可用。
const retryAfterMaxCooldown = 120 * time.Second

// channelCooldownRegistry 按通道 ID 记录「最早可再发请求」的时刻。
// 进程级单例：同一通道的主代理/子代理/summarization/不同会话共享。
type channelCooldownRegistry struct {
	mu    sync.Mutex
	until map[string]time.Time
}

var channelCooldowns = &channelCooldownRegistry{until: make(map[string]time.Time)}

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
	if err == nil && resp != nil && t.channelID != "" {
		t.observeRateLimitResponse(resp)
	}
	return resp, err
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
