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
		v1.GET("/chat/history/:conversationId", handleGetHistory)
	}

	r.POST("/open-apis/bot/chat", handleDirectProxy)
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

func buildEffectiveMessages(localMessages []models.Message, requestMessages []models.Message) []models.Message {
	filteredRequest := make([]models.Message, 0, len(requestMessages))
	for _, message := range requestMessages {
		if message.Role == "system" {
			filteredRequest = append(filteredRequest, message)
			continue
		}
		if strings.TrimSpace(utils.FormatMessageForMiMo(message)) == "" {
			continue
		}
		filteredRequest = append(filteredRequest, message)
	}

	if len(localMessages) == 0 {
		return filteredRequest
	}

	filteredLocal := make([]models.Message, 0, len(localMessages))
	for _, local := range localMessages {
		if local.Role == "system" {
			continue
		}
		if strings.TrimSpace(utils.FormatMessageForMiMo(local)) == "" {
			continue
		}
		filteredLocal = append(filteredLocal, local)
	}

	if len(filteredLocal) == 0 {
		return filteredRequest
	}

	overlap := 0
	maxOverlap := len(filteredLocal)
	if len(filteredRequest) < maxOverlap {
		maxOverlap = len(filteredRequest)
	}
	for n := maxOverlap; n >= 1; n-- {
		match := true
		for i := 0; i < n; i++ {
			if filteredLocal[len(filteredLocal)-n+i].Role != filteredRequest[i].Role ||
				utils.FormatMessageForMiMo(filteredLocal[len(filteredLocal)-n+i]) != utils.FormatMessageForMiMo(filteredRequest[i]) {
				match = false
				break
			}
		}
		if match {
			overlap = n
			break
		}
	}

	merged := append([]models.Message{}, filteredLocal[:len(filteredLocal)-overlap]...)
	merged = append(merged, filteredRequest...)
	return merged
}

