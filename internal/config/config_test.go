package config

import (
	"os"
	"strings"
	"testing"
)

func TestLoad_MissingRequiredKeys(t *testing.T) {
	os.Unsetenv("GATEWAY_API_KEY")
	os.Unsetenv("GROQ_API_KEY")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when GATEWAY_API_KEY is empty, got nil")
	}
	if !strings.Contains(err.Error(), "GATEWAY_API_KEY") {
		t.Errorf("error should mention GATEWAY_API_KEY, got: %v", err)
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("GATEWAY_API_KEY", "test-key-1234567890")
	t.Setenv("GROQ_API_KEY", "gsk_dummy")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("default Port = %d, want 8080", cfg.Server.Port)
	}
	if cfg.Upstream.Model != "llama-3.3-70b-versatile" {
		t.Errorf("default Model = %q, want llama-3.3-70b-versatile", cfg.Upstream.Model)
	}
	if cfg.Embedding.Model != "nomic-embed-text" {
		t.Errorf("default Embedding.Model = %q, want nomic-embed-text", cfg.Embedding.Model)
	}
	if cfg.Embedding.Dims != 768 {
		t.Errorf("default Embedding.Dims = %d, want 768", cfg.Embedding.Dims)
	}
	if cfg.Cache.SimilarityThreshold != 0.85 {
		t.Errorf("default SimilarityThreshold = %f, want 0.85", cfg.Cache.SimilarityThreshold)
	}
	if cfg.Breaker.FailureThreshold != 3 {
		t.Errorf("default FailureThreshold = %d, want 3", cfg.Breaker.FailureThreshold)
	}
}

func TestLoad_Overrides(t *testing.T) {
	t.Setenv("GATEWAY_API_KEY", "test-key-1234567890")
	t.Setenv("GROQ_API_KEY", "gsk_dummy")
	t.Setenv("GATEWAY_PORT", "9090")
	t.Setenv("CACHE_SIMILARITY_THRESHOLD", "0.85")
	t.Setenv("BREAKER_FAILURE_THRESHOLD", "5")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Server.Port != 9090 {
		t.Errorf("override Port = %d, want 9090", cfg.Server.Port)
	}
	if cfg.Cache.SimilarityThreshold != 0.85 {
		t.Errorf("override SimilarityThreshold = %f, want 0.85", cfg.Cache.SimilarityThreshold)
	}
	if cfg.Breaker.FailureThreshold != 5 {
		t.Errorf("override FailureThreshold = %d, want 5", cfg.Breaker.FailureThreshold)
	}
}
