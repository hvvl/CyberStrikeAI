package multiagent

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestChannelLimiterCapsConcurrency 验证同一通道并发不超过 max_concurrency。
func TestChannelLimiterCapsConcurrency(t *testing.T) {
	resetChannelGatesForTest()
	var mu sync.Mutex
	cur, peak := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		cur++
		if cur > peak {
			peak = cur
		}
		mu.Unlock()
		time.Sleep(60 * time.Millisecond)
		mu.Lock()
		cur--
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{Transport: channelLimiterTransport("captest", 2, srv.Client().Transport)}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Get(srv.URL)
			if err != nil {
				t.Errorf("request: %v", err)
				return
			}
			resp.Body.Close() // 关闭 Body 释放信号量槽位
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if peak > 2 {
		t.Fatalf("并发峰值 %d 超过通道上限 2", peak)
	}
	if peak < 2 {
		t.Fatalf("并发峰值 %d 未达上限 2，信号量疑似未放行", peak)
	}
}

// TestChannelLimiterUnlimited max_concurrency <= 0 时原样返回 base，不做限制。
func TestChannelLimiterUnlimited(t *testing.T) {
	base := http.DefaultTransport
	if got := channelLimiterTransport("nolimit", 0, base); got != base {
		t.Fatal("max_concurrency=0 应原样返回 base transport")
	}
	if got := channelLimiterTransport("nolimit", -1, nil); got == nil {
		t.Fatal("base 为 nil 时应回退 DefaultTransport")
	}
}

// TestChannelSemaphoreRebuildsOnCapChange 后台改 max_concurrency 时 gate 整体重建。
func TestChannelSemaphoreRebuildsOnCapChange(t *testing.T) {
	resetChannelGatesForTest()
	s1 := channelSemaphoreFor("rebuild", 2)
	s2 := channelSemaphoreFor("rebuild", 2)
	if s1 != s2 {
		t.Fatal("容量不变时应复用同一 gate")
	}
	s3 := channelSemaphoreFor("rebuild", 5)
	if s3 == s1 {
		t.Fatal("容量变化后应重建 gate")
	}
	// 通道 ID 归一化：大小写不敏感
	s4 := channelSemaphoreFor("REBUILD", 5)
	if s4 != s3 {
		t.Fatal("通道 ID 应大小写归一化后共享 gate")
	}
}
