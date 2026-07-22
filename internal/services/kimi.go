package services

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"flip-ai/internal/models"
)

const kimiWebBaseURL = "https://www.kimi.com"
const kimiWebChatURL = kimiWebBaseURL + "/apiv2/kimi.gateway.chat.v1.ChatService/Chat"

type KimiChatResult struct {
	Content       string
	ReasoningText string
}

func IsKimiModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return model == "kimi-k3" || model == "kimi/k3" || model == "k3"
}

func GetSelectedKimiSession() (StoredWebSession, string, error) {
	session, err := GetStoredWebSession("kimi")
	if err != nil {
		return StoredWebSession{}, "", err
	}
	token := strings.TrimSpace(WebSessionToken(session))
	if token == "" {
		token = kimiTokenFromCookie(session.Cookie)
	}
	if token == "" {
		return StoredWebSession{}, "", errors.New("missing Kimi access_token from localStorage or kimi-auth cookie")
	}
	return session, token, nil
}

func kimiTokenFromCookie(rawCookie string) string {
	for _, part := range strings.Split(rawCookie, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && strings.EqualFold(strings.TrimSpace(name), "kimi-auth") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func KimiChat(session StoredWebSession, accessToken string, messages []models.Message) (KimiChatResult, error) {
	prompt, systemPrompt, err := foldKimiMessages(messages)
	if err != nil {
		return KimiChatResult{}, err
	}
	if prompt == "" {
		return KimiChatResult{}, errors.New("Kimi requires a non-empty user message")
	}

	options := map[string]interface{}{
		"thinking":         true,
		"enable_plugin":    false,
		"reasoning_effort": "REASONING_EFFORT_MAX",
		"context_length":   "CONTEXT_LENGTH_L",
	}
	if systemPrompt != "" {
		options["system_prompt"] = systemPrompt
	}
	payload := map[string]interface{}{
		"chat_id":     "",
		"kimiplus_id": "ok-computer",
		"scenario":    "SCENARIO_OK_COMPUTER",
		"tools":       []interface{}{},
		"message": map[string]interface{}{
			"id": "", "parent_id": "", "children_message_ids": []interface{}{}, "role": "user",
			"blocks": []interface{}{map[string]interface{}{"id": "", "message_id": "", "text": map[string]string{"content": prompt}}},
			"scenario": "SCENARIO_OK_COMPUTER", "labels": []interface{}{}, "references": []interface{}{}, "is_goal": false,
		},
		"options": options, "project_id": "",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return KimiChatResult{}, err
	}
	req, err := http.NewRequest(http.MethodPost, kimiWebChatURL, bytes.NewReader(kimiConnectFrame(body)))
	if err != nil {
		return KimiChatResult{}, err
	}
	userAgent := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"
	if strings.TrimSpace(session.UserAgent) != "" {
		userAgent = session.UserAgent
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/connect+json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Origin", kimiWebBaseURL)
	req.Header.Set("Referer", kimiWebBaseURL+"/")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Connect-Protocol-Version", "1")

	resp, err := GlobalHTTPClient.Do(req)
	if err != nil {
		return KimiChatResult{}, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return KimiChatResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return KimiChatResult{}, fmt.Errorf("Kimi returned %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return parseKimiConnectStream(responseBody)
}

func kimiConnectFrame(payload []byte) []byte {
	framed := make([]byte, len(payload)+5)
	binary.BigEndian.PutUint32(framed[1:5], uint32(len(payload)))
	copy(framed[5:], payload)
	return framed
}

func parseKimiConnectStream(raw []byte) (KimiChatResult, error) {
	var result KimiChatResult
	for len(raw) > 0 {
		if len(raw) < 5 {
			return KimiChatResult{}, errors.New("truncated Kimi Connect frame")
		}
		flags := raw[0]
		length := int(binary.BigEndian.Uint32(raw[1:5]))
		if length > 8*1024*1024 || len(raw) < 5+length {
			return KimiChatResult{}, errors.New("invalid Kimi Connect frame length")
		}
		payload := raw[5 : 5+length]
		raw = raw[5+length:]
		var event map[string]interface{}
		if len(payload) > 0 && json.Unmarshal(payload, &event) != nil {
			return KimiChatResult{}, errors.New("invalid Kimi Connect payload")
		}
		if flags&2 != 0 {
			if errValue, ok := event["error"].(map[string]interface{}); ok {
				return KimiChatResult{}, fmt.Errorf("Kimi stream error: %v", errValue["message"])
			}
			return result, nil
		}
		op, _ := event["op"].(string)
		mask, _ := event["mask"].(string)
		block, _ := event["block"].(map[string]interface{})
		if block == nil {
			continue
		}
		if op == "set" && mask == "block.text" {
			result.Content += kimiBlockContent(block, "text")
		}
		if op == "append" && mask == "block.text.content" {
			result.Content += kimiBlockContent(block, "text")
		}
		if op == "set" && mask == "block.think" {
			result.ReasoningText += kimiBlockContent(block, "think")
		}
		if op == "append" && mask == "block.think.content" {
			result.ReasoningText += kimiBlockContent(block, "think")
		}
	}
	return KimiChatResult{}, errors.New("Kimi stream ended without an end frame")
}

func kimiBlockContent(block map[string]interface{}, kind string) string {
	item, _ := block[kind].(map[string]interface{})
	content, _ := item["content"].(string)
	return content
}

func foldKimiMessages(messages []models.Message) (string, string, error) {
	var system, conversation []string
	for _, message := range messages {
		text := ExtractText(message.Content, false)
		switch message.Role {
		case "system", "developer":
			if text != "" {
				system = append(system, text)
			}
		case "user":
			if text != "" {
				if len(conversation) == 0 {
					conversation = append(conversation, text)
				} else {
					conversation = append(conversation, "User: "+text)
				}
			}
		case "assistant":
			if text != "" {
				conversation = append(conversation, "Assistant: "+text)
			}
		default:
			return "", "", fmt.Errorf("Kimi Web does not support message role %s", message.Role)
		}
	}
	return strings.Join(conversation, "\n\n"), strings.Join(system, "\n\n"), nil
}
