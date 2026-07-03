package services

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"flip-ai/internal/models"
	"net/http"
	"os"
	"strings"
	"time"
)

const deepSeekBaseURL = "https://chat.deepseek.com"

var ErrDeepSeekPoWRequired = errors.New("DeepSeek web requires a proof-of-work challenge response for this request")

func IsDeepSeekModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return model == "deepseek" || strings.HasPrefix(model, "deepseek-")
}

func ValidateDeepSeekAuthInput(rawCookie string, token string) (models.DeepSeekAuth, error) {
	rawCookie = strings.TrimSpace(rawCookie)
	token = cleanEnvValue(token)

	if token == "" {
		token = extractDeepSeekUserToken(rawCookie)
	}
	if rawCookie == "" {
		return models.DeepSeekAuth{}, errors.New("missing DeepSeek cookie jar")
	}
	if token == "" {
		return models.DeepSeekAuth{}, errors.New("missing DeepSeek userToken from localStorage")
	}

	return models.DeepSeekAuth{
		Cookie: rawCookie,
		Token:  token,
	}, nil
}

func GetSelectedDeepSeekAuth() (models.DeepSeekAuth, error) {
	stored, err := LoadStoredAuth()
	if err != nil {
		return models.DeepSeekAuth{}, err
	}
	if strings.TrimSpace(stored.DeepSeekCookie) == "" && strings.TrimSpace(stored.DeepSeekToken) == "" {
		return models.DeepSeekAuth{}, errors.New("DeepSeek session not configured. Import a session with the Chrome extension or POST /auth/deepseek/import")
	}
	return ValidateDeepSeekAuthInput(stored.DeepSeekCookie, stored.DeepSeekToken)
}

func DeepSeekHeaders(auth models.DeepSeekAuth, customHeaders map[string]string) map[string]string {
	headers := map[string]string{
		"accept":          "application/json",
		"accept-encoding": "gzip",
		"authorization":   "Bearer " + auth.Token,
		"content-type":    "application/json",
		"cookie":          auth.Cookie,
		"host":            "chat.deepseek.com",
		"origin":          deepSeekBaseURL,
		"referer":         deepSeekBaseURL + "/",
		"user-agent":      "DeepSeek/1.0.13 Android/35",
	}

	for _, key := range []string{"accept-language", "user-agent"} {
		if val, ok := customHeaders[key]; ok && strings.TrimSpace(val) != "" {
			headers[key] = val
		}
	}
	if pow := strings.TrimSpace(os.Getenv("DEEPSEEK_POW_RESPONSE")); pow != "" {
		headers["x-ds-pow-response"] = pow
	}

	return headers
}

