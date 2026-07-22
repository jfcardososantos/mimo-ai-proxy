package services

import (
	"encoding/json"
	"testing"
)

func TestParseKimiConnectStream(t *testing.T) {
	frame := func(flags byte, event map[string]interface{}) []byte {
		body, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		result := kimiConnectFrame(body)
		result[0] = flags
		return result
	}
	raw := append(frame(0, map[string]interface{}{"op": "set", "mask": "block.think", "block": map[string]interface{}{"think": map[string]string{"content": "raciocinio"}}}), frame(0, map[string]interface{}{"op": "append", "mask": "block.text.content", "block": map[string]interface{}{"text": map[string]string{"content": "resposta"}}})...)
	raw = append(raw, frame(2, map[string]interface{})...)

	result, err := parseKimiConnectStream(raw)
	if err != nil {
		t.Fatalf("parse stream: %v", err)
	}
	if result.Content != "resposta" || result.ReasoningText != "raciocinio" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestKimiModelAliases(t *testing.T) {
	for _, model := range []string{"kimi-k3", "kimi/k3", "k3", "kimi-k2.6", "kimi/k2.6", "k2d6"} {
		if !IsKimiModel(model) {
			t.Fatalf("expected %q to route to Kimi", model)
		}
	}
}
