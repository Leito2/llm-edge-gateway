package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/valyala/fasthttp"

	"github.com/Leito2/llm-edge-gateway/internal/breaker"
	"github.com/Leito2/llm-edge-gateway/internal/cache"
	"github.com/Leito2/llm-edge-gateway/internal/embedder"
	"github.com/Leito2/llm-edge-gateway/internal/metrics"
	"github.com/Leito2/llm-edge-gateway/internal/providers"
	"github.com/Leito2/llm-edge-gateway/pkg/types"
)

// StreamProvider is the minimal interface for streaming providers.
// Both providers.GroqProvider and fallback.OllamaLocal satisfy it.
type StreamProvider = providers.StreamProvider

type Proxy struct {
	Embedder  embedder.Embedder
	Cache     *cache.SemanticCache
	Breaker   *breaker.Breaker
	Upstream  providers.Provider
	UpstreamS providers.StreamProvider
	Fallback  providers.Provider
	FallbackS providers.StreamProvider
	Metrics   *metrics.Metrics
	StartTime time.Time
}

func extractQuery(messages []types.ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	var parts []string
	for _, m := range messages {
		parts = append(parts, m.Content)
	}
	return strings.Join(parts, "\n")
}

func (p *Proxy) HandleChat(c *fiber.Ctx) error {
	p.Metrics.TotalRequests.Add(1)
	start := time.Now()

	var req types.ChatRequest
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid JSON: " + err.Error(),
		})
	}
	if len(req.Messages) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "messages must not be empty",
		})
	}

	query := extractQuery(req.Messages)
	if query == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "no user message found",
		})
	}

	embedCtx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	vec, err := p.Embedder.Embed(embedCtx, query)
	cancel()
	if err != nil {
		log.Printf("[proxy] embed error: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "embedding failed",
		})
	}

	cacheCtx, ccancel := context.WithTimeout(c.Context(), 1*time.Second)
	entry, sim, err := p.Cache.Get(cacheCtx, vec)
	ccancel()
	if err != nil {
		log.Printf("[proxy] cache get error: %v", err)
	}
	if entry != nil {
		p.Metrics.CacheHits.Add(1)
		var cached types.ChatResponse
		if err := json.Unmarshal([]byte(entry.ResponseJSON), &cached); err == nil {
			cached.CacheStatus = "HIT"
			cached.Provider = entry.Provider
			cached.LatencyMS = time.Since(start).Milliseconds()
			c.Set("X-Cache-Status", "HIT")
			c.Set("X-Provider", entry.Provider)
			c.Set("X-Cache-Similarity", fmt.Sprintf("%.4f", sim))
			return c.JSON(cached)
		}
	}
	p.Metrics.CacheMisses.Add(1)

	resp, usedFallback, err := p.executeWithFallback(c.Context(), req)
	if err != nil {
		log.Printf("[proxy] all providers failed: %v", err)
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"error": "all providers failed: " + err.Error(),
		})
	}

	resp.LatencyMS = time.Since(start).Milliseconds()
	if usedFallback {
		p.Metrics.FallbackUsed.Add(1)
		resp.CacheStatus = "MISS-FALLBACK"
		c.Set("X-Cache-Status", "MISS-FALLBACK")
	} else {
		resp.CacheStatus = "MISS"
		c.Set("X-Cache-Status", "MISS")
	}
	c.Set("X-Provider", resp.Provider)

	go p.writeback(query, vec, resp)

	return c.JSON(resp)
}

func (p *Proxy) executeWithFallback(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, bool, error) {
	v, err := p.Breaker.Execute(ctx, func(ctx context.Context) (any, error) {
		upCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		resp, err := p.Upstream.Chat(upCtx, req)
		if err != nil {
			p.Metrics.UpstreamFail.Add(1)
			return nil, err
		}
		p.Metrics.UpstreamOK.Add(1)
		return resp, nil
	})
	if err == nil {
		return v.(*types.ChatResponse), false, nil
	}
	if !errors.Is(err, breaker.ErrOpen) {
		log.Printf("[proxy] upstream error (will fallback): %v", err)
	} else {
		log.Printf("[proxy] circuit OPEN, going straight to fallback")
	}

	fbCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	resp, fbErr := p.Fallback.Chat(fbCtx, req)
	if fbErr != nil {
		return nil, false, fmt.Errorf("upstream: %w, fallback: %v", err, fbErr)
	}
	return resp, true, nil
}

