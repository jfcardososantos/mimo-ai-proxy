package utils

import (
	"flip-ai/internal/models"
	"strings"
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

func TestTrimMessagesForProxyPreservesLongSystemPrompt(t *testing.T) {
	longSystem := strings.Repeat("a", 12000)
	msgs := []models.Message{
		{Role: "system", Content: longSystem},
		{Role: "tool", Content: strings.Repeat("b", 12000)},
	}

	out := TrimMessagesForProxy(msgs, ContextLimits{
		MaxMessages:        10,
		MaxChars:           20000,
		MaxToolResultChars: 1000,
	})

	if got := out[0].Content.(string); len(got) != len(longSystem) {
		t.Fatalf("expected system prompt to use MaxChars, got length %d", len(got))
	}
	if got := out[1].Content.(string); len(got) > 1100 {
		t.Fatalf("expected tool result to be capped, got length %d", len(got))
	}
}
