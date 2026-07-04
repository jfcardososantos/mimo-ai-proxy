package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"flip-ai/internal/models"
	"flip-ai/internal/services"

	"github.com/gin-gonic/gin"
)

func TestOllamaGenerateOfficialProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("GEMINI_API_KEY", "test-key")
	t.Setenv("AUTH_STORE_PATH", t.TempDir()+"/auth.json")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected auth header: %s", got)
		}

		var payload struct {
			Model    string           `json:"model"`
			Messages []models.Message `json:"messages"`
			Stream   bool             `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode upstream payload: %v", err)
		}
		if payload.Model != "gemini-test-model" {
			t.Fatalf("unexpected provider model: %s", payload.Model)
		}
		if payload.Stream {
			t.Fatalf("expected non-stream request")
		}
		if len(payload.Messages) != 1 || payload.Messages[0].Role != "user" {
			t.Fatalf("unexpected messages: %+v", payload.Messages)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"model":"gemini-test-model","choices":[{"message":{"role":"assistant","content":"provider ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	}))
	defer upstream.Close()
	t.Setenv("GEMINI_BASE_URL", upstream.URL)

	r := gin.New()
	registerOllamaRoutes(r, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/generate", strings.NewReader(`{"model":"gemini-test-model","prompt":"ola","stream":false}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got models.OllamaGenerateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode ollama response: %v", err)
	}
	if got.Model != "gemini-test-model" {
		t.Fatalf("unexpected model: %s", got.Model)
	}
	if got.Response != "provider ok" {
		t.Fatalf("unexpected response: %q", got.Response)
	}
	if !got.Done || got.DoneReason != "stop" {
		t.Fatalf("unexpected done fields: %+v", got)
	}
	if got.PromptEvalCount != 3 || got.EvalCount != 2 {
		t.Fatalf("unexpected usage: %+v", got)
	}
}

func TestOllamaTagsDoNotRequireXiaomiAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("AUTH_STORE_PATH", t.TempDir()+"/auth.json")
	services.GlobalCache.Delete("ollama_models_list")

	r := gin.New()
	registerOllamaRoutes(r, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"default", "deepseek-chat", "gemini-2.5-flash", "openrouter/google/gemma-3-12b-it:free"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected tags to include %q, got %s", want, body)
		}
	}
}
