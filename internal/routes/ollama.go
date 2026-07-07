package routes

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"flip-ai/internal/models"
	"flip-ai/internal/services"
	"flip-ai/internal/utils"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func registerOllamaRoutes(r *gin.Engine, authMiddleware gin.HandlerFunc) {
	api := r.Group("/api")
	if authMiddleware != nil {
		api.Use(authMiddleware)
	}

	api.GET("/tags", handleOllamaTags)
	api.POST("/chat", handleOllamaChat)
	api.POST("/generate", handleOllamaGenerate)
	api.GET("/version", handleOllamaVersion)
}

func handleOllamaVersion(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"version": "0.0.0-mimo-proxy"})
}

func handleOllamaTags(c *gin.Context) {
	if cached, found := services.GlobalCache.Get("ollama_models_list"); found {
		c.JSON(http.StatusOK, cached)
		return
	}

	modelsList := make([]gin.H, 0, 16)
	seen := make(map[string]bool)
	addModel := func(model, family string) {
		model = strings.TrimSpace(model)
		if model == "" || seen[model] {
			return
		}
		seen[model] = true
		modelsList = append(modelsList, ollamaModelTag(model, family))
	}

	addModel("default", "flip-ai")
	addModel("deepseek-chat", "deepseek")
	addModel("deepseek-reasoner", "deepseek")
	for _, model := range services.OfficialProviderModels() {
		id, _ := model["id"].(string)
		addModel(id, ollamaFamilyForModel(id))
	}

	auth, err := services.GetSelectedAuth()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"models": modelsList})
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
					Model string `json:"model"`
				} `json:"modelConfigList"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err == nil && result.Code == 0 {
			for _, m := range result.Data.ModelConfigList {
				addModel(m.Model, "mimo")
			}
			response := gin.H{"models": modelsList}
			services.GlobalCache.Set("ollama_models_list", response, 30*time.Minute)
			c.JSON(http.StatusOK, response)
			return
		}
	}

	addModel("mimo-v2.5-pro", "mimo")
	c.JSON(http.StatusOK, gin.H{"models": modelsList})
}

func ollamaModelTag(model string, family string) gin.H {
	return gin.H{
		"name":        model,
		"model":       model,
		"modified_at": time.Now().UTC().Format(time.RFC3339Nano),
		"size":        int64(0),
		"digest":      "",
		"details": gin.H{
			"format":             family,
			"family":             family,
			"families":           []string{family},
			"parameter_size":     "",
			"quantization_level": "",
		},
	}
}

func ollamaFamilyForModel(model string) string {
	lower := strings.ToLower(strings.TrimSpace(model))
	switch {
	case lower == "default":
		return "flip-ai"
	case services.IsDeepSeekModel(lower):
		return "deepseek"
	case strings.HasPrefix(lower, "gemini-"):
		return "gemini"
	case strings.HasPrefix(lower, "groq/") || strings.HasPrefix(lower, "groq-"):
		return "groq"
	case strings.HasPrefix(lower, "openrouter/"):
		return "openrouter"
	case strings.HasPrefix(lower, "cf/") || strings.HasPrefix(lower, "cloudflare/") || strings.HasPrefix(lower, "@cf/"):
		return "cloudflare"
	default:
		return "mimo"
	}
}

func handleOllamaChat(c *gin.Context) {
	bodyCopy, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	var input models.OllamaChatRequest
	if err := json.Unmarshal(bodyCopy, &input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if len(input.Messages) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "messages is required"})
		return
	}

	chatMessages := translateOllamaMessages(input.Messages)
	stream := input.Stream != nil && *input.Stream
	thinkingRequested := ollamaThinkingEnabled(input.Think)

	runOllamaRequest(c, ollamaRequestSpec{
		Model:             input.Model,
		Messages:          chatMessages,
		Tools:             input.Tools,
		Stream:            stream,
		ThinkingRequested: thinkingRequested,
		ResponseMode:      "chat",
	})
}

func handleOllamaGenerate(c *gin.Context) {
	bodyCopy, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	var input models.OllamaGenerateRequest
	if err := json.Unmarshal(bodyCopy, &input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if strings.TrimSpace(input.Prompt) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "prompt is required"})
		return
	}

	messages := make([]models.Message, 0, 2)
	if strings.TrimSpace(input.System) != "" {
		messages = append(messages, models.Message{Role: "system", Content: input.System})
	}

	prompt := input.Prompt
	if strings.TrimSpace(input.Suffix) != "" {
		prompt += "\n\n[suffix]\n" + input.Suffix
	}
	messages = append(messages, models.Message{Role: "user", Content: prompt})

	stream := input.Stream != nil && *input.Stream
	thinkingRequested := ollamaThinkingEnabled(input.Think)

	runOllamaRequest(c, ollamaRequestSpec{
		Model:             input.Model,
		Messages:          messages,
		Stream:            stream,
		ThinkingRequested: thinkingRequested,
		ResponseMode:      "generate",
	})
}

type ollamaRequestSpec struct {
	Model             string
	Messages          []models.Message
	Tools             []models.Tool
	Stream            bool
	ThinkingRequested bool
	ResponseMode      string
	StatToken         string
}

func runOllamaRequest(c *gin.Context, spec ollamaRequestSpec) {
	startedAt := time.Now()
	completionID := utils.GenerateID()
	selectedModel := services.ResolveRequestedModel(spec.Model)
	spec.Model = selectedModel
	if services.IsDeepSeekModel(selectedModel) {
		runDeepSeekOllamaRequest(c, spec, selectedModel, startedAt)
		return
	}
	if provider, ok := services.SelectOfficialProvider(selectedModel); ok {
		runOfficialOllamaRequest(c, spec, selectedModel, provider, startedAt)
		return
	}

	toolChoice := "auto"
	agentMode := len(spec.Tools) > 0
	contextLimits := utils.ContextLimitsFromEnv(agentMode)
	if agentMode {
		spec.Messages = utils.TrimMessagesForProxy(spec.Messages, contextLimits)
	}

	var toolInstructions string
	if agentMode && utils.AgentFastModeEnabled() {
		toolInstructions = utils.FormatToolsAsInstructionsCompact(spec.Tools, toolChoice)
	} else {
		toolInstructions = utils.FormatToolsAsInstructionsWithChoice(spec.Tools, toolChoice)
	}

	sessionHandle := services.GenerateFingerprint(spec.Messages)
	if len(spec.Messages) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "messages cannot be empty"})
		return
	}

	if sessionHandle != "" {
		if pending, found := services.GlobalCache.Get("pending_tools_" + sessionHandle); found {
			if pendingTools, ok := pending.([]models.ToolCall); ok && len(pendingTools) > 0 {
				lastMsg := spec.Messages[len(spec.Messages)-1]
				if lastMsg.Role == "tool" {
					nextTool := pendingTools[0]
					remaining := pendingTools[1:]
					if len(remaining) > 0 {
						services.GlobalCache.Set("pending_tools_"+sessionHandle, remaining, 10*time.Minute)
					} else {
						services.GlobalCache.Delete("pending_tools_" + sessionHandle)
					}

					if spec.Stream {
						c.Header("Content-Type", "application/x-ndjson")
						writeOllamaJSONLine(c, buildOllamaToolChunk(spec.ResponseMode, selectedModel, nextTool, false))
						writeOllamaJSONLine(c, buildOllamaDoneChunk(spec.ResponseMode, selectedModel, "tool_calls", time.Since(startedAt), len(buildConversationQuery(spec.Messages, toolInstructions))/4, 0))
						return
					}

					spec.Model = selectedModel
					writeOllamaImmediateToolResponse(c, spec, nextTool, startedAt, toolInstructions)
					return
				}

				services.GlobalCache.Delete("pending_tools_" + sessionHandle)
			}
		}
	}

	query := buildConversationQuery(spec.Messages, toolInstructions)
	if len(spec.Messages) > 1 {
		systemPrefix := ""
		for _, m := range spec.Messages {
			if m.Role == "system" {
				systemPrefix = services.ExtractText(m.Content, false) + toolInstructions
				break
			}
		}
		query = truncateConversationQuery(query, systemPrefix, contextLimits.MaxChars)
	}

	targetModel := selectedModel

	enableThinking := !strings.Contains(targetModel, "no-thinking")
	if agentMode {
		enableThinking = false
		if os.Getenv("AGENT_ENABLE_THINKING") == "true" || os.Getenv("AGENT_ENABLE_THINKING") == "1" {
			enableThinking = !strings.Contains(targetModel, "no-thinking")
		}
	}

	payload := models.MimoPayload{
		MsgID:          utils.GenerateID(),
		ConversationID: utils.GenerateID(),
		Query:          query,
		IsEditedQuery:  false,
		ModelConfig: models.ModelConfig{
			EnableThinking:  enableThinking,
			WebSearchStatus: "disabled",
			Model:           targetModel,
		},
		MultiMedias: []models.MultiMedia{},
	}

	if sessionHandle != "" {
		if saved, err := services.GetSession(sessionHandle); err == nil && saved != "" {
			payload.ConversationID = saved
		} else {
			auth, authErr := services.GetSelectedAuth()
			if authErr == nil {
				go func(a models.Auth, id, fp string) {
					if err := services.CreateConversation(a, id); err == nil {
						_ = services.SaveSession(fp, id)
					}
				}(auth, payload.ConversationID, sessionHandle)
			}
		}
	}

	customHeaders := make(map[string]string)
	for k, v := range c.Request.Header {
		customHeaders[strings.ToLower(k)] = v[0]
	}

	auth, authErr := services.GetSelectedAuth()
	if authErr != nil {
		if spec.Stream {
			c.Header("Content-Type", "application/x-ndjson")
			writeOllamaJSONLine(c, gin.H{"error": "invalid Xiaomi auth configuration"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid Xiaomi auth configuration"})
		return
	}
	spec.StatToken = auth.Token

	resp, err := sendOllamaUpstream(payload, customHeaders, completionID, auth)
	if err != nil {
		if spec.Stream {
			c.Header("Content-Type", "application/x-ndjson")
			writeOllamaJSONLine(c, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	bodyReader := io.Reader(resp.Body)
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gz, gzErr := gzip.NewReader(resp.Body)
		if gzErr == nil {
			defer gz.Close()
			bodyReader = gz
		}
	}

	if spec.Stream {
		streamOllamaResponse(c, bodyReader, spec, targetModel, query, sessionHandle, toolInstructions, startedAt)
		return
	}

	respondOllamaNonStream(c, bodyReader, spec, targetModel, query, sessionHandle, toolInstructions, startedAt)
}

func runOfficialOllamaRequest(c *gin.Context, spec ollamaRequestSpec, targetModel string, provider services.OfficialProvider, startedAt time.Time) {
	if !provider.Configured {
		writeOllamaError(c, spec.Stream, http.StatusServiceUnavailable, officialProviderConfigMessage(provider))
		return
	}

	body, err := buildOllamaOpenAIChatBody(spec, targetModel)
	if err != nil {
		writeOllamaError(c, spec.Stream, http.StatusBadRequest, "failed to build provider request: "+err.Error())
		return
	}

	resp, err := services.ForwardOfficialChat(provider, body)
	if err != nil {
		writeOllamaError(c, spec.Stream, http.StatusBadGateway, "Failed to call "+provider.Name+": "+err.Error())
		return
	}
	if resp == nil {
		writeOllamaError(c, spec.Stream, http.StatusBadGateway, provider.Name+" returned no response")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		writeOllamaError(c, spec.Stream, resp.StatusCode, strings.TrimSpace(string(body)))
		return
	}

	if spec.Stream {
		streamOpenAIAsOllama(c, resp.Body, spec, targetModel, startedAt)
		return
	}

	respondOpenAIAsOllama(c, resp.Body, spec, targetModel, startedAt)
}

func runDeepSeekOllamaRequest(c *gin.Context, spec ollamaRequestSpec, targetModel string, startedAt time.Time) {
	session, auth, err := services.GetSelectedDeepSeekSession()
	if err != nil {
		writeOllamaError(c, spec.Stream, http.StatusInternalServerError, "Invalid DeepSeek auth configuration: "+err.Error())
		return
	}

	customHeaders := make(map[string]string)
	for k, v := range c.Request.Header {
		customHeaders[strings.ToLower(k)] = v[0]
	}

	sessionHandle := services.GenerateFingerprint(spec.Messages)
	sessionID := ""
	if sessionHandle != "" {
		if cached, found := services.GlobalCache.Get("deepseek_session_" + sessionHandle); found {
			if s, ok := cached.(string); ok {
				sessionID = s
			}
		}
	}
	if sessionID == "" {
		sessionID, err = services.CreateDeepSeekSession(auth, session, customHeaders)
		if err != nil {
			writeOllamaError(c, spec.Stream, http.StatusBadGateway, "Failed to create DeepSeek chat session: "+err.Error())
			return
		}
		if sessionHandle != "" {
			services.GlobalCache.Set("deepseek_session_"+sessionHandle, sessionID, 55*time.Minute)
		}
	}

	toolChoice := "auto"
	agentMode := len(spec.Tools) > 0
	if agentMode {
		spec.Messages = utils.TrimMessagesForProxy(spec.Messages, utils.ContextLimitsFromEnv(true))
	}
	toolInstructions := ""
	if agentMode && utils.AgentFastModeEnabled() {
		toolInstructions = utils.FormatToolsAsInstructionsCompact(spec.Tools, toolChoice)
	} else if agentMode {
		toolInstructions = utils.FormatToolsAsInstructionsWithChoice(spec.Tools, toolChoice)
	}

	prompt := buildDeepSeekPromptWithTools(spec.Messages, toolInstructions)
	thinking := deepSeekThinkingEnabled(targetModel)
	search := strings.Contains(strings.ToLower(targetModel), "search")

	resp, err := services.SendDeepSeekChatRequest(auth, session, sessionID, prompt, thinking, search, customHeaders)
	if err != nil {
		writeOllamaError(c, spec.Stream, http.StatusBadGateway, "Failed to call DeepSeek: "+err.Error())
		return
	}
	if resp == nil {
		writeOllamaError(c, spec.Stream, http.StatusBadGateway, "DeepSeek returned no response")
		return
	}

	if resp.StatusCode != http.StatusOK {
		bodyReader, closeBody := services.ReadDeepSeekBody(resp)
		body, _ := io.ReadAll(bodyReader)
		closeBody()
		writeOllamaError(c, spec.Stream, resp.StatusCode, strings.TrimSpace(string(body)))
		return
	}

	bodyReader, closeBody := services.ReadDeepSeekBody(resp)
	result := services.ParseDeepSeekStream(bodyReader)
	closeBody()
	result.Usage.PromptTokens = len(prompt) / 4
	result.Usage.TotalTokens = result.Usage.PromptTokens + result.Usage.CompletionTokens

	cleanText, toolCalls := utils.ParseToolCalls(result.Content)
	toolCalls = finalizeToolCalls(toolCalls)
	if len(toolCalls) > 0 {
		result.Content = ""
		result.ReasoningText = ""
		storePendingToolCalls(sessionHandle, toolCalls)
	} else {
		result.Content = cleanText
	}

	if sessionHandle != "" {
		services.SaveMessage(sessionHandle, "asst_"+utils.GenerateID(), "assistant", assistantTranscript(result.Content, result.ReasoningText))
	}

	if spec.Stream {
		streamBufferedOllamaResult(c, spec, targetModel, result.Content, result.ReasoningText, toolCalls, "stop", &result.Usage, startedAt)
		return
	}

	respondBufferedOllamaResult(c, spec, targetModel, result.Content, result.ReasoningText, toolCalls, "stop", &result.Usage, startedAt)
}

func buildOllamaOpenAIChatBody(spec ollamaRequestSpec, targetModel string) ([]byte, error) {
	payload := map[string]interface{}{
		"model":    targetModel,
		"messages": spec.Messages,
		"stream":   spec.Stream,
	}
	if len(spec.Tools) > 0 {
		payload["tools"] = spec.Tools
		payload["tool_choice"] = "auto"
	}
	return json.Marshal(payload)
}

type openAIChatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message      models.Delta `json:"message"`
		Delta        models.Delta `json:"delta"`
		FinishReason *string      `json:"finish_reason"`
	} `json:"choices"`
	Usage *models.Usage `json:"usage,omitempty"`
}

func respondOpenAIAsOllama(c *gin.Context, body io.Reader, spec ollamaRequestSpec, targetModel string, startedAt time.Time) {
	respBody, err := io.ReadAll(body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to read provider response: " + err.Error()})
		return
	}

	var parsed openAIChatResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to parse provider response", "details": string(respBody)})
		return
	}

	content, reasoning, toolCalls, finishReason := openAIResponseParts(parsed)
	respondBufferedOllamaResult(c, spec, targetModel, content, reasoning, toolCalls, finishReason, parsed.Usage, startedAt)
}

func streamOpenAIAsOllama(c *gin.Context, body io.Reader, spec ollamaRequestSpec, targetModel string, startedAt time.Time) {
	c.Header("Content-Type", "application/x-ndjson")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	if rc := http.NewResponseController(c.Writer); rc != nil {
		_ = rc.SetWriteDeadline(time.Time{})
	}

	reader := bufio.NewReaderSize(body, 4*1024*1024)
	var content strings.Builder
	var reasoning strings.Builder
	var usage *models.Usage
	finishReason := "stop"
	var toolCalls []models.ToolCall

	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			dataStr := strings.TrimSpace(line[5:])
			if dataStr == "[DONE]" {
				break
			}
			var chunk openAIChatResponse
			if json.Unmarshal([]byte(dataStr), &chunk) == nil {
				if chunk.Usage != nil {
					usage = chunk.Usage
				}
				chunkContent, chunkReasoning, chunkTools, chunkFinish := openAIResponseParts(chunk)
				if chunkContent != "" {
					content.WriteString(chunkContent)
					writeOllamaJSONLine(c, ollamaContentChunk(spec, targetModel, chunkContent))
				}
				if chunkReasoning != "" {
					reasoning.WriteString(chunkReasoning)
					if spec.ThinkingRequested {
						writeOllamaJSONLine(c, ollamaThinkingChunk(spec, targetModel, chunkReasoning))
					}
				}
				if len(chunkTools) > 0 {
					toolCalls = mergeToolCallDeltas(toolCalls, chunkTools)
				}
				if chunkFinish != "" {
					finishReason = chunkFinish
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				writeOllamaJSONLine(c, gin.H{"error": err.Error()})
				return
			}
			break
		}
	}

	if len(toolCalls) > 0 {
		for _, toolCall := range toolCalls {
			writeOllamaJSONLine(c, buildOllamaToolChunk(spec.ResponseMode, targetModel, toolCall, false))
		}
		if finishReason == "stop" {
			finishReason = "tool_calls"
		}
	}
	writeOllamaDoneFromUsage(c, spec, targetModel, finishReason, usage, len(buildConversationQuery(spec.Messages, "")), content.Len(), startedAt)
}

func openAIResponseParts(resp openAIChatResponse) (string, string, []models.ToolCall, string) {
	if len(resp.Choices) == 0 {
		return "", "", nil, "stop"
	}
	choice := resp.Choices[0]
	message := choice.Message
	if message.Content == "" && message.ReasoningContent == "" && len(message.ToolCalls) == 0 {
		message = choice.Delta
	}
	finishReason := "stop"
	if choice.FinishReason != nil && strings.TrimSpace(*choice.FinishReason) != "" {
		finishReason = *choice.FinishReason
	}
	return message.Content, message.ReasoningContent, message.ToolCalls, finishReason
}

func respondBufferedOllamaResult(c *gin.Context, spec ollamaRequestSpec, targetModel, content, reasoning string, toolCalls []models.ToolCall, finishReason string, usage *models.Usage, startedAt time.Time) {
	if finishReason == "" {
		finishReason = "stop"
	}
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
		content = ""
	}
	if usage == nil {
		usage = estimateOllamaUsage(spec.Messages, content)
	}

	if spec.ResponseMode == "generate" {
		resp := models.OllamaGenerateResponse{
			Model:              targetModel,
			CreatedAt:          time.Now().UTC().Format(time.RFC3339Nano),
			Response:           strings.TrimSpace(content),
			Done:               true,
			DoneReason:         finishReason,
			TotalDuration:      time.Since(startedAt).Nanoseconds(),
			LoadDuration:       0,
			PromptEvalCount:    usage.PromptTokens,
			PromptEvalDuration: 0,
			EvalCount:          usage.CompletionTokens,
			EvalDuration:       time.Since(startedAt).Nanoseconds(),
		}
		if spec.ThinkingRequested && strings.TrimSpace(reasoning) != "" {
			resp.Thinking = strings.TrimSpace(reasoning)
		}
		c.JSON(http.StatusOK, resp)
		return
	}

	resp := models.OllamaChatResponse{
		Model:     targetModel,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Message: models.OllamaMessage{
			Role:    "assistant",
			Content: strings.TrimSpace(content),
		},
		Done:               true,
		DoneReason:         finishReason,
		TotalDuration:      time.Since(startedAt).Nanoseconds(),
		LoadDuration:       0,
		PromptEvalCount:    usage.PromptTokens,
		PromptEvalDuration: 0,
		EvalCount:          usage.CompletionTokens,
		EvalDuration:       time.Since(startedAt).Nanoseconds(),
	}
	if spec.ThinkingRequested && strings.TrimSpace(reasoning) != "" {
		resp.Message.Thinking = strings.TrimSpace(reasoning)
	}
	if len(toolCalls) > 0 {
		resp.Message.ToolCalls = toOllamaToolCalls(toolCalls)
	}
	c.JSON(http.StatusOK, resp)
}

func streamBufferedOllamaResult(c *gin.Context, spec ollamaRequestSpec, targetModel, content, reasoning string, toolCalls []models.ToolCall, finishReason string, usage *models.Usage, startedAt time.Time) {
	c.Header("Content-Type", "application/x-ndjson")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	if spec.ThinkingRequested && strings.TrimSpace(reasoning) != "" {
		writeOllamaJSONLine(c, ollamaThinkingChunk(spec, targetModel, reasoning))
	}
	if strings.TrimSpace(content) != "" {
		writeOllamaJSONLine(c, ollamaContentChunk(spec, targetModel, content))
	}
	if len(toolCalls) > 0 {
		for _, toolCall := range toolCalls {
			writeOllamaJSONLine(c, buildOllamaToolChunk(spec.ResponseMode, targetModel, toolCall, false))
		}
		finishReason = "tool_calls"
	}
	writeOllamaDoneFromUsage(c, spec, targetModel, finishReason, usage, len(buildConversationQuery(spec.Messages, "")), len(content), startedAt)
}

func writeOllamaDoneFromUsage(c *gin.Context, spec ollamaRequestSpec, targetModel, finishReason string, usage *models.Usage, promptChars, completionChars int, startedAt time.Time) {
	if finishReason == "" {
		finishReason = "stop"
	}
	promptTokens := promptChars / 4
	completionTokens := completionChars / 4
	if usage != nil {
		promptTokens = usage.PromptTokens
		completionTokens = usage.CompletionTokens
	}
	writeOllamaJSONLine(c, buildOllamaDoneChunk(spec.ResponseMode, targetModel, finishReason, time.Since(startedAt), promptTokens, completionTokens))
}

func ollamaContentChunk(spec ollamaRequestSpec, model, content string) interface{} {
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	if spec.ResponseMode == "generate" {
		return models.OllamaGenerateResponse{Model: model, CreatedAt: createdAt, Response: content, Done: false}
	}
	return models.OllamaChatResponse{
		Model:     model,
		CreatedAt: createdAt,
		Message:   models.OllamaMessage{Role: "assistant", Content: content},
		Done:      false,
	}
}

func ollamaThinkingChunk(spec ollamaRequestSpec, model, thinking string) interface{} {
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	if spec.ResponseMode == "generate" {
		return models.OllamaGenerateResponse{Model: model, CreatedAt: createdAt, Thinking: thinking, Done: false}
	}
	return models.OllamaChatResponse{
		Model:     model,
		CreatedAt: createdAt,
		Message:   models.OllamaMessage{Role: "assistant", Thinking: thinking},
		Done:      false,
	}
}

func mergeToolCallDeltas(existing []models.ToolCall, deltas []models.ToolCall) []models.ToolCall {
	for _, delta := range deltas {
		idx := delta.Index
		for len(existing) <= idx {
			existing = append(existing, models.ToolCall{Index: len(existing), Type: "function"})
		}
		if delta.ID != "" {
			existing[idx].ID = delta.ID
		}
		if delta.Type != "" {
			existing[idx].Type = delta.Type
		}
		if delta.Function.Name != "" {
			existing[idx].Function.Name = delta.Function.Name
		}
		if delta.Function.Arguments != "" {
			existing[idx].Function.Arguments += delta.Function.Arguments
		}
	}
	return existing
}

func estimateOllamaUsage(messages []models.Message, completion string) *models.Usage {
	usage := services.EstimateUsageFromMessages(messages, completion)
	return &usage
}

func writeOllamaError(c *gin.Context, stream bool, status int, message string) {
	if strings.TrimSpace(message) == "" {
		message = http.StatusText(status)
	}
	if stream {
		c.Header("Content-Type", "application/x-ndjson")
		c.Status(status)
		writeOllamaJSONLine(c, gin.H{"error": message})
		return
	}
	c.JSON(status, gin.H{"error": message})
}

func sendOllamaUpstream(payload models.MimoPayload, customHeaders map[string]string, completionID string, auth models.Auth) (*http.Response, error) {
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		resp, err := sendMimoChatRequest(auth, payload, customHeaders, completionID)
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				return resp, nil
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode >= 500 && i < maxRetries-1 {
				continue
			}
			return nil, fmt.Errorf("xiaomi api error: %s", strings.TrimSpace(string(body)))
		}
		if i == maxRetries-1 {
			return nil, err
		}
	}
	return nil, fmt.Errorf("failed to proxy request")
}

func streamOllamaResponse(c *gin.Context, body io.Reader, spec ollamaRequestSpec, model, query, sessionHandle, toolInstructions string, startedAt time.Time) {
	c.Header("Content-Type", "application/x-ndjson")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	if rc := http.NewResponseController(c.Writer); rc != nil {
		_ = rc.SetWriteDeadline(time.Time{})
	}

	reader := bufio.NewReaderSize(body, 16*1024*1024)
	state := newOllamaStreamState(spec, model, query, sessionHandle, startedAt)
	var eventType string

	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			if strings.HasPrefix(line, "event:") {
				eventType = strings.TrimSpace(line[6:])
			} else if strings.HasPrefix(line, "data:") {
				dataStr := strings.TrimSpace(line[5:])
				if parseErr := state.consume(eventType, dataStr, true, c); parseErr != nil {
					writeOllamaJSONLine(c, gin.H{"error": parseErr.Error()})
					return
				}
				eventType = ""
			}
		}
		if err != nil {
			if err != io.EOF {
				writeOllamaJSONLine(c, gin.H{"error": err.Error()})
				return
			}
			break
		}
	}

	state.finalize(c)
}

func respondOllamaNonStream(c *gin.Context, body io.Reader, spec ollamaRequestSpec, model, query, sessionHandle, toolInstructions string, startedAt time.Time) {
	respBody, _ := io.ReadAll(body)
	events := strings.Split(string(respBody), "\n\n")
	state := newOllamaStreamState(spec, model, query, sessionHandle, startedAt)

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
			if err := state.consume(eventType, dataStr, false, nil); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
	}

	state.finalize(nil)
	if spec.ResponseMode == "generate" {
		c.JSON(http.StatusOK, state.generateResponse())
		return
	}
	c.JSON(http.StatusOK, state.chatResponse())
}

type ollamaStreamState struct {
	spec            ollamaRequestSpec
	model           string
	query           string
	sessionHandle   string
	startedAt       time.Time
	inThinking      bool
	inToolCall      bool
	toolCallBuffer  strings.Builder
	fullText        strings.Builder
	reasoningText   strings.Builder
	usage           models.Usage
	streamDone      bool
}

func newOllamaStreamState(spec ollamaRequestSpec, model, query, sessionHandle string, startedAt time.Time) *ollamaStreamState {
	return &ollamaStreamState{
		spec:          spec,
		model:         model,
		query:         query,
		sessionHandle: sessionHandle,
		startedAt:     startedAt,
	}
}

func (s *ollamaStreamState) consume(eventType, dataStr string, stream bool, c *gin.Context) error {
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
				s.usage.PromptTokens = u.PromptTokens
				s.usage.CompletionTokens = u.CompletionTokens
				s.usage.TotalTokens = u.TotalTokens
			} else {
				s.usage.PromptTokens = u.NativeUsage.PromptTokens
				s.usage.CompletionTokens = u.NativeUsage.CompletionTokens
				s.usage.TotalTokens = u.NativeUsage.TotalTokens
			}
		}
		return nil
	}

	if eventType != "message" {
		return nil
	}

	var d struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(dataStr), &d); err != nil {
		return nil
	}

	remaining := strings.ReplaceAll(d.Content, "\x00", "")
	for len(remaining) > 0 {
		if s.inThinking {
			endIdx := strings.Index(remaining, utils.ThinkingCloseTag)
			chunk := remaining
			if endIdx != -1 {
				chunk = remaining[:endIdx]
			}
			s.reasoningText.WriteString(chunk)
			if stream && c != nil && s.spec.ThinkingRequested && chunk != "" {
				writeOllamaJSONLine(c, s.thinkingChunk(chunk))
			}
			if endIdx == -1 {
				break
			}
			s.inThinking = false
			remaining = remaining[endIdx+len(utils.ThinkingCloseTag):]
			continue
		}

		if s.inToolCall {
			endIdx := strings.Index(remaining, utils.ToolCallCloseTag)
			chunk := remaining
			if endIdx != -1 {
				chunk = remaining[:endIdx]
			}
			s.toolCallBuffer.WriteString(chunk)
			if endIdx == -1 {
				break
			}

			rawToolCall := utils.ToolCallOpenTag + s.toolCallBuffer.String() + utils.ToolCallCloseTag
			s.fullText.WriteString(rawToolCall)
			if stream && c != nil {
				if _, parsedToolCalls := utils.ParseToolCalls(rawToolCall); len(parsedToolCalls) > 0 {
					writeOllamaJSONLine(c, buildOllamaToolChunk(s.spec.ResponseMode, s.model, parsedToolCalls[0], false))
				}
			}
			s.toolCallBuffer.Reset()
			s.inToolCall = false
			remaining = remaining[endIdx+len(utils.ToolCallCloseTag):]
			continue
		}

		thinkIdx := strings.Index(remaining, utils.ThinkingOpenTag)
		toolIdx := strings.Index(remaining, utils.ToolCallOpenTag)
		if thinkIdx != -1 && (toolIdx == -1 || thinkIdx < toolIdx) {
			text := remaining[:thinkIdx]
			if strings.TrimSpace(strings.Trim(text, ",")) != "" {
				s.fullText.WriteString(text)
				if stream && c != nil {
					writeOllamaJSONLine(c, s.contentChunk(text))
				}
			}
			s.inThinking = true
			remaining = remaining[thinkIdx+len(utils.ThinkingOpenTag):]
			continue
		}

		if toolIdx != -1 {
			text := remaining[:toolIdx]
			if strings.TrimSpace(strings.Trim(text, ",")) != "" {
				s.fullText.WriteString(text)
				if stream && c != nil {
					writeOllamaJSONLine(c, s.contentChunk(text))
				}
			}
			s.inToolCall = true
			s.toolCallBuffer.Reset()
			remaining = remaining[toolIdx+len(utils.ToolCallOpenTag):]
			continue
		}

		s.fullText.WriteString(remaining)
		if stream && c != nil {
			writeOllamaJSONLine(c, s.contentChunk(remaining))
		}
		break
	}

	return nil
}

func (s *ollamaStreamState) finalize(c *gin.Context) {
	if s.streamDone {
		return
	}

	if s.inToolCall && s.toolCallBuffer.Len() > 0 {
		s.fullText.WriteString(utils.ToolCallOpenTag)
		s.fullText.WriteString(s.toolCallBuffer.String())
		s.fullText.WriteString(utils.ToolCallCloseTag)
		s.toolCallBuffer.Reset()
		s.inToolCall = false
	}

	_, toolCalls := utils.ParseToolCalls(s.fullText.String())
	toolCalls = finalizeToolCalls(toolCalls)
	toolCalls = responseToolCalls(toolCalls, nil, len(s.spec.Tools) > 0)

	if s.usage.TotalTokens == 0 {
		s.usage.PromptTokens = len(s.query) / 4
		s.usage.CompletionTokens = s.fullText.Len() / 4
		s.usage.TotalTokens = s.usage.PromptTokens + s.usage.CompletionTokens
	}
	IncrementTokenStat(s.spec.StatToken, s.usage.TotalTokens)
	services.SaveMessage(s.sessionHandle, "asst_"+utils.GenerateID(), "assistant", assistantTranscript(s.fullText.String(), s.reasoningText.String()))

	if len(toolCalls) > 0 {
		storePendingToolCalls(s.sessionHandle, toolCalls)
	}

	if c != nil {
		finishReason := "stop"
		if len(toolCalls) > 0 {
			finishReason = "tool_calls"
		}
		writeOllamaJSONLine(c, buildOllamaDoneChunk(s.spec.ResponseMode, s.model, finishReason, time.Since(s.startedAt), s.usage.PromptTokens, s.usage.CompletionTokens))
	}

	s.streamDone = true
}

func (s *ollamaStreamState) chatResponse() models.OllamaChatResponse {
	cleanText, toolCalls := utils.ParseToolCalls(s.fullText.String())
	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
		cleanText = ""
	}

	resp := models.OllamaChatResponse{
		Model:     s.model,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Message: models.OllamaMessage{
			Role:    "assistant",
			Content: strings.TrimSpace(cleanText),
		},
		Done:               true,
		DoneReason:         finishReason,
		TotalDuration:      time.Since(s.startedAt).Nanoseconds(),
		LoadDuration:       0,
		PromptEvalCount:    s.usage.PromptTokens,
		PromptEvalDuration: 0,
		EvalCount:          s.usage.CompletionTokens,
		EvalDuration:       time.Since(s.startedAt).Nanoseconds(),
	}

	if s.spec.ThinkingRequested && strings.TrimSpace(s.reasoningText.String()) != "" {
		resp.Message.Thinking = strings.TrimSpace(s.reasoningText.String())
	}
	if len(toolCalls) > 0 {
		resp.Message.ToolCalls = toOllamaToolCalls(toolCalls)
	}
	return resp
}

func (s *ollamaStreamState) generateResponse() models.OllamaGenerateResponse {
	cleanText, toolCalls := utils.ParseToolCalls(s.fullText.String())
	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
		cleanText = ""
	}

	resp := models.OllamaGenerateResponse{
		Model:              s.model,
		CreatedAt:          time.Now().UTC().Format(time.RFC3339Nano),
		Response:           strings.TrimSpace(cleanText),
		Done:               true,
		DoneReason:         finishReason,
		TotalDuration:      time.Since(s.startedAt).Nanoseconds(),
		LoadDuration:       0,
		PromptEvalCount:    s.usage.PromptTokens,
		PromptEvalDuration: 0,
		EvalCount:          s.usage.CompletionTokens,
		EvalDuration:       time.Since(s.startedAt).Nanoseconds(),
	}

	if s.spec.ThinkingRequested && strings.TrimSpace(s.reasoningText.String()) != "" {
		resp.Thinking = strings.TrimSpace(s.reasoningText.String())
	}
	return resp
}

func (s *ollamaStreamState) contentChunk(content string) interface{} {
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	if s.spec.ResponseMode == "generate" {
		return models.OllamaGenerateResponse{
			Model:     s.model,
			CreatedAt: createdAt,
			Response:  content,
			Done:      false,
		}
	}
	return models.OllamaChatResponse{
		Model:     s.model,
		CreatedAt: createdAt,
		Message: models.OllamaMessage{
			Role:    "assistant",
			Content: content,
		},
		Done: false,
	}
}

func (s *ollamaStreamState) thinkingChunk(thinking string) interface{} {
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	if s.spec.ResponseMode == "generate" {
		return models.OllamaGenerateResponse{
			Model:     s.model,
			CreatedAt: createdAt,
			Thinking:  thinking,
			Done:      false,
		}
	}
	return models.OllamaChatResponse{
		Model:     s.model,
		CreatedAt: createdAt,
		Message: models.OllamaMessage{
			Role:     "assistant",
			Thinking: thinking,
		},
		Done: false,
	}
}

func buildOllamaToolChunk(mode, model string, toolCall models.ToolCall, done bool) interface{} {
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	ollamaToolCalls := toOllamaToolCalls([]models.ToolCall{toolCall})
	if mode == "generate" {
		return models.OllamaGenerateResponse{
			Model:     model,
			CreatedAt: createdAt,
			Done:      done,
		}
	}
	return models.OllamaChatResponse{
		Model:     model,
		CreatedAt: createdAt,
		Message: models.OllamaMessage{
			Role:      "assistant",
			ToolCalls: ollamaToolCalls,
		},
		Done: done,
	}
}

func buildOllamaDoneChunk(mode, model, finishReason string, duration time.Duration, promptTokens, completionTokens int) interface{} {
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	if mode == "generate" {
		return models.OllamaGenerateResponse{
			Model:              model,
			CreatedAt:          createdAt,
			Done:               true,
			DoneReason:         finishReason,
			TotalDuration:      duration.Nanoseconds(),
			LoadDuration:       0,
			PromptEvalCount:    promptTokens,
			PromptEvalDuration: 0,
			EvalCount:          completionTokens,
			EvalDuration:       duration.Nanoseconds(),
		}
	}
	return models.OllamaChatResponse{
		Model:     model,
		CreatedAt: createdAt,
		Message: models.OllamaMessage{
			Role: "assistant",
		},
		Done:               true,
		DoneReason:         finishReason,
		TotalDuration:      duration.Nanoseconds(),
		LoadDuration:       0,
		PromptEvalCount:    promptTokens,
		PromptEvalDuration: 0,
		EvalCount:          completionTokens,
		EvalDuration:       duration.Nanoseconds(),
	}
}

func writeOllamaImmediateToolResponse(c *gin.Context, spec ollamaRequestSpec, toolCall models.ToolCall, startedAt time.Time, toolInstructions string) {
	if spec.ResponseMode == "generate" {
		c.JSON(http.StatusOK, buildOllamaDoneChunk("generate", spec.Model, "tool_calls", time.Since(startedAt), 0, 0))
		return
	}

	resp := models.OllamaChatResponse{
		Model:     spec.Model,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Message: models.OllamaMessage{
			Role:      "assistant",
			ToolCalls: toOllamaToolCalls([]models.ToolCall{toolCall}),
		},
		Done:               true,
		DoneReason:         "tool_calls",
		TotalDuration:      time.Since(startedAt).Nanoseconds(),
		PromptEvalCount:    len(buildConversationQuery(spec.Messages, toolInstructions)) / 4,
		EvalCount:          0,
		PromptEvalDuration: 0,
		EvalDuration:       time.Since(startedAt).Nanoseconds(),
	}
	c.JSON(http.StatusOK, resp)
}

func writeOllamaJSONLine(c *gin.Context, payload interface{}) {
	b, _ := json.Marshal(payload)
	c.Writer.Write(b)
	c.Writer.Write([]byte("\n"))
	c.Writer.Flush()
}

func translateOllamaMessages(input []models.OllamaMessage) []models.Message {
	out := make([]models.Message, 0, len(input))
	for _, msg := range input {
		m := models.Message{
			Role:             msg.Role,
			Content:          msg.Content,
			ReasoningContent: msg.Thinking,
			Name:             msg.ToolName,
		}
		if len(msg.ToolCalls) > 0 {
			m.ToolCalls = make([]models.ToolCall, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				argsBytes, _ := json.Marshal(tc.Function.Arguments)
				m.ToolCalls = append(m.ToolCalls, models.ToolCall{
					Type: "function",
					Function: models.ToolFunction{
						Name:      tc.Function.Name,
						Arguments: string(argsBytes),
					},
				})
			}
		}
		out = append(out, m)
	}
	return out
}

func ollamaThinkingEnabled(raw interface{}) bool {
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		v = strings.TrimSpace(strings.ToLower(v))
		return v == "true" || v == "high" || v == "medium" || v == "low"
	default:
		return false
	}
}

func toOllamaToolCalls(toolCalls []models.ToolCall) []models.OllamaToolCall {
	out := make([]models.OllamaToolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		var args interface{}
		if strings.TrimSpace(tc.Function.Arguments) != "" {
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				args = map[string]interface{}{"raw": tc.Function.Arguments}
			}
		} else {
			args = map[string]interface{}{}
		}
		out = append(out, models.OllamaToolCall{
			Type: "function",
			Function: models.OllamaToolFunction{
				Name:      tc.Function.Name,
				Arguments: args,
			},
		})
	}
	return out
}