func buildConversationQuery(messages []models.Message, toolInstructions string) string {
	var processedMessages []string
	var systemPrompt string

	for _, message := range messages {
		if message.Role == "system" {
			systemPrompt = services.ExtractText(message.Content, false) + toolInstructions
			break
		}
	}

	for _, message := range messages {
		if message.Role == "system" {
			continue
		}
		formatted := utils.FormatMessageForMiMo(message)
		if strings.TrimSpace(formatted) != "" {
			processedMessages = append(processedMessages, formatted)
		}
	}

	if systemPrompt != "" {
		return systemPrompt + "\n\n" + strings.Join(processedMessages, "\n\n")
	}
	if toolInstructions != "" {
		return strings.TrimSpace(toolInstructions) + "\n\n" + strings.Join(processedMessages, "\n\n")
	}
	return strings.Join(processedMessages, "\n\n")
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

	var streamProbe struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(bodyCopy, &streamProbe)

	if !streamProbe.Stream {
		if cached, found := services.GlobalCache.Get(cacheKey); found {
			c.JSON(http.StatusOK, cached)
			return
		}
	}

	var input struct {
		Messages  []models.Message `json:"messages"`
		Model     string           `json:"model"`
		Stream    bool             `json:"stream"`
		User      string           `json:"user"`
		Tools     []models.Tool    `json:"tools"`
		WebSearch bool             `json:"web_search"`
	}

	if err = c.ShouldBindJSON(&input); err != nil {
		utils.SendError(c, http.StatusBadRequest, "Invalid request body", "invalid_request_error", nil)
		return
	}

	if len(input.Messages) == 0 {
		utils.SendError(c, http.StatusBadRequest, "Messages array is required and cannot be empty", "invalid_request_error", nil)
		return
	}

	toolInstructions := utils.FormatToolsAsInstructions(input.Tools)

	if input.User != "" {
		if pending, found := services.GlobalCache.Get("pending_tools_" + input.User); found {
			if pendingTools, ok := pending.([]models.ToolCall); ok && len(pendingTools) > 0 {
				lastMsg := input.Messages[len(input.Messages)-1]
				if lastMsg.Role == "tool" {
					nextTool := pendingTools[0]
					remaining := pendingTools[1:]
					if len(remaining) > 0 {
						services.GlobalCache.Set("pending_tools_"+input.User, remaining, 10*time.Minute)
					} else {
						services.GlobalCache.Delete("pending_tools_" + input.User)
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

				services.GlobalCache.Delete("pending_tools_" + input.User)
			}
		}
	}

	var query string
	convID := input.User

	if convID == "" && len(input.Messages) > 0 {
		sessionKey := services.GenerateFingerprint(input.Messages)
		if cachedID, found := services.GlobalCache.Get(sessionKey); found {
			convID = cachedID.(string)
			fmt.Printf("Detected existing session via fingerprint: %s\n", convID)
		} else if storedID, err := services.GetSession(sessionKey); err == nil && storedID != "" {
			convID = storedID
			services.GlobalCache.Set(sessionKey, convID, 24*time.Hour)
			fmt.Printf("Detected existing session via database fingerprint: %s\n", convID)
		} else {
			convID = utils.GenerateID()
			services.GlobalCache.Set(sessionKey, convID, 24*time.Hour)
			_ = services.SaveSession(sessionKey, convID)
			auth, authErr := services.GetSelectedAuth()
			if authErr == nil {
				if err := services.CreateConversation(auth, convID); err != nil {
					fmt.Printf("Failed to register conversation with Xiaomi: %v\n", err)
				}
			}
			fmt.Printf("Started and registered new session for fingerprint: %s\n", convID)
		}
	}

	if convID != "" {
		localMsgs, _ := services.GetLocalHistory(convID)
		if len(localMsgs) == 0 {
			auth, authErr := services.GetSelectedAuth()
			if authErr == nil {
				_ = services.CreateConversation(auth, convID)
				remoteHistory, err := services.GetConversationHistory(auth, convID)
				if err == nil && len(remoteHistory) > 0 {
					for _, item := range remoteHistory {
						services.SaveMessage(convID, item.MsgID+"_u", "user", item.InputInfo.Query)
						if len(item.DialogLogDetailList) > 0 {
							services.SaveMessage(convID, item.MsgID+"_a", "assistant", item.DialogLogDetailList[0].Result)
						}
					}
				}
			}
			localMsgs, _ = services.GetLocalHistory(convID)
		}

		syncConversationMessages(convID, input.Messages)
		effectiveMessages := buildEffectiveMessages(localMsgs, input.Messages)
		if len(effectiveMessages) == 0 {
			effectiveMessages = input.Messages
		}
		query = buildConversationQuery(effectiveMessages, toolInstructions)
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
				systemPrefix = services.ExtractText(m.Content, false) + toolInstructions
				break
			}
		}
		query = truncateConversationQuery(query, systemPrefix, 4000000)
	}

	targetModel := strings.TrimSpace(input.Model)
	if targetModel == "" {
		targetModel = "mimo-v2.5-pro"
	}

	enableThinking := !strings.Contains(input.Model, "no-thinking")
	webSearchStatus := "disabled"
	if strings.Contains(input.Model, "search") || input.WebSearch {
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
	var resp *http.Response

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
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")

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

		processStream(c, bodyReader, completionID, targetModel, payload.ConversationID, query, input.Tools)
		return
	}

	processNonStream(c, bodyReader, completionID, targetModel, cacheKey, payload.ConversationID, query, input.Tools)
}

