package rpc

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"
)

// authCheckWithHyphenatedSecret 用连字符形式的 metadata key 发起认证。gRPC/HTTP2
// 会把 header 归一化为小写但不统一下划线与连字符，故 "client-secret" 与
// "client_secret" 是两个不同 key。某些 agent 构建/中间代理发送连字符形式，曾导致
// 合法 agent 认证失败 (#1197)。
func authCheckWithHyphenatedSecret(secret, uuid string) (uint64, error) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"client-secret", secret,
		"client-uuid", uuid,
	))
	return (&authHandler{}).Check(ctx)
}

func TestAuthCheckAcceptsHyphenatedMetadata(t *testing.T) {
	defer setupAuthHandshakeFixture(t)()
	cid, err := authCheckWithHyphenatedSecret("alice-global", authHandshakeUUID)
	if err != nil {
		t.Fatalf("hyphenated metadata must authenticate: %v", err)
	}
	if cid != 11 {
		t.Fatalf("expected server ID 11, got %d", cid)
	}
}

// TestAuthCheckStillAcceptsUnderscoreMetadata 确认新增连字符兼容没有破坏原有的
// 下划线形式（绝大多数现网 agent 仍用下划线）。
func TestAuthCheckStillAcceptsUnderscoreMetadata(t *testing.T) {
	defer setupAuthHandshakeFixture(t)()
	cid, err := authCheckWithSecret("alice-global", authHandshakeUUID)
	if err != nil {
		t.Fatalf("underscore metadata must still authenticate: %v", err)
	}
	if cid != 11 {
		t.Fatalf("expected server ID 11, got %d", cid)
	}
}
