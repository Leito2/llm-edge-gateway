package cache

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func newTestCache(t *testing.T, threshold float64) (*SemanticCache, *redis.Client) {
	t.Helper()
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not reachable at %s: %v", addr, err)
	}
	return New(rdb, threshold, 0), rdb
}

func cleanupKeys(t *testing.T, rdb *redis.Client, pattern string) {
	t.Helper()
	ctx := context.Background()
	// SCAN to avoid blocking on large keyspaces
	var cursor uint64
	for {
		keys, next, err := rdb.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return
		}
		if len(keys) > 0 {
			_ = rdb.Del(ctx, keys...).Err()
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
}

func TestMain(m *testing.M) {
	// Best-effort: clean any leftover cache entries from prior runs.
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, DialTimeout: 2 * time.Second})
	if err := rdb.Ping(context.Background()).Err(); err == nil {
		// delete cache: keys but DO NOT touch the index
		var cursor uint64
		for {
			keys, next, err := rdb.Scan(context.Background(), cursor, "cache:*", 100).Result()
			if err == nil && len(keys) > 0 {
				rdb.Del(context.Background(), keys...)
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
	rdb.Close()
	os.Exit(m.Run())
}

func makeVec(seed int, dims int) []float32 {
	v := make([]float32, dims)
	for i := range v {
		v[i] = float32(seed+i) * 0.001
	}
	return v
}

func TestSemanticCache_Miss_Empty(t *testing.T) {
	c, rdb := newTestCache(t, 0.96)
	defer rdb.Close()

	entry, sim, err := c.Get(context.Background(), makeVec(1, 768))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if entry != nil {
		t.Errorf("expected nil entry on empty cache, got %+v", entry)
	}
	if sim != 0 {
		t.Errorf("expected sim 0 on miss, got %f", sim)
	}
}

func TestSemanticCache_SetThenGetHit(t *testing.T) {
	c, rdb := newTestCache(t, 0.96)
	defer rdb.Close()
	ctx := context.Background()
	t.Cleanup(func() { cleanupKeys(t, rdb, "cache:*") })

	vec := makeVec(42, 768)
	respJSON := `{"text":"the response"}`

	if err := c.Set(ctx, "what is Go?", vec, respJSON, "llama-3.3-70b-versatile", "groq"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	entry, sim, err := c.Get(ctx, vec)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if entry == nil {
		t.Fatal("expected hit, got nil")
	}
	if sim < 0.99 {
		t.Errorf("expected near-1.0 similarity for identical vec, got %f", sim)
	}
	if entry.Query != "what is Go?" {
		t.Errorf("entry.Query = %q, want 'what is Go?'", entry.Query)
	}
	if entry.ResponseJSON != respJSON {
		t.Errorf("entry.ResponseJSON = %q, want %q", entry.ResponseJSON, respJSON)
	}
	if entry.Provider != "groq" {
		t.Errorf("entry.Provider = %q, want 'groq'", entry.Provider)
	}
}

func TestSemanticCache_Get_BelowThreshold(t *testing.T) {
	c, rdb := newTestCache(t, 0.96)
	defer rdb.Close()
	ctx := context.Background()
	t.Cleanup(func() { cleanupKeys(t, rdb, "cache:*") })

	vecA := makeVec(1, 768)
	vecB := makeVec(99999, 768)

	if err := c.Set(ctx, "q", vecA, `{"x":1}`, "m", "groq"); err != nil {
		t.Fatal(err)
	}

	entry, sim, err := c.Get(ctx, vecB)
	if err != nil {
		t.Fatal(err)
	}
	if entry != nil {
		t.Errorf("expected miss for unrelated vector, got %+v", entry)
	}
	if sim >= 0.96 {
		t.Errorf("expected sim < threshold, got %f", sim)
	}
}

func TestSemanticCache_Stats(t *testing.T) {
	c, rdb := newTestCache(t, 0.5)
	defer rdb.Close()
	ctx := context.Background()
	t.Cleanup(func() { cleanupKeys(t, rdb, "cache:*") })

	vec := makeVec(7, 768)
	if err := c.Set(ctx, "q", vec, `{"x":1}`, "m", "groq"); err != nil {
		t.Fatal(err)
	}

	// 3 hits (same vec)
	for i := 0; i < 3; i++ {
		entry, sim, _ := c.Get(ctx, vec)
		if entry == nil {
			t.Fatalf("expected hit %d, got miss (sim=%f)", i, sim)
		}
	}

	// 2 misses (empty cache paths) - Get with empty vec forces miss
	c.misses.Store(0) // reset before counting
	for i := 0; i < 2; i++ {
		_, _, _ = c.Get(ctx, []float32{}) // empty vec is a forced miss
	}

	stats, err := c.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Hits != 3 {
		t.Errorf("hits = %d, want 3", stats.Hits)
	}
	if stats.Misses != 2 {
		t.Errorf("misses = %d, want 2", stats.Misses)
	}
	expectedRate := 3.0 / 5.0
	if stats.HitRate < expectedRate-0.01 || stats.HitRate > expectedRate+0.01 {
		t.Errorf("hit_rate = %f, want ~%f", stats.HitRate, expectedRate)
	}
	if stats.Size < 1 {
		t.Errorf("size = %d, want >= 1", stats.Size)
	}
}
