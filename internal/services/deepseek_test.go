package services

import (
	"strings"
	"testing"

	"flip-ai/internal/models"
)

func TestParseDeepSeekDataSkipsFinishedStatus(t *testing.T) {
	result := models.DeepSeekChatResult{}

	parseDeepSeekData(`{"p":"response/status","v":"FINISHED"}`, &result)
	parseDeepSeekData(`{"p":"response/content","v":"ok"}`, &result)

	if result.Content != "ok" {
		t.Fatalf("expected FINISHED status to be ignored, got %q", result.Content)
	}
}

func TestParseDeepSeekDataSkipsStatusText(t *testing.T) {
	result := models.DeepSeekChatResult{}

	parseDeepSeekData(`{"p":"status","v":"almost done"}`, &result)
	parseDeepSeekData(`{"p":"response/content","v":"done"}`, &result)

	if strings.Contains(result.Content, "almost done") {
		t.Fatalf("expected status text to be ignored, got %q", result.Content)
	}
	if result.Content != "done" {
		t.Fatalf("unexpected content: %q", result.Content)
	}
}
