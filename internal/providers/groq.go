package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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

// StreamChat returns a channel of streaming chunks from Groq.
func (g *GroqProvider) StreamChat(ctx context.Context, req types.ChatRequest) (<-chan StreamChunk, <-chan error) {
	chunks := make(chan StreamChunk, 16)
	errs := make(chan error, 1)

	if req.Model == "" {
		req.Model = g.model
	}
	req.Stream = true

	go func() {
		defer close(chunks)
		defer close(errs)

		body, err := json.Marshal(req)
		if err != nil {
			errs <- fmt.Errorf("marshal request: %w", err)
			return
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
			g.baseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			errs <- fmt.Errorf("build request: %w", err)
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+g.apiKey)
		httpReq.Header.Set("Accept", "text/event-stream")

		resp, err := g.hc.Do(httpReq)
		if err != nil {
			errs <- fmt.Errorf("call groq: %w", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			errs <- fmt.Errorf("groq: status %d: %s", resp.StatusCode, string(raw))
			return
		}

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimPrefix(line, "data: ")
			if payload == "[DONE]" {
				chunks <- StreamChunk{Done: true}
				return
			}
			var evt struct {
				Choices []struct {
					Delta        map[string]any `json:"delta"`
					FinishReason string         `json:"finish_reason"`
				} `json:"choices"`
			}
			if err := json.Unmarshal([]byte(payload), &evt); err != nil {
				continue
			}
			if len(evt.Choices) == 0 {
				continue
			}
			content, _ := evt.Choices[0].Delta["content"].(string)
			chunks <- StreamChunk{
				Content:      content,
				FinishReason: evt.Choices[0].FinishReason,
			}
		}
		if err := scanner.Err(); err != nil {
			errs <- fmt.Errorf("groq stream read: %w", err)
		}
	}()

	return chunks, errs
}
