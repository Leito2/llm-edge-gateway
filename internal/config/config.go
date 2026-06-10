package config

import (
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type ServerConfig struct {
	Port         int           `envconfig:"GATEWAY_PORT" default:"8080"`
	ReadTimeout  time.Duration `envconfig:"GATEWAY_READ_TIMEOUT" default:"30s"`
	WriteTimeout time.Duration `envconfig:"GATEWAY_WRITE_TIMEOUT" default:"30s"`
}

type AuthConfig struct {
	APIKey string `envconfig:"GATEWAY_API_KEY" required:"true"`
}

type UpstreamConfig struct {
	BaseURL string        `envconfig:"GROQ_BASE_URL" default:"https://api.groq.com/openai/v1"`
	APIKey  string        `envconfig:"GROQ_API_KEY" required:"true"`
	Model   string        `envconfig:"GROQ_MODEL" default:"llama-3.3-70b-versatile"`
	Timeout time.Duration `envconfig:"GROQ_TIMEOUT" default:"8s"`
}

type LocalConfig struct {
	URL     string        `envconfig:"OLLAMA_URL" default:"http://localhost:11434"`
	Model   string        `envconfig:"OLLAMA_MODEL" default:"gemma3:1b"`
	Timeout time.Duration `envconfig:"OLLAMA_TIMEOUT" default:"120s"`
}

type EmbeddingConfig struct {
	URL   string `envconfig:"EMBEDDING_URL" default:"http://localhost:11434"`
	Model string `envconfig:"EMBEDDING_MODEL" default:"nomic-embed-text"`
	Dims  int    `envconfig:"EMBEDDING_DIMS" default:"768"`
}

type RedisConfig struct {
	Addr     string `envconfig:"REDIS_ADDR" default:"localhost:6379"`
	Password string `envconfig:"REDIS_PASSWORD" default:""`
	DB       int    `envconfig:"REDIS_DB" default:"0"`
}

type CacheConfig struct {
	SimilarityThreshold float64       `envconfig:"CACHE_SIMILARITY_THRESHOLD" default:"0.85"`
	TTL                 time.Duration `envconfig:"CACHE_TTL_HOURS" default:"168h"`
}

type BreakerConfig struct {
	FailureThreshold    int           `envconfig:"BREAKER_FAILURE_THRESHOLD" default:"3"`
	OpenTimeout         time.Duration `envconfig:"BREAKER_OPEN_TIMEOUT" default:"30s"`
	HalfOpenMaxRequests uint32        `envconfig:"BREAKER_HALF_OPEN_MAX_REQUESTS" default:"1"`
	RequestTimeout      time.Duration `envconfig:"BREAKER_REQUEST_TIMEOUT" default:"5s"`
}

type Config struct {
	Server    ServerConfig
	Auth      AuthConfig
	Upstream  UpstreamConfig
	Local     LocalConfig
	Embedding EmbeddingConfig
	Redis     RedisConfig
	Cache     CacheConfig
	Breaker   BreakerConfig
}

func Load() (*Config, error) {
	var c Config
	if err := envconfig.Process("", &c); err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	return &c, nil
}
