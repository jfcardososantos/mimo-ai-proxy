package services

import (
	"errors"
	"strings"
	"time"
)

type StoredWebSession struct {
	Provider  string            `json:"provider,omitempty"`
	Cookie    string            `json:"cookie,omitempty"`
	Token     string            `json:"token,omitempty"`
	UserAgent string            `json:"userAgent,omitempty"`
	Origin    string            `json:"origin,omitempty"`
	Referer   string            `json:"referer,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Storage   map[string]string `json:"storage,omitempty"`
	Source    string            `json:"source,omitempty"`
	UpdatedAt string            `json:"updatedAt,omitempty"`
}

type WebProviderDefinition struct {
	ID          string
	Name        string
	LoginURL    string
	Description string
	Implemented bool
}

func NormalizeWebProviderName(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

func WebProviderDefinitions() []WebProviderDefinition {
	return []WebProviderDefinition{
		{
			ID:          "deepseek",
			Name:        "DeepSeek Web",
			LoginURL:    "https://chat.deepseek.com/",
			Description: "Adapter web implementado com cookie, userToken e PoW.",
			Implemented: true,
		},
		{
			ID:          "kimi",
			Name:        "Kimi Web",
			LoginURL:    "https://www.kimi.com/",
			Description: "Adapter web implementado com access_token do localStorage e cookie de contingência.",
			Implemented: true,
		},
		{
			ID:          "gemini-web",
			Name:        "Gemini Web",
			LoginURL:    "https://gemini.google.com/",
			Description: "Sessão web armazenável; adapter ainda não implementado.",
			Implemented: false,
		},
		{
			ID:          "chatgpt-web",
			Name:        "ChatGPT Web",
			LoginURL:    "https://chatgpt.com/",
			Description: "Sessão web armazenável; adapter ainda não implementado.",
			Implemented: false,
		},
		{
			ID:          "claude-web",
			Name:        "Claude Web",
			LoginURL:    "https://claude.ai/",
			Description: "Sessão web armazenável; adapter ainda não implementado.",
			Implemented: false,
		},
		{
			ID:          "perplexity-web",
			Name:        "Perplexity Web",
			LoginURL:    "https://www.perplexity.ai/",
			Description: "Sessão web armazenável; adapter ainda não implementado.",
			Implemented: false,
		},
	}
}

func GetStoredWebSession(provider string) (StoredWebSession, error) {
	provider = NormalizeWebProviderName(provider)
	if provider == "" {
		return StoredWebSession{}, errors.New("missing web provider")
	}

	stored, err := LoadStoredAuth()
	if err != nil {
		return StoredWebSession{}, err
	}

	sessions := StoredWebSessions(stored)
	session, ok := sessions[provider]
	if !ok {
		return StoredWebSession{}, errors.New(provider + " web session not configured")
	}
	session.Provider = provider
	return session, nil
}

func StoredWebSessions(stored StoredAuth) map[string]StoredWebSession {
	out := make(map[string]StoredWebSession)
	for provider, session := range stored.WebSessions {
		key := NormalizeWebProviderName(provider)
		if key == "" {
			continue
		}
		session.Provider = key
		out[key] = normalizeStoredWebSession(session)
	}

	if legacy := legacyDeepSeekWebSession(stored); legacy.Cookie != "" || WebSessionToken(legacy) != "" {
		if _, ok := out["deepseek"]; !ok {
			legacy.Provider = "deepseek"
			out["deepseek"] = normalizeStoredWebSession(legacy)
		}
	}

	return out
}

func UpsertStoredWebSession(existing StoredAuth, provider string, session StoredWebSession) StoredAuth {
	provider = NormalizeWebProviderName(provider)
	if existing.WebSessions == nil {
		existing.WebSessions = make(map[string]StoredWebSession)
	}

	session.Provider = provider
	session = normalizeStoredWebSession(session)
	if session.UpdatedAt == "" {
		session.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	existing.WebSessions[provider] = session

	if provider == "deepseek" {
		existing.DeepSeekCookie = session.Cookie
		existing.DeepSeekToken = WebSessionToken(session)
	}

	return existing
}

func WebSessionToken(session StoredWebSession) string {
	if token := strings.TrimSpace(session.Token); token != "" {
		return token
	}
	if token := strings.TrimSpace(session.Storage["userToken"]); token != "" {
		return token
	}
	if token := strings.TrimSpace(session.Storage["access_token"]); token != "" {
		return token
	}
	if authHeader := strings.TrimSpace(session.Headers["authorization"]); strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return strings.TrimSpace(authHeader[7:])
	}
	return ""
}

func ValidateWebSessionInput(provider string, session StoredWebSession) (StoredWebSession, error) {
	provider = NormalizeWebProviderName(provider)
	if provider == "" {
		return StoredWebSession{}, errors.New("provider is required")
	}

	session.Provider = provider
	session.Cookie = strings.TrimSpace(session.Cookie)
	session.Token = strings.TrimSpace(session.Token)
	session.UserAgent = strings.TrimSpace(session.UserAgent)
	session.Origin = strings.TrimSpace(session.Origin)
	session.Referer = strings.TrimSpace(session.Referer)
	session.Source = strings.TrimSpace(session.Source)
	session.Headers = normalizeStringMap(session.Headers)
	session.Storage = normalizeStringMap(session.Storage)

	switch provider {
	case "deepseek":
		auth, err := ValidateDeepSeekAuthInput(session.Cookie, WebSessionToken(session))
		if err != nil {
			return StoredWebSession{}, err
		}
		session.Cookie = auth.Cookie
		session.Token = auth.Token
		if session.Storage == nil {
			session.Storage = make(map[string]string)
		}
		if strings.TrimSpace(session.Storage["userToken"]) == "" {
			session.Storage["userToken"] = auth.Token
		}
	case "kimi":
		if WebSessionToken(session) == "" && kimiTokenFromCookie(session.Cookie) == "" {
			return StoredWebSession{}, errors.New("missing Kimi access_token from localStorage or kimi-auth cookie")
		}
	default:
		if session.Cookie == "" {
			return StoredWebSession{}, errors.New("missing raw cookie jar")
		}
		session.Token = WebSessionToken(session)
	}

	if session.UpdatedAt == "" {
		session.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return normalizeStoredWebSession(session), nil
}

func legacyDeepSeekWebSession(stored StoredAuth) StoredWebSession {
	session := StoredWebSession{
		Provider: "deepseek",
		Cookie:   strings.TrimSpace(stored.DeepSeekCookie),
		Token:    strings.TrimSpace(stored.DeepSeekToken),
	}
	if session.Token != "" {
		session.Storage = map[string]string{"userToken": session.Token}
	}
	return normalizeStoredWebSession(session)
}

func normalizeStoredWebSession(session StoredWebSession) StoredWebSession {
	session.Provider = NormalizeWebProviderName(session.Provider)
	session.Cookie = strings.TrimSpace(session.Cookie)
	session.Token = strings.TrimSpace(session.Token)
	session.UserAgent = strings.TrimSpace(session.UserAgent)
	session.Origin = strings.TrimSpace(session.Origin)
	session.Referer = strings.TrimSpace(session.Referer)
	session.Source = strings.TrimSpace(session.Source)
	session.Headers = normalizeStringMap(session.Headers)
	session.Storage = normalizeStringMap(session.Storage)
	return session
}

func normalizeStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string)
	for key, value := range values {
		cleanKey := strings.TrimSpace(key)
		cleanValue := strings.TrimSpace(value)
		if cleanKey == "" || cleanValue == "" {
			continue
		}
		out[cleanKey] = cleanValue
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
