package services

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const authStorePath = "data/auth.json"

type StoredAuth struct {
	XiaomiCookie          string `json:"xiaomiCookie,omitempty"`
	ServiceToken          string `json:"serviceToken,omitempty"`
	UserID                string `json:"userId,omitempty"`
	XiaomiChatbot         string `json:"xiaomiChatbotPh,omitempty"`
	DeepSeekCookie        string `json:"deepseekCookie,omitempty"`
	DeepSeekToken         string `json:"deepseekToken,omitempty"`
	GeminiAPIKey          string `json:"geminiApiKey,omitempty"`
	GroqAPIKey            string `json:"groqApiKey,omitempty"`
	OpenRouterAPIKey      string `json:"openRouterApiKey,omitempty"`
	OpenRouterHTTPReferer string `json:"openRouterHttpReferer,omitempty"`
	OpenRouterAppTitle    string `json:"openRouterAppTitle,omitempty"`
	CloudflareAPIKey      string `json:"cloudflareApiKey,omitempty"`
	CloudflareAccountID   string `json:"cloudflareAccountId,omitempty"`
	DefaultModel          string `json:"defaultModel,omitempty"`
	RequestAPIKey         string `json:"requestApiKey,omitempty"`
}

func authConfigPath() string {
	if custom := strings.TrimSpace(os.Getenv("AUTH_STORE_PATH")); custom != "" {
		return custom
	}
	return authStorePath
}

func AuthStorePathForDisplay() string {
	return authConfigPath()
}

func LoadStoredAuth() (StoredAuth, error) {
	path := authConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return StoredAuth{}, nil
		}
		return StoredAuth{}, err
	}

	var stored StoredAuth
	if err := json.Unmarshal(data, &stored); err != nil {
		return StoredAuth{}, err
	}

	return stored, nil
}

func SaveStoredAuth(stored StoredAuth) error {
	path := authConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o600)
}

func ClearStoredAuth() error {
	path := authConfigPath()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func ConfiguredDefaultModel() string {
	if value := validDefaultModel(os.Getenv("DEFAULT_MODEL")); value != "" {
		return value
	}
	stored, err := LoadStoredAuth()
	if err == nil {
		if value := validDefaultModel(stored.DefaultModel); value != "" {
			return value
		}
	}
	return "mimo-v2.5-pro"
}

func validDefaultModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" || strings.EqualFold(model, "default") {
		return ""
	}
	return model
}

func ResolveRequestedModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" || strings.EqualFold(model, "default") {
		return ConfiguredDefaultModel()
	}
	return model
}

func RequestAPIKeys() []string {
	var keys []string
	seen := make(map[string]bool)
	for _, envKey := range []string{"REQUEST_API_KEY", "INFERENCE_API_KEY", "PROXY_API_KEY", "API_KEY"} {
		value := strings.TrimSpace(os.Getenv(envKey))
		if value != "" && !seen[value] {
			keys = append(keys, value)
			seen[value] = true
		}
	}
	stored, err := LoadStoredAuth()
	if err == nil {
		value := strings.TrimSpace(stored.RequestAPIKey)
		if value != "" && !seen[value] {
			keys = append(keys, value)
		}
	}
	return keys
}

func RequestAuthEnabled() bool {
	return len(RequestAPIKeys()) > 0
}

func ValidateRequestAPIKey(candidate string) bool {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return false
	}
	for _, key := range RequestAPIKeys() {
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(key)) == 1 {
			return true
		}
	}
	return false
}
