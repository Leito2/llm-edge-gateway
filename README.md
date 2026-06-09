# LLM Edge Gateway

> High-performance AI infrastructure: a Go/Fiber reverse proxy for commercial LLM APIs with **semantic caching**, **circuit-breaker failover**, and a **local Gemma 3 4B fallback** — built to cut API costs 30–40% and guarantee 99.9% uptime.

[![Go](https://img.shields.io/badge/Go-1.22-00ADD8?style=flat&logo=go)](https://golang.org)
[![Fiber](https://img.shields.io/badge/Fiber-v2-00ACD7?style=flat)](https://gofiber.io)
[![Redis](https://img.shields.io/badge/Redis-7-DC382D?style=flat&logo=redis)](https://redis.io)
[![Ollama](https://img.shields.io/badge/Ollama-local-000?style=flat)](https://ollama.com)
[![License](https://img.shields.io/badge/license-MIT-green?style=flat)](LICENSE)

---

## The Problem

Commercial LLM APIs (OpenAI, Anthropic, etc.) have two production-killing characteristics:

1. **Unpredictable latency** — p99 can spike from 800ms to 8s without warning, breaking user-facing UX.
2. **Linear cost scaling** — every token costs money; semantically equivalent queries are re-billed indefinitely.

At enterprise scale, these compound into a serious engineering and business constraint. You either pay a fortune, accept downtime, or build a custom abstraction layer.

**LLM Edge Gateway** is that abstraction layer.

## The Solution

A lightweight, single-binary Go proxy that sits between your application and any LLM provider. It:

1. **Embeds** every incoming query locally with `nomic-embed-text` (via Ollama on GPU).
2. **Looks up** the embedding in a Redis vector index (RediSearch, cosine similarity ≥ 0.96). On hit → returns the cached response in **<10ms**, zero API cost.
3. On miss → calls the upstream provider (Groq, free tier) through a **circuit breaker**.
4. If the upstream is slow (>5s) or erroring → the circuit **opens** and traffic is routed to a **local Gemma 3 4B** model running via Ollama on CPU.
5. Every successful upstream response is **cached** for next time.

---

## Architecture

```
                          ┌──────────────────────┐
                          │  Client Application  │
                          └──────────┬───────────┘
                                     │  POST /v1/chat/completions
                                     │  Authorization: Bearer <key>
                                     ▼
                    ┌────────────────────────────────┐
                    │       Go / Fiber Gateway        │
                    │  (fasthttp, single binary)      │
                    └────────────┬───────────────────┘
                                 │
                                 ▼
                    ┌────────────────────────────────┐
                    │  Embedder: nomic-embed-text    │
                    │  (Ollama, GPU, <5ms)           │
                    └────────────┬───────────────────┘
                                 │  768-dim vector
                                 ▼
                    ┌────────────────────────────────┐
                    │  Semantic Cache (Redis +        │
                    │  RediSearch, KNN cosine)       │
                    └────┬───────────────────────┬───┘
                         │ HIT (≥0.96)           │ MISS
                         │ <10ms                 ▼
                         │              ┌──────────────────────┐
                ┌────────┴───┐          │   Circuit Breaker    │
                │  Cached    │          │  (sony/gobreaker)    │
                │  Response  │          └────┬─────────────┬───┘
                └────────────┘               │             │
                                             │ healthy     │ open / >5s
                                             ▼             ▼
                                    ┌──────────────┐  ┌──────────────┐
                                    │  Upstream    │  │  Local       │
                                    │  Groq /      │  │  Gemma 3 4B  │
                                    │  OpenAI /    │  │  (Ollama,    │
                                    │  Anthropic   │  │   CPU)       │
                                    └──────┬───────┘  └──────┬───────┘
                                           │                 │
                                           └────────┬────────┘
                                                    ▼
                                          ┌──────────────────┐
                                          │ Cache writeback  │
                                          │ (best-effort)    │
                                          └──────────────────┘
```

---

## Key Features

| Feature | Description | Latency / Cost impact |
|---|---|---|
| **Semantic Caching** | Cosine-similarity vector search on every query. Threshold 0.96. | Cache hit: <10ms, $0. Cache miss: same as upstream. |
| **Circuit Breaker** | 3-state breaker (`sony/gobreaker`) around upstream. Opens on 3 consecutive failures or p95 latency > 5s. | Guarantees traffic keeps flowing when upstream degrades. |
| **Local Fallback** | Gemma 3 4B (`gemma3:4b`) on CPU via Ollama. | Availability floor: 99.9% even with upstream down. |
| **Single Binary** | One statically linked Go binary, no runtime deps except Redis + Ollama. | Trivial to deploy, no Python, no Node. |
| **Zero-Copy HTTP** | Built on Fiber/fasthttp — minimal allocations under high concurrency. | 10K+ req/s on a single core. |
| **Provider Agnostic** | Pluggable `Provider` interface. Groq default, swap to OpenAI/Anthropic/vLLM in one env var. | No vendor lock-in. |
| **Auth** | Bearer token, constant-time comparison. Fail-closed on missing key. | Safe to expose on a public network. |

---

## Tech Stack (Final, Tuned for Hardware)

| Layer | Choice | Rationale |
|---|---|---|
| **Language** | Go 1.22+ | Goroutine model, fasthttp performance, single static binary. |
| **HTTP Framework** | Fiber v2 (fasthttp) | 3x faster than `net/http` for proxy workloads, aggressive buffer pooling. |
| **Cache Store** | Redis Stack 7.4 (RediSearch module) | Native vector search (HNSW), single-process, easy ops. |
| **Embeddings** | `nomic-embed-text` via Ollama (768 dim) | Multilingual, fast on GPU, no Python dependency. |
| **Upstream LLM** | Groq (`llama-3.3-70b-versatile`) | Free tier, OpenAI-compatible API, sub-second inference. |
| **Local Fallback** | Gemma 3 4B (`gemma3:4b`) via Ollama, **CPU** | Fits in 4GB VRAM constraint + 8GB RAM + 7.6GB zram. |
| **Circuit Breaker** | `github.com/sony/gobreaker` | Battle-tested, used in production at Sony. |
| **Config** | Env vars + `.env` (no config files) | 12-factor. |
| **Container** | Docker Compose | Local dev; production deployment TBD. |

### Hardware profile (development)

- **GPU**: NVIDIA GTX 1650, 4 GB VRAM
- **RAM**: 8 GB
- **Swap**: 7.6 GB zram (CachyOS default)
- **Effective memory for inference**: ~15 GB compressed

> **Production note**: the 4GB VRAM constraint forces Gemma to run on CPU, capping fallback throughput at ~5–15 tok/s. On a server with ≥12 GB VRAM (e.g., RTX 3060, A4000), the same model would run on GPU at 30+ tok/s. The codebase is identical — only `OLLAMA_NUM_GPU` changes.

---

## API

The gateway is **API-compatible with OpenAI's `/v1/chat/completions`**. Existing OpenAI client libraries work without modification — just point the base URL at the gateway.

### `POST /v1/chat/completions`

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $GATEWAY_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "llama-3.3-70b-versatile",
    "messages": [
      {"role": "user", "content": "Explain the circuit breaker pattern in 3 sentences."}
    ]
  }'
```

Response is identical to OpenAI's format, with one extra header:

| Header | Meaning |
|---|---|
| `X-Cache-Status: HIT` | Served from semantic cache, no upstream call. |
| `X-Cache-Status: MISS` | Fetched from upstream (or fallback). |
| `X-Cache-Status: MISS-FALLBACK` | Circuit was open, served by local model. |
| `X-Provider: groq` / `X-Provider: ollama-local` | Which backend served the response. |

### `GET /health`

```json
{"status": "ok", "redis": "up", "ollama": "up", "upstream": "reachable"}
```

### `GET /stats`

```json
{
  "cache": {
    "hits": 1247,
    "misses": 318,
    "hit_rate": 0.797,
    "size": 1565
  },
  "circuit_breaker": {
    "state": "closed",
    "failures": 0
  },
  "uptime_seconds": 8642
}
```

---

## Project Structure

```
llm-edge-gateway/
├── cmd/
│   └── gateway/
│       └── main.go              # Entry point, wiring
├── internal/
│   ├── auth/                    # Bearer token middleware
│   ├── breaker/                 # Circuit breaker wrapper
│   ├── cache/                   # Semantic cache (Redis + RediSearch)
│   ├── config/                  # Env-based config
│   ├── embedder/                # Ollama embeddings client
│   ├── fallback/                # Local model provider
│   ├── metrics/                 # Cache stats, hit rate
│   ├── providers/               # Upstream providers (Groq, etc.)
│   └── proxy/                   # Request orchestration
├── pkg/
│   └── types/                   # Shared structs (ChatRequest, etc.)
├── configs/                     # Sample configs
├── scripts/
│   └── init-redis.sh            # Vector index bootstrap
├── test/                        # Integration tests
├── docs/                        # Architecture diagrams, design notes
├── docker-compose.yml           # Redis Stack service
├── .env.example                 # Environment template
├── AGENTS.md                    # Agent-facing build instructions
├── go.mod
├── go.sum
└── README.md
```

---

## Development Phases

| # | Phase | Status | Output |
|---|---|---|---|
| 0 | Prerrequisitos (Go, Docker, Ollama, GPU, swap) | ✅ | Verified environment |
| 1 | Redis Stack + init vector index | ⏳ | `docker-compose.yml`, `init-redis.sh` |
| 2 | Tipos compartidos + config desde env | ⏳ | `pkg/types/`, `internal/config/` |
| 3 | Embedder (Ollama, GPU) | ⏳ | `internal/embedder/` |
| 4 | Cache semántico con RediSearch | ⏳ | `internal/cache/` |
| 5 | Provider Groq (upstream) | ⏳ | `internal/providers/groq.go` |
| 6 | Circuit Breaker (5s threshold) | ⏳ | `internal/breaker/` |
| 7 | Fallback Gemma 3 4B (CPU) | ⏳ | `internal/fallback/` |
| 8 | Orquestador + main.go (Fiber) | ⏳ | `internal/proxy/`, `cmd/gateway/main.go` |
| 9 | Tests + benchmark + docs | ⏳ | `*_test.go`, benchmark numbers |
| 10 | Demo circuit breaker | ⏳ | End-to-end failover demo |
| 11 | Streaming SSE (post-MVP) | 🔮 | `text/event-stream` passthrough |

See [`AGENTS.md`](./AGENTS.md) for the **complete step-by-step build instructions** for each phase, including concepts to learn, file contents, and verification commands.

---

## Performance Targets

| Metric | Target | Notes |
|---|---|---|
| Cache hit latency | <10ms p99 | Pure Redis lookup, no model call |
| Cache hit rate (production) | 30–50% | Depends on query diversity |
| Cache miss → upstream (Groq) | ~800ms p50 | Groq's own latency |
| Cache miss → fallback (local Gemma) | 2–8s p50 | CPU-bound, 4B model |
| Concurrency | 10K+ req/s | Fiber/fasthttp limit |
| Memory footprint | <100MB (Go binary) | Plus Redis (~500MB) and Ollama (~4GB when active) |
| API cost reduction | 30–40% | Direct function of cache hit rate |

---

## Author

**Leandro Cataño Cardeño** — AI & ML Engineer
[LinkedIn](https://www.linkedin.com/in/leandro-cataño-cardeño) · [GitHub](https://github.com/Leito2) · leandro.cc.pro@gmail.com

---

## License

MIT — see [LICENSE](./LICENSE).
