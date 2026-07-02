/*
 * File: chat.go
 * Project: mimoproxy
 * Created: 2026-04-29
 */

package routes

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"mimoproxy/internal/models"
	"mimoproxy/internal/services"
	"mimoproxy/internal/utils"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

var (
	TokenStats      = make(map[string]int)
	TokenUsageStats = make(map[string]int)
	ResponseTimes   = make([]int64, 0)
	StatsMutex      sync.Mutex
)

var (
	agentLocationOnlyRegex = regexp.MustCompile(`(?i)^\s*(?:/[^\n]+|[A-Za-z]:\\[^\n]+|\.{0,2}/[^\n]+)\.(?:tsx|ts|jsx|js|css|scss|sass|less|html|json|md|mdx|go|py|php|vue|svelte|astro|yml|yaml)(?::|\s+)\d+(?::|\s+)\d+\s*$`)
	agentPathLocationRegex = regexp.MustCompile(`(?i)((?:/[^\s]+|[A-Za-z]:\\[^\s]+|\.{1,2}/[^\s]+)\.(?:tsx|ts|jsx|js|css|scss|sass|less|html|json|md|mdx|go|py|php|vue|svelte|astro|yml|yaml))(?:(?::|\s+)\d+(?::|\s+)\d+)?`)
)

func AddResponseTime(t int64) {
	StatsMutex.Lock()
	defer StatsMutex.Unlock()
	ResponseTimes = append(ResponseTimes, t)
	if len(ResponseTimes) > 50 {
		ResponseTimes = ResponseTimes[1:]
	}
}

func IncrementTokenStat(token string, tokens int) {
	StatsMutex.Lock()
	defer StatsMutex.Unlock()
	TokenStats[token]++
	TokenUsageStats[token] += tokens
}

func GetStats() (map[string]int, map[string]int, []int64) {
	StatsMutex.Lock()
	defer StatsMutex.Unlock()

	stats := make(map[string]int)
	for k, v := range TokenStats {
		stats[k] = v
	}

	usage := make(map[string]int)
	for k, v := range TokenUsageStats {
		usage[k] = v
	}

	times := make([]int64, len(ResponseTimes))
	copy(times, ResponseTimes)
	return stats, usage, times
}

func sendMimoChatRequest(auth models.Auth, payload models.MimoPayload, customHeaders map[string]string, completionID string) (*http.Response, error) {
	requestURL := fmt.Sprintf("https://aistudio.xiaomimimo.com/open-apis/bot/chat?xiaomichatbot_ph=%s", url.QueryEscape(auth.Ph))

	payloadBytes, _ := json.Marshal(payload)
	fmt.Printf("[%s] Chat Request: %d bytes | Model: %s | Media: %d\n",
		completionID, len(payloadBytes), payload.ModelConfig.Model, len(payload.MultiMedias))

	req, _ := http.NewRequest("POST", requestURL, bytes.NewBuffer(payloadBytes))
	headers := services.GetOfficialHeaders(auth, customHeaders)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	startTime := time.Now()
	resp, err := services.GlobalHTTPClient.Do(req)
	if err == nil {
		fmt.Printf("Xiaomi Response Status: %s (Duration: %v)\n", resp.Status, time.Since(startTime))
		if resp.StatusCode == http.StatusOK {
			AddResponseTime(time.Since(startTime).Milliseconds())
		}
	}
	return resp, err
}

func RegisterChatRoutes(r *gin.Engine, authMiddleware gin.HandlerFunc) {
	v1 := r.Group("/v1")
	if authMiddleware != nil {
		v1.Use(authMiddleware)
	}

	{
		v1.GET("/models", handleModels)
		v1.POST("/chat/completions", handleChatCompletions)
		v1.POST("/completions", handleCompletions)
		v1.GET("/chat/history/:conversationId", handleGetHistory)
	}

	r.POST("/open-apis/bot/chat", handleDirectProxy)
	registerOllamaRoutes(r, authMiddleware)
}

