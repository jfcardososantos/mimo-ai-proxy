/*
 * File: tool.go
 * Project: mimoproxy
 * Created: 2026-04-29
 */

package utils

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"mimoproxy/internal/models"
)

var (
	trailingToolJSONRegex = regexp.MustCompile(`(?s)\{\s*"name"\s*:\s*"[^"]+"\s*,\s*"arguments"\s*:\s*.*\}$`)
	fencedJSONRegex      = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)\\s*```")
)

// FormatToolsAsInstructions mirrors the simpler behavior from the first project version.
func FormatToolsAsInstructions(tools []models.Tool) string {
	if len(tools) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\n# Tools\n\nYou have access to the following tools. To call a tool, you MUST use exactly this XML format and no Markdown fence:\n")
	sb.WriteString("<tool_call>{\"name\":\"function_name\",\"arguments\":{\"arg1\":\"value1\"}}</tool_call>\n\n")
	sb.WriteString("This adapter will convert that XML into OpenAI-compatible tool_calls for the client.\n")
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
	if toolChoice == "none" {
		sb.WriteString("Do NOT call tools this turn. Answer normally using the provided context.\n")
		return sb.String()
	}
	if toolChoice == "required" || toolChoice == "any" {
		sb.WriteString("You MUST respond with at least one tool call using the XML format above.\n")
	} else {
		sb.WriteString("You MUST call only this function name if a tool is needed: ")
		sb.WriteString(toolChoice)
		sb.WriteString(".\n")
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
- Tool results from the client appear as <tool_result ...>. Use them as observations and continue with the next required tool call or final answer.
- For web/search tasks, use the available search/browser tools for current or source-dependent facts. If search results are thin, incomplete, or ambiguous, refine the query or open relevant results before finalizing.
- Do not summarize partial evidence as a final answer when another tool call is needed to complete the task.
`
}

// ShouldEnableWebSearch decides when to turn on Xiaomi native web search.
// Web search adds significant latency; with agent/tools it is opt-in only unless DEFAULT_WEB_SEARCH is set.
func ShouldEnableWebSearch(model string, webSearchFlag bool, tools []models.Tool) bool {
	if webSearchFlag || strings.Contains(strings.ToLower(model), "search") {
		return true
	}
	for _, tool := range tools {
		toolType := strings.ToLower(strings.TrimSpace(tool.Type))
		if toolType != "function" && strings.Contains(toolType, "search") {
			return true
		}
	}
	if len(tools) > 0 {
		return false
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

// ParseToolCalls accepts the XML bridge format plus common JSON shapes emitted by
// models despite the instruction to use XML.
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
		if parsed := parseToolCallJSON(jsonStr); len(parsed) > 0 {
			toolCalls = append(toolCalls, parsed...)
			cleanText = strings.Replace(cleanText, match[0], "", 1)
		}
	}

	if len(toolCalls) == 0 {
		trimmedText := stripMarkdownFence(strings.TrimSpace(text))
		if strings.HasPrefix(trimmedText, "{") || strings.HasPrefix(trimmedText, "[") {
			if parsed := parseToolCallJSON(trimmedText); len(parsed) > 0 {
				toolCalls = append(toolCalls, parsed...)
				cleanText = ""
			} else if parsed, consumed := parseToolCallJSONSequence(trimmedText); len(parsed) > 0 {
				toolCalls = append(toolCalls, parsed...)
				cleanText = strings.TrimSpace(trimmedText[consumed:])
			}
		}
	}

	if len(toolCalls) == 0 {
		trimmedText := strings.TrimSpace(text)
		for _, match := range fencedJSONRegex.FindAllStringSubmatch(trimmedText, -1) {
			if len(match) < 2 {
				continue
			}
			fenced := strings.TrimSpace(match[1])
			if parsed := parseToolCallJSON(fenced); len(parsed) > 0 {
				toolCalls = append(toolCalls, parsed...)
				cleanText = strings.TrimSpace(strings.Replace(cleanText, match[0], "", 1))
				break
			} else if parsed, _ := parseToolCallJSONSequence(fenced); len(parsed) > 0 {
				toolCalls = append(toolCalls, parsed...)
				cleanText = strings.TrimSpace(strings.Replace(cleanText, match[0], "", 1))
				break
			}
		}
	}

	if len(toolCalls) == 0 {
		trimmedText := stripMarkdownFence(strings.TrimSpace(text))

		if match := trailingToolJSONRegex.FindString(trimmedText); match != "" {
			idx := strings.LastIndex(trimmedText, match)
			candidate := strings.TrimSpace(trimmedText[idx:])
			if parsed := parseToolCallJSON(candidate); len(parsed) > 0 {
				toolCalls = append(toolCalls, parsed...)
				cleanText = strings.TrimSpace(trimmedText[:idx])
			}
		} else if idx := strings.LastIndex(trimmedText, `{"name"`); idx != -1 {
			candidate := strings.TrimSpace(trimmedText[idx:])
			if strings.HasSuffix(candidate, "}") {
				if parsed := parseToolCallJSON(candidate); len(parsed) > 0 {
					toolCalls = append(toolCalls, parsed...)
					cleanText = strings.TrimSpace(trimmedText[:idx])
				}
			}
		}
	}

	if len(toolCalls) == 0 {
		trimmedText := strings.TrimSpace(text)
		if idx := firstToolJSONIndex(trimmedText); idx != -1 {
			if parsed, consumed := parseToolCallJSONSequence(trimmedText[idx:]); len(parsed) > 0 {
				toolCalls = append(toolCalls, parsed...)
				cleanText = strings.TrimSpace(trimmedText[:idx] + trimmedText[idx+consumed:])
			}
		}
	}

	cleanText = strings.TrimSpace(cleanText)
	return cleanText, toolCalls
}