func (p *Proxy) writeback(query string, vec []float32, resp *types.ChatResponse) {
	if resp == nil {
		return
	}
	jsonBytes, err := json.Marshal(resp)
	if err != nil {
		log.Printf("[proxy] writeback marshal error: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := p.Cache.Set(ctx, query, vec, string(jsonBytes), resp.Model, resp.Provider); err != nil {
		log.Printf("[proxy] writeback set error: %v", err)
	}
}

func (p *Proxy) HandleHealth(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status":         "ok",
		"uptime_seconds": int(time.Since(p.StartTime).Seconds()),
		"breaker_state":  p.Breaker.State(),
	})
}

func (p *Proxy) HandleStats(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 1*time.Second)
	defer cancel()
	cacheStats, err := p.Cache.Stats(ctx)
	if err != nil {
		log.Printf("[proxy] cache stats error: %v", err)
	}
	return c.JSON(fiber.Map{
		"uptime_seconds": int(time.Since(p.StartTime).Seconds()),
		"cache":          cacheStats,
		"breaker":        p.Breaker.Stats(),
		"metrics":        p.Metrics.Snapshot(),
	})
}

// HandleChatStream is the streaming equivalent of HandleChat. It detects
// `stream: true` in the request body and routes through the streaming
// pipeline. Cache hits simulate streaming by chunking the cached
// response. Cache misses stream from upstream with async writeback.
func (p *Proxy) HandleChatStream(c *fiber.Ctx) error {
	p.Metrics.TotalRequests.Add(1)
	start := time.Now()

	var req types.ChatRequest
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid JSON: " + err.Error(),
		})
	}
	if len(req.Messages) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "messages must not be empty",
		})
	}

	query := extractQuery(req.Messages)
	if query == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "no user message found",
		})
	}

	embedCtx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	vec, err := p.Embedder.Embed(embedCtx, query)
	cancel()
	if err != nil {
		log.Printf("[proxy] embed error: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "embedding failed",
		})
	}

	cacheCtx, ccancel := context.WithTimeout(c.Context(), 1*time.Second)
	entry, sim, err := p.Cache.Get(cacheCtx, vec)
	ccancel()
	if err != nil {
		log.Printf("[proxy] cache get error: %v", err)
	}

	if entry != nil {
		p.Metrics.CacheHits.Add(1)
		var cached types.ChatResponse
		if jerr := json.Unmarshal([]byte(entry.ResponseJSON), &cached); jerr == nil {
			c.Set("X-Cache-Status", "HIT")
			c.Set("X-Provider", entry.Provider)
			c.Set("X-Cache-Similarity", fmt.Sprintf("%.4f", sim))
			return p.streamCachedResponse(c, &cached, entry.Provider, start)
		}
	}
	p.Metrics.CacheMisses.Add(1)

	provider, isFallback, err := p.selectStreamProvider(c.Context())
	if err != nil {
		log.Printf("[proxy] all stream providers failed: %v", err)
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"error": "all providers failed: " + err.Error(),
		})
	}
	if isFallback {
		p.Metrics.FallbackUsed.Add(1)
		c.Set("X-Cache-Status", "MISS-FALLBACK")
	} else {
		c.Set("X-Cache-Status", "MISS")
	}
	c.Set("X-Provider", provider.Name())
	c.Set("X-Cache-Similarity", "")

	return p.streamFromProvider(c, req, query, vec, provider, isFallback, start)
}

func (p *Proxy) selectStreamProvider(ctx context.Context) (StreamProvider, bool, error) {
	// The streaming path uses the circuit breaker only for selection;
	// the actual streaming happens via the channel returned by StreamChat.
	// We do a quick "ping" through the breaker: if the breaker is open,
	// the execute returns ErrOpen immediately and we go to fallback.
	_, err := p.Breaker.Execute(ctx, func(ctx context.Context) (any, error) {
		if p.UpstreamS == nil {
			return nil, fmt.Errorf("upstream streaming not configured")
		}
		return p.UpstreamS, nil
	})
	if err == nil {
		return p.UpstreamS, false, nil
	}
	if !errors.Is(err, breaker.ErrOpen) {
		log.Printf("[proxy] upstream error (will fallback): %v", err)
	} else {
		log.Printf("[proxy] circuit OPEN, going straight to fallback")
	}
	if p.FallbackS == nil {
		return nil, false, fmt.Errorf("upstream: %w, fallback: not configured", err)
	}
	return p.FallbackS, true, nil
}

