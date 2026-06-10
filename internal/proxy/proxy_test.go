package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"

	"github.com/Leito2/llm-edge-gateway/internal/breaker"
	"github.com/Leito2/llm-edge-gateway/internal/cache"
	"github.com/Leito2/llm-edge-gateway/internal/metrics"
	"github.com/Leito2/llm-edge-gateway/internal/providers"
	"github.com/Leito2/llm-edge-gateway/pkg/types"
)

type mockEmbedder struct {
	dim int
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	v := make([]float32, m.dim)
	for i := range v {
		v[i] = float32(i) * 0.001
	}
	return v, nil
}
func (m *mockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		v := make([]float32, m.dim)
		out[i] = v
	}
	return out, nil
}
func (m *mockEmbedder) Dimensions() int { return m.dim }

type okProvider struct {
	name string
	body string
}

func (o *okProvider) Name() string { return o.name }
func (o *okProvider) Chat(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error) {
	return &types.ChatResponse{
		ID:      "test-1",
		Object:  "chat.completion",
		Created: 1700000000,
		Model:   "test-model",
		Choices: []types.ChatChoice{{Index: 0, Message: types.ChatMessage{Role: "assistant", Content: o.body}, FinishReason: "stop"}},
		Usage:   types.Usage{PromptTokens: 5, CompletionTokens: 10, TotalTokens: 15},
		Provider: o.name,
	}, nil
}

type failProvider struct {
	name string
	err  error
}

func (f *failProvider) Name() string { return f.name }
func (f *failProvider) Chat(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error) {
	return nil, f.err
}

func newTestProxy(t *testing.T, up, fb providers.Provider) (*Proxy, *fiber.App) {
	t.Helper()
	addr := "localhost:6379"
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not reachable: %v", err)
	}
	// Clean cache: keys so prior tests don't pollute
	var cursor uint64
	for {
		keys, next, err := rdb.Scan(ctx, cursor, "cache:*", 100).Result()
		if err == nil && len(keys) > 0 {
			rdb.Del(ctx, keys...)
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}

	c := cache.New(rdb, 0.5, 0)
	cb := breaker.New(breaker.Settings{
		Name:             "test",
		FailureThreshold: 3,
		OpenTimeout:      100 * time.Millisecond,
		Interval:         200 * time.Millisecond,
	})
	p := &Proxy{
		Embedder:  &mockEmbedder{dim: 768},
		Cache:     c,
		Breaker:   cb,
		Upstream:  up,
		Fallback:  fb,
		Metrics:   metrics.New(),
		StartTime: time.Now(),
	}
	app := fiber.New()
	app.Post("/v1/chat/completions", p.HandleChat)
	app.Get("/health", p.HandleHealth)
	app.Get("/stats", p.HandleStats)
	return p, app
}

func postJSON(t *testing.T, app *fiber.App, body string) (*http.Response, error) {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return app.Test(req)
}

func TestProxy_MissThenUpstreamOK(t *testing.T) {
	up := &okProvider{name: "groq", body: "hello from upstream"}
	fb := &okProvider{name: "ollama-local", body: "fallback"}
	_, app := newTestProxy(t, up, fb)

	resp, err := postJSON(t, app, `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Provider"); got != "groq" {
		t.Errorf("X-Provider = %q, want 'groq'", got)
	}
	if got := resp.Header.Get("X-Cache-Status"); got != "MISS" {
		t.Errorf("X-Cache-Status = %q, want 'MISS'", got)
	}
}

func TestProxy_CacheHitOnSecondRequest(t *testing.T) {
	up := &okProvider{name: "groq", body: "first call"}
	fb := &okProvider{name: "ollama-local", body: "fallback"}
	_, app := newTestProxy(t, up, fb)

	body := `{"model":"m","messages":[{"role":"user","content":"unique-cache-key-1"}]}`
	for i := 0; i < 2; i++ {
		resp, _ := postJSON(t, app, body)
		if resp.StatusCode != 200 {
			t.Fatalf("call %d: status = %d", i, resp.StatusCode)
		}
	}

	resp, _ := postJSON(t, app, body)
	if got := resp.Header.Get("X-Cache-Status"); got != "HIT" {
		t.Errorf("3rd call X-Cache-Status = %q, want 'HIT'", got)
	}
}

func TestProxy_FallbackOnUpstreamFailure(t *testing.T) {
	up := &failProvider{name: "groq", err: errors.New("503 service unavailable")}
	fb := &okProvider{name: "ollama-local", body: "from local"}
	_, app := newTestProxy(t, up, fb)

	resp, _ := postJSON(t, app, `{"model":"m","messages":[{"role":"user","content":"test-fallback-unique"}]}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Provider"); got != "ollama-local" {
		t.Errorf("X-Provider = %q, want 'ollama-local'", got)
	}
	if got := resp.Header.Get("X-Cache-Status"); got != "MISS-FALLBACK" {
		t.Errorf("X-Cache-Status = %q, want 'MISS-FALLBACK'", got)
	}
}

func TestProxy_EmptyMessages(t *testing.T) {
	up := &okProvider{name: "groq", body: "x"}
	fb := &okProvider{name: "ollama-local", body: "x"}
	_, app := newTestProxy(t, up, fb)

	resp, _ := postJSON(t, app, `{"model":"m","messages":[]}`)
	if resp.StatusCode != 400 {
		t.Errorf("empty messages: status = %d, want 400", resp.StatusCode)
	}
}

func TestProxy_InvalidJSON(t *testing.T) {
	up := &okProvider{name: "groq", body: "x"}
	fb := &okProvider{name: "ollama-local", body: "x"}
	_, app := newTestProxy(t, up, fb)

	resp, _ := postJSON(t, app, `{not json`)
	if resp.StatusCode != 400 {
		t.Errorf("invalid json: status = %d, want 400", resp.StatusCode)
	}
}

func TestProxy_Health(t *testing.T) {
	up := &okProvider{name: "groq", body: "x"}
	fb := &okProvider{name: "ollama-local", body: "x"}
	_, app := newTestProxy(t, up, fb)

	resp, _ := app.Test(httptest.NewRequest("GET", "/health", nil))
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestProxy_Stats(t *testing.T) {
	up := &okProvider{name: "groq", body: "x"}
	fb := &okProvider{name: "ollama-local", body: "x"}
	_, app := newTestProxy(t, up, fb)

	resp, _ := app.Test(httptest.NewRequest("GET", "/stats", nil))
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}
