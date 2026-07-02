package routes

import (
	"strings"
	"testing"

	"mimoproxy/internal/models"
)

func TestAgentLocationOnlyRegex(t *testing.T) {
	text := "/Users/jfcardososantos/Documents/alfst-homepage/src/app/budget/page.tsx 80 20"
	if !agentLocationOnlyRegex.MatchString(text) {
		t.Fatalf("expected location-only response to match")
	}

	final := "Alterei /Users/me/app/page.tsx e concluí os ajustes solicitados."
	if agentLocationOnlyRegex.MatchString(final) {
		t.Fatalf("expected normal final response not to match")
	}
}

func TestExtractPathOnlyResponse(t *testing.T) {
	text := "/Users/jfcardososantos/Documents/alfst-homepage/src/app/budget/page.tsx /Users/jfcardososantos/Documents/alfst-homepage/src/app/contact/page.tsx"
	paths := extractPathOnlyResponse(text)
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(paths))
	}

	withLocations := "/Users/jfcardososantos/Documents/alfst-homepage/src/app/budget/page.tsx 80 30 /Users/jfcardososantos/Documents/alfst-homepage/src/app/contact/page.tsx 80 30"
	paths = extractPathOnlyResponse(withLocations)
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths with locations, got %d", len(paths))
	}
	if strings.Contains(paths[0], "80") || strings.Contains(paths[1], "80") {
		t.Fatalf("expected returned paths without line/column, got %+v", paths)
	}

	final := "Concluí ajustes em /Users/me/app/page.tsx e /Users/me/app/contact/page.tsx."
	if paths := extractPathOnlyResponse(final); len(paths) != 0 {
		t.Fatalf("expected normal final response not to be path-only, got %+v", paths)
	}
}

func TestSynthesizePathReadToolCalls(t *testing.T) {
	result := parsedMimoChat{
		CleanText:    "/tmp/app/page.tsx /tmp/app/contact/page.tsx",
		FinishReason: "stop",
	}
	tools := []models.Tool{{
		Type: "function",
		Function: models.ToolDefinition{
			Name: "read",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"filePath": map[string]interface{}{"type": "string"},
				},
			},
		},
	}}

	out := synthesizePathReadToolCalls(result, tools, nil)
	if out.FinishReason != "tool_calls" {
		t.Fatalf("expected tool_calls finish reason, got %s", out.FinishReason)
	}
	if len(out.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(out.ToolCalls))
	}
	if out.ToolCalls[0].Function.Name != "read" {
		t.Fatalf("unexpected tool name: %s", out.ToolCalls[0].Function.Name)
	}
	if !strings.Contains(out.ToolCalls[1].Function.Arguments, "contact/page.tsx") {
		t.Fatalf("unexpected arguments: %s", out.ToolCalls[1].Function.Arguments)
	}
}

func TestSynthesizeReadCommandToolCalls(t *testing.T) {
	result := parsedMimoChat{
		CleanText:    "sed -n '82,95p' /Users/jfcardososantos/Documents/alfst-homepage/src/app/budget/page.tsx Read budget page lines 82-95 sed -n '84,97p' /Users/jfcardososantos/Documents/alfst-homepage/src/app/contact/page.tsx Read contact page lines 84-97",
		FinishReason: "stop",
	}
	tools := []models.Tool{{
		Type: "function",
		Function: models.ToolDefinition{
			Name: "read",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"filePath": map[string]interface{}{"type": "string"},
				},
			},
		},
	}}

	out := synthesizePathReadToolCalls(result, tools, nil)
	if out.FinishReason != "tool_calls" {
		t.Fatalf("expected tool_calls finish reason, got %s", out.FinishReason)
	}
	if len(out.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(out.ToolCalls))
	}
	if !strings.Contains(out.ToolCalls[0].Function.Arguments, "budget/page.tsx") {
		t.Fatalf("unexpected first arguments: %s", out.ToolCalls[0].Function.Arguments)
	}
	if !strings.Contains(out.ToolCalls[1].Function.Arguments, "contact/page.tsx") {
		t.Fatalf("unexpected second arguments: %s", out.ToolCalls[1].Function.Arguments)
	}
}
