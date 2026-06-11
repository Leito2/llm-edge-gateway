package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"

	"github.com/Leito2/llm-edge-gateway/internal/auth"
	"github.com/Leito2/llm-edge-gateway/internal/breaker"
	"github.com/Leito2/llm-edge-gateway/internal/cache"
	"github.com/Leito2/llm-edge-gateway/internal/config"
	"github.com/Leito2/llm-edge-gateway/internal/embedder"
	"github.com/Leito2/llm-edge-gateway/internal/fallback"
	"github.com/Leito2/llm-edge-gateway/internal/metrics"
	"github.com/Leito2/llm-edge-gateway/internal/providers"
	"github.com/Leito2/llm-edge-gateway/internal/proxy"
)

func main() {
	// Load .env file from the current directory (or the binary's directory)
	// if it exists. Real env vars always take precedence over .env values.
	// This is a convenience so users can just run `./gateway` after `cp
	// .env.example .env` without having to `export $(cat .env | xargs)`.
	envPath := ".env"
	if _, err := os.Stat(envPath); err == nil {
		if loadErr := godotenv.Load(envPath); loadErr != nil {
			log.Printf("[main] warning: could not load %s: %v (continuing with real env)", envPath, loadErr)
		} else {
			log.Printf("[main] loaded config from %s", envPath)
		}
	} else {
		log.Printf("[main] no .env found, using real environment variables")
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config load failed: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	pingCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		cancel()
		log.Fatalf("redis ping failed: %v", err)
	}
	cancel()
	log.Printf("[main] redis OK at %s", cfg.Redis.Addr)

	emb := embedder.NewOllamaEmbedder(cfg.Embedding.URL, cfg.Embedding.Model, cfg.Embedding.Dims)
	embCtx, embCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if _, err := emb.Embed(embCtx, "warmup"); err != nil {
		embCancel()
		log.Fatalf("embedder warmup failed: %v", err)
	}
	embCancel()
	log.Printf("[main] embedder OK (%s, %d dims)", cfg.Embedding.Model, cfg.Embedding.Dims)

	c := cache.New(rdb, cfg.Cache.SimilarityThreshold, cfg.Cache.TTL)
	log.Printf("[main] cache OK (threshold=%.2f, ttl=%s)", cfg.Cache.SimilarityThreshold, cfg.Cache.TTL)

	cb := breaker.New(breaker.Settings{
		Name:             "groq",
		FailureThreshold: uint32(cfg.Breaker.FailureThreshold),
		OpenTimeout:      cfg.Breaker.OpenTimeout,
		Interval:         cfg.Breaker.OpenTimeout * 2,
		HalfOpenMax:      cfg.Breaker.HalfOpenMaxRequests,
		OnStateChange: func(name, from, to string) {
			log.Printf("[breaker] state change: %s %s -> %s", name, from, to)
		},
	})
	log.Printf("[main] circuit breaker OK (threshold=%d, timeout=%s)", cfg.Breaker.FailureThreshold, cfg.Breaker.OpenTimeout)

	up := providers.NewGroqProvider(cfg.Upstream.BaseURL, cfg.Upstream.APIKey, cfg.Upstream.Model, cfg.Upstream.Timeout)
	fb := fallback.NewOllamaLocal(cfg.Local.URL, cfg.Local.Model, cfg.Local.Timeout)
	m := metrics.New()

	p := &proxy.Proxy{
		Embedder:  emb,
		Cache:     c,
		Breaker:   cb,
		Upstream:  up,
		UpstreamS: up,
		Fallback:  fb,
		FallbackS: fb,
		Metrics:   m,
		StartTime: time.Now(),
	}

	app := fiber.New(fiber.Config{
		AppName:               "llm-edge-gateway",
		ReadTimeout:           cfg.Server.ReadTimeout,
		WriteTimeout:          cfg.Server.WriteTimeout,
		DisableStartupMessage: false,
		StreamRequestBody:     true,
	})
	app.Use(recover.New())
	app.Use(logger.New(logger.Config{
		Format:     "${time} ${status} ${method} ${path} (${latency})\n",
		TimeFormat: "15:04:05",
	}))

	app.Get("/", p.HandleUI)
	app.Get("/health", p.HandleHealth)
	app.Get("/stats", p.HandleStats)

	v1 := app.Group("/v1", auth.RequireAPIKey(cfg.Auth.APIKey))
	v1.Post("/chat/completions", func(c *fiber.Ctx) error {
		var preview struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(c.Body(), &preview)
		if preview.Stream {
			return p.HandleChatStream(c)
		}
		return p.HandleChat(c)
	})

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	go func() {
		log.Printf("[main] starting gateway on %s", addr)
		if err := app.Listen(addr); err != nil {
			log.Fatalf("listen failed: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Printf("[main] shutdown signal received")
	if err := app.ShutdownWithTimeout(5 * time.Second); err != nil {
		log.Printf("[main] shutdown error: %v", err)
	}
	_ = rdb.Close()
}
