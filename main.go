/*
 * File: main.go
 * Project: flip-ai
 * Author: Pedro Farias
 * Created: 2026-04-29
 */

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"flip-ai/internal/routes"
	"flip-ai/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

var startTime time.Time

const settingsCookieName = "flip_ai_settings"

var usageStats = struct {
	sync.Mutex
	TotalRequests int
	ChatRequests  int
	LastRequestAt time.Time
	StatusCounts  map[int]int
}{
	StatusCounts: make(map[int]int),
}

func init() {
	startTime = time.Now()
}

func loginURL() string {
	return "https://aistudio.xiaomimimo.com/"
}

func qrCodeURL(target string) string {
	return "https://api.qrserver.com/v1/create-qr-code/?size=220x220&data=" + url.QueryEscape(target)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func maskValue(raw string) gin.H {
	trimmed := strings.TrimSpace(raw)
	masked := ""

	switch {
	case trimmed == "":
		masked = ""
	case len(trimmed) <= 8:
		masked = trimmed
	default:
		masked = trimmed[:4] + "..." + trimmed[len(trimmed)-4:]
	}

	return gin.H{
		"present":       trimmed != "",
		"length":        len(trimmed),
		"masked":        masked,
		"hasSpaces":     strings.Contains(trimmed, " "),
		"hasQuotes":     strings.ContainsAny(trimmed, `"'`),
		"startsWith":    func() string { if len(trimmed) >= 4 { return trimmed[:4] }; return trimmed }(),
		"endsWith":      func() string { if len(trimmed) >= 4 { return trimmed[len(trimmed)-4:] }; return trimmed }(),
	}
}

func buildStoredAuth(rawCookie string, token string, userID string, ph string) services.StoredAuth {
	return services.StoredAuth{
		XiaomiCookie:  strings.TrimSpace(rawCookie),
		ServiceToken:  strings.TrimSpace(token),
		UserID:        strings.TrimSpace(userID),
		XiaomiChatbot: strings.TrimSpace(ph),
	}
}

func mergeXiaomiAuth(existing services.StoredAuth, rawCookie string, token string, userID string, ph string) services.StoredAuth {
	stored := buildStoredAuth(rawCookie, token, userID, ph)
	stored.DeepSeekCookie = existing.DeepSeekCookie
	stored.DeepSeekToken = existing.DeepSeekToken
	stored.GeminiAPIKey = existing.GeminiAPIKey
	stored.GroqAPIKey = existing.GroqAPIKey
	stored.OpenRouterAPIKey = existing.OpenRouterAPIKey
	stored.OpenRouterHTTPReferer = existing.OpenRouterHTTPReferer
	stored.OpenRouterAppTitle = existing.OpenRouterAppTitle
	stored.CloudflareAPIKey = existing.CloudflareAPIKey
	stored.CloudflareAccountID = existing.CloudflareAccountID
	stored.DefaultModel = existing.DefaultModel
	stored.RequestAPIKey = existing.RequestAPIKey
	return stored
}

func mergeDeepSeekAuth(existing services.StoredAuth, rawCookie string, token string) services.StoredAuth {
	existing.DeepSeekCookie = strings.TrimSpace(rawCookie)
	existing.DeepSeekToken = strings.TrimSpace(token)
	return existing
}

func mergeProviderAuth(existing services.StoredAuth, provider string, apiKey string, accountID string, httpReferer string, appTitle string) (services.StoredAuth, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	apiKey = strings.TrimSpace(apiKey)
	accountID = strings.TrimSpace(accountID)
	httpReferer = strings.TrimSpace(httpReferer)
	appTitle = strings.TrimSpace(appTitle)

	switch provider {
	case "gemini":
		existing.GeminiAPIKey = apiKey
	case "groq":
		existing.GroqAPIKey = apiKey
	case "openrouter":
		existing.OpenRouterAPIKey = apiKey
		if httpReferer != "" {
			existing.OpenRouterHTTPReferer = httpReferer
		}
		if appTitle != "" {
			existing.OpenRouterAppTitle = appTitle
		}
	case "cloudflare":
		existing.CloudflareAPIKey = apiKey
		existing.CloudflareAccountID = accountID
	default:
		return existing, fmt.Errorf("unsupported provider %q", provider)
	}

	return existing, nil
}

func zipDirectory(root string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		writer, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		_, err = io.Copy(writer, file)
		return err
	})
	if err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func hasStoredAuth(stored services.StoredAuth, storedErr error) bool {
	return storedErr == nil &&
		(strings.TrimSpace(stored.XiaomiCookie) != "" ||
			strings.TrimSpace(stored.ServiceToken) != "" ||
			strings.TrimSpace(stored.UserID) != "" ||
			strings.TrimSpace(stored.XiaomiChatbot) != "" ||
			strings.TrimSpace(stored.DeepSeekCookie) != "" ||
			strings.TrimSpace(stored.DeepSeekToken) != "" ||
			strings.TrimSpace(stored.GeminiAPIKey) != "" ||
			strings.TrimSpace(stored.GroqAPIKey) != "" ||
			strings.TrimSpace(stored.OpenRouterAPIKey) != "" ||
			strings.TrimSpace(stored.OpenRouterHTTPReferer) != "" ||
			strings.TrimSpace(stored.OpenRouterAppTitle) != "" ||
			strings.TrimSpace(stored.CloudflareAPIKey) != "" ||
			strings.TrimSpace(stored.CloudflareAccountID) != "" ||
			strings.TrimSpace(stored.DefaultModel) != "" ||
			strings.TrimSpace(stored.RequestAPIKey) != "")
}

