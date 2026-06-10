package providers

import (
	"context"

	"github.com/Leito2/llm-edge-gateway/pkg/types"
)

type Provider interface {
	Name() string
	Chat(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error)
}

// StreamChunk represents one delta piece returned by a streaming provider.
type StreamChunk struct {
	Content      string
	FinishReason string
	Done         bool
}

// StreamProvider is the minimal interface for streaming providers.
type StreamProvider interface {
	Name() string
	StreamChat(ctx context.Context, req types.ChatRequest) (<-chan StreamChunk, <-chan error)
}
