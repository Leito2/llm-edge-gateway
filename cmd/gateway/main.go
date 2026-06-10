package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/Leito2/llm-edge-gateway/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config load failed: %v", err)
	}

	hide := func(s string) string {
		if len(s) <= 8 {
			return "***"
		}
		return s[:4] + "***" + s[len(s)-4:]
	}

	snapshot := struct {
		Server    string
		Auth      string
		Upstream  string
		Local     string
		Embedding string
		Redis     string
		Cache     string
		Breaker   string
	}{
		Server:    fmt.Sprintf("port=%d read=%s write=%s", cfg.Server.Port, cfg.Server.ReadTimeout, cfg.Server.WriteTimeout),
		Auth:      fmt.Sprintf("api_key=%s", hide(cfg.Auth.APIKey)),
		Upstream:  fmt.Sprintf("base=%s model=%s key=%s timeout=%s", cfg.Upstream.BaseURL, cfg.Upstream.Model, hide(cfg.Upstream.APIKey), cfg.Upstream.Timeout),
		Local:     fmt.Sprintf("url=%s model=%s timeout=%s", cfg.Local.URL, cfg.Local.Model, cfg.Local.Timeout),
		Embedding: fmt.Sprintf("url=%s model=%s dims=%d", cfg.Embedding.URL, cfg.Embedding.Model, cfg.Embedding.Dims),
		Redis:     fmt.Sprintf("addr=%s db=%d", cfg.Redis.Addr, cfg.Redis.DB),
		Cache:     fmt.Sprintf("threshold=%.2f ttl=%s", cfg.Cache.SimilarityThreshold, cfg.Cache.TTL),
		Breaker:   fmt.Sprintf("threshold=%d open_timeout=%s request_timeout=%s", cfg.Breaker.FailureThreshold, cfg.Breaker.OpenTimeout, cfg.Breaker.RequestTimeout),
	}
	out, _ := json.MarshalIndent(snapshot, "", "  ")
	fmt.Println(string(out))
	os.Exit(0)
}