func detectAuthSource(stored services.StoredAuth, storedErr error) string {
	if hasStoredAuth(stored, storedErr) {
		return "data/auth.json"
	}
	return "none"
}

func officialModelListHTML() string {
	var items []string
	for _, model := range services.OfficialProviderModels() {
		id, _ := model["id"].(string)
		description, _ := model["description"].(string)
		if id != "" {
			items = append(items, fmt.Sprintf("<li><code>%s</code> - %s</li>", id, description))
		}
	}
	return strings.Join(items, "")
}

func setupSecrets() []string {
	var secrets []string
	seen := make(map[string]bool)
	for _, key := range []string{"API_KEY", "SETTINGS_PASSWORD", "CONFIG_PASSWORD"} {
		value := strings.TrimSpace(os.Getenv(key))
		if value != "" && !seen[value] {
			secrets = append(secrets, value)
			seen[value] = true
		}
	}
	return secrets
}

func requestAPIKeySource(stored services.StoredAuth) string {
	for _, key := range []string{"REQUEST_API_KEY", "INFERENCE_API_KEY", "PROXY_API_KEY", "API_KEY"} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return key
		}
	}
	if strings.TrimSpace(stored.RequestAPIKey) != "" {
		return "data/auth.json"
	}
	return "disabled"
}

func defaultModelSource(stored services.StoredAuth) string {
	if value := strings.TrimSpace(os.Getenv("DEFAULT_MODEL")); value != "" && !strings.EqualFold(value, "default") {
		return "env"
	}
	if value := strings.TrimSpace(stored.DefaultModel); value != "" && !strings.EqualFold(value, "default") {
		return "data/auth.json"
	}
	return "built-in"
}

func settingsPassword() string {
	for _, key := range []string{"SETTINGS_PASSWORD", "CONFIG_PASSWORD", "API_KEY"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func secretMatches(candidate string, secrets []string) bool {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return false
	}
	for _, secret := range secrets {
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(secret)) == 1 {
			return true
		}
	}
	return false
}

func settingsSessionValue(password string) string {
	sum := sha256.Sum256([]byte("flip-ai-settings:" + password))
	return hex.EncodeToString(sum[:])
}

func settingsAuthenticated(c *gin.Context) bool {
	password := settingsPassword()
	if password == "" {
		return false
	}
	cookieValue, err := c.Cookie(settingsCookieName)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookieValue), []byte(settingsSessionValue(password))) == 1
}

func setSettingsCookie(c *gin.Context) {
	password := settingsPassword()
	if password == "" {
		return
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(settingsCookieName, settingsSessionValue(password), 8*60*60, "/", "", false, true)
}

func clearSettingsCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(settingsCookieName, "", -1, "/", "", false, true)
}

func isAPIRoute(path string) bool {
	return strings.HasPrefix(path, "/v1/") ||
		strings.HasPrefix(path, "/api/") ||
		path == "/open-apis/bot/chat"
}

func recordAPIUsage(path string, status int) {
	if !isAPIRoute(path) {
		return
	}
	usageStats.Lock()
	defer usageStats.Unlock()
	usageStats.TotalRequests++
	if strings.Contains(path, "/chat") || strings.Contains(path, "/completions") || path == "/api/generate" {
		usageStats.ChatRequests++
	}
	usageStats.LastRequestAt = time.Now()
	usageStats.StatusCounts[status]++
}

func usageSnapshot() gin.H {
	usageStats.Lock()
	defer usageStats.Unlock()

	lastRequest := "Nunca"
	if !usageStats.LastRequestAt.IsZero() {
		lastRequest = usageStats.LastRequestAt.Format(time.RFC3339)
	}

	success := usageStats.StatusCounts[200] + usageStats.StatusCounts[201] + usageStats.StatusCounts[204]
	errors := 0
	for status, count := range usageStats.StatusCounts {
		if status >= 400 {
			errors += count
		}
	}

	return gin.H{
		"TotalRequests": usageStats.TotalRequests,
		"ChatRequests":  usageStats.ChatRequests,
		"LastRequestAt": lastRequest,
		"Success":       success,
		"Errors":        errors,
	}
}

func providerConfiguredFromEnvOrStore(envKey string, storedValue string) (bool, string) {
	if strings.TrimSpace(os.Getenv(envKey)) != "" {
		return true, "env"
	}
	if strings.TrimSpace(storedValue) != "" {
		return true, "data/auth.json"
	}
	return false, "missing"
}

func statusLabel(configured bool) string {
	if configured {
		return "online"
	}
	return "pendente"
}

func storedXiaomiPresent(stored services.StoredAuth) bool {
	return strings.TrimSpace(stored.XiaomiCookie) != "" ||
		strings.TrimSpace(stored.ServiceToken) != "" ||
		strings.TrimSpace(stored.UserID) != "" ||
		strings.TrimSpace(stored.XiaomiChatbot) != ""
}

