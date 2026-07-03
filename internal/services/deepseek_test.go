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

func TestParseDeepSeekDataReadsNestedInitialContent(t *testing.T) {
	result := models.DeepSeekChatResult{}

	parseDeepSeekData(`{"p":"response/content","v":{"content":"O"}}`, &result)
	parseDeepSeekData(`{"p":"response/content","v":"la"}`, &result)

	if result.Content != "Ola" {
		t.Fatalf("expected nested first token to be preserved, got %q", result.Content)
	}
}

func TestParseDeepSeekDataReadsArrayContent(t *testing.T) {
	result := models.DeepSeekChatResult{}

	parseDeepSeekData(`{"p":"response/content","v":[{"text":"O"},"la","FINISHED"]}`, &result)

	if result.Content != "Ola" {
		t.Fatalf("expected array content to be preserved without status, got %q", result.Content)
	}
}