// streamCachedResponse simulates streaming by emitting the cached content
// in small chunks with a small delay between them. This keeps the UX
// consistent with the streaming responses from the upstream.
func (p *Proxy) streamCachedResponse(c *fiber.Ctx, cached *types.ChatResponse, providerName string, start time.Time) error {
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")
	c.Set("X-Latency-Ms", fmt.Sprintf("%d", time.Since(start).Milliseconds()))

	content := ""
	if len(cached.Choices) > 0 {
		content = cached.Choices[0].Message.Content
	}

	streamID := cached.ID
	if streamID == "" {
		streamID = fmt.Sprintf("cached-%d", time.Now().UnixNano())
	}
	created := cached.Created
	if created == 0 {
		created = time.Now().Unix()
	}
	model := cached.Model
	if model == "" {
		model = "cached"
	}

	c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {
		tokens := tokenize(content)
		for _, tok := range tokens {
			chunk := map[string]any{
				"id":      streamID,
				"object":  "chat.completion.chunk",
				"created": created,
				"model":   model,
				"choices": []map[string]any{
					{
						"index": 0,
						"delta": map[string]any{"content": tok},
					},
				},
			}
			b, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", b)
			if err := w.Flush(); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		finish := map[string]any{
			"id":      streamID,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   model,
			"choices": []map[string]any{
				{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"},
			},
		}
		b, _ := json.Marshal(finish)
		fmt.Fprintf(w, "data: %s\n\n", b)
		fmt.Fprintf(w, "data: [DONE]\n\n")
		_ = w.Flush()
	}))
	return nil
}

// streamFromProvider streams from the given provider and accumulates the
// full content for async cache writeback.
func (p *Proxy) streamFromProvider(c *fiber.Ctx, req types.ChatRequest, query string, vec []float32, provider StreamProvider, isFallback bool, start time.Time) error {
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")

	if isFallback {
		p.Metrics.UpstreamFail.Add(1)
	} else {
		p.Metrics.UpstreamOK.Add(1)
	}

	streamCtx, cancel := context.WithCancel(c.Context())
	defer cancel()

	chunks, errs := provider.StreamChat(streamCtx, req)

	streamID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	created := time.Now().Unix()
	model := req.Model
	if model == "" {
		model = "unknown"
	}

	var (
		accumulated strings.Builder
		writeOnce   sync.Once
	)

	c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {
		for c := range chunks {
			if c.Done {
				finish := map[string]any{
					"id":      streamID,
					"object":  "chat.completion.chunk",
					"created": created,
					"model":   model,
					"choices": []map[string]any{
						{"index": 0, "delta": map[string]any{}, "finish_reason": c.FinishReason},
					},
				}
				b, _ := json.Marshal(finish)
				fmt.Fprintf(w, "data: %s\n\n", b)
				fmt.Fprintf(w, "data: [DONE]\n\n")
				_ = w.Flush()
				break
			}
			if c.Content == "" {
				continue
			}
			accumulated.WriteString(c.Content)
			chunk := map[string]any{
				"id":      streamID,
				"object":  "chat.completion.chunk",
				"created": created,
				"model":   model,
				"choices": []map[string]any{
					{
						"index": 0,
						"delta": map[string]any{"content": c.Content},
					},
				},
			}
			b, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", b)
			if err := w.Flush(); err != nil {
				cancel()
				return
			}
		}

		select {
		case e := <-errs:
			if e != nil {
				log.Printf("[proxy] stream error: %v", e)
				errChunk := map[string]any{"error": map[string]string{"message": e.Error()}}
				b, _ := json.Marshal(errChunk)
				fmt.Fprintf(w, "data: %s\n\n", b)
				fmt.Fprintf(w, "data: [DONE]\n\n")
				_ = w.Flush()
				return
			}
		default:
		}

		fullContent := accumulated.String()
		if fullContent != "" {
			cached := types.ChatResponse{
				ID:      streamID,
				Object:  "chat.completion",
				Created: created,
				Model:   model,
				Choices: []types.ChatChoice{
					{
						Index: 0,
						Message: types.ChatMessage{
							Role:    "assistant",
							Content: fullContent,
						},
						FinishReason: "stop",
					},
				},
				Provider: provider.Name(),
			}
			writeOnce.Do(func() {
				go p.writeback(query, vec, &cached)
			})
		}
	}))

	return nil
}

func tokenize(s string) []string {
	var tokens []string
	word := strings.Builder{}
	for _, r := range s {
		word.WriteRune(r)
		if r == ' ' || r == '\n' || r == '.' || r == ',' || r == '!' || r == '?' {
			tokens = append(tokens, word.String())
			word.Reset()
		}
	}
	if word.Len() > 0 {
		tokens = append(tokens, word.String())
	}
	return tokens
}
