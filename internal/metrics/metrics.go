package metrics

import "sync/atomic"

type Metrics struct {
	CacheHits     atomic.Int64
	CacheMisses   atomic.Int64
	UpstreamOK    atomic.Int64
	UpstreamFail  atomic.Int64
	FallbackUsed  atomic.Int64
	TotalRequests atomic.Int64
}

func New() *Metrics { return &Metrics{} }

type Snapshot struct {
	CacheHits     int64   `json:"cache_hits"`
	CacheMisses   int64   `json:"cache_misses"`
	CacheHitRate  float64 `json:"cache_hit_rate"`
	UpstreamOK    int64   `json:"upstream_ok"`
	UpstreamFail  int64   `json:"upstream_fail"`
	FallbackUsed  int64   `json:"fallback_used"`
	TotalRequests int64   `json:"total_requests"`
}

func (m *Metrics) Snapshot() Snapshot {
	hits := m.CacheHits.Load()
	misses := m.CacheMisses.Load()
	total := hits + misses
	var rate float64
	if total > 0 {
		rate = float64(hits) / float64(total)
	}
	return Snapshot{
		CacheHits:     hits,
		CacheMisses:   misses,
		CacheHitRate:  rate,
		UpstreamOK:    m.UpstreamOK.Load(),
		UpstreamFail:  m.UpstreamFail.Load(),
		FallbackUsed:  m.FallbackUsed.Load(),
		TotalRequests: m.TotalRequests.Load(),
	}
}
