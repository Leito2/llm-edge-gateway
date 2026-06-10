package cache

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

type SemanticCache struct {
	rdb      *redis.Client
	threshold float64
	ttl      time.Duration

	hits   atomic.Int64
	misses atomic.Int64
}

type Entry struct {
	Key          string
	Query        string
	ResponseJSON string
	Model        string
	Provider     string
	CreatedAt    time.Time
	Hits         int64
}

type Stats struct {
	Hits    int64   `json:"hits"`
	Misses  int64   `json:"misses"`
	HitRate float64 `json:"hit_rate"`
	Size    int64   `json:"size"`
}

func New(rdb *redis.Client, threshold float64, ttl time.Duration) *SemanticCache {
	return &SemanticCache{rdb: rdb, threshold: threshold, ttl: ttl}
}

func floatsToBytes(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func newKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "cache:" + hex.EncodeToString(b)
}

func (c *SemanticCache) Get(ctx context.Context, embedding []float32) (*Entry, float64, error) {
	if len(embedding) == 0 {
		c.misses.Add(1)
		return nil, 0, nil
	}

	vecBlob := floatsToBytes(embedding)

	res, err := c.rdb.Do(ctx, "FT.SEARCH", "cache_idx",
		"*=>[KNN 1 @embedding $vec]",
		"PARAMS", "2", "vec", vecBlob,
		"DIALECT", "2",
		"LOAD", "6", "@query", "@response_json", "@model", "@provider", "@created_at", "@hits",
	).Result()
	if err != nil {
		c.misses.Add(1)
		return nil, 0, fmt.Errorf("ft.search: %w", err)
	}

	total, fieldsList, err := parseSearchResponse(res)
	if err != nil {
		c.misses.Add(1)
		return nil, 0, nil
	}
	if total == 0 || len(fieldsList) == 0 {
		c.misses.Add(1)
		return nil, 0, nil
	}

	doc := fieldsList[0]
	similarity := 1.0 - doc.score
	if similarity < c.threshold {
		c.misses.Add(1)
		return nil, similarity, nil
	}

	entry := &Entry{
		Key:          doc.key,
		Query:        doc.fields["query"],
		ResponseJSON: doc.fields["response_json"],
		Model:        doc.fields["model"],
		Provider:     doc.fields["provider"],
		CreatedAt:    parseCreatedAt(doc.fields["created_at"]),
		Hits:         parseInt64(doc.fields["hits"]),
	}

	_ = c.rdb.HIncrBy(ctx, entry.Key, "hits", 1).Err()

	c.hits.Add(1)
	return entry, similarity, nil
}

type searchDoc struct {
	key    string
	fields map[string]string
	score  float64
}

func parseSearchResponse(res any) (int, []searchDoc, error) {
	m, ok := res.(map[any]any)
	if !ok {
		if sm, sok := res.(map[string]any); sok {
			m = make(map[any]any, len(sm))
			for k, v := range sm {
				m[k] = v
			}
		} else {
			return 0, nil, fmt.Errorf("unexpected FT.SEARCH response type: %T", res)
		}
	}

	total := 0
	if tr, ok := m["total_results"]; ok {
		switch v := tr.(type) {
		case int64:
			total = int(v)
		case int:
			total = v
		}
	}

	resultsRaw, ok := m["results"].([]any)
	if !ok {
		return total, nil, nil
	}

	docs := make([]searchDoc, 0, len(resultsRaw))
	for _, r := range resultsRaw {
		rm, ok := r.(map[any]any)
		if !ok {
			if sm, sok := r.(map[string]any); sok {
				rm = make(map[any]any, len(sm))
				for k, v := range sm {
					rm[k] = v
				}
			} else {
				continue
			}
		}
		key, _ := rm["id"].(string)

		// With LOAD + DIALECT 2, RediSearch returns fields inside extra_attributes,
		// not in `values`. Merge both to be safe.
		fields := make(map[string]string)
		if vs, ok := rm["values"].([]any); ok {
			for k, v := range parseFields(vs) {
				fields[k] = v
			}
		}
		score := 0.0
		if ea, ok := rm["extra_attributes"].(map[any]any); ok {
			score = readFloat(ea["__embedding_score"])
			for k, v := range ea {
				if ks, ok := k.(string); ok {
					if vs, ok := v.(string); ok {
						if ks != "__embedding_score" {
							fields[ks] = vs
						}
					}
				}
			}
		} else if ea, ok := rm["extra_attributes"].(map[string]any); ok {
			score = readFloat(ea["__embedding_score"])
			for k, v := range ea {
				if k != "__embedding_score" {
					if vs, ok := v.(string); ok {
						fields[k] = vs
					}
				}
			}
		}

		docs = append(docs, searchDoc{
			key:    key,
			fields: fields,
			score:  score,
		})
	}

	return total, docs, nil
}

func readFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int64:
		return float64(x)
	case int:
		return float64(x)
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

func (c *SemanticCache) Set(ctx context.Context, query string, embedding []float32, respJSON, model, provider string) error {
	key := newKey()
	vecBlob := floatsToBytes(embedding)
	now := time.Now().Unix()

	fields := map[string]any{
		"query":         query,
		"embedding":     vecBlob,
		"response_json": respJSON,
		"model":         model,
		"provider":      provider,
		"created_at":    now,
		"hits":          0,
	}

	pipe := c.rdb.Pipeline()
	pipe.HSet(ctx, key, fields)
	if c.ttl > 0 {
		pipe.Expire(ctx, key, c.ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("cache set: %w", err)
	}
	return nil
}

func (c *SemanticCache) Stats(ctx context.Context) (Stats, error) {
	hits := c.hits.Load()
	misses := c.misses.Load()
	total := hits + misses
	var rate float64
	if total > 0 {
		rate = float64(hits) / float64(total)
	}

	size, err := c.rdb.DBSize(ctx).Result()
	if err != nil {
		return Stats{}, err
	}

	return Stats{
		Hits:    hits,
		Misses:  misses,
		HitRate: rate,
		Size:    size,
	}, nil
}

func parseFields(fields []any) map[string]string {
	out := make(map[string]string, len(fields)/2)
	for i := 0; i+1 < len(fields); i += 2 {
		k, _ := fields[i].(string)
		v, _ := fields[i+1].(string)
		out[k] = v
	}
	return out
}

func parseCreatedAt(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

func parseInt64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func (c *SemanticCache) Reset(ctx context.Context) error {
	return c.rdb.FlushDB(ctx).Err()
}

func (c *SemanticCache) DeleteByKey(ctx context.Context, key string) error {
	return c.rdb.Del(ctx, key).Err()
}

func MustEncode(v any) string {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		return ""
	}
	return buf.String()
}
