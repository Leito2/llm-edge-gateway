package embedder

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func float32Slice(t *testing.T, vals ...float32) []float32 {
	t.Helper()
	return vals
}

func TestOllamaEmbedder_Embed_Success(t *testing.T) {
	expected := make([]float32, 768)
	for i := range expected {
		expected[i] = float32(i) * 0.001
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embeddings" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		var req ollamaRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("invalid request body: %v", err)
		}
		if req.Model != "nomic-embed-text" {
			t.Errorf("expected model nomic-embed-text, got %q", req.Model)
		}
		if req.Prompt != "hello" {
			t.Errorf("expected prompt 'hello', got %q", req.Prompt)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ollamaResponse{Embedding: expected})
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(srv.URL, "nomic-embed-text", 768)
	got, err := e.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}
	if len(got) != 768 {
		t.Fatalf("expected 768 dims, got %d", len(got))
	}
	if got[0] != expected[0] {
		t.Errorf("first dim mismatch: got %f, want %f", got[0], expected[0])
	}
	if e.Dimensions() != 768 {
		t.Errorf("Dimensions() = %d, want 768", e.Dimensions())
	}
}

func TestOllamaEmbedder_Embed_DimMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ollamaResponse{Embedding: make([]float32, 384)})
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(srv.URL, "wrong-model", 768)
	_, err := e.Embed(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected dimension mismatch error, got nil")
	}
}

func TestOllamaEmbedder_Embed_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("model not found"))
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(srv.URL, "missing-model", 768)
	_, err := e.Embed(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

func TestOllamaEmbedder_Embed_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		_, _ = w.Write([]byte("late"))
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(srv.URL, "m", 768)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := e.Embed(ctx, "hi")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestOllamaEmbedder_EmbedBatch(t *testing.T) {
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		vec := make([]float32, 768)
		_ = json.NewEncoder(w).Encode(ollamaResponse{Embedding: vec})
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(srv.URL, "m", 768)
	got, err := e.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 vectors, got %d", len(got))
	}
	if count != 3 {
		t.Errorf("expected 3 calls, got %d", count)
	}
}
