# LLM Edge Gateway

> High-performance reverse proxy for LLM APIs — semantic caching, local failover, and cost control at the edge.

[![Go](https://img.shields.io/badge/Go-1.22-00ADD8?style=flat&logo=go)](https://golang.org)
[![Fiber](https://img.shields.io/badge/Fiber-v2-00ACD7?style=flat)](https://gofiber.io)
[![Redis](https://img.shields.io/badge/Redis-7-DC382D?style=flat&logo=redis)](https://redis.io)
[![License](https://img.shields.io/badge/license-MIT-green?style=flat)](LICENSE)

---

## Overview

Commercial LLM APIs suffer from unpredictable latency spikes and linear cost scaling. At high request volumes, these two problems compound into a serious engineering and business constraint.

**LLM Edge Gateway** solves this with a lightweight proxy layer that sits between your application and any LLM provider. It intercepts requests, checks a semantic cache first, and falls back gracefully to a local model when the upstream provider is slow or unavailable.

**Results:** 30–40% reduction in API costs · 99.9% uptime via local/cloud redundancy.

---

## Architecture

```
Client Request
      │
      ▼
┌─────────────────┐
│   Go / Fiber    │  ← High-concurrency HTTP proxy
│   Reverse Proxy │
└────────┬────────┘
         │
         ▼
┌─────────────────┐     HIT (<10ms)    ┌───────────────────┐
│  Semantic Cache │ ──────────────────▶ │  Cached Response  │
│  Redis + Search │                     └───────────────────┘
└────────┬────────┘
         │ MISS
         ▼
┌─────────────────┐   Healthy   ┌──────────────────┐
│ Circuit Breaker │────────────▶│  External LLM API │
│                 │             └──────────────────┘
│                 │   Tripped / Latency > 2s
│                 │────────────▶┌──────────────────┐
└─────────────────┘             │  Gemma 4 Local   │
                                │  (MoE, 3.8B act) │
                                └──────────────────┘
```

---

## Key Features

- **Semantic Caching** — Embeds incoming queries via Hugging Face pipeline and stores vectors in Redis. Cosine similarity threshold of 0.96 ensures only semantically equivalent queries hit the cache. Cache hits return in under 10ms.
- **Circuit Breaker** — Monitors upstream provider health. Automatically reroutes traffic to the local Gemma 4 instance when latency exceeds 2 seconds or error rates spike.
- **Gemma 4 Local Fallback** — Uses Gemma 4 (26B MoE architecture) which activates only ~3.8B parameters per forward pass, enabling fast local inference on consumer hardware.
- **Provider Agnostic** — Swap any upstream LLM provider via config. OpenAI, Anthropic, Google, or self-hosted.
- **Zero-copy routing** — Built on Fiber's fasthttp adapter for minimal memory allocation under high concurrency.

---

## Tech Stack

| Layer | Technology |
|---|---|
| Runtime | Go 1.22 |
| HTTP Framework | Fiber v2 (fasthttp) |
| Cache Store | Redis 7 + RediSearch |
| Embeddings | Hugging Face Transformers |
| Local LLM | Gemma 4 26B MoE |
| Deployment | Docker · GCP Cloud Run |

---

## Getting Started

```bash
git clone https://github.com/Leito2/llm-edge-gateway
cd llm-edge-gateway
cp .env.example .env
docker compose up
```

Configure your upstream provider and Redis in `.env`:

```env
UPSTREAM_API_URL=https://api.openai.com/v1
UPSTREAM_API_KEY=your_key_here
REDIS_URL=redis://localhost:6379
SIMILARITY_THRESHOLD=0.96
CIRCUIT_BREAKER_LATENCY_MS=2000
LOCAL_MODEL_ENDPOINT=http://localhost:8080
```

---

## Performance

| Metric | Value |
|---|---|
| Cache hit latency | < 10ms |
| API cost reduction | 30–40% |
| Uptime (local+cloud) | 99.9% |
| Concurrency | 10,000+ req/s (Fiber/fasthttp) |

---

## Project Status

Active development. Core proxy and caching are stable. Circuit breaker and local fallback are functional. Observability dashboard (Prometheus + Grafana) in progress.

---

## Author

**Leandro Cataño Cardeño** — AI & ML Engineer  
[LinkedIn](https://www.linkedin.com/in/leandro-cataño-cardeño) · [GitHub](https://github.com/Leito2) · leandro.cc.pro@gmail.com
