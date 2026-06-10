package fallback

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Leito2/llm-edge-gateway/pkg/types"
)

func TestOllamaLocal_Name(t *testing.T) {
	o := NewOllamaLocal("http://x", "gemma3:1b", time.Second)
	if o.Name() != "ollama-local" {
		t.Errorf("Name() = %q, want 'ollama-local'", o.Name())
	}
}

func TestOllamaLocal_Chat_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var req ollamaChatRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("invalid request body: %v", err)
		}
		if req.Model != "gemma3:1b" {
			t.Errorf("model = %q, want gemma3:1b", req.Model)
		}
		if req.Stream {
			t.Error("expected stream=false")
		}
		if len(req.Messages) != 1 || req.Messages[0].Content != "hi" {
			t.Errorf("messages = %+v", req.Messages)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ollamaChatResponse{
			Model: "gemma3:1b",
			Message: types.ChatMessage{
				Role:    "assistant",
				Content: "Hello! How can I help?",
			},
			Done:      true,
			EvalCount: 8,
		})
	}))
	defer srv.Close()

	o := NewOllamaLocal(srv.URL, "gemma3:1b", 2*time.Second)
	resp, err := o.Chat(context.Background(), types.ChatRequest{
		Messages: []types.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Provider != "ollama-local" {
		t.Errorf("Provider = %q, want 'ollama-local'", resp.Provider)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Hello! How can I help?" {
		t.Errorf("content = %q", resp.Choices[0].Message.Content)
	}
	if resp.Usage.CompletionTokens != 8 {
		t.Errorf("CompletionTokens = %d, want 8", resp.Usage.CompletionTokens)
	}
}

func TestOllamaLocal_Chat_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("model crashed"))
	}))
	defer srv.Close()

	o := NewOllamaLocal(srv.URL, "gemma3:1b", 2*time.Second)
	_, err := o.Chat(context.Background(), types.ChatRequest{
		Messages: []types.ChatMessage{{Role: "user", Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

func TestOllamaLocal_Chat_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	o := NewOllamaLocal(srv.URL, "gemma3:1b", 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := o.Chat(ctx, types.ChatRequest{
		Messages: []types.ChatMessage{{Role: "user", Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestOllamaLocal_Chat_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	o := NewOllamaLocal(srv.URL, "gemma3:1b", 2*time.Second)
	_, err := o.Chat(context.Background(), types.ChatRequest{
		Messages: []types.ChatMessage{{Role: "user", Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected decode error, got nil")
	}
}

func TestOllamaLocal_Chat_DefaultModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req ollamaChatRequest
		_ = json.Unmarshal(body, &req)
		if req.Model != "default-fallback" {
			t.Errorf("model = %q, want 'default-fallback'", req.Model)
		}
		_ = json.NewEncoder(w).Encode(ollamaChatResponse{Message: types.ChatMessage{Content: "ok"}})
	}))
	defer srv.Close()

	o := NewOllamaLocal(srv.URL, "default-fallback", 2*time.Second)
	_, err := o.Chat(context.Background(), types.ChatRequest{
		Messages: []types.ChatMessage{{Role: "user", Content: "x"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}