func handleModels(c *gin.Context) {
	auth, err := services.GetSelectedAuth()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid Xiaomi auth configuration", "details": err.Error()})
		return
	}

	if cached, found := services.GlobalCache.Get("models_list"); found {
		c.JSON(http.StatusOK, cached)
		return
	}

	headers := services.GetOfficialHeaders(auth, nil)
	req, _ := http.NewRequest("GET", "https://aistudio.xiaomimimo.com/open-apis/bot/config", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := services.GlobalHTTPClient.Do(req)
	if err == nil {
		defer resp.Body.Close()
	}
	if err == nil && resp.StatusCode == http.StatusOK {
		var result struct {
			Code int `json:"code"`
			Data struct {
				ModelConfigList []struct {
					Model   string `json:"model"`
					EnIntro string `json:"enIntro"`
				} `json:"modelConfigList"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err == nil && result.Code == 0 {
			modelsList := make([]map[string]interface{}, 0)
			for _, m := range result.Data.ModelConfigList {
				modelsList = append(modelsList, map[string]interface{}{
					"id":          m.Model,
					"object":      "model",
					"created":     1700000000,
					"owned_by":    "xiaomi",
					"description": m.EnIntro,
				})
			}
			response := gin.H{"object": "list", "data": modelsList}
			services.GlobalCache.Set("models_list", response, 30*time.Minute)
			c.JSON(http.StatusOK, response)
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"object": "list", "data": []map[string]interface{}{
		{"id": "mimo-v2.5-pro", "object": "model", "created": 1700000000, "owned_by": "xiaomi"},
	}})
}

func handleDirectProxy(c *gin.Context) {
	auth, err := services.GetSelectedAuth()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid Xiaomi auth configuration", "details": err.Error()})
		return
	}

	requestURL := fmt.Sprintf("https://aistudio.xiaomimimo.com/open-apis/bot/chat?xiaomichatbot_ph=%s", url.QueryEscape(auth.Ph))

	body, _ := io.ReadAll(c.Request.Body)
	req, _ := http.NewRequest("POST", requestURL, bytes.NewBuffer(body))

	customHeaders := make(map[string]string)
	for k, v := range c.Request.Header {
		customHeaders[strings.ToLower(k)] = v[0]
	}
	headers := services.GetOfficialHeaders(auth, customHeaders)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := services.GlobalHTTPClient.Do(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to proxy request", "details": err.Error()})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result interface{}
	_ = json.Unmarshal(respBody, &result)
	c.JSON(resp.StatusCode, result)
}

func handleGetHistory(c *gin.Context) {
	conversationID := c.Param("conversationId")
	syncParam := c.Query("sync") == "true"

	if conversationID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "conversationId is required"})
		return
	}

	var messages []models.Message
	var err error

	if syncParam {
		auth, authErr := services.GetSelectedAuth()
		if authErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid Xiaomi auth configuration", "details": authErr.Error()})
			return
		}

		history, err := services.GetConversationHistory(auth, conversationID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch history", "details": err.Error()})
			return
		}

		for _, item := range history {
			messages = append(messages, models.Message{
				Role:    "user",
				Content: item.InputInfo.Query,
			})
			services.SaveMessage(conversationID, item.MsgID+"_u", "user", item.InputInfo.Query)

			if len(item.DialogLogDetailList) > 0 {
				messages = append(messages, models.Message{
					Role:    "assistant",
					Content: item.DialogLogDetailList[0].Result,
				})
				services.SaveMessage(conversationID, item.MsgID+"_a", "assistant", item.DialogLogDetailList[0].Result)
			}
		}
	} else {
		messages, err = services.GetLocalHistory(conversationID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get local history", "details": err.Error()})
			return
		}

		if len(messages) == 0 {
			auth, authErr := services.GetSelectedAuth()
			if authErr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid Xiaomi auth configuration", "details": authErr.Error()})
				return
			}

			history, _ := services.GetConversationHistory(auth, conversationID)
			for _, item := range history {
				messages = append(messages, models.Message{Role: "user", Content: item.InputInfo.Query})
				services.SaveMessage(conversationID, item.MsgID+"_u", "user", item.InputInfo.Query)
				if len(item.DialogLogDetailList) > 0 {
					messages = append(messages, models.Message{Role: "assistant", Content: item.DialogLogDetailList[0].Result})
					services.SaveMessage(conversationID, item.MsgID+"_a", "assistant", item.DialogLogDetailList[0].Result)
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"conversationId": conversationID,
		"messages":       messages,
		"source":         "local+synced",
	})
}

func syncConversationMessages(convID string, messages []models.Message) {
	if convID == "" {
		return
	}

	occurrences := make(map[string]int)
	for _, message := range messages {
		if message.Role == "system" {
			continue
		}
		content := utils.FormatMessageForMiMo(message)
		if strings.TrimSpace(content) == "" {
			continue
		}
		key := message.Role + "\x00" + content
		occurrences[key]++
		msgID := services.StableMessageID(convID, message.Role, content, occurrences[key])
		_ = services.SaveMessageIfMissing(convID, msgID, message.Role, content)
	}
}

func buildConversationQuery(messages []models.Message, toolInstructions string) string {
	var processedMessages []string
	var systemPrompt string

	for _, message := range messages {
		if message.Role == "system" {
			systemPrompt = strings.TrimSpace(services.ExtractText(message.Content, false))
			break
		}
	}

	for _, message := range messages {
		if message.Role == "system" {
			continue
		}
		formatted := formatConversationTurn(message)
		if strings.TrimSpace(formatted) != "" {
			processedMessages = append(processedMessages, formatted)
		}
	}

	if systemPrompt != "" {
		if strings.TrimSpace(toolInstructions) != "" {
			systemPrompt += "\n\n" + strings.TrimSpace(toolInstructions)
		}
		return systemPrompt + "\n\n" + strings.Join(processedMessages, "\n\n")
	}
	if toolInstructions != "" {
		return strings.TrimSpace(toolInstructions) + "\n\n" + strings.Join(processedMessages, "\n\n")
	}
	return strings.Join(processedMessages, "\n\n")
}

func formatConversationTurn(message models.Message) string {
	formatted := utils.FormatMessageForMiMo(message)
	if strings.TrimSpace(formatted) == "" {
		return ""
	}

	switch message.Role {
	case "user":
		return "User:\n" + formatted
	case "assistant":
		return "Assistant:\n" + formatted
	case "tool":
		if message.ToolCallID != "" {
			return "Tool result (" + message.ToolCallID + "):\n" + formatted
		}
		return "Tool result:\n" + formatted
	case "developer":
		return "Developer instructions:\n" + formatted
	default:
		return roleLabel(message.Role) + ":\n" + formatted
	}
}

func roleLabel(role string) string {
	role = strings.TrimSpace(role)
	if role == "" {
		return "Message"
	}
	return strings.ToUpper(role[:1]) + role[1:]
}

func truncateConversationQuery(query string, systemPrefix string, maxChars int) string {
	if maxChars <= 0 || len(query) <= maxChars {
		return query
	}

	truncated := query[len(query)-maxChars:]
	if idx := strings.Index(truncated, "\n"); idx != -1 {
		truncated = truncated[idx+1:]
	}

	if systemPrefix != "" && len(systemPrefix) >= 10 && !strings.Contains(truncated, systemPrefix[:10]) {
		query = systemPrefix + "\n\n... (context truncated) ...\n\n" + truncated
	} else {
		query = truncated
	}

	if len(query) > maxChars+100000 {
		query = query[:maxChars+100000]
	}
	return query
}

func handleChatCompletions(c *gin.Context) {
	completionID := utils.GenerateID()

	bodyCopy, err := io.ReadAll(c.Request.Body)
	if err != nil {
		fmt.Printf("Error reading request body: %v\n", err)
		utils.SendError(c, http.StatusBadRequest, "Failed to read request body", "invalid_request_error", nil)
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyCopy))
	cacheKey := fmt.Sprintf("req_%x", bodyCopy)
	fmt.Printf("Incoming request size: %d bytes\n", len(bodyCopy))

	var input struct {
		Messages          []models.Message `json:"messages"`
		Model             string           `json:"model"`
		Stream            bool             `json:"stream"`
		User              string           `json:"user"`
		Tools             []models.Tool    `json:"tools"`
		ToolChoice        interface{}      `json:"tool_choice"`
		ParallelToolCalls *bool            `json:"parallel_tool_calls"`
		WebSearch         bool             `json:"web_search"`
	}

	if err = json.Unmarshal(bodyCopy, &input); err != nil {
		utils.SendError(c, http.StatusBadRequest, "Invalid request body", "invalid_request_error", nil)
		return
	}

	// Não cachear agent/tool loops — evita repetir respostas "stop" sem tool_calls.
	if !input.Stream && len(input.Tools) == 0 {
		if cached, found := services.GlobalCache.Get(cacheKey); found {
			c.JSON(http.StatusOK, cached)
			return
		}
	}

	if len(input.Messages) == 0 {
		utils.SendError(c, http.StatusBadRequest, "Messages array is required and cannot be empty", "invalid_request_error", nil)
		return
	}

	toolChoice := resolveToolChoice(input.ToolChoice)
	agentMode := len(input.Tools) > 0
	contextLimits := utils.ContextLimitsFromEnv(agentMode)
	if agentMode {
		input.Messages = utils.TrimMessagesForProxy(input.Messages, contextLimits)
	}
	var toolInstructions string
	if agentMode && utils.AgentFastModeEnabled() {
		toolInstructions = utils.FormatToolsAsInstructionsCompact(input.Tools, toolChoice)
	} else {
		toolInstructions = utils.FormatToolsAsInstructionsWithChoice(input.Tools, toolChoice)
	}
	sessionHandle := strings.TrimSpace(input.User)
	if sessionHandle == "" {
		sessionHandle = services.GenerateFingerprint(input.Messages)
	}

	if sessionHandle != "" {
		if pending, found := services.GlobalCache.Get("pending_tools_" + sessionHandle); found {
			if pendingTools, ok := pending.([]models.ToolCall); ok && len(pendingTools) > 0 {
				lastMsg := input.Messages[len(input.Messages)-1]
				if lastMsg.Role == "tool" {
					nextTool := pendingTools[0]
					if nextTool.ID == "" {
						nextTool.ID = "call_" + utils.GenerateID()
					}
					if nextTool.Type == "" {
						nextTool.Type = "function"
					}
					remaining := pendingTools[1:]
					if len(remaining) > 0 {
						services.GlobalCache.Set("pending_tools_"+sessionHandle, remaining, 10*time.Minute)
					} else {
						services.GlobalCache.Delete("pending_tools_" + sessionHandle)
					}

					response := models.ChatCompletionChunk{
						ID:      "chatcmpl-" + completionID,
						Object:  "chat.completion.chunk",
						Created: time.Now().Unix(),
						Model:   input.Model,
						Choices: []models.Choice{
							{
								Index: 0,
								Delta: models.Delta{
									Role:      "assistant",
									ToolCalls: []models.ToolCall{nextTool},
								},
								FinishReason: utils.PointerToString("tool_calls"),
							},
						},
					}

					if input.Stream {
						c.Header("Content-Type", "text/event-stream")

						roleChunk := response
						roleChunk.Choices[0].Delta = models.Delta{Role: "assistant"}
						roleChunk.Choices[0].FinishReason = nil
						b1, _ := json.Marshal(roleChunk)
						c.Writer.WriteString(fmt.Sprintf("data: %s\n\n", string(b1)))

						b2, _ := json.Marshal(response)
						c.Writer.WriteString(fmt.Sprintf("data: %s\n\n", string(b2)))
						c.Writer.WriteString("data: [DONE]\n\n")
						c.Writer.Flush()
						return
					}

					type nonStreamChoice struct {
						Index        int          `json:"index"`
						Message      models.Delta `json:"message"`
						FinishReason *string      `json:"finish_reason"`
					}
					type nonStreamResponse struct {
						ID      string            `json:"id"`
						Object  string            `json:"object"`
						Created int64             `json:"created"`
						Model   string            `json:"model"`
						Choices []nonStreamChoice `json:"choices"`
					}

					ns := nonStreamResponse{
						ID:      response.ID,
						Object:  "chat.completion",
						Created: response.Created,
						Model:   response.Model,
						Choices: []nonStreamChoice{{
							Index:        0,
							Message:      response.Choices[0].Delta,
							FinishReason: response.Choices[0].FinishReason,
						}},
					}
					c.JSON(http.StatusOK, ns)
					return
				}

				services.GlobalCache.Delete("pending_tools_" + sessionHandle)
			}
		}
	}

	var query string
	historyID := sessionHandle
	if historyID == "" {
		historyID = utils.GenerateID()
	}

	convID := strings.TrimSpace(input.User)
	if convID == "" && sessionHandle != "" {
		if saved, err := services.GetSession(sessionHandle); err == nil && saved != "" {
			convID = saved
		}
	}
	if convID == "" {
		convID = utils.GenerateID()
		auth, authErr := services.GetSelectedAuth()
		if authErr == nil {
			go func(a models.Auth, id, fp string) {
				if err := services.CreateConversation(a, id); err != nil {
					fmt.Printf("CreateConversation async: %v\n", err)
					return
				}
				if fp != "" {
					_ = services.SaveSession(fp, id)
				}
			}(auth, convID, sessionHandle)
		}
		fmt.Printf("Started Xiaomi conversation %s for session %s\n", convID, historyID)
	} else if sessionHandle != "" {
		go func(fp, id string) { _ = services.SaveSession(fp, id) }(sessionHandle, convID)
	}

	messagesCopy := input.Messages
	if historyID != "" {
		if agentMode && utils.AgentFastModeEnabled() {
			go syncConversationMessages(historyID, messagesCopy)
		} else {
			syncConversationMessages(historyID, input.Messages)
		}
		query = buildConversationQuery(input.Messages, toolInstructions)
	} else if len(input.Messages) <= 1 {
		lastMessage := input.Messages[len(input.Messages)-1]
		query = utils.FormatMessageForMiMo(lastMessage)
	} else {
		query = buildConversationQuery(input.Messages, toolInstructions)
	}

	if len(input.Messages) > 1 {
		systemPrefix := ""
		for _, m := range input.Messages {
			if m.Role == "system" {
				systemPrefix = strings.TrimSpace(services.ExtractText(m.Content, false))
				if strings.TrimSpace(toolInstructions) != "" {
					systemPrefix += "\n\n" + strings.TrimSpace(toolInstructions)
				}
				break
			}
		}
		query = truncateConversationQuery(query, systemPrefix, contextLimits.MaxChars)
	}
	fmt.Printf("[%s] Query size: %d chars | agent=%v | messages=%d\n",
		completionID, len(query), agentMode, len(input.Messages))

	targetModel := strings.TrimSpace(input.Model)
	if targetModel == "" {
		targetModel = "mimo-v2.5-pro"
	}

	enableThinking := !strings.Contains(input.Model, "no-thinking")
	if len(input.Tools) > 0 {
		// Com tools, thinking longo costuma gerar só planejamento e finish_reason=stop no Kilo/agent.
		enableThinking = false
		if os.Getenv("AGENT_ENABLE_THINKING") == "true" || os.Getenv("AGENT_ENABLE_THINKING") == "1" {
			enableThinking = !strings.Contains(input.Model, "no-thinking")
		}
	}
	webSearchStatus := "disabled"
	if utils.ShouldEnableWebSearch(targetModel, input.WebSearch, input.Tools) ||
		os.Getenv("DEFAULT_WEB_SEARCH") == "true" || os.Getenv("DEFAULT_WEB_SEARCH") == "1" {
		webSearchStatus = "enabled"
	}

	payload := models.MimoPayload{
		MsgID:          utils.GenerateID(),
		ConversationID: convID,
		Query:          query,
		IsEditedQuery:  false,
		ModelConfig: models.ModelConfig{
			EnableThinking:  enableThinking,
			WebSearchStatus: webSearchStatus,
			Model:           targetModel,
		},
		MultiMedias: []models.MultiMedia{},
	}
	if payload.ConversationID == "" {
		payload.ConversationID = utils.GenerateID()
	}

	maxRetries := 3
	if agentMode {
		maxRetries = 2
	}
	var resp *http.Response
	statToken := ""

	customHeaders := make(map[string]string)
	for k, v := range c.Request.Header {
		customHeaders[strings.ToLower(k)] = v[0]
	}

	for i := 0; i < maxRetries; i++ {
		auth, authErr := services.GetSelectedAuth()
		if authErr != nil {
			utils.SendError(c, http.StatusInternalServerError, "Invalid Xiaomi auth configuration", "server_error", nil)
			return
		}
		statToken = auth.Token

		resp, err = sendMimoChatRequest(auth, payload, customHeaders, completionID)
		if err == nil {
			if resp.StatusCode != http.StatusOK {
				fmt.Printf("Xiaomi returned non-200 status: %d\n", resp.StatusCode)
				if resp.StatusCode >= 500 {
					resp.Body.Close()
					continue
				}
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				c.JSON(resp.StatusCode, gin.H{"error": "Xiaomi API error", "status": resp.StatusCode, "details": string(body)})
				return
			}
			break
		}

		fmt.Printf("Error calling Xiaomi (retry %d): %v\n", i, err)
		if i == maxRetries-1 {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to proxy request", "details": err.Error()})
			return
		}
	}
	if input.Stream && agentMode {
		c.Header("Content-Type", "text/event-stream; charset=utf-8")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		if rc := http.NewResponseController(c.Writer); rc != nil {
			_ = rc.SetWriteDeadline(time.Time{})
		}
		processAgentStreamBuffered(c, resp, payload, customHeaders, completionID, targetModel, historyID, query, statToken, input.ParallelToolCalls, toolChoice, input.Tools)
		return
	}

	if agentMode {
		processAgentNonStreamBuffered(c, resp, payload, customHeaders, completionID, targetModel, cacheKey, historyID, query, statToken, input.ParallelToolCalls, toolChoice, input.Tools)
		return
	}

	defer resp.Body.Close()

	var bodyReader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err == nil {
			defer gz.Close()
			bodyReader = gz
		}
	}

	if input.Stream {
		c.Header("Content-Type", "text/event-stream; charset=utf-8")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		if rc := http.NewResponseController(c.Writer); rc != nil {
			_ = rc.SetWriteDeadline(time.Time{})
		}

		initialChunk := models.ChatCompletionChunk{
			ID:      "chatcmpl-" + completionID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   targetModel,
			Choices: []models.Choice{
				{
					Index: 0,
					Delta: models.Delta{Role: "assistant"},
				},
			},
		}
		initialBytes, _ := json.Marshal(initialChunk)
		c.Writer.WriteString(fmt.Sprintf("data: %s\n\n", string(initialBytes)))
		c.Writer.Flush()

		processStream(c, bodyReader, completionID, targetModel, historyID, query, statToken, input.ParallelToolCalls, agentMode)
		return
	}

	processNonStream(c, bodyReader, completionID, targetModel, cacheKey, historyID, query, statToken, input.ParallelToolCalls, len(input.Tools) == 0, agentMode)
}

func assistantTranscript(content, reasoning string) string {
	reasoning = strings.TrimSpace(reasoning)
	content = strings.TrimSpace(content)
	if reasoning == "" {
		return content
	}
	if content == "" {
		return utils.ThinkingOpenTag + reasoning + utils.ThinkingCloseTag
	}
	return utils.ThinkingOpenTag + reasoning + utils.ThinkingCloseTag + "\n" + content
}

func resolveToolChoice(raw interface{}) string {
	switch v := raw.(type) {
	case string:
		return v
	case map[string]interface{}:
		if fn, ok := v["function"].(map[string]interface{}); ok {
			if name, ok := fn["name"].(string); ok {
				return name
			}
		}
		if t, ok := v["type"].(string); ok {
			return t
		}
	}
	return ""
}

// handleCompletions exposes the legacy OpenAI completions API by mapping prompt -> chat messages.
func handleCompletions(c *gin.Context) {
	bodyCopy, err := io.ReadAll(c.Request.Body)
	if err != nil {
		utils.SendError(c, http.StatusBadRequest, "Failed to read request body", "invalid_request_error", nil)
		return
	}

	var legacy struct {
		Model       string      `json:"model"`
		Prompt      interface{} `json:"prompt"`
		Stream      bool        `json:"stream"`
		MaxTokens   int         `json:"max_tokens"`
		Temperature float64     `json:"temperature"`
		User        string      `json:"user"`
	}
	if err := json.Unmarshal(bodyCopy, &legacy); err != nil {
		utils.SendError(c, http.StatusBadRequest, "Invalid request body", "invalid_request_error", nil)
		return
	}

	promptText := ""
	switch p := legacy.Prompt.(type) {
	case string:
		promptText = p
	case []interface{}:
		var parts []string
		for _, item := range p {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			}
		}
		promptText = strings.Join(parts, "\n")
	}

	if strings.TrimSpace(promptText) == "" {
		utils.SendError(c, http.StatusBadRequest, "prompt is required", "invalid_request_error", nil)
		return
	}

	chatBody := map[string]interface{}{
		"model":    legacy.Model,
		"stream":   legacy.Stream,
		"user":     legacy.User,
		"messages": []models.Message{{Role: "user", Content: promptText}},
	}
	chatBytes, _ := json.Marshal(chatBody)
	c.Request.Body = io.NopCloser(bytes.NewBuffer(chatBytes))
	c.Request.ContentLength = int64(len(chatBytes))
	handleChatCompletions(c)
}

func processStream(c *gin.Context, body io.Reader, completionID, model string, userID string, query string, statToken string, parallelToolCalls *bool, agentMode bool) {
	reader := bufio.NewReaderSize(body, 16*1024*1024)

	var inThinking bool
	var inToolCall bool
	var sentToolCallName bool
	var emittedToolCall bool
	var currentToolID string
	var toolCallIndex int
	var toolCallBuffer strings.Builder
	var fullText strings.Builder
	var reasoningText strings.Builder
	var usage models.Usage
	var eventType string
	var upstreamErr error

	streamDone := false
	defer func() {
		if streamDone {
			return
		}
		finishReason := "stop"
		if _, tcs := utils.ParseToolCalls(fullText.String()); len(tcs) > 0 {
			finishReason = "tool_calls"
		}
		utils.FinalizeChatStream(c, completionID, model, finishReason, &usage)
		if upstreamErr != nil {
			fmt.Printf("[%s] Stream recovered after upstream error: %v\n", completionID, upstreamErr)
		}
	}()

	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			if strings.HasPrefix(line, "event:") {
				eventType = strings.TrimSpace(line[6:])
			} else if strings.HasPrefix(line, "data:") {
				dataStr := strings.TrimSpace(line[5:])
				processEvent(c, eventType, dataStr, completionID, model, true, &inThinking, &inToolCall, &sentToolCallName, &currentToolID, &toolCallIndex, &toolCallBuffer, &fullText, &reasoningText, &usage)
				eventType = ""
			}
		}
		if err != nil {
			if err != io.EOF {
				upstreamErr = err
				fmt.Printf("[%s] Upstream reader error: %v\n", completionID, err)
			}
			break
		}
	}

	if inThinking {
		inThinking = false
	}

	if inToolCall && toolCallBuffer.Len() > 0 {
		fullText.WriteString(utils.ToolCallOpenTag)
		fullText.WriteString(toolCallBuffer.String())
		fullText.WriteString(utils.ToolCallCloseTag)

		if _, parsedToolCalls := utils.ParseToolCalls(utils.ToolCallOpenTag + toolCallBuffer.String() + utils.ToolCallCloseTag); len(parsedToolCalls) > 0 {
			parsedToolCalls = utils.AssignToolCallIndexes(parsedToolCalls)
			parsedToolCalls[0].Index = toolCallIndex
			utils.EmitStreamToolCall(c, completionID, model, parsedToolCalls[0])
			emittedToolCall = true
		}
	}

	_, toolCalls := utils.ParseToolCalls(fullText.String())
	toolCalls = finalizeToolCalls(toolCalls)
	responseCalls := responseToolCalls(toolCalls, parallelToolCalls, agentMode)

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}

	if usage.TotalTokens == 0 {
		usage.PromptTokens = len(query) / 4
		usage.CompletionTokens = len(fullText.String()) / 4
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	IncrementTokenStat(statToken, usage.TotalTokens)

	services.SaveMessage(userID, "asst_"+completionID, "assistant", assistantTranscript(fullText.String(), reasoningText.String()))

	if finishReason == "tool_calls" {
		if !emittedToolCall && len(responseCalls) > 0 {
			for _, tc := range responseCalls {
				utils.EmitStreamToolCall(c, completionID, model, tc)
			}
		}
		storePendingToolCalls(userID, toolCalls)
	}
	streamDone = true
	utils.FinalizeChatStream(c, completionID, model, finishReason, &usage)
	if upstreamErr != nil {
		fmt.Printf("[%s] Stream completed after upstream error: %v\n", completionID, upstreamErr)
	}
}

func finalizeToolCalls(toolCalls []models.ToolCall) []models.ToolCall {
	return utils.AssignToolCallIndexes(toolCalls)
}

func responseToolCalls(toolCalls []models.ToolCall, parallelToolCalls *bool, agentMode bool) []models.ToolCall {
	if parallelToolCalls != nil && !*parallelToolCalls && len(toolCalls) > 1 {
		return toolCalls[:1]
	}
	if agentMode && !utils.AgentSequentialToolsEnabled() {
		return toolCalls
	}
	return toolCalls
}

func storePendingToolCalls(sessionID string, toolCalls []models.ToolCall) {
	if utils.AgentSequentialToolsEnabled() {
		if sessionID == "" || len(toolCalls) <= 1 {
			return
		}
		services.GlobalCache.Set("pending_tools_"+sessionID, toolCalls[1:], 10*time.Minute)
	}
}

type parsedMimoChat struct {
	FullText      string
	CleanText     string
	ReasoningText string
	ToolCalls     []models.ToolCall
	ResponseCalls []models.ToolCall
	FinishReason  string
	Usage         models.Usage
	Query         string
}

func parseMimoHTTPResponse(resp *http.Response, completionID, model, query string, parallelToolCalls *bool, agentMode bool) (parsedMimoChat, error) {
	defer resp.Body.Close()

	var bodyReader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return parsedMimoChat{}, err
		}
		defer gz.Close()
		bodyReader = gz
	}

	return parseMimoChatBody(bodyReader, completionID, model, query, parallelToolCalls, agentMode), nil
}

func parseMimoChatBody(body io.Reader, completionID, model, query string, parallelToolCalls *bool, agentMode bool) parsedMimoChat {
	respBody, _ := io.ReadAll(body)
	events := strings.Split(string(respBody), "\n\n")

	var inThinking bool
	var inToolCall bool
	var sentToolCallName bool
	var currentToolID string
	var toolCallIndex int
	var toolCallBuffer strings.Builder
	var fullText strings.Builder
	var reasoningText strings.Builder
	var usage models.Usage

	for _, event := range events {
		if strings.TrimSpace(event) == "" {
			continue
		}

		lines := strings.Split(event, "\n")
		var eventType string
		var dataStr string
		for _, line := range lines {
			if strings.HasPrefix(line, "event:") {
				eventType = strings.TrimSpace(line[6:])
			} else if strings.HasPrefix(line, "data:") {
				dataStr = strings.TrimSpace(line[5:])
			}
		}
		if dataStr != "" {
			processEvent(nil, eventType, dataStr, completionID, model, false, &inThinking, &inToolCall, &sentToolCallName, &currentToolID, &toolCallIndex, &toolCallBuffer, &fullText, &reasoningText, &usage)
		}
	}

	if inToolCall && toolCallBuffer.Len() > 0 {
		fullText.WriteString(utils.ToolCallOpenTag)
		fullText.WriteString(toolCallBuffer.String())
		fullText.WriteString(utils.ToolCallCloseTag)
	}

	cleanText, toolCalls := utils.ParseToolCalls(fullText.String())
	cleanText = strings.TrimSpace(cleanText)
	toolCalls = finalizeToolCalls(toolCalls)
	responseCalls := responseToolCalls(toolCalls, parallelToolCalls, agentMode)

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
		cleanText = ""
	}

	if usage.TotalTokens == 0 {
		usage.PromptTokens = len(query) / 4
		usage.CompletionTokens = fullText.Len() / 4
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	return parsedMimoChat{
		FullText:      fullText.String(),
		CleanText:     cleanText,
		ReasoningText: strings.TrimSpace(reasoningText.String()),
		ToolCalls:     toolCalls,
		ResponseCalls: responseCalls,
		FinishReason:  finishReason,
		Usage:         usage,
		Query:         query,
	}
}

func processAgentStreamBuffered(c *gin.Context, firstResp *http.Response, payload models.MimoPayload, customHeaders map[string]string, completionID, model, userID, query, statToken string, parallelToolCalls *bool, toolChoice string, tools []models.Tool) {
	initialChunk := models.ChatCompletionChunk{
		ID:      "chatcmpl-" + completionID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []models.Choice{
			{
				Index: 0,
				Delta: models.Delta{Role: "assistant"},
			},
		},
	}
	initialBytes, _ := json.Marshal(initialChunk)
	c.Writer.WriteString(fmt.Sprintf("data: %s\n\n", string(initialBytes)))
	c.Writer.Flush()

	result, statToken := runAgentSemanticRetries(firstResp, payload, customHeaders, completionID, model, query, statToken, parallelToolCalls, toolChoice, tools)
	IncrementTokenStat(statToken, result.Usage.TotalTokens)
	services.SaveMessage(userID, "asst_"+completionID, "assistant", assistantTranscript(result.FullText, result.ReasoningText))

	if result.FinishReason == "tool_calls" {
		for _, tc := range result.ResponseCalls {
			utils.EmitStreamToolCall(c, completionID, model, tc)
		}
		storePendingToolCalls(userID, result.ToolCalls)
		utils.FinalizeChatStream(c, completionID, model, "tool_calls", &result.Usage)
		return
	}

	if strings.TrimSpace(result.CleanText) != "" {
		chunk := utils.CreateChatCompletionChunk(completionID, result.CleanText, model, nil, "", nil, nil)
		b, _ := json.Marshal(chunk)
		c.Writer.WriteString(fmt.Sprintf("data: %s\n\n", string(b)))
		c.Writer.Flush()
	}
	utils.FinalizeChatStream(c, completionID, model, "stop", &result.Usage)
}

func processAgentNonStreamBuffered(c *gin.Context, firstResp *http.Response, payload models.MimoPayload, customHeaders map[string]string, completionID, model, cacheKey, userID, query, statToken string, parallelToolCalls *bool, toolChoice string, tools []models.Tool) {
	result, statToken := runAgentSemanticRetries(firstResp, payload, customHeaders, completionID, model, query, statToken, parallelToolCalls, toolChoice, tools)
	IncrementTokenStat(statToken, result.Usage.TotalTokens)
	services.SaveMessage(userID, "asst_"+completionID, "assistant", assistantTranscript(result.FullText, result.ReasoningText))
	if result.FinishReason == "tool_calls" {
		storePendingToolCalls(userID, result.ToolCalls)
	}
	responseResult := result
	responseResult.ReasoningText = ""
	writeNonStreamResponse(c, completionID, model, responseResult)
	_ = cacheKey
}

func runAgentSemanticRetries(firstResp *http.Response, payload models.MimoPayload, customHeaders map[string]string, completionID, model, query, statToken string, parallelToolCalls *bool, toolChoice string, tools []models.Tool) (parsedMimoChat, string) {
	currentResp := firstResp
	currentPayload := payload
	currentQuery := query
	last := parsedMimoChat{
		FinishReason: "stop",
		Query:        query,
		Usage: models.Usage{
			PromptTokens: len(query) / 4,
			TotalTokens:  len(query) / 4,
		},
	}

	const maxAgentSemanticAttempts = 4
	for attempt := 0; attempt < maxAgentSemanticAttempts; attempt++ {
		parsed, err := parseMimoHTTPResponse(currentResp, completionID, model, currentQuery, parallelToolCalls, true)
		if err != nil {
			fmt.Printf("[%s] Failed to parse Xiaomi response: %v\n", completionID, err)
			return last, statToken
		}
		last = parsed

		needsRetry := shouldRetryAgentToolCall(parsed, toolChoice)
		if !needsRetry {
			return parsed, statToken
		}
		if attempt == maxAgentSemanticAttempts-1 {
			if synthesized := synthesizePathReadToolCalls(parsed, tools, parallelToolCalls); len(synthesized.ToolCalls) > 0 {
				return synthesized, statToken
			}
			return parsed, statToken
		}

		currentQuery = buildAgentToolRetryQuery(payload.Query, parsed)
		currentPayload.Query = currentQuery
		currentPayload.MsgID = utils.GenerateID()

		auth, authErr := services.GetSelectedAuth()
		if authErr != nil {
			fmt.Printf("[%s] Agent semantic retry auth error: %v\n", completionID, authErr)
			return parsed, statToken
		}
		statToken = auth.Token

		resp, reqErr := sendMimoChatRequest(auth, currentPayload, customHeaders, completionID)
		if reqErr != nil {
			fmt.Printf("[%s] Agent semantic retry request error: %v\n", completionID, reqErr)
			return parsed, statToken
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			fmt.Printf("[%s] Agent semantic retry non-200: %d - %s\n", completionID, resp.StatusCode, string(body))
			return parsed, statToken
		}
		currentResp = resp
	}

	return last, statToken
}

func synthesizePathReadToolCalls(result parsedMimoChat, tools []models.Tool, parallelToolCalls *bool) parsedMimoChat {
	paths := extractPathOnlyResponse(result.CleanText)
	if len(paths) == 0 {
		paths = extractReadCommandPaths(result.CleanText)
	}
	if len(paths) == 0 {
		return parsedMimoChat{}
	}

	toolName, argName := selectReadTool(tools)
	if toolName == "" {
		return parsedMimoChat{}
	}

	calls := make([]models.ToolCall, 0, len(paths))
	for _, path := range paths {
		args, _ := json.Marshal(map[string]string{argName: path})
		calls = append(calls, models.ToolCall{
			ID:   "call_" + utils.GenerateID(),
			Type: "function",
			Function: models.ToolFunction{
				Name:      toolName,
				Arguments: string(args),
			},
		})
	}
	calls = finalizeToolCalls(calls)

	result.CleanText = ""
	result.ToolCalls = calls
	result.ResponseCalls = responseToolCalls(calls, parallelToolCalls, true)
	result.FinishReason = "tool_calls"
	return result
}

func extractReadCommandPaths(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	lowered := strings.ToLower(text)
	readMarkers := []string{"sed -n ", "cat ", "head ", "tail "}
	hasReadCommand := false
	for _, marker := range readMarkers {
		if strings.Contains(lowered, marker) {
			hasReadCommand = true
			break
		}
	}
	if !hasReadCommand {
		return nil
	}

	matches := agentPathLocationRegex.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	paths := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) <= 1 || seen[match[1]] {
			continue
		}
		seen[match[1]] = true
		paths = append(paths, match[1])
	}
	return paths
}

func extractPathOnlyResponse(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	matches := agentPathLocationRegex.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}

	remaining := agentPathLocationRegex.ReplaceAllString(text, "")
	remaining = strings.TrimSpace(strings.Trim(remaining, ",;|`'\"()[]{}"))
	if remaining != "" {
		return nil
	}

	paths := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			paths = append(paths, match[1])
		}
	}
	return paths
}

func selectReadTool(tools []models.Tool) (string, string) {
	preferredNames := []string{"read", "read_file", "readFile", "open_file", "view_file"}
	for _, preferred := range preferredNames {
		for _, tool := range tools {
			if tool.Type == "function" && tool.Function.Name == preferred {
				return tool.Function.Name, selectPathArgumentName(tool)
			}
		}
	}

	for _, tool := range tools {
		if tool.Type != "function" {
			continue
		}
		name := strings.ToLower(tool.Function.Name)
		if strings.Contains(name, "read") || strings.Contains(name, "open") || strings.Contains(name, "view") {
			return tool.Function.Name, selectPathArgumentName(tool)
		}
	}
	return "", ""
}

func selectPathArgumentName(tool models.Tool) string {
	var schema map[string]interface{}
	b, err := json.Marshal(tool.Function.Parameters)
	if err == nil {
		_ = json.Unmarshal(b, &schema)
	}

	if props, ok := schema["properties"].(map[string]interface{}); ok {
		for _, candidate := range []string{"filePath", "path", "absolutePath", "filename", "file"} {
			if _, ok := props[candidate]; ok {
				return candidate
			}
		}
	}
	return "filePath"
}

func shouldRetryAgentToolCall(result parsedMimoChat, toolChoice string) bool {
	if len(result.ToolCalls) > 0 {
		return false
	}
	choice := strings.ToLower(strings.TrimSpace(toolChoice))
	if choice == "none" {
		return false
	}
	if choice == "required" || choice == "any" || (choice != "" && choice != "auto") {
		return true
	}

	rawClean := strings.TrimSpace(result.CleanText)
	if agentLocationOnlyRegex.MatchString(rawClean) || len(extractPathOnlyResponse(rawClean)) > 0 || len(extractReadCommandPaths(rawClean)) > 0 {
		return true
	}

	clean := strings.ToLower(rawClean)
	reasoning := strings.TrimSpace(result.ReasoningText)
	if clean == "" && reasoning != "" {
		return true
	}

	actionOnlyMarkers := []string{
		"let me start",
		"i'll start",
		"i will start",
		"i need to inspect",
		"i need to read",
		"vou começar",
		"vou iniciar",
		"vou construir",
		"vou implementar",
		"vou criar",
		"vou modificar",
		"vou ajustar",
		"vou corrigir",
		"vou verificar",
		"vou ler",
		"vou abrir",
		"vou editar",
		"vamos começar",
		"preciso verificar",
		"preciso ler",
		"começar constru",
	}
	for _, marker := range actionOnlyMarkers {
		if strings.Contains(clean, marker) {
			return true
		}
	}
	return false
}

func buildAgentToolRetryQuery(originalQuery string, result parsedMimoChat) string {
	var sb strings.Builder
	sb.WriteString(originalQuery)
	sb.WriteString("\n\n# Adapter correction\n")
	sb.WriteString("Your previous response did not contain a tool call, so the IDE client could not continue the work.\n")
	sb.WriteString("If work remains, respond with exactly one <tool_call>{\"name\":\"...\",\"arguments\":{...}}</tool_call> block using one available tool. Do not write prose, do not output only a file location, and do not use Markdown fences.\n")
	sb.WriteString("Only provide a final answer when all requested work is complete.\n")
	if strings.TrimSpace(result.CleanText) != "" {
		sb.WriteString("Previous non-action response: ")
		sb.WriteString(strings.TrimSpace(result.CleanText))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func writeNonStreamResponse(c *gin.Context, completionID, model string, result parsedMimoChat) {
	type nonStreamChoice struct {
		Index        int          `json:"index"`
		Message      models.Delta `json:"message"`
		FinishReason *string      `json:"finish_reason"`
	}
	type nonStreamResponse struct {
		ID      string            `json:"id"`
		Object  string            `json:"object"`
		Created int64             `json:"created"`
		Model   string            `json:"model"`
		Choices []nonStreamChoice `json:"choices"`
		Usage   *models.Usage     `json:"usage"`
	}

	response := nonStreamResponse{
		ID:      "chatcmpl-" + completionID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []nonStreamChoice{
			{
				Index: 0,
				Message: models.Delta{
					Role:             "assistant",
					Content:          result.CleanText,
					ReasoningContent: result.ReasoningText,
					ToolCalls:        result.ResponseCalls,
				},
				FinishReason: &result.FinishReason,
			},
		},
		Usage: &result.Usage,
	}
	c.JSON(http.StatusOK, response)
}

func processNonStream(c *gin.Context, body io.Reader, completionID, model string, cacheKey string, userID string, query string, statToken string, parallelToolCalls *bool, allowResponseCache bool, agentMode bool) {
	respBody, _ := io.ReadAll(body)
	events := strings.Split(string(respBody), "\n\n")

	var inThinking bool
	var inToolCall bool
	var sentToolCallName bool
	var currentToolID string
	var toolCallIndex int
	var toolCallBuffer strings.Builder
	var fullText strings.Builder
	var reasoningText strings.Builder
	var usage models.Usage

	for _, event := range events {
		if strings.TrimSpace(event) == "" {
			continue
		}

		lines := strings.Split(event, "\n")
		var eventType string
		var dataStr string
		for _, line := range lines {
			if strings.HasPrefix(line, "event:") {
				eventType = strings.TrimSpace(line[6:])
			} else if strings.HasPrefix(line, "data:") {
				dataStr = strings.TrimSpace(line[5:])
			}
		}
		if dataStr != "" {
			processEvent(c, eventType, dataStr, completionID, model, false, &inThinking, &inToolCall, &sentToolCallName, &currentToolID, &toolCallIndex, &toolCallBuffer, &fullText, &reasoningText, &usage)
		}
	}

	if inThinking {
		inThinking = false
	}

	if inToolCall && toolCallBuffer.Len() > 0 {
		fullText.WriteString(utils.ToolCallOpenTag)
		fullText.WriteString(toolCallBuffer.String())
		fullText.WriteString(utils.ToolCallCloseTag)
	}

	cleanText, toolCalls := utils.ParseToolCalls(fullText.String())
	cleanText = strings.TrimSpace(cleanText)
	toolCalls = finalizeToolCalls(toolCalls)
	responseCalls := responseToolCalls(toolCalls, parallelToolCalls, agentMode)

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
		cleanText = ""
		storePendingToolCalls(userID, toolCalls)
	}

	if usage.TotalTokens == 0 {
		usage.PromptTokens = len(query) / 4
		usage.CompletionTokens = fullText.Len() / 4
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	IncrementTokenStat(statToken, usage.TotalTokens)

	type nonStreamChoice struct {
		Index        int          `json:"index"`
		Message      models.Delta `json:"message"`
		FinishReason *string      `json:"finish_reason"`
	}
	type nonStreamResponse struct {
		ID      string            `json:"id"`
		Object  string            `json:"object"`
		Created int64             `json:"created"`
		Model   string            `json:"model"`
		Choices []nonStreamChoice `json:"choices"`
		Usage   *models.Usage     `json:"usage"`
	}

	response := nonStreamResponse{
		ID:      "chatcmpl-" + completionID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []nonStreamChoice{
			{
				Index: 0,
				Message: models.Delta{
					Role:             "assistant",
					Content:          cleanText,
					ReasoningContent: strings.TrimSpace(reasoningText.String()),
					ToolCalls:        responseCalls,
				},
				FinishReason: &finishReason,
			},
		},
		Usage: &usage,
	}

	services.SaveMessage(userID, "asst_"+completionID, "assistant", assistantTranscript(fullText.String(), reasoningText.String()))
	if allowResponseCache {
		services.GlobalCache.Set(cacheKey, response, 5*time.Minute)
	}
	c.JSON(http.StatusOK, response)
}

func processEvent(c *gin.Context, eventType, dataStr, completionID, model string, isStreaming bool, inThinking, inToolCall, sentToolCallName *bool, currentToolID *string, toolCallIndex *int, toolCallBuffer, fullText, reasoningText *strings.Builder, usage *models.Usage) {
	if eventType == "usage" {
		var u struct {
			PromptTokens     int `json:"promptTokens"`
			CompletionTokens int `json:"completionTokens"`
			TotalTokens      int `json:"totalTokens"`
			NativeUsage      struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"nativeUsage"`
		}
		if err := json.Unmarshal([]byte(dataStr), &u); err == nil {
			if u.PromptTokens > 0 {
				usage.PromptTokens = u.PromptTokens
				usage.CompletionTokens = u.CompletionTokens
				usage.TotalTokens = u.TotalTokens
			} else {
				usage.PromptTokens = u.NativeUsage.PromptTokens
				usage.CompletionTokens = u.NativeUsage.CompletionTokens
				usage.TotalTokens = u.NativeUsage.TotalTokens
			}
		}
		return
	}

	if eventType != "message" {
		return
	}

	var d struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(dataStr), &d); err != nil {
		return
	}

	content := strings.ReplaceAll(d.Content, "\x00", "")
	remaining := content

	for len(remaining) > 0 {
		if *inThinking {
			endIdx := strings.Index(remaining, utils.ThinkingCloseTag)
			if endIdx != -1 {
				text := remaining[:endIdx]
				reasoningText.WriteString(text)
				if isStreaming {
					chunk := utils.CreateChatCompletionChunk(completionID, "", model, nil, text, nil, nil)
					b, _ := json.Marshal(chunk)
					c.Writer.WriteString(fmt.Sprintf("data: %s\n\n", string(b)))
					c.Writer.Flush()
				}
				*inThinking = false
				remaining = remaining[endIdx+len(utils.ThinkingCloseTag):]
			} else {
				reasoningText.WriteString(remaining)
				if isStreaming {
					chunk := utils.CreateChatCompletionChunk(completionID, "", model, nil, remaining, nil, nil)
					b, _ := json.Marshal(chunk)
					c.Writer.WriteString(fmt.Sprintf("data: %s\n\n", string(b)))
					c.Writer.Flush()
				}
				remaining = ""
			}
			continue
		}

		if *inToolCall {
			endIdx := strings.Index(remaining, utils.ToolCallCloseTag)
			contentToProcess := remaining
			if endIdx != -1 {
				contentToProcess = remaining[:endIdx]
			}

			toolCallBuffer.WriteString(contentToProcess)

			if endIdx != -1 {
				rawToolCall := "<tool_call>" + toolCallBuffer.String() + "</tool_call>"
				fullText.WriteString("<tool_call>")
				fullText.WriteString(toolCallBuffer.String())
				fullText.WriteString("</tool_call>")

				if isStreaming {
					_, parsedToolCalls := utils.ParseToolCalls(rawToolCall)
					if len(parsedToolCalls) > 0 {
						parsedToolCalls = utils.AssignToolCallIndexes(parsedToolCalls)
						parsedToolCalls[0].Index = *toolCallIndex
						utils.EmitStreamToolCall(c, completionID, model, parsedToolCalls[0])
					}
				}

				*inToolCall = false
				*sentToolCallName = false
				*currentToolID = ""
				*toolCallIndex++
				toolCallBuffer.Reset()
				remaining = remaining[endIdx+len(utils.ToolCallCloseTag):]
			} else {
				remaining = ""
			}
			continue
		}

		thinkStartIdx := strings.Index(remaining, utils.ThinkingOpenTag)
		toolStartIdx := strings.Index(remaining, utils.ToolCallOpenTag)

		if thinkStartIdx != -1 && (toolStartIdx == -1 || thinkStartIdx < toolStartIdx) {
			text := remaining[:thinkStartIdx]
			if strings.TrimSpace(strings.Trim(text, ",")) != "" {
				fullText.WriteString(text)
			}
			if isStreaming && strings.TrimSpace(strings.Trim(text, ",")) != "" {
				chunk := utils.CreateChatCompletionChunk(completionID, text, model, nil, "", nil, nil)
				b, _ := json.Marshal(chunk)
				c.Writer.WriteString(fmt.Sprintf("data: %s\n\n", string(b)))
				c.Writer.Flush()
			}
			*inThinking = true
			remaining = remaining[thinkStartIdx+len(utils.ThinkingOpenTag):]
			continue
		}

		if toolStartIdx != -1 {
			text := remaining[:toolStartIdx]
			if strings.TrimSpace(strings.Trim(text, ",")) != "" {
				fullText.WriteString(text)
			}
			if isStreaming && strings.TrimSpace(strings.Trim(text, ",")) != "" {
				chunk := utils.CreateChatCompletionChunk(completionID, text, model, nil, "", nil, nil)
				b, _ := json.Marshal(chunk)
				c.Writer.WriteString(fmt.Sprintf("data: %s\n\n", string(b)))
				c.Writer.Flush()
			}
			*inToolCall = true
			toolCallBuffer.Reset()
			remaining = remaining[toolStartIdx+len(utils.ToolCallOpenTag):]
			continue
		}

		fullText.WriteString(remaining)
		if isStreaming {
			chunk := utils.CreateChatCompletionChunk(completionID, remaining, model, nil, "", nil, nil)
			b, _ := json.Marshal(chunk)
			c.Writer.WriteString(fmt.Sprintf("data: %s\n\n", string(b)))
			c.Writer.Flush()
		}
		remaining = ""
	}
}
