package app

import (
	"context"
	"net"
	"net/http"
	"net/http/pprof"
	"time"

	"go.uber.org/zap"
)

// startPprofServer 在独立 listener 上挂标准库 pprof 五个 handler（/debug/pprof/、
// /debug/pprof/cmdline、/debug/pprof/profile、/debug/pprof/symbol、/debug/pprof/trace）。
// 监听失败仅告警不阻断主服务；返回的 stop 随应用优雅关闭。
//
// 注意：无鉴权，配置时务必仅绑定回环地址（127.0.0.1），不要暴露到外部网络。
func startPprofServer(addr string, logger *zap.Logger) (stop func()) {
	if addr == "" || logger == nil {
		return func() {}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Warn("pprof 诊断监听启动失败（不影响主服务）", zap.String("addr", addr), zap.Error(err))
		return func() {}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Warn("pprof 诊断服务退出", zap.String("addr", addr), zap.Error(err))
		}
	}()
	logger.Info("pprof 诊断监听已启动", zap.String("addr", addr), zap.String("path", "/debug/pprof/"))

	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Warn("pprof 诊断服务关闭失败", zap.Error(err))
		}
	}
}
