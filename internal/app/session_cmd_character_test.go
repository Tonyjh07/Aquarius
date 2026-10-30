package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestCharacterCommandReportsNotWired 命令位置先占住、如实报未接入：端口与命令面
// 分批落地期间，/character 有回应（而不是「未知命令」），用户看得出是本版还没做。
func TestCharacterCommandReportsNotWired(t *testing.T) {
	s, _, _ := newTestSession(t, newMemStore())
	_, err := s.execCommand(context.Background(), port.Command{Name: "character", Args: []string{"list"}})
	if err == nil || !strings.Contains(err.Error(), "未接入") {
		t.Fatalf("character err = %v, want 含「未接入」", err)
	}
}
