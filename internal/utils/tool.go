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

var trailingToolJSONRegex = regexp.MustCompile(`(?s)\{\s*"name"\s*:\s*"[^"]+"\s*,\s*"arguments"\s*:\s*.*\}$`)

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

// FormatToolsAsInstructionsWithChoice appends tool_choice guidance for IDE clients.
func FormatToolsAsInstructionsWithChoice(tools []models.Tool, toolChoice string) string {
	base := FormatToolsAsInstructions(tools)
	if base == "" {
		return base
	}
	var sb strings.Builder
	sb.WriteString(base)
	sb.WriteString(agentToolExecutionRules())

	if toolChoice == "" || toolChoice == "auto" {
		return sb.String()
	}
	sb.WriteString("\nTool choice policy: ")
	sb.WriteString(toolChoice)
	sb.WriteString(".\n")
	if toolChoice == "required" || toolChoice == "any" {
		sb.WriteString("You MUST respond with at least one tool call using the XML format above.\n")
	}
	return sb.String()
}

func agentToolExecutionRules() string {
	return `
## Agent execution (mandatory)
- After any thinking/planning, you MUST call tools using the XML format. Never stop with only a plan.
- Do NOT say you "will" read files or run commands without immediately emitting the matching <tool_call>.
- Each turn that requires action must include at least one <tool_call>...</tool_call> block.
- Plain text without tool_call XML is only valid when the task is fully complete and no tools are needed.
`
}

// ShouldEnableWebSearch decides when to turn on Xiaomi native web search.
func ShouldEnableWebSearch(model string, webSearchFlag bool, tools []models.Tool) bool {
	if webSearchFlag || strings.Contains(strings.ToLower(model), "search") {
		return true
	}
	for _, tool := range tools {
		name := strings.ToLower(tool.Function.Name)
		if strings.Contains(name, "search") || strings.Contains(name, "web") || strings.Contains(name, "browse") {
			return true
		}
	}
	return false
}

// AssignToolCallIndexes ensures OpenAI-compatible index fields on each call.
func AssignToolCallIndexes(toolCalls []models.ToolCall) []models.ToolCall {
	for i := range toolCalls {
		toolCalls[i].Index = i
		if toolCalls[i].ID == "" {
			toolCalls[i].ID = "call_" + GenerateID()
		}
		if toolCalls[i].Type == "" {
			toolCalls[i].Type = "function"
		}
	}
	return toolCalls
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

	if len(toolCalls) == 0 {
		trimmedText := strings.TrimSpace(text)
		trimmedText = strings.TrimPrefix(trimmedText, "```json")
		trimmedText = strings.TrimPrefix(trimmedText, "```")
		trimmedText = strings.TrimSuffix(trimmedText, "```")
		trimmedText = strings.TrimSpace(trimmedText)

		if match := trailingToolJSONRegex.FindString(trimmedText); match != "" {
			idx := strings.LastIndex(trimmedText, match)
			candidate := strings.TrimSpace(trimmedText[idx:])
			var toolCallData struct {
				Name      string      `json:"name"`
				Arguments interface{} `json:"arguments"`
			}
			if err := json.Unmarshal([]byte(candidate), &toolCallData); err == nil && toolCallData.Name != "" && toolCallData.Arguments != nil {
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
				cleanText = strings.TrimSpace(trimmedText[:idx])
			}
		} else if idx := strings.LastIndex(trimmedText, `{"name"`); idx != -1 {
			candidate := strings.TrimSpace(trimmedText[idx:])
			if strings.HasSuffix(candidate, "}") {
				var toolCallData struct {
					Name      string      `json:"name"`
					Arguments interface{} `json:"arguments"`
				}
				if err := json.Unmarshal([]byte(candidate), &toolCallData); err == nil && toolCallData.Name != "" && toolCallData.Arguments != nil {
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
					cleanText = strings.TrimSpace(trimmedText[:idx])
				}
			}
		}
	}

	cleanText = strings.TrimSpace(cleanText)
	return cleanText, toolCalls
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

	if rc := strings.TrimSpace(message.ReasoningContent); rc != "" {
		parts = append(parts, ThinkingOpenTag+rc+ThinkingCloseTag)
	}

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
