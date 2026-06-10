package providers

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

type GroqProvider struct {
	baseURL string
	apiKey  string
	model   string
	hc      *http.Client
}

func NewGroqProvider(baseURL, apiKey, model string, timeout time.Duration) *GroqProvider {
	return &GroqProvider{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		hc:      &http.Client{Timeout: timeout},
	}
}

func (g *GroqProvider) Name() string { return "groq" }

func (g *GroqProvider) Chat(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error) {
	if req.Model == "" {
		req.Model = g.model
	}
	req.Stream = false

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		g.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+g.apiKey)

	resp, err := g.hc.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call groq: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("groq: status %d: %s", resp.StatusCode, string(raw))
	}

	var out types.ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	out.Provider = "groq"

	return &out, nil
}
