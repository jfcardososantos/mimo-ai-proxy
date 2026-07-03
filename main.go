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
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"flip-ai/internal/routes"
	"flip-ai/internal/services"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

var startTime time.Time

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
			strings.TrimSpace(stored.DeepSeekToken) != "")
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

func validateSetupAccess(c *gin.Context) bool {
	apiKey := strings.TrimSpace(os.Getenv("API_KEY"))
	if apiKey == "" {
		return true
	}

	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
	if strings.HasPrefix(authHeader, "Bearer ") && strings.TrimPrefix(authHeader, "Bearer ") == apiKey {
		return true
	}

	if strings.TrimSpace(c.PostForm("api_key")) == apiKey {
		return true
	}

	if strings.TrimSpace(c.Query("api_key")) == apiKey {
		return true
	}

	c.JSON(http.StatusUnauthorized, gin.H{
		"error":   "Missing or invalid API key",
		"details": "Use Authorization: Bearer <API_KEY> or submit api_key in the setup form.",
	})
	return false
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

	// Configurable CORS
	r.Use(func(c *gin.Context) {
		origin := os.Getenv("CORS_ORIGIN")
		if origin == "" {
			origin = "*"
		}
		c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, Accept, X-Timezone, OpenAI-Beta")
		c.Writer.Header().Set("Access-Control-Expose-Headers", "Content-Type")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	// Root route - Dashboard
	r.GET("/", func(c *gin.Context) {
		tokenStats, tokenUsage, responseTimes := routes.GetStats()
		
		var statsItems []string
		for token, count := range tokenStats {
			displayToken := token
			if len(token) > 10 {
				displayToken = token[:10] + "..."
			}
			usage := tokenUsage[token]
			statsItems = append(statsItems, fmt.Sprintf("<li>Token <code>%s</code>: <strong>%d</strong> reqs, <strong>%.1fk</strong> tokens</li>", displayToken, count, float64(usage)/1000.0))
		}
		statsHtml := strings.Join(statsItems, "")
		if statsHtml == "" {
			statsHtml = "<li>No requests processed yet.</li>"
		}

		var avgTimeStr string = "N/A"
		if len(responseTimes) > 0 {
			var sum int64
			for _, t := range responseTimes {
				sum += t
			}
			avgTimeStr = fmt.Sprintf("%dms", sum/int64(len(responseTimes)))
		}

		modelListHtml := officialModelListHTML()
		if modelListHtml == "" {
			modelListHtml = "<li>API models unavailable</li>"
		}
		storedAuth, storedAuthErr := services.LoadStoredAuth()
		authSource := detectAuthSource(storedAuth, storedAuthErr)

		auth, err := services.GetSelectedAuth()
		if err != nil {
			modelListHtml = fmt.Sprintf("<li>Xiaomi auth config invalid: %s</li>", err.Error()) + modelListHtml
		} else {
			headers := services.GetOfficialHeaders(auth, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "GET", "https://aistudio.xiaomimimo.com/open-apis/bot/config", nil)
			for k, v := range headers {
				req.Header.Set(k, v)
			}
			resp, err := services.GlobalHTTPClient.Do(req)
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
					var modelItems []string
					for _, m := range result.Data.ModelConfigList {
						modelItems = append(modelItems, fmt.Sprintf("<li><code>%s</code> - %s</li>", m.Model, m.EnIntro))
					}
					modelListHtml = strings.Join(modelItems, "") + officialModelListHTML()
				}
			}
		}

		c.HTML(http.StatusOK, "dashboard.html", gin.H{
			"ProductName": "flip-ai",
			"Uptime":      fmt.Sprintf("%.0f", time.Since(startTime).Seconds()),
			"ModelList":   modelListHtml,
			"AvgLatency":  avgTimeStr,
			"Stats":       statsHtml,
			"LoginURL":    loginURL(),
			"QRCodeURL":   qrCodeURL(loginURL()),
			"AuthSource":  authSource,
			"AuthError":   errString(err),
			"AuthStore":   services.AuthStorePathForDisplay(),
			"StoredAuth":  storedAuth,
			"StoredError": errString(storedAuthErr),
		})
	})

	r.GET("/auth/status", func(c *gin.Context) {
		auth, authErr := services.GetSelectedAuth()
		stored, storedErr := services.LoadStoredAuth()
		c.JSON(http.StatusOK, gin.H{
			"configured":       authErr == nil,
			"authError":        errString(authErr),
			"authSource":       detectAuthSource(stored, storedErr),
			"storePath":        services.AuthStorePathForDisplay(),
			"storeError":       errString(storedErr),
			"storedHasCookie":  stored.XiaomiCookie != "",
			"storedHasToken":   stored.ServiceToken != "",
			"storedHasUserID":  stored.UserID != "",
			"storedHasChatbot": stored.XiaomiChatbot != "",
			"deepseekConfigured": stored.DeepSeekCookie != "" && stored.DeepSeekToken != "",
			"storedHasDeepSeekCookie": stored.DeepSeekCookie != "",
			"storedHasDeepSeekToken":  stored.DeepSeekToken != "",
			"providers": gin.H{
				"gemini":     stored.GeminiAPIKey != "" || strings.TrimSpace(os.Getenv("GEMINI_API_KEY")) != "",
				"groq":       stored.GroqAPIKey != "" || strings.TrimSpace(os.Getenv("GROQ_API_KEY")) != "",
				"openrouter": stored.OpenRouterAPIKey != "" || strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")) != "",
				"cloudflare": (stored.CloudflareAPIKey != "" || strings.TrimSpace(os.Getenv("CLOUDFLARE_API_KEY")) != "") && (stored.CloudflareAccountID != "" || strings.TrimSpace(os.Getenv("CLOUDFLARE_ACCOUNT_ID")) != ""),
			},
			"selectedPh":       auth.Ph,
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
	routes.RegisterChatRoutes(r, nil)
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
