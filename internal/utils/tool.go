/*
 * File: tool.go
 * Project: mimoproxy
 * Created: 2026-04-29
 */

package utils

import (
	"encoding/json"
	"fmt"
	"mimoproxy/internal/models"
	"regexp"
	"strings"
)

// FormatToolsAsInstructions mirrors the simpler behavior from the first project version.
func FormatToolsAsInstructions(tools []models.Tool) string {
	if len(tools) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\n# Tools\n\nYou have access to the following tools. To call a tool, you MUST use the following XML format:\n")
	sb.WriteString("<tool_call>{\"name\": \"function_name\", \"arguments\": {\"arg1\": \"value1\"}}</tool_call>\n\n")
	sb.WriteString("Available tools:\n")

	for _, tool := range tools {
		if tool.Type != "function" {
			continue
		}
		funcDef := tool.Function
		sb.WriteString(fmt.Sprintf("\n- %s: %s\n", funcDef.Name, funcDef.Description))
		params, _ := json.Marshal(funcDef.Parameters)
		sb.WriteString(fmt.Sprintf("  Parameters: %s\n", string(params)))
	}

	return sb.String()
}

// ParseToolCalls mirrors the simpler, more permissive parser from the first project version.
func ParseToolCalls(text string) (string, []models.ToolCall) {
	var toolCalls []models.ToolCall
	cleanText := text

	toolCallRegex := regexp.MustCompile(`(?s)<tool_call>(.*?)</tool_call>`)
	matches := toolCallRegex.FindAllStringSubmatch(text, -1)

	for _, match := range matches {
		if len(match) < 2 {
			continue
		}

		jsonStr := strings.TrimSpace(match[1])
		var toolCallData struct {
			Name      string      `json:"name"`
			Arguments interface{} `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &toolCallData); err == nil {
			var argsStr string
			switch v := toolCallData.Arguments.(type) {
			case string:
				argsStr = v
			default:
				b, _ := json.Marshal(v)
				argsStr = string(b)
			}

			toolCalls = append(toolCalls, models.ToolCall{
				ID:   "call_" + GenerateID(),
				Type: "function",
				Function: models.ToolFunction{
					Name:      toolCallData.Name,
					Arguments: argsStr,
				},
			})
			cleanText = strings.Replace(cleanText, match[0], "", 1)
		}
	}

	if len(toolCalls) == 0 {
		trimmedText := strings.TrimSpace(text)
		if strings.HasPrefix(trimmedText, "{") && strings.HasSuffix(trimmedText, "}") {
			var toolCallData struct {
				Name      string      `json:"name"`
				Arguments interface{} `json:"arguments"`
			}
			if err := json.Unmarshal([]byte(trimmedText), &toolCallData); err == nil && toolCallData.Name != "" && toolCallData.Arguments != nil {
				var argsStr string
				switch v := toolCallData.Arguments.(type) {
				case string:
					argsStr = v
				default:
					b, _ := json.Marshal(v)
					argsStr = string(b)
				}
				toolCalls = append(toolCalls, models.ToolCall{
					ID:   "call_" + GenerateID(),
					Type: "function",
					Function: models.ToolFunction{
						Name:      toolCallData.Name,
						Arguments: argsStr,
					},
				})
				cleanText = ""
			}
		}
	}

	return strings.TrimSpace(cleanText), toolCalls
}

// NormalizeToolCalls is intentionally a no-op to avoid proxy-side mutation of the model output.
func NormalizeToolCalls(toolCalls []models.ToolCall, _ []models.Tool) []models.ToolCall {
	return toolCalls
}

// RepairToolArguments is intentionally a no-op to avoid rewriting model-emitted arguments.
func RepairToolArguments(raw string) string {
	return raw
}

// ExtractTerminalToolContent is intentionally a no-op to avoid treating model text as pseudo-tools.
func ExtractTerminalToolContent(toolCalls []models.ToolCall) (string, []models.ToolCall) {
	return "", toolCalls
}

// FormatMessageForMiMo mirrors the simpler formatting from the first version.
func FormatMessageForMiMo(message models.Message) string {
	var parts []string

	if message.Role == "tool" {
		contentStr := ""
		switch v := message.Content.(type) {
		case string:
			contentStr = v
		case []interface{}:
			for _, item := range v {
				if m, ok := item.(map[string]interface{}); ok {
					if m["type"] == "text" {
						if text, ok := m["text"].(string); ok {
							contentStr += text
						}
					}
				}
			}
		}
		return fmt.Sprintf("<tool_result>%s</tool_result>", contentStr)
	}

	if message.Content != nil {
		switch v := message.Content.(type) {
		case string:
			parts = append(parts, v)
		case []interface{}:
			for _, item := range v {
				if m, ok := item.(map[string]interface{}); ok {
					mType, _ := m["type"].(string)
					switch mType {
					case "text":
						if text, ok := m["text"].(string); ok {
							parts = append(parts, text)
						}
					case "reasoning":
						if text, ok := m["text"].(string); ok {
							parts = append(parts, fmt.Sprintf("<think>%s</think>", text))
						}
					case "tool_use":
						name, _ := m["name"].(string)
						input := m["input"]
						inputBytes, _ := json.Marshal(input)
						parts = append(parts, fmt.Sprintf("<tool_call>{\"name\": \"%s\", \"arguments\": %s}</tool_call>", name, string(inputBytes)))
					case "tool_result":
						content, _ := m["content"].(string)
						parts = append(parts, fmt.Sprintf("<tool_result>%s</tool_result>", content))
					}
				}
			}
		}
	}

	if len(message.ToolCalls) > 0 {
		for _, tc := range message.ToolCalls {
			if tc.Type == "function" {
				var args interface{}
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				argsBytes, _ := json.Marshal(args)
				parts = append(parts, fmt.Sprintf("<tool_call>{\"name\": \"%s\", \"arguments\": %s}</tool_call>", tc.Function.Name, string(argsBytes)))
			}
		}
	}

	return strings.TrimSpace(strings.Join(parts, "\n"))
}
