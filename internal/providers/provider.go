package providers

import (
	"context"

	"github.com/Leito2/llm-edge-gateway/pkg/types"
)

type Provider interface {
	Name() string
	Chat(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error)
}