func xiaomiCredentialSource(stored services.StoredAuth, storedErr error, configured bool) string {
	if !configured {
		return "missing"
	}
	if strings.TrimSpace(os.Getenv("XIAOMI_COOKIE")) != "" ||
		strings.TrimSpace(os.Getenv("SERVICE_TOKEN")) != "" ||
		strings.TrimSpace(os.Getenv("USER_ID")) != "" ||
		strings.TrimSpace(os.Getenv("XIAOMI_CHATBOT_PH")) != "" {
		return "env"
	}
	if storedErr == nil && storedXiaomiPresent(stored) {
		return "data/auth.json"
	}
	return "configured"
}

func sourceWhenConfigured(configured bool, source string) string {
	if configured {
		return source
	}
	return "missing"
}

func cloudflareProviderDetail(keyConfigured bool, accountConfigured bool) string {
	if keyConfigured && !accountConfigured {
		return "CLOUDFLARE_ACCOUNT_ID ausente"
	}
	return ""
}

func providerRows(stored services.StoredAuth, storedErr error) []gin.H {
	rows := []gin.H{}

	_, xiaomiErr := services.GetSelectedAuth()
	xiaomiConfigured := xiaomiErr == nil
	rows = append(rows, gin.H{
		"Name":       "Xiaomi Mimo",
		"Key":        "xiaomi",
		"Configured": xiaomiConfigured,
		"Source":     xiaomiCredentialSource(stored, storedErr, xiaomiConfigured),
		"Status":     statusLabel(xiaomiConfigured),
		"Detail":     errString(xiaomiErr),
	})

	_, deepSeekErr := services.GetSelectedDeepSeekAuth()
	deepSeekConfigured := deepSeekErr == nil
	rows = append(rows, gin.H{
		"Name":       "DeepSeek Web",
		"Key":        "deepseek",
		"Configured": deepSeekConfigured,
		"Source":     sourceWhenConfigured(deepSeekConfigured, "data/auth.json"),
		"Status":     statusLabel(deepSeekConfigured),
		"Detail":     errString(deepSeekErr),
	})

	geminiConfigured, geminiSource := providerConfiguredFromEnvOrStore("GEMINI_API_KEY", stored.GeminiAPIKey)
	groqConfigured, groqSource := providerConfiguredFromEnvOrStore("GROQ_API_KEY", stored.GroqAPIKey)
	openRouterConfigured, openRouterSource := providerConfiguredFromEnvOrStore("OPENROUTER_API_KEY", stored.OpenRouterAPIKey)
	cloudflareKeyConfigured, cloudflareKeySource := providerConfiguredFromEnvOrStore("CLOUDFLARE_API_KEY", stored.CloudflareAPIKey)
	cloudflareAccountConfigured, _ := providerConfiguredFromEnvOrStore("CLOUDFLARE_ACCOUNT_ID", stored.CloudflareAccountID)

	rows = append(rows,
		gin.H{"Name": "Gemini", "Key": "gemini", "Configured": geminiConfigured, "Source": geminiSource, "Status": statusLabel(geminiConfigured), "Detail": ""},
		gin.H{"Name": "Groq", "Key": "groq", "Configured": groqConfigured, "Source": groqSource, "Status": statusLabel(groqConfigured), "Detail": ""},
		gin.H{"Name": "OpenRouter", "Key": "openrouter", "Configured": openRouterConfigured, "Source": openRouterSource, "Status": statusLabel(openRouterConfigured), "Detail": ""},
		gin.H{"Name": "Cloudflare Workers AI", "Key": "cloudflare", "Configured": cloudflareKeyConfigured && cloudflareAccountConfigured, "Source": cloudflareKeySource, "Status": statusLabel(cloudflareKeyConfigured && cloudflareAccountConfigured), "Detail": cloudflareProviderDetail(cloudflareKeyConfigured, cloudflareAccountConfigured)},
	)

	return rows
}

func availableModelRows() []gin.H {
	rows := []gin.H{
		{"ID": "default", "Provider": "flip-ai", "Description": "Alias para " + services.ConfiguredDefaultModel()},
	}
	if _, err := services.GetSelectedAuth(); err == nil {
		rows = append(rows,
			gin.H{"ID": "mimo-v2.5-pro", "Provider": "Xiaomi Mimo", "Description": "Modelo principal Mimo"},
			gin.H{"ID": "mimo-v2.5-pro-no-thinking", "Provider": "Xiaomi Mimo", "Description": "Mimo sem reasoning para agentes"},
		)
	}
	if _, err := services.GetSelectedDeepSeekAuth(); err == nil {
		rows = append(rows,
			gin.H{"ID": "deepseek-chat", "Provider": "DeepSeek Web", "Description": "Chat web DeepSeek"},
			gin.H{"ID": "deepseek-reasoner", "Provider": "DeepSeek Web", "Description": "DeepSeek com thinking"},
			gin.H{"ID": "deepseek-search", "Provider": "DeepSeek Web", "Description": "DeepSeek com busca web"},
		)
	}
	for _, model := range services.OfficialProviderModels() {
		id, _ := model["id"].(string)
		description, _ := model["description"].(string)
		provider, ok := services.SelectOfficialProvider(id)
		if ok && provider.Configured {
			rows = append(rows, gin.H{
				"ID":          id,
				"Provider":    provider.Name,
				"Description": description,
			})
		}
	}
	return rows
}

