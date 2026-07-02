package utils

import (
	"strings"
	"testing"

	"mimoproxy/internal/models"
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

func TestTrimMessagesForProxyPreservesLongToolResultTail(t *testing.T) {
	longResult := strings.Repeat("a", 5000) + "IMPORTANT_TAIL_RESULT"
	msgs := []models.Message{
		{Role: "tool", Content: longResult},
	}

	out := TrimMessagesForProxy(msgs, ContextLimits{MaxMessages: 10, MaxChars: 1000, MaxToolResultChars: 3000})
	content, ok := out[0].Content.(string)
	if !ok {
		t.Fatalf("expected string content")
	}
	if !strings.Contains(content, "IMPORTANT_TAIL_RESULT") {
		t.Fatalf("expected truncated content to preserve the tail, got %q", content[len(content)-80:])
	}
	if !strings.Contains(content, "proxy omitted") {
		t.Fatalf("expected truncation marker, got %q", content)
	}
}