func CreateDeepSeekSession(auth models.DeepSeekAuth, customHeaders map[string]string) (string, error) {
	payloadBytes, _ := json.Marshal(map[string]string{"agent": "chat"})
	req, _ := http.NewRequest("POST", deepSeekBaseURL+"/api/v0/chat_session/create", bytes.NewBuffer(payloadBytes))
	for k, v := range DeepSeekHeaders(auth, customHeaders) {
		req.Header.Set(k, v)
	}

	resp, err := GlobalHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := readMaybeGzip(resp)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("DeepSeek session error: %d - %s", resp.StatusCode, string(body))
	}

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			BizData struct {
				ID string `json:"id"`
			} `json:"biz_data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if result.Code != 0 || result.Data.BizData.ID == "" {
		if result.Msg == "" {
			result.Msg = string(body)
		}
		return "", fmt.Errorf("DeepSeek session business error: %d - %s", result.Code, result.Msg)
	}
	return result.Data.BizData.ID, nil
}

func SendDeepSeekChatRequest(auth models.DeepSeekAuth, sessionID string, prompt string, thinking bool, search bool, customHeaders map[string]string) (*http.Response, error) {
	if strings.TrimSpace(os.Getenv("DEEPSEEK_POW_RESPONSE")) == "" {
		if required, err := deepSeekPoWRequired(auth, customHeaders); err == nil && required {
			return nil, ErrDeepSeekPoWRequired
		}
	}

	payload := map[string]interface{}{
		"chat_session_id":   sessionID,
		"parent_message_id": nil,
		"prompt":            prompt,
		"ref_file_ids":      []string{},
		"thinking_enabled":  thinking,
		"search_enabled":    search,
	}
	payloadBytes, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", deepSeekBaseURL+"/api/v0/chat/completion", bytes.NewBuffer(payloadBytes))
	for k, v := range DeepSeekHeaders(auth, customHeaders) {
		req.Header.Set(k, v)
	}

	return GlobalHTTPClient.Do(req)
}

func ParseDeepSeekStream(body io.Reader) models.DeepSeekChatResult {
	reader := bufio.NewReaderSize(body, 4*1024*1024)
	var result models.DeepSeekChatResult

	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			parseDeepSeekData(strings.TrimSpace(line[5:]), &result)
		}
		if err != nil {
			break
		}
	}

	if result.Usage.TotalTokens == 0 {
		result.Usage.CompletionTokens = len(result.Content) / 4
		result.Usage.TotalTokens = result.Usage.CompletionTokens
	}
	return result
}

func ReadDeepSeekBody(resp *http.Response) (io.Reader, func()) {
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err == nil {
			return gz, func() {
				_ = gz.Close()
				_ = resp.Body.Close()
			}
		}
	}
	return resp.Body, func() { _ = resp.Body.Close() }
}

func parseDeepSeekData(dataStr string, result *models.DeepSeekChatResult) {
	if dataStr == "" || dataStr == "{}" || dataStr == "[DONE]" {
		return
	}

	var chunk map[string]interface{}
	if err := json.Unmarshal([]byte(dataStr), &chunk); err != nil {
		return
	}

	if id, ok := chunk["response_message_id"].(string); ok && id != "" {
		result.MessageID = id
	}
	if v, ok := chunk["v"].(map[string]interface{}); ok {
		if response, ok := v["response"].(map[string]interface{}); ok {
			if id, ok := response["message_id"].(string); ok && id != "" {
				result.MessageID = id
			}
		}
	}

	path, _ := chunk["p"].(string)
	v, exists := chunk["v"]
	if !exists {
		return
	}
	if text, ok := v.(string); ok && text != "" {
		if strings.Contains(strings.ToLower(path), "thinking") {
			result.ReasoningText += text
			return
		}
		result.Content += text
	}
}

func deepSeekPoWRequired(auth models.DeepSeekAuth, customHeaders map[string]string) (bool, error) {
	payloadBytes, _ := json.Marshal(map[string]string{"target_path": "/api/v0/chat/completion"})
	req, _ := http.NewRequest("POST", deepSeekBaseURL+"/api/v0/chat/create_pow_challenge", bytes.NewBuffer(payloadBytes))
	for k, v := range DeepSeekHeaders(auth, customHeaders) {
		req.Header.Set(k, v)
	}

	client := *GlobalHTTPClient
	client.Timeout = 20 * time.Second
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	body, err := readMaybeGzip(resp)
	if err != nil {
		return false, err
	}
	if resp.StatusCode != http.StatusOK {
		return false, nil
	}

	var result struct {
		Code int `json:"code"`
		Data struct {
			BizData struct {
				Challenge interface{} `json:"challenge"`
			} `json:"biz_data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return false, nil
	}
	return result.Code == 0 && result.Data.BizData.Challenge != nil, nil
}

func readMaybeGzip(resp *http.Response) ([]byte, error) {
	var body io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err == nil {
			defer gz.Close()
			body = gz
		}
	}
	return io.ReadAll(body)
}

func extractDeepSeekUserToken(raw string) string {
	if token := extractCookieValue(raw, "userToken"); token != "" {
		return token
	}
	return ""
}
