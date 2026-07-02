package utils

import (
	"mimoproxy/internal/models"
	"testing"
)

func TestTrimMessagesForProxyKeepsSystemAndTail(t *testing.T) {
	msgs := []models.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "1"},
		{Role: "assistant", Content: "2"},
		{Role: "user", Content: "3"},
	}
	out := TrimMessagesForProxy(msgs, ContextLimits{MaxMessages: 2, MaxChars: 1000, MaxToolResultChars: 10})
	if len(out) != 3 {
		t.Fatalf("expected 3 messages (system + 2 tail), got %d", len(out))
	}
	if out[0].Role != "system" {
		t.Fatalf("expected system first, got %s", out[0].Role)
	}
}
