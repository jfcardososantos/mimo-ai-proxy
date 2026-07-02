package utils

import (
	"strings"
	"testing"
)

func TestParseToolCallsXML(t *testing.T) {
	text := `Vou buscar isso.
<tool_call>{"name": "WebSearch", "arguments": {"search_term": "golang 1.24"}}</tool_call>`
	clean, calls := ParseToolCalls(text)
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].Function.Name != "WebSearch" {
		t.Fatalf("unexpected name: %s", calls[0].Function.Name)
	}
	if !strings.Contains(calls[0].Function.Arguments, "golang") {
		t.Fatalf("unexpected arguments: %s", calls[0].Function.Arguments)
	}
	if strings.Contains(clean, "tool_call") {
		t.Fatalf("expected clean text without tool markup, got %q", clean)
	}
}

func TestParseToolCallsTrailingJSON(t *testing.T) {
	text := "Resposta aqui.\n```json\n{\"name\": \"read_file\", \"arguments\": {\"path\": \"/tmp/a\"}}\n```"
	_, calls := ParseToolCalls(text)
	if len(calls) != 1 || calls[0].Function.Name != "read_file" {
		t.Fatalf("expected read_file tool call, got %+v", calls)
	}
}

func TestParseToolCallsOpenAIShape(t *testing.T) {
	text := `{"tool_calls":[{"id":"call_123","type":"function","function":{"name":"replace_in_file","arguments":"{\"path\":\"main.go\",\"old\":\"a\",\"new\":\"b\"}"}}]}`
	clean, calls := ParseToolCalls(text)
	if clean != "" {
		t.Fatalf("expected empty clean text, got %q", clean)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].ID != "call_123" {
		t.Fatalf("expected id to be preserved, got %s", calls[0].ID)
	}
	if calls[0].Function.Name != "replace_in_file" {
		t.Fatalf("unexpected tool name: %s", calls[0].Function.Name)
	}
	if !strings.Contains(calls[0].Function.Arguments, "main.go") {
		t.Fatalf("unexpected arguments: %s", calls[0].Function.Arguments)
	}
}

func TestParseToolCallsFencedOpenAIShape(t *testing.T) {
	text := "Vou editar.\n```json\n{\"tool_call\":{\"name\":\"execute_command\",\"arguments\":{\"command\":\"go test ./...\"}}}\n```"
	clean, calls := ParseToolCalls(text)
	if strings.Contains(clean, "tool_call") {
		t.Fatalf("expected clean text without fenced tool json, got %q", clean)
	}
	if len(calls) != 1 || calls[0].Function.Name != "execute_command" {
		t.Fatalf("expected execute_command call, got %+v", calls)
	}
}

func TestParseToolCallsConcatenatedJSONObjects(t *testing.T) {
	text := `{"name": "read", "arguments": {"filePath": "/tmp/index.html"}} {"name": "read", "arguments": {"filePath": "/tmp/package.json"}}`
	clean, calls := ParseToolCalls(text)
	if clean != "" {
		t.Fatalf("expected empty clean text, got %q", clean)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(calls))
	}
	if calls[0].Function.Name != "read" || calls[1].Function.Name != "read" {
		t.Fatalf("unexpected tool calls: %+v", calls)
	}
	if !strings.Contains(calls[1].Function.Arguments, "package.json") {
		t.Fatalf("unexpected arguments: %s", calls[1].Function.Arguments)
	}
}

func TestShouldEnableWebSearch(t *testing.T) {
	if !ShouldEnableWebSearch("mimo-search", false, nil) {
		t.Fatal("expected search in model name to enable web search")
	}
	if !ShouldEnableWebSearch("mimo", true, nil) {
		t.Fatal("expected explicit web_search flag")
	}
}
