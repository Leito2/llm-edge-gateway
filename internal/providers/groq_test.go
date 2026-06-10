package providers

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

func newChatReq() types.ChatRequest {
	return types.ChatRequest{
		Model: "llama-3.3-70b-versatile",
		Messages: []types.ChatMessage{
			{Role: "user", Content: "What is Go?"},
		},
	}
}

func TestGroqProvider_Name(t *testing.T) {
	p := NewGroqProvider("http://x", "k", "m", time.Second)
	if p.Name() != "groq" {
		t.Errorf("Name() = %q, want 'groq'", p.Name())
	}
}

func TestGroqProvider_Chat_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want 'Bearer test-key'", got)
		}
		body, _ := io.ReadAll(r.Body)
		var req types.ChatRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("invalid request body: %v", err)
		}
		if req.Model != "llama-3.3-70b-versatile" {
			t.Errorf("model = %q, want llama-3.3-70b-versatile", req.Model)
		}
		if req.Stream {
			t.Error("expected stream=false in request")
		}
		if len(req.Messages) != 1 || req.Messages[0].Content != "What is Go?" {
			t.Errorf("messages = %+v", req.Messages)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.ChatResponse{
			ID:      "chatcmpl-123",
			Object:  "chat.completion",
			Created: 1700000000,
			Model:   "llama-3.3-70b-versatile",
			Choices: []types.ChatChoice{
				{
					Index: 0,
					Message: types.ChatMessage{
						Role:    "assistant",
						Content: "Go is a statically typed, compiled language.",
					},
					FinishReason: "stop",
				},
			},
			Usage: types.Usage{
				PromptTokens:     5,
				CompletionTokens: 10,
				TotalTokens:      15,
			},
		})
	}))
	defer srv.Close()

	p := NewGroqProvider(srv.URL, "test-key", "llama-3.3-70b-versatile", 2*time.Second)
	resp, err := p.Chat(context.Background(), newChatReq())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Provider != "groq" {
		t.Errorf("Provider = %q, want 'groq'", resp.Provider)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Go is a statically typed, compiled language." {
		t.Errorf("unexpected content: %q", resp.Choices[0].Message.Content)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("TotalTokens = %d, want 15", resp.Usage.TotalTokens)
	}
}

func TestGroqProvider_Chat_DefaultModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req types.ChatRequest
		_ = json.Unmarshal(body, &req)
		if req.Model != "default-model" {
			t.Errorf("model = %q, want 'default-model'", req.Model)
		}
		_ = json.NewEncoder(w).Encode(types.ChatResponse{Choices: []types.ChatChoice{}})
	}))
	defer srv.Close()

	p := NewGroqProvider(srv.URL, "k", "default-model", 2*time.Second)
	req := newChatReq()
	req.Model = "" // should default
	if _, err := p.Chat(context.Background(), req); err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestGroqProvider_Chat_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	p := NewGroqProvider(srv.URL, "bad", "m", 2*time.Second)
	_, err := p.Chat(context.Background(), newChatReq())
	if err == nil {
		t.Fatal("expected error on 401, got nil")
	}
}

func TestGroqProvider_Chat_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	p := NewGroqProvider(srv.URL, "k", "m", 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := p.Chat(ctx, newChatReq())
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestGroqProvider_Chat_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	p := NewGroqProvider(srv.URL, "k", "m", 2*time.Second)
	_, err := p.Chat(context.Background(), newChatReq())
	if err == nil {
		t.Fatal("expected decode error, got nil")
	}
}

func TestGroqProvider_StreamChat_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("expected Accept: text/event-stream, got %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		chunks := []string{
			`data: {"choices":[{"delta":{"content":"Go "},"finish_reason":""}]}`,
			`data: {"choices":[{"delta":{"content":"is "},"finish_reason":""}]}`,
			`data: {"choices":[{"delta":{"content":"fast"},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}
		for _, c := range chunks {
			_, _ = w.Write([]byte(c + "\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()

	p := NewGroqProvider(srv.URL, "k", "m", 2*time.Second)
	chunks, errs := p.StreamChat(context.Background(), newChatReq())

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
	if got != "Go is fast" {
		t.Errorf("streamed content = %q, want 'Go is fast'", got)
	}
}

func TestGroqProvider_StreamChat_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("invalid key"))
	}))
	defer srv.Close()

	p := NewGroqProvider(srv.URL, "k", "m", 2*time.Second)
	_, errs := p.StreamChat(context.Background(), newChatReq())
	for range []int{0: 0} {
		break
	}
	select {
	case e := <-errs:
		if e == nil {
			t.Fatal("expected error, got nil")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("expected error channel signal, got none")
	}
}