func processStream(c *gin.Context, body io.Reader, completionID, model string, userID string, query string, availableTools []models.Tool) {
	reader := bufio.NewReaderSize(body, 16*1024*1024)

	var inThinking bool
	var inToolCall bool
	var sentToolCallName bool
	var currentToolID string
	var toolCallIndex int
	var toolCallBuffer strings.Builder
	var fullText strings.Builder
	var reasoningText strings.Builder
	var usage models.Usage
	var eventType string

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				fmt.Printf("Reader error: %v\n", err)
			}
			break
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(line[6:])
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataStr := strings.TrimSpace(line[5:])
			processEvent(c, eventType, dataStr, completionID, model, true, &inThinking, &inToolCall, &sentToolCallName, &currentToolID, &toolCallIndex, &toolCallBuffer, &fullText, &reasoningText, &usage)
			eventType = ""
		}
	}

	if inToolCall && toolCallBuffer.Len() > 0 {
		fullText.WriteString("<tool_call>")
		fullText.WriteString(toolCallBuffer.String())
		fullText.WriteString("</tool_call>")

		if _, parsedToolCalls := utils.ParseToolCalls("<tool_call>" + toolCallBuffer.String() + "</tool_call>"); len(parsedToolCalls) > 0 {
			parsedToolCalls[0].Index = toolCallIndex
			if parsedToolCalls[0].ID == "" {
				parsedToolCalls[0].ID = "call_" + utils.GenerateID()
			}
			if parsedToolCalls[0].Type == "" {
				parsedToolCalls[0].Type = "function"
			}
			chunk := utils.CreateChatCompletionChunk(completionID, "", model, nil, "", nil, []models.ToolCall{parsedToolCalls[0]})
			b, _ := json.Marshal(chunk)
			c.Writer.WriteString(fmt.Sprintf("data: %s\n\n", string(b)))
			c.Writer.Flush()
		}
	}

	var toolCalls []models.ToolCall
	if len(availableTools) > 0 {
		_, toolCalls = utils.ParseToolCalls(fullText.String())
	}

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}

	if usage.TotalTokens == 0 {
		usage.PromptTokens = len(query) / 4
		usage.CompletionTokens = len(fullText.String()) / 4
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	IncrementTokenStat(os.Getenv("SERVICE_TOKEN"), usage.TotalTokens)

	services.SaveMessage(userID, "asst_"+completionID, "assistant", fullText.String())

	finalChunk := utils.CreateChatCompletionChunk(completionID, "", model, &finishReason, "", &usage, nil)
	finalBytes, _ := json.Marshal(finalChunk)
	c.Writer.WriteString(fmt.Sprintf("data: %s\n\n", string(finalBytes)))
	c.Writer.WriteString("data: [DONE]\n\n")
	c.Writer.Flush()
}

func processNonStream(c *gin.Context, body io.Reader, completionID, model string, cacheKey string, userID string, query string, availableTools []models.Tool) {
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

	if inToolCall && toolCallBuffer.Len() > 0 {
		fullText.WriteString("<tool_call>")
		fullText.WriteString(toolCallBuffer.String())
		fullText.WriteString("</tool_call>")
	}

	cleanText := strings.TrimSpace(fullText.String())
	var toolCalls []models.ToolCall
	if len(availableTools) > 0 {
		cleanText, toolCalls = utils.ParseToolCalls(fullText.String())
	}

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
		cleanText = ""
		reasoningText.Reset()
		if userID != "" && len(toolCalls) > 1 {
			services.GlobalCache.Set("pending_tools_"+userID, toolCalls[1:], 10*time.Minute)
		}
	}

	if usage.TotalTokens == 0 {
		usage.PromptTokens = len(query) / 4
		usage.CompletionTokens = fullText.Len() / 4
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	IncrementTokenStat(os.Getenv("SERVICE_TOKEN"), usage.TotalTokens)

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
					ToolCalls:        toolCalls,
				},
				FinishReason: &finishReason,
			},
		},
		Usage: &usage,
	}

	services.SaveMessage(userID, "asst_"+completionID, "assistant", fullText.String())
	services.GlobalCache.Set(cacheKey, response, 5*time.Minute)
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
			endIdx := strings.Index(remaining, "</think>")
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
				remaining = remaining[endIdx+8:]
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
			endIdx := strings.Index(remaining, "</tool_call>")
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
						parsedToolCalls[0].Index = *toolCallIndex
						if parsedToolCalls[0].ID == "" {
							parsedToolCalls[0].ID = "call_" + utils.GenerateID()
						}
						if parsedToolCalls[0].Type == "" {
							parsedToolCalls[0].Type = "function"
						}
						chunk := utils.CreateChatCompletionChunk(completionID, "", model, nil, "", nil, []models.ToolCall{parsedToolCalls[0]})
						b, _ := json.Marshal(chunk)
						c.Writer.WriteString(fmt.Sprintf("data: %s\n\n", string(b)))
						c.Writer.Flush()
					}
				}

				*inToolCall = false
				*sentToolCallName = false
				*currentToolID = ""
				*toolCallIndex++
				toolCallBuffer.Reset()
				remaining = remaining[endIdx+12:]
			} else {
				remaining = ""
			}
			continue
		}

		thinkStartIdx := strings.Index(remaining, "<think>")
		toolStartIdx := strings.Index(remaining, "<tool_call>")

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
			remaining = remaining[thinkStartIdx+7:]
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
			remaining = remaining[toolStartIdx+11:]
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
