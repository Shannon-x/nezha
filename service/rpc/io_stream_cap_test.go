package rpc

import (
	"errors"
	"fmt"
	"testing"
)

// GHSA-jg62-j5h6-8mpq：terminal/file-manager 之前无上限地创建 IO 流，任一已认证
// 成员可开成千上万条耗尽 dashboard 与 agent 资源。CreateStream 现按用户(20)与
// 服务器(40)双重限流；uid==0 的内部流（NAT/迁移/MCP）豁免按用户限流但仍计入
// 按服务器限流。

func capStreamID(i int) string { return fmt.Sprintf("stream-%d", i) }

func TestCreateStream_PerUserCapRejectsOverflow(t *testing.T) {
	h := NewNezhaHandler()
	const uid = uint64(7)
	// 每条流指向不同 server，避免触发 per-server 限制，单独检验 per-user。
	for i := 0; i < maxStreamsPerUser; i++ {
		if err := h.CreateStream(capStreamID(i), uid, uint64(i)); err != nil {
			t.Fatalf("stream %d below per-user cap must succeed: %v", i, err)
		}
	}
	if err := h.CreateStream("overflow", uid, 9999); !errors.Is(err, ErrTooManyStreamsForUser) {
		t.Fatalf("stream #%d for the user must be rejected with ErrTooManyStreamsForUser, got %v", maxStreamsPerUser+1, err)
	}
}

func TestCreateStream_PerServerCapRejectsOverflow(t *testing.T) {
	h := NewNezhaHandler()
	const targetServer = uint64(42)
	// uid==0 豁免 per-user 限制，单独检验 per-server。
	for i := 0; i < maxStreamsPerServer; i++ {
		if err := h.CreateStream(capStreamID(i), 0, targetServer); err != nil {
			t.Fatalf("stream %d below per-server cap must succeed: %v", i, err)
		}
	}
	if err := h.CreateStream("overflow", 0, targetServer); !errors.Is(err, ErrTooManyStreamsForServer) {
		t.Fatalf("stream #%d for the server must be rejected with ErrTooManyStreamsForServer, got %v", maxStreamsPerServer+1, err)
	}
}

func TestCreateStream_InternalUIDExemptFromPerUserCap(t *testing.T) {
	h := NewNezhaHandler()
	// uid==0（NAT/迁移/MCP 内部流）超过 per-user 上限仍放行，只要不撞 per-server。
	for i := 0; i < maxStreamsPerUser+5; i++ {
		if err := h.CreateStream(capStreamID(i), 0, uint64(i)); err != nil {
			t.Fatalf("internal uid==0 stream %d must be exempt from per-user cap: %v", i, err)
		}
	}
}

func TestCreateStream_CloseReleasesSlot(t *testing.T) {
	h := NewNezhaHandler()
	const uid = uint64(7)
	for i := 0; i < maxStreamsPerUser; i++ {
		if err := h.CreateStream(capStreamID(i), uid, uint64(i)); err != nil {
			t.Fatalf("setup stream %d: %v", i, err)
		}
	}
	if err := h.CreateStream("overflow", uid, 9999); !errors.Is(err, ErrTooManyStreamsForUser) {
		t.Fatalf("precondition: per-user cap must be hit, got %v", err)
	}
	if err := h.CloseStream(capStreamID(0)); err != nil {
		t.Fatalf("close stream: %v", err)
	}
	if err := h.CreateStream("after-release", uid, 9999); err != nil {
		t.Fatalf("after closing one stream a new one must fit: %v", err)
	}
}
