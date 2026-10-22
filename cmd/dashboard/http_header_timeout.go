package main

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"
)

const dashboardReadHeaderTimeout = 5 * time.Second

func newDashboardHTTPServer(handler http.Handler) *http.Server {
	// ReadHeaderTimeout protects incomplete HTTP/1 requests and is cleared after
	// headers. Unlike ReadTimeout it does not impose a lifetime on HTTP/2 streams.
	return &http.Server{
		Handler:           clearH2CHeaderDeadline(handler),
		ReadHeaderTimeout: dashboardReadHeaderTimeout,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, dashboardConnKey{}, &dashboardConn{conn: c})
		},
	}
}

type dashboardConnKey struct{}

type dashboardConn struct {
	conn    net.Conn
	cleared sync.Once
}

// clearH2CHeaderDeadline 修复 h2c 长连接被 ReadHeaderTimeout 掐断的问题。
// net/http 在接受连接时按 ReadHeaderTimeout 设置读截止时间；明文 HTTP/2
// (prior knowledge) 连接随后直接交给内置 HTTP/2 服务端，而后者只在配置了
// ReadTimeout 时才清除该截止时间。结果是整条连接（包括 agent 的 gRPC 长流）
// 在建立 ReadHeaderTimeout 之后被断开。这里在该连接第一个请求的头部到达后
// 清除截止时间，保留"头部须在超时内到达"的语义。TLS 连接不受影响。
func clearH2CHeaderDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && r.TLS == nil {
			if dc, ok := r.Context().Value(dashboardConnKey{}).(*dashboardConn); ok {
				dc.cleared.Do(func() { _ = dc.conn.SetReadDeadline(time.Time{}) })
			}
		}
		next.ServeHTTP(w, r)
	})
}
