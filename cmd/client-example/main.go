// Command client-example demonstrates how to use the llm-edge-gateway
// from another Go program, using the official openai-go SDK.
//
// Usage:
//     GATEWAY_API_KEY=demo-key-1234567890 go run ./cmd/client-example/ non-stream
//     GATEWAY_API_KEY=demo-key-1234567890 go run ./cmd/client-example/ stream
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/sashabaranov/go-openai"
)

func main() {
	mode := "non-stream"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}

	apiKey := os.Getenv("GATEWAY_API_KEY")
	if apiKey == "" {
		apiKey = "demo-key-1234567890"
	}

	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = "http://localhost:8080/v1"
	client := openai.NewClientWithConfig(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req := openai.ChatCompletionRequest{
		Model: "gemma3:1b",
		Messages: []openai.ChatCompletionMessage{
			{Role: "user", Content: "¿Qué es un circuit breaker en 1 frase?"},
		},
	}

	switch mode {
	case "non-stream":
		nonStream(ctx, client, req)
	case "stream":
		stream(ctx, client, req)
	default:
		log.Fatalf("Unknown mode %q. Use 'non-stream' or 'stream'.", mode)
	}
}

func nonStream(ctx context.Context, client *openai.Client, req openai.ChatCompletionRequest) {
	start := time.Now()
	resp, err := client.CreateChatCompletion(ctx, req)
	if err != nil {
		log.Fatalf("chat: %v", err)
	}
	fmt.Printf("[non-stream] %dms\n", time.Since(start).Milliseconds())
	fmt.Printf("  Response: %s\n", resp.Choices[0].Message.Content)
}

func stream(ctx context.Context, client *openai.Client, req openai.ChatCompletionRequest) {
	req.Stream = true
	start := time.Now()
	stream, err := client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		log.Fatalf("stream: %v", err)
	}
	defer stream.Close()

	fmt.Println("--- Streaming output ---")
	var firstChunk time.Duration
	full := ""
	for {
		chunk, err := stream.Recv()
		if err != nil {
			break
		}
		if firstChunk == 0 {
			firstChunk = time.Since(start)
		}
		if len(chunk.Choices) > 0 {
			delta := chunk.Choices[0].Delta.Content
			full += delta
			fmt.Print(delta)
		}
	}
	fmt.Printf("\n\n[stream] First chunk: %dms, total: %dms\n", firstChunk.Milliseconds(), time.Since(start).Milliseconds())
	fmt.Printf("[stream] Full: %s\n", full)
}
