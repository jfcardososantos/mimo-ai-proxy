/*
 * File: helpers.go
 * Project: mimoproxy
 * Created: 2026-04-29
 *
 * Last Modified: Wed Apr 29 2026
 * Modified By: Pedro Farias
 */

package utils

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mimoproxy/internal/models"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func GenerateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func CreateChatCompletionChunk(id, content, model string, finishReason *string, reasoning string, usage *models.Usage, toolCalls []models.ToolCall) models.ChatCompletionChunk {
	chunk := models.ChatCompletionChunk{
		ID:      "chatcmpl-" + id,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []models.Choice{
			{
				Index: 0,
				Delta: models.Delta{},
				FinishReason: finishReason,
			},
		},
	}

	if content != "" {
		chunk.Choices[0].Delta.Content = content
	}
	if reasoning != "" {
		chunk.Choices[0].Delta.ReasoningContent = reasoning
	}
	if toolCalls != nil {
		chunk.Choices[0].Delta.ToolCalls = toolCalls
	}
	if usage != nil {
		chunk.Usage = usage
	}
	return chunk
}

func SendError(c *gin.Context, status int, message, errorType string, code *string) {
	c.JSON(status, models.ErrorResponse{
		Error: models.ErrorDetail{
			Message: message,
			Type:    errorType,
			Param:   nil,
			Code:    code,
		},
	})
}

func PointerToString(s string) *string {
	return &s
}

// WriteSSEChunk writes one OpenAI-style SSE data frame.
func WriteSSEChunk(c *gin.Context, chunk models.ChatCompletionChunk) {
	b, _ := json.Marshal(chunk)
	c.Writer.WriteString(fmt.Sprintf("data: %s\n\n", string(b)))
	c.Writer.Flush()
}

// EmitStreamToolCall sends tool call deltas in the OpenAI streaming shape (id/name, then arguments).
func EmitStreamToolCall(c *gin.Context, completionID, model string, tc models.ToolCall) {
	callID := tc.ID
	if callID == "" {
		callID = "call_" + GenerateID()
	}
	idx := tc.Index

	nameChunk := CreateChatCompletionChunk(completionID, "", model, nil, "", nil, []models.ToolCall{{
		Index: idx,
		ID:    callID,
		Type:  "function",
		Function: models.ToolFunction{
			Name: tc.Function.Name,
		},
	}})
	WriteSSEChunk(c, nameChunk)

	args := strings.TrimSpace(tc.Function.Arguments)
	if args == "" {
		args = "{}"
	}
	argsChunk := CreateChatCompletionChunk(completionID, "", model, nil, "", nil, []models.ToolCall{{
		Index: idx,
		Function: models.ToolFunction{
			Arguments: args,
		},
	}})
	WriteSSEChunk(c, argsChunk)
}

// FinalizeChatStream emits the terminal OpenAI SSE frames (finish_reason + [DONE]).
func FinalizeChatStream(c *gin.Context, completionID, model, finishReason string, usage *models.Usage) {
	fr := finishReason
	WriteSSEChunk(c, CreateChatCompletionChunk(completionID, "", model, &fr, "", usage, nil))
	c.Writer.WriteString("data: [DONE]\n\n")
	c.Writer.Flush()
}
