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

func TestOllamaLocal_StreamChat_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollamaChatRequest
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		if !req.Stream {
			t.Error("expected stream=true in request")
		}
		flusher, _ := w.(http.Flusher)
		chunks := []ollamaChatResponse{
			{Model: "gemma3:1b", Message: types.ChatMessage{Role: "assistant", Content: "Hi "}, Done: false},
			{Model: "gemma3:1b", Message: types.ChatMessage{Role: "assistant", Content: "there"}, Done: false},
			{Model: "gemma3:1b", Message: types.ChatMessage{Role: "assistant", Content: "."}, Done: false, EvalCount: 1},
			{Model: "gemma3:1b", Message: types.ChatMessage{Role: "assistant", Content: ""}, Done: true, EvalCount: 3},
		}
		for _, c := range chunks {
			b, _ := json.Marshal(c)
			_, _ = w.Write(b)
			_, _ = w.Write([]byte("\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()

	o := NewOllamaLocal(srv.URL, "gemma3:1b", 2*time.Second)
	chunks, errs := o.StreamChat(context.Background(), types.ChatRequest{
		Messages: []types.ChatMessage{{Role: "user", Content: "x"}},
	})

	var collected []string
	for c := range chunks {
		if c.Done {
			break
		}
		collected = append(collected, c.Content)
	}
	select {
	case e := <-errs:
		if e != nil {
			t.Fatalf("unexpected error: %v", e)
		}
	default:
	}

	got := ""
	for _, c := range collected {
		got += c
	}
	if got != "Hi there." {
		t.Errorf("streamed content = %q, want 'Hi there.'", got)
	}
}

func TestOllamaLocal_StreamChat_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("model not loaded"))
	}))
	defer srv.Close()

	o := NewOllamaLocal(srv.URL, "gemma3:1b", 2*time.Second)
	_, errs := o.StreamChat(context.Background(), types.ChatRequest{
		Messages: []types.ChatMessage{{Role: "user", Content: "x"}},
	})
	select {
	case e := <-errs:
		if e == nil {
			t.Fatal("expected error, got nil")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("expected error channel signal, got none")
	}
}