func maskedStoredAuth(stored services.StoredAuth) gin.H {
	return gin.H{
		"XIAOMI_COOKIE":           maskValue(stored.XiaomiCookie),
		"SERVICE_TOKEN":           maskValue(stored.ServiceToken),
		"USER_ID":                 maskValue(stored.UserID),
		"XIAOMI_CHATBOT_PH":       maskValue(stored.XiaomiChatbot),
		"DEEPSEEK_COOKIE":         maskValue(stored.DeepSeekCookie),
		"DEEPSEEK_TOKEN":          maskValue(stored.DeepSeekToken),
		"GEMINI_API_KEY":          maskValue(stored.GeminiAPIKey),
		"GROQ_API_KEY":            maskValue(stored.GroqAPIKey),
		"OPENROUTER_API_KEY":      maskValue(stored.OpenRouterAPIKey),
		"OPENROUTER_HTTP_REFERER": maskValue(stored.OpenRouterHTTPReferer),
		"OPENROUTER_APP_TITLE":    maskValue(stored.OpenRouterAppTitle),
		"CLOUDFLARE_API_KEY":      maskValue(stored.CloudflareAPIKey),
		"CLOUDFLARE_ACCOUNT_ID":   maskValue(stored.CloudflareAccountID),
		"REQUEST_API_KEY":         maskValue(stored.RequestAPIKey),
	}
}

func totalTrackedProviderRequests() gin.H {
	tokenStats, tokenUsage, responseTimes := routes.GetStats()
	totalRequests := 0
	totalTokens := 0
	for token, count := range tokenStats {
		totalRequests += count
		totalTokens += tokenUsage[token]
	}

	avgTimeStr := "N/A"
	if len(responseTimes) > 0 {
		var sum int64
		for _, t := range responseTimes {
			sum += t
		}
		avgTimeStr = fmt.Sprintf("%dms", sum/int64(len(responseTimes)))
	}

	return gin.H{
		"ProviderRequests": totalRequests,
		"ProviderTokens":   totalTokens,
		"AvgLatency":       avgTimeStr,
	}
}

func renderDashboard(c *gin.Context) {
	storedAuth, storedAuthErr := services.LoadStoredAuth()
	modelRows := availableModelRows()
	providers := providerRows(storedAuth, storedAuthErr)
	usage := usageSnapshot()
	providerUsage := totalTrackedProviderRequests()

	status := "operacional"
	configuredProviders := 0
	for _, provider := range providers {
		if configured, _ := provider["Configured"].(bool); configured {
			configuredProviders++
		}
	}
	if configuredProviders == 0 {
		status = "sem modelos configurados"
	}

	c.HTML(http.StatusOK, "dashboard.html", gin.H{
		"ProductName":     "flip-ai",
		"Status":          status,
		"Uptime":          fmt.Sprintf("%.0f", time.Since(startTime).Seconds()),
		"Usage":           usage,
		"ProviderUsage":   providerUsage,
		"Models":          modelRows,
		"Providers":       providers,
		"AuthStore":       services.AuthStorePathForDisplay(),
		"StoredError":     errString(storedAuthErr),
		"SettingsEnabled": settingsPassword() != "",
		"DefaultModel":    services.ConfiguredDefaultModel(),
		"DefaultSource":   defaultModelSource(storedAuth),
		"RequestAuth":     services.RequestAuthEnabled(),
		"RequestKeySource": requestAPIKeySource(storedAuth),
	})
}

func renderSettings(c *gin.Context, status int, message string, errorMessage string, authenticated bool) {
	storedAuth, storedAuthErr := services.LoadStoredAuth()
	c.HTML(status, "settings.html", gin.H{
		"ProductName":       "flip-ai",
		"Authenticated":     authenticated,
		"SettingsEnabled":   settingsPassword() != "",
		"Message":           message,
		"Error":             errorMessage,
		"AuthStore":         services.AuthStorePathForDisplay(),
		"StoredError":       errString(storedAuthErr),
		"Stored":            maskedStoredAuth(storedAuth),
		"Providers":         providerRows(storedAuth, storedAuthErr),
		"ExtensionDownload": "/downloads/flip-ai-session-extension.zip",
		"DefaultModel":      services.ConfiguredDefaultModel(),
		"DefaultSource":     defaultModelSource(storedAuth),
		"RequestAuth":       services.RequestAuthEnabled(),
		"RequestKeySource":  requestAPIKeySource(storedAuth),
	})
}

func requireSettingsAccess(c *gin.Context) bool {
	if settingsAuthenticated(c) {
		return true
	}
	message := ""
	if settingsPassword() == "" {
		message = "Defina SETTINGS_PASSWORD no env para habilitar a tela protegida de configurações."
	}
	renderSettings(c, http.StatusUnauthorized, "", message, false)
	return false
}

func validateSetupAccess(c *gin.Context) bool {
	secrets := setupSecrets()
	if len(secrets) == 0 {
		return true
	}

	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
	if strings.HasPrefix(authHeader, "Bearer ") && secretMatches(strings.TrimPrefix(authHeader, "Bearer "), secrets) {
		return true
	}

	if secretMatches(c.PostForm("api_key"), secrets) {
		return true
	}

	if secretMatches(c.Query("api_key"), secrets) {
		return true
	}

	c.JSON(http.StatusUnauthorized, gin.H{
		"error":   "Missing or invalid API key",
		"details": "Use Authorization: Bearer <API_KEY or SETTINGS_PASSWORD> or submit api_key in the setup form.",
	})
	return false
}

