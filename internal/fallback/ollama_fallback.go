package fallback

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Leito2/llm-edge-gateway/pkg/types"
)

type OllamaLocal struct {
	baseURL string
	model   string
	hc      *http.Client
}

type ollamaChatRequest struct {
	Model    string             `json:"model"`
	Messages []types.ChatMessage `json:"messages"`
	Stream   bool               `json:"stream"`
	Options  map[string]any     `json:"options,omitempty"`
}

type ollamaChatResponse struct {
	Model     string             `json:"model"`
	Message   types.ChatMessage  `json:"message"`
	Done      bool               `json:"done"`
	EvalCount int                `json:"eval_count"`
}

func NewOllamaLocal(baseURL, model string, timeout time.Duration) *OllamaLocal {
	return &OllamaLocal{
		baseURL: baseURL,
		model:   model,
		hc:      &http.Client{Timeout: timeout},
	}
}

func (o *OllamaLocal) Name() string { return "ollama-local" }

func (o *OllamaLocal) Chat(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error) {
	if req.Model == "" {
		req.Model = o.model
	}
	req.Stream = false

	body, err := json.Marshal(ollamaChatRequest{
		Model:    req.Model,
		Messages: req.Messages,
		Stream:   false,
		Options: map[string]any{
			"num_ctx": 2048,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		o.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.hc.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call ollama local: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama local: status %d: %s", resp.StatusCode, string(raw))
	}

	var out ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	chatResp := &types.ChatResponse{
		ID:      fmt.Sprintf("ollama-%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   out.Model,
		Choices: []types.ChatChoice{
			{
				Index: 0,
				Message: types.ChatMessage{
					Role:    "assistant",
					Content: out.Message.Content,
				},
				FinishReason: "stop",
			},
		},
		Usage: types.Usage{
			PromptTokens:     len(req.Messages) * 10,
			CompletionTokens: out.EvalCount,
			TotalTokens:      len(req.Messages)*10 + out.EvalCount,
		},
		Provider: "ollama-local",
	}
	return chatResp, nil
}