func stripMarkdownFence(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func parseToolCallJSON(raw string) []models.ToolCall {
	raw = stripMarkdownFence(raw)
	if raw == "" {
		return nil
	}

	var value interface{}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil
	}
	return parseToolCallValue(value)
}

func parseToolCallJSONSequence(raw string) ([]models.ToolCall, int) {
	raw = stripMarkdownFence(raw)
	if raw == "" {
		return nil, 0
	}

	decoder := json.NewDecoder(strings.NewReader(raw))
	var calls []models.ToolCall
	var consumed int64
	for {
		var value interface{}
		if err := decoder.Decode(&value); err != nil {
			break
		}
		parsed := parseToolCallValue(value)
		if len(parsed) == 0 {
			break
		}
		calls = append(calls, parsed...)
		consumed = decoder.InputOffset()
	}
	return calls, int(consumed)
}

func firstToolJSONIndex(s string) int {
	candidates := []string{
		`{"name"`,
		`{ "name"`,
		`{"tool_call"`,
		`{ "tool_call"`,
		`{"tool_calls"`,
		`{ "tool_calls"`,
	}
	best := -1
	for _, candidate := range candidates {
		if idx := strings.Index(s, candidate); idx != -1 && (best == -1 || idx < best) {
			best = idx
		}
	}
	return best
}

func parseToolCallValue(value interface{}) []models.ToolCall {
	switch v := value.(type) {
	case []interface{}:
		var calls []models.ToolCall
		for _, item := range v {
			calls = append(calls, parseToolCallValue(item)...)
		}
		return calls
	case map[string]interface{}:
		if nested, ok := v["tool_calls"]; ok {
			return parseToolCallValue(nested)
		}
		if nested, ok := v["tool_call"]; ok {
			return parseToolCallValue(nested)
		}
		if nested, ok := v["calls"]; ok {
			return parseToolCallValue(nested)
		}
		return parseSingleToolCall(v)
	default:
		return nil
	}
}

func parseSingleToolCall(data map[string]interface{}) []models.ToolCall {
	id, _ := data["id"].(string)
	callType, _ := data["type"].(string)
	if callType == "" {
		callType = "function"
	}

	name, _ := data["name"].(string)
	args := data["arguments"]

	if fn, ok := data["function"].(map[string]interface{}); ok {
		if n, ok := fn["name"].(string); ok {
			name = n
		}
		if a, ok := fn["arguments"]; ok {
			args = a
		}
	} else if fnName, ok := data["function"].(string); ok && name == "" {
		name = fnName
	}

	for _, key := range []string{"tool", "tool_name", "function_name", "action"} {
		if name != "" {
			break
		}
		if n, ok := data[key].(string); ok {
			name = n
		}
	}

	if args == nil {
		for _, key := range []string{"input", "parameters", "params", "args"} {
			if a, ok := data[key]; ok {
				args = a
				break
			}
		}
	}

	if name == "" {
		return nil
	}
	if args == nil {
		args = map[string]interface{}{}
	}

	argsStr := "{}"
	switch v := args.(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			argsStr = strings.TrimSpace(v)
		}
	default:
		b, err := json.Marshal(v)
		if err == nil && len(b) > 0 {
			argsStr = string(b)
		}
	}

	if id == "" {
		id = "call_" + GenerateID()
	}
	return []models.ToolCall{{
		ID:   id,
		Type: callType,
		Function: models.ToolFunction{
			Name:      name,
			Arguments: argsStr,
		},
	}}
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
		if message.ToolCallID != "" {
			return fmt.Sprintf("<tool_result tool_call_id=\"%s\">%s</tool_result>", message.ToolCallID, contentStr)
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