func requestAPIKeyFromRequest(c *gin.Context) string {
	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
	if strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	}
	if value := strings.TrimSpace(c.GetHeader("X-API-Key")); value != "" {
		return value
	}
	if value := strings.TrimSpace(c.Query("api_key")); value != "" {
		return value
	}
	return ""
}

func inferenceAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !services.RequestAuthEnabled() {
			c.Next()
			return
		}
		if services.ValidateRequestAPIKey(requestAPIKeyFromRequest(c)) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{
				"message": "Missing or invalid API key",
				"type":    "authentication_error",
				"param":   nil,
				"code":    "invalid_api_key",
			},
		})
	}
}

func main() {
	_ = godotenv.Load()
	
	// Initialize local database
	services.InitDB()

	r := gin.New()
	
	// Set up templates
	r.SetFuncMap(template.FuncMap{
		"safe": func(s string) template.HTML {
			return template.HTML(s)
		},
	})
	r.LoadHTMLGlob("templates/*")

	// Set Max Request Body Size to 100MB
	r.Use(func(c *gin.Context) {
		// Increase limit to 100MB
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 100<<20)
		c.Next()
	})
	r.Use(gin.Recovery())

	// Custom Logger
	r.Use(func(c *gin.Context) {
		if c.Request.URL.Path != "/health" {
			log.Printf("[%s] %s %s", time.Now().Format(time.RFC3339), c.Request.Method, c.Request.URL.Path)
		}
		c.Next()
	})

	r.Use(func(c *gin.Context) {
		c.Next()
		recordAPIUsage(c.Request.URL.Path, c.Writer.Status())
	})

	// Configurable CORS
	r.Use(func(c *gin.Context) {
		origin := os.Getenv("CORS_ORIGIN")
		if origin == "" {
			origin = "*"
		}
		c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key, X-Requested-With, Accept, X-Timezone, OpenAI-Beta")
		c.Writer.Header().Set("Access-Control-Expose-Headers", "Content-Type")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	r.GET("/", renderDashboard)
	r.GET("/dashboard", renderDashboard)

	r.GET("/settings", func(c *gin.Context) {
		if !requireSettingsAccess(c) {
			return
		}
		renderSettings(c, http.StatusOK, "", "", true)
	})

	r.POST("/settings/login", func(c *gin.Context) {
		password := settingsPassword()
		if password == "" {
			renderSettings(c, http.StatusServiceUnavailable, "", "Defina SETTINGS_PASSWORD no env antes de acessar as configurações.", false)
			return
		}
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(c.PostForm("password"))), []byte(password)) != 1 {
			renderSettings(c, http.StatusUnauthorized, "", "Senha inválida.", false)
			return
		}
		setSettingsCookie(c)
		c.Redirect(http.StatusSeeOther, "/settings")
	})

	r.POST("/settings/logout", func(c *gin.Context) {
		clearSettingsCookie(c)
		c.Redirect(http.StatusSeeOther, "/dashboard")
	})

	r.POST("/settings/xiaomi/import", func(c *gin.Context) {
		if !requireSettingsAccess(c) {
			return
		}
		auth, err := services.ValidateAuthInput(c.PostForm("xiaomi_cookie"), c.PostForm("service_token"), c.PostForm("user_id"), c.PostForm("xiaomi_chatbot_ph"))
		if err != nil {
			renderSettings(c, http.StatusBadRequest, "", "Credenciais Xiaomi inválidas: "+err.Error(), true)
			return
		}
		existing, _ := services.LoadStoredAuth()
		stored := mergeXiaomiAuth(existing, c.PostForm("xiaomi_cookie"), auth.Token, auth.UserID, auth.Ph)
		if err := services.SaveStoredAuth(stored); err != nil {
			renderSettings(c, http.StatusInternalServerError, "", "Falha ao salvar Xiaomi: "+err.Error(), true)
			return
		}
		renderSettings(c, http.StatusOK, "Sessão Xiaomi salva.", "", true)
	})

	r.POST("/settings/deepseek/import", func(c *gin.Context) {
		if !requireSettingsAccess(c) {
			return
		}
		auth, err := services.ValidateDeepSeekAuthInput(c.PostForm("deepseek_cookie"), c.PostForm("deepseek_token"))
		if err != nil {
			renderSettings(c, http.StatusBadRequest, "", "Credenciais DeepSeek inválidas: "+err.Error(), true)
			return
		}
		existing, _ := services.LoadStoredAuth()
		stored := mergeDeepSeekAuth(existing, auth.Cookie, auth.Token)
		if err := services.SaveStoredAuth(stored); err != nil {
			renderSettings(c, http.StatusInternalServerError, "", "Falha ao salvar DeepSeek: "+err.Error(), true)
			return
		}
		renderSettings(c, http.StatusOK, "Sessão DeepSeek salva.", "", true)
	})

	r.POST("/settings/provider/import", func(c *gin.Context) {
		if !requireSettingsAccess(c) {
			return
		}
		provider := c.PostForm("provider")
		if strings.TrimSpace(provider) == "" {
			renderSettings(c, http.StatusBadRequest, "", "Escolha um provedor.", true)
			return
		}
		if strings.TrimSpace(c.PostForm("api_key")) == "" {
			renderSettings(c, http.StatusBadRequest, "", "A chave de API é obrigatória.", true)
			return
		}
		if strings.EqualFold(strings.TrimSpace(provider), "cloudflare") && strings.TrimSpace(c.PostForm("account_id")) == "" {
			renderSettings(c, http.StatusBadRequest, "", "Cloudflare exige Account ID.", true)
			return
		}
		existing, _ := services.LoadStoredAuth()
		stored, err := mergeProviderAuth(existing, provider, c.PostForm("api_key"), c.PostForm("account_id"), c.PostForm("http_referer"), c.PostForm("app_title"))
		if err != nil {
			renderSettings(c, http.StatusBadRequest, "", err.Error(), true)
			return
		}
		if err := services.SaveStoredAuth(stored); err != nil {
			renderSettings(c, http.StatusInternalServerError, "", "Falha ao salvar provedor: "+err.Error(), true)
			return
		}
		renderSettings(c, http.StatusOK, "Provedor salvo.", "", true)
	})

	r.POST("/settings/runtime", func(c *gin.Context) {
		if !requireSettingsAccess(c) {
			return
		}
		defaultModel := strings.TrimSpace(c.PostForm("default_model"))
		requestAPIKey := strings.TrimSpace(c.PostForm("request_api_key"))
		clearRequestAPIKey := c.PostForm("clear_request_api_key") == "on"

		if strings.EqualFold(defaultModel, "default") {
			renderSettings(c, http.StatusBadRequest, "", "O modelo padrão não pode ser o alias default.", true)
			return
		}

		existing, _ := services.LoadStoredAuth()
		if defaultModel != "" {
			existing.DefaultModel = defaultModel
		}
		if clearRequestAPIKey {
			existing.RequestAPIKey = ""
		} else if requestAPIKey != "" {
			existing.RequestAPIKey = requestAPIKey
		}

		if err := services.SaveStoredAuth(existing); err != nil {
			renderSettings(c, http.StatusInternalServerError, "", "Falha ao salvar configurações da API: "+err.Error(), true)
			return
		}
		renderSettings(c, http.StatusOK, "Configurações da API salvas.", "", true)
	})

	r.POST("/settings/clear", func(c *gin.Context) {
		if !requireSettingsAccess(c) {
			return
		}
		if err := services.ClearStoredAuth(); err != nil {
			renderSettings(c, http.StatusInternalServerError, "", "Falha ao limpar credenciais: "+err.Error(), true)
			return
		}
		renderSettings(c, http.StatusOK, "Credenciais locais removidas.", "", true)
	})

	r.GET("/auth/status", func(c *gin.Context) {
		auth, authErr := services.GetSelectedAuth()
		stored, storedErr := services.LoadStoredAuth()
		c.JSON(http.StatusOK, gin.H{
			"configured":              authErr == nil,
			"authError":               errString(authErr),
			"authSource":              detectAuthSource(stored, storedErr),
			"storePath":               services.AuthStorePathForDisplay(),
			"storeError":              errString(storedErr),
			"storedHasCookie":         stored.XiaomiCookie != "",
			"storedHasToken":          stored.ServiceToken != "",
			"storedHasUserID":         stored.UserID != "",
			"storedHasChatbot":        stored.XiaomiChatbot != "",
			"deepseekConfigured":      stored.DeepSeekCookie != "" && stored.DeepSeekToken != "",
			"storedHasDeepSeekCookie": stored.DeepSeekCookie != "",
			"storedHasDeepSeekToken":  stored.DeepSeekToken != "",
			"defaultModel":            services.ConfiguredDefaultModel(),
			"defaultModelSource":      defaultModelSource(stored),
			"requestAuthEnabled":      services.RequestAuthEnabled(),
			"requestApiKeySource":     requestAPIKeySource(stored),
			"providers": gin.H{
				"gemini":     stored.GeminiAPIKey != "" || strings.TrimSpace(os.Getenv("GEMINI_API_KEY")) != "",
				"groq":       stored.GroqAPIKey != "" || strings.TrimSpace(os.Getenv("GROQ_API_KEY")) != "",
				"openrouter": stored.OpenRouterAPIKey != "" || strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")) != "",
				"cloudflare": (stored.CloudflareAPIKey != "" || strings.TrimSpace(os.Getenv("CLOUDFLARE_API_KEY")) != "") && (stored.CloudflareAccountID != "" || strings.TrimSpace(os.Getenv("CLOUDFLARE_ACCOUNT_ID")) != ""),
			},
			"selectedPh": auth.Ph,
		})
	})

	serveExtensionDownload := func(c *gin.Context) {
		zipBytes, err := zipDirectory("extension")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to package extension", "details": err.Error()})
			return
		}

		c.Header("Content-Type", "application/zip")
		c.Header("Content-Disposition", `attachment; filename="flip-ai-session-extension.zip"`)
		c.Data(http.StatusOK, "application/zip", zipBytes)
	}
	r.GET("/downloads/flip-ai-session-extension.zip", serveExtensionDownload)
	r.GET("/downloads/mimo-xiaomi-session-extension.zip", serveExtensionDownload)

	r.GET("/auth/debug", func(c *gin.Context) {
		if !validateSetupAccess(c) {
			return
		}

		stored, storedErr := services.LoadStoredAuth()
		auth, authErr := services.GetSelectedAuth()
		c.JSON(http.StatusOK, gin.H{
			"configured": authErr == nil,
			"authError":  errString(authErr),
			"authSource": detectAuthSource(stored, storedErr),
			"stored": gin.H{
				"loadError":         errString(storedErr),
				"XIAOMI_COOKIE":     maskValue(stored.XiaomiCookie),
				"SERVICE_TOKEN":     maskValue(stored.ServiceToken),
				"USER_ID":           maskValue(stored.UserID),
				"XIAOMI_CHATBOT_PH": maskValue(stored.XiaomiChatbot),
				"DEEPSEEK_COOKIE":   maskValue(stored.DeepSeekCookie),
				"DEEPSEEK_TOKEN":    maskValue(stored.DeepSeekToken),
				"GEMINI_API_KEY":          maskValue(stored.GeminiAPIKey),
				"GROQ_API_KEY":            maskValue(stored.GroqAPIKey),
				"OPENROUTER_API_KEY":      maskValue(stored.OpenRouterAPIKey),
				"OPENROUTER_HTTP_REFERER": maskValue(stored.OpenRouterHTTPReferer),
				"OPENROUTER_APP_TITLE":    maskValue(stored.OpenRouterAppTitle),
				"CLOUDFLARE_API_KEY":      maskValue(stored.CloudflareAPIKey),
				"CLOUDFLARE_ACCOUNT_ID":   maskValue(stored.CloudflareAccountID),
				"DEFAULT_MODEL":           maskValue(stored.DefaultModel),
				"REQUEST_API_KEY":         maskValue(stored.RequestAPIKey),
			},
			"selectedAuth": gin.H{
				"token": maskValue(auth.Token),
				"userID": maskValue(auth.UserID),
				"ph":    maskValue(auth.Ph),
			},
		})
	})

	r.POST("/auth/extension/import", func(c *gin.Context) {
		if !validateSetupAccess(c) {
			return
		}

		var payload struct {
			ServiceToken  string `json:"serviceToken"`
			UserID        string `json:"userId"`
			XiaomiChatbot string `json:"xiaomichatbotPh"`
			RawCookie     string `json:"rawCookie"`
			Source        string `json:"source"`
		}

		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid extension payload", "details": err.Error()})
			return
		}

		auth, err := services.ValidateAuthInput(payload.RawCookie, payload.ServiceToken, payload.UserID, payload.XiaomiChatbot)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Xiaomi session from extension", "details": err.Error()})
			return
		}

		existing, _ := services.LoadStoredAuth()
		stored := mergeXiaomiAuth(existing, payload.RawCookie, auth.Token, auth.UserID, auth.Ph)
		if err := services.SaveStoredAuth(stored); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to persist extension session", "details": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"saved":         true,
			"authSource":    "data/auth.json",
			"selectedPh":    auth.Ph,
			"storePath":     services.AuthStorePathForDisplay(),
			"importedFrom":  payload.Source,
			"cookiePresent": strings.TrimSpace(payload.RawCookie) != "",
		})
	})

	r.POST("/auth/import", func(c *gin.Context) {
		if !validateSetupAccess(c) {
			return
		}

		var payload struct {
			XiaomiCookie  string `json:"xiaomi_cookie" form:"xiaomi_cookie"`
			ServiceToken  string `json:"service_token" form:"service_token"`
			UserID        string `json:"user_id" form:"user_id"`
			XiaomiChatbot string `json:"xiaomi_chatbot_ph" form:"xiaomi_chatbot_ph"`
		}

		if strings.Contains(c.GetHeader("Content-Type"), "application/json") {
			body, _ := io.ReadAll(c.Request.Body)
			if len(strings.TrimSpace(string(body))) > 0 {
				if err := json.Unmarshal(body, &payload); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON payload", "details": err.Error()})
					return
				}
			}
		} else if err := c.ShouldBind(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid form payload", "details": err.Error()})
			return
		}

		auth, err := services.ValidateAuthInput(payload.XiaomiCookie, payload.ServiceToken, payload.UserID, payload.XiaomiChatbot)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Xiaomi credentials", "details": err.Error()})
			return
		}

		existing, _ := services.LoadStoredAuth()
		stored := mergeXiaomiAuth(existing, payload.XiaomiCookie, payload.ServiceToken, payload.UserID, payload.XiaomiChatbot)
		if err := services.SaveStoredAuth(stored); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to persist credentials", "details": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"saved":      true,
			"authSource": "data/auth.json",
			"selectedPh": auth.Ph,
			"storePath":  services.AuthStorePathForDisplay(),
		})
	})

	r.POST("/auth/deepseek/extension/import", func(c *gin.Context) {
		if !validateSetupAccess(c) {
			return
		}

		var payload struct {
			UserToken string `json:"userToken"`
			RawCookie string `json:"rawCookie"`
			Source    string `json:"source"`
		}

		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid DeepSeek extension payload", "details": err.Error()})
			return
		}

		auth, err := services.ValidateDeepSeekAuthInput(payload.RawCookie, payload.UserToken)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid DeepSeek session from extension", "details": err.Error()})
			return
		}

		existing, _ := services.LoadStoredAuth()
		stored := mergeDeepSeekAuth(existing, auth.Cookie, auth.Token)
		if err := services.SaveStoredAuth(stored); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to persist DeepSeek session", "details": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"saved":         true,
			"authSource":    "data/auth.json",
			"storePath":     services.AuthStorePathForDisplay(),
			"importedFrom":  payload.Source,
			"cookiePresent": strings.TrimSpace(auth.Cookie) != "",
			"tokenPresent":  strings.TrimSpace(auth.Token) != "",
		})
	})

	r.POST("/auth/deepseek/import", func(c *gin.Context) {
		if !validateSetupAccess(c) {
			return
		}

		var payload struct {
			DeepSeekCookie string `json:"deepseek_cookie" form:"deepseek_cookie"`
			DeepSeekToken  string `json:"deepseek_token" form:"deepseek_token"`
		}

		if strings.Contains(c.GetHeader("Content-Type"), "application/json") {
			body, _ := io.ReadAll(c.Request.Body)
			if len(strings.TrimSpace(string(body))) > 0 {
				if err := json.Unmarshal(body, &payload); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON payload", "details": err.Error()})
					return
				}
			}
		} else if err := c.ShouldBind(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid form payload", "details": err.Error()})
			return
		}

		auth, err := services.ValidateDeepSeekAuthInput(payload.DeepSeekCookie, payload.DeepSeekToken)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid DeepSeek credentials", "details": err.Error()})
			return
		}

		existing, _ := services.LoadStoredAuth()
		stored := mergeDeepSeekAuth(existing, auth.Cookie, auth.Token)
		if err := services.SaveStoredAuth(stored); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to persist DeepSeek credentials", "details": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"saved":      true,
			"authSource": "data/auth.json",
			"storePath":  services.AuthStorePathForDisplay(),
		})
	})

	r.POST("/auth/provider/import", func(c *gin.Context) {
		if !validateSetupAccess(c) {
			return
		}

		var payload struct {
			Provider    string `json:"provider" form:"provider"`
			APIKey      string `json:"api_key" form:"api_key"`
			AccountID   string `json:"account_id" form:"account_id"`
			HTTPReferer string `json:"http_referer" form:"http_referer"`
			AppTitle    string `json:"app_title" form:"app_title"`
		}

		if strings.Contains(c.GetHeader("Content-Type"), "application/json") {
			body, _ := io.ReadAll(c.Request.Body)
			if len(strings.TrimSpace(string(body))) > 0 {
				if err := json.Unmarshal(body, &payload); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON payload", "details": err.Error()})
					return
				}
			}
		} else if err := c.ShouldBind(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid form payload", "details": err.Error()})
			return
		}

		if strings.TrimSpace(payload.Provider) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "provider is required"})
			return
		}
		if strings.TrimSpace(payload.APIKey) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "api_key is required"})
			return
		}
		if strings.EqualFold(strings.TrimSpace(payload.Provider), "cloudflare") && strings.TrimSpace(payload.AccountID) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "account_id is required for cloudflare"})
			return
		}

		existing, _ := services.LoadStoredAuth()
		stored, err := mergeProviderAuth(existing, payload.Provider, payload.APIKey, payload.AccountID, payload.HTTPReferer, payload.AppTitle)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid provider", "details": err.Error()})
			return
		}
		if err := services.SaveStoredAuth(stored); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to persist provider credentials", "details": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"saved":      true,
			"provider":   strings.ToLower(strings.TrimSpace(payload.Provider)),
			"authSource": "data/auth.json",
			"storePath":  services.AuthStorePathForDisplay(),
		})
	})

	r.POST("/auth/clear", func(c *gin.Context) {
		if !validateSetupAccess(c) {
			return
		}
		if err := services.ClearStoredAuth(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to clear stored credentials", "details": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"cleared": true})
	})

	r.GET("/health", func(c *gin.Context) {
		_, authErr := services.GetSelectedAuth()
		authStatus := "ok"
		authDetails := ""
		if authErr != nil {
			authStatus = "invalid"
			authDetails = authErr.Error()
		}

		c.JSON(http.StatusOK, gin.H{
			"status":     "ok",
			"uptime":     time.Since(startTime).Seconds(),
			"authStatus": authStatus,
			"authError":  authDetails,
		})
	})

	// Mount chat routes
	routes.RegisterChatRoutes(r, inferenceAuthMiddleware())
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	// For Docker environments, it's safer to bind to 0.0.0.0 explicitly
	address := "0.0.0.0:" + port

	srv := &http.Server{
		Addr:           address,
		Handler:        r,
		MaxHeaderBytes: 1 << 20, // 1MB headers
		ReadTimeout:    600 * time.Second,
		WriteTimeout:   600 * time.Second,
	}

	go func() {
		log.Printf("flip-ai listening on %s", address)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server with
	// a timeout of 5 seconds.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Printf("Shutdown Server ... signal=%s", sig.String())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server Shutdown:", err)
	}
	log.Println("Server exiting")
}
