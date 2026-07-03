package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"flip-ai/internal/models"
	"net/http"
	"os"
	"strings"
)

type OfficialProvider struct {
	Name       string
	OwnedBy    string
	Prefix     string
	BaseURL    string
	APIKey     string
	Model      string
	Headers    map[string]string
	Configured bool
}

func SelectOfficialProvider(model string) (OfficialProvider, bool) {
	model = strings.TrimSpace(model)
	lower := strings.ToLower(model)
	stored, _ := LoadStoredAuth()

	if strings.HasPrefix(lower, "gemini-") {
		key := firstNonEmpty(os.Getenv("GEMINI_API_KEY"), stored.GeminiAPIKey)
		return OfficialProvider{
			Name:       "gemini",
			OwnedBy:    "google",
			Prefix:     "gemini",
			BaseURL:    envOrDefault("GEMINI_BASE_URL", "https://generativelanguage.googleapis.com/v1beta/openai"),
			APIKey:     key,
			Model:      model,
			Configured: key != "",
		}, true
	}

	if strings.HasPrefix(lower, "groq/") || strings.HasPrefix(lower, "groq-") {
		key := firstNonEmpty(os.Getenv("GROQ_API_KEY"), stored.GroqAPIKey)
		providerModel := strings.TrimPrefix(model, "groq/")
		providerModel = strings.TrimPrefix(providerModel, "groq-")
		return OfficialProvider{
			Name:       "groq",
			OwnedBy:    "groq",
			Prefix:     "groq",
			BaseURL:    envOrDefault("GROQ_BASE_URL", "https://api.groq.com/openai/v1"),
			APIKey:     key,
			Model:      providerModel,
			Configured: key != "",
		}, true
	}

	if strings.HasPrefix(lower, "openrouter/") {
		key := firstNonEmpty(os.Getenv("OPENROUTER_API_KEY"), stored.OpenRouterAPIKey)
		headers := map[string]string{}
		if referer := firstNonEmpty(os.Getenv("OPENROUTER_HTTP_REFERER"), stored.OpenRouterHTTPReferer); referer != "" {
			headers["HTTP-Referer"] = referer
		}
		if title := firstNonEmpty(os.Getenv("OPENROUTER_APP_TITLE"), stored.OpenRouterAppTitle); title != "" {
			headers["X-Title"] = title
		}
		return OfficialProvider{
			Name:       "openrouter",
			OwnedBy:    "openrouter",
			Prefix:     "openrouter",
			BaseURL:    envOrDefault("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"),
			APIKey:     key,
			Model:      strings.TrimPrefix(model, "openrouter/"),
			Headers:    headers,
			Configured: key != "",
		}, true
	}

	if strings.HasPrefix(lower, "cf/") || strings.HasPrefix(lower, "cloudflare/") || strings.HasPrefix(lower, "@cf/") {
		key := firstNonEmpty(os.Getenv("CLOUDFLARE_API_KEY"), stored.CloudflareAPIKey)
		accountID := firstNonEmpty(os.Getenv("CLOUDFLARE_ACCOUNT_ID"), stored.CloudflareAccountID)
		providerModel := model
		providerModel = strings.TrimPrefix(providerModel, "cf/")
		providerModel = strings.TrimPrefix(providerModel, "cloudflare/")
		if !strings.HasPrefix(providerModel, "@cf/") {
			providerModel = "@cf/" + providerModel
		}
		return OfficialProvider{
			Name:       "cloudflare",
			OwnedBy:    "cloudflare",
			Prefix:     "cf",
			BaseURL:    strings.TrimRight(envOrDefault("CLOUDFLARE_BASE_URL", fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/ai/v1", accountID)), "/"),
			APIKey:     key,
			Model:      providerModel,
			Configured: key != "" && accountID != "",
		}, true
	}

	return OfficialProvider{}, false
}

func OfficialProviderModels() []map[string]interface{} {
	return []map[string]interface{}{
		{"id": "gemini-3.5-flash", "object": "model", "created": 1700000000, "owned_by": "google", "description": "Google Gemini API free-tier capable model"},
		{"id": "gemini-2.5-flash", "object": "model", "created": 1700000000, "owned_by": "google", "description": "Google Gemini API flash model"},
		{"id": "groq/llama-3.1-8b-instant", "object": "model", "created": 1700000000, "owned_by": "groq", "description": "Groq free plan model"},
		{"id": "groq/llama-3.3-70b-versatile", "object": "model", "created": 1700000000, "owned_by": "groq", "description": "Groq free plan model"},
		{"id": "openrouter/meta-llama/llama-3.1-8b-instruct:free", "object": "model", "created": 1700000000, "owned_by": "openrouter", "description": "OpenRouter free model alias"},
		{"id": "openrouter/google/gemma-3-12b-it:free", "object": "model", "created": 1700000000, "owned_by": "openrouter", "description": "OpenRouter free model alias"},
		{"id": "cf/@cf/meta/llama-3.1-8b-instruct", "object": "model", "created": 1700000000, "owned_by": "cloudflare", "description": "Cloudflare Workers AI model"},
		{"id": "cf/@cf/openai/gpt-oss-120b", "object": "model", "created": 1700000000, "owned_by": "cloudflare", "description": "Cloudflare Workers AI model"},
	}
}

func ForwardOfficialChat(provider OfficialProvider, rawBody []byte) (*http.Response, error) {
	if !provider.Configured {
		return nil, fmt.Errorf("%s provider is not configured", provider.Name)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		return nil, err
	}
	payload["model"] = provider.Model
	payloadBytes, _ := json.Marshal(payload)

	endpoint := strings.TrimRight(provider.BaseURL, "/") + "/chat/completions"
	req, _ := http.NewRequest("POST", endpoint, bytes.NewBuffer(payloadBytes))
	req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if stream, _ := payload["stream"].(bool); stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	for k, v := range provider.Headers {
		req.Header.Set(k, v)
	}

	return GlobalHTTPClient.Do(req)
}

func ReadOfficialProviderBody(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, errors.New("empty provider response")
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func envOrDefault(key string, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return strings.TrimRight(val, "/")
	}
	return strings.TrimRight(fallback, "/")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if cleaned := strings.TrimSpace(value); cleaned != "" {
			return cleaned
		}
	}
	return ""
}

func EstimateUsageFromMessages(messages []models.Message, completion string) models.Usage {
	promptChars := 0
	for _, message := range messages {
		promptChars += len(ExtractText(message.Content, false))
	}
	return models.Usage{
		PromptTokens:     promptChars / 4,
		CompletionTokens: len(completion) / 4,
		TotalTokens:      (promptChars + len(completion)) / 4,
	}
}
