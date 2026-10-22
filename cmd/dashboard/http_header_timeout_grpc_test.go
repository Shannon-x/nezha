package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// 真实 gRPC 客户端走明文 HTTP/2 (h2c) 的长流必须活过 ReadHeaderTimeout，
// 对应 agent 直连 dashboard 或经 nginx grpc_pass 转发的场景。
func TestDashboardH2CGRPCStreamOutlivesHeaderTimeout(t *testing.T) {
	gs := grpc.NewServer()
	healthpb.RegisterHealthServer(gs, health.NewServer())
	srv := newDashboardHTTPServer(gs)
	srv.ReadHeaderTimeout = 1 * time.Second
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetUnencryptedHTTP2(true)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	defer srv.Close()
	cc, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	stream, err := healthpb.NewHealthClient(cc).Watch(context.Background(), &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("first recv: %v", err)
	}
	done := make(chan error, 1)
	go func() { _, err := stream.Recv(); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("stream ended after header timeout: %v", err)
	case <-time.After(4 * time.Second):
	}
}
