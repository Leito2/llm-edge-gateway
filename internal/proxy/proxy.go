package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/Leito2/llm-edge-gateway/internal/breaker"
	"github.com/Leito2/llm-edge-gateway/internal/cache"
	"github.com/Leito2/llm-edge-gateway/internal/embedder"
	"github.com/Leito2/llm-edge-gateway/internal/metrics"
	"github.com/Leito2/llm-edge-gateway/internal/providers"
	"github.com/Leito2/llm-edge-gateway/pkg/types"
)

type Proxy struct {
	Embedder  embedder.Embedder
	Cache     *cache.SemanticCache
	Breaker   *breaker.Breaker
	Upstream  providers.Provider
	Fallback  providers.Provider
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
