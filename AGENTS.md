# AGENTS.md — LLM Edge Gateway Build Instructions

> Complete, step-by-step build instructions for an autonomous coding agent (or human) implementing the LLM Edge Gateway MVP.
>
> **Read this file top-to-bottom before making any change.** It encodes the architecture, the rationale for every technical decision, and the exact commands/files/verification steps for each phase.

---

## Table of Contents

- [Context: What We're Building and Why](#context-what-were-building-and-why)
- [Architecture: The Request Flow](#architecture-the-request-flow)
- [Hardware Constraints & Adaptations](#hardware-constraints--adaptations)
- [Tech Stack (Locked)](#tech-stack-locked)
- [API Key Strategy](#api-key-strategy)
- [Phase 0 — Prerequisites (CURRENT PHASE — DO THIS FIRST)](#phase-0--prerequisites-current-phase--do-this-first)
- [Phase 1 — Redis Stack + Vector Index (DRAFT)](#phase-1--redis-stack--vector-index-draft)
- [Phase 2 — Types & Config (DRAFT)](#phase-2--types--config-draft)
- [Phase 3 — Embedder (DRAFT)](#phase-3--embedder-draft)
- [Phase 4 — Semantic Cache (DRAFT)](#phase-4--semantic-cache-draft)
- [Phase 5 — Groq Provider (DRAFT)](#phase-5--groq-provider-draft)
- [Phase 6 — Circuit Breaker (DRAFT)](#phase-6--circuit-breaker-draft)
- [Phase 7 — Local Fallback (DRAFT)](#phase-7--local-fallback-draft)
- [Phase 8 — Orchestrator + Main (DRAFT)](#phase-8--orchestrator--main-draft)
- [Phase 9 — Tests, Benchmarks, Docs (DRAFT)](#phase-9--tests-benchmarks-docs-draft)
- [Phase 10 — Demo (DRAFT)](#phase-10--demo-draft)
- [Phase 11 — Streaming SSE (POST-MVP, DRAFT)](#phase-11--streaming-sse-post-mvp-draft)
- [Conventions](#conventions)
- [Troubleshooting](#troubleshooting)

---

## Context: What We're Building and Why

### The problem we're solving

Commercial LLM APIs (OpenAI, Anthropic, Google) have two production-killing properties:

1. **Unpredictable latency** — p99 latency can spike from 800ms to 8s+ without warning, breaking user-facing UX.
2. **Linear cost scaling** — every token costs money. Semantically equivalent queries ("¿qué es Go?", "explícame Go", "Go language intro") all get re-billed at full cost.

For a startup or enterprise running thousands of LLM calls per day, these compound into a serious engineering AND business constraint. The choice becomes: pay a fortune, accept downtime, or build a custom abstraction layer.

### The solution

**LLM Edge Gateway** is a Go reverse proxy that sits between your application and any LLM provider. It:

1. Embeds every incoming query locally with `nomic-embed-text` (via Ollama, on GPU).
2. Looks up that embedding in a Redis vector index (RediSearch, cosine similarity ≥ 0.96). On hit → returns the cached response in <10ms, zero API cost.
3. On miss → calls the upstream provider (Groq, free tier) through a circuit breaker.
4. If the upstream is slow (>5s) or erroring → the circuit opens and traffic is routed to a local Gemma 3 4B model running via Ollama on CPU.
5. Every successful upstream response is written back to the cache for next time.

### Business impact (per the original project brief)

- **30–40% reduction in API operational costs** (driven by cache hit rate).
- **99.9% uptime** via local/cloud redundancy (the fallback guarantees the service stays up even if the upstream is down for hours).

### Why this is a "systems engineering" project, not just "an API wrapper"

This is not a thin HTTP client. The interesting parts are:

- **fasthttp + Fiber's buffer pooling** to handle 10K+ req/s on a single core with negligible RAM.
- **Vector search in Redis** with HNSW indexing and cosine similarity thresholding.
- **Circuit breaker state machine** with health-probing transitions.
- **Mixture-of-Experts inference** (Gemma 3 4B's MoE architecture activates only a subset of parameters per token).
- **CPU/GPU memory budgeting** on constrained hardware (4GB VRAM, 8GB RAM, 7.6GB zram).

Together these demonstrate the ability to design **AI infrastructure**, not just call models.

---

## Architecture: The Request Flow

```
Client → POST /v1/chat/completions
          ↓
        Go/Fiber (single binary, fasthttp)
          ↓
        Embedder (Ollama, nomic-embed-text, GPU) → 768-dim vector
          ↓
        Semantic Cache (Redis + RediSearch, HNSW, cosine)
          ↓
        ┌─────────────────────┬─────────────────────────┐
        │ HIT (sim ≥ 0.96)    │ MISS                    │
        │ return cached resp  │ call circuit breaker    │
        │ <10ms, no cost      │                         │
        └─────────────────────┴────────┬────────────────┘
                                       ↓
                              ┌────────────────────────┐
                              │  Circuit Breaker       │
                              │  (sony/gobreaker)      │
                              │  state: closed/open/   │
                              │         half-open      │
                              └───┬──────────────┬─────┘
                                  │ closed       │ open
                                  ↓              ↓
                          ┌──────────────┐  ┌──────────────┐
                          │ Upstream:    │  │ Local:       │
                          │  Groq        │  │  Gemma 3 4B  │
                          │  (free tier) │  │  (Ollama,    │
                          │  ~800ms p50  │  │   CPU)       │
                          └──────┬───────┘  └──────┬───────┘
                                 │                 │
                                 └────────┬────────┘
                                          ↓
                                 Cache writeback
                                 (best-effort, async)
                                          ↓
                              Response to client
                              + X-Cache-Status header
                              + X-Provider header
```

---

## Hardware Constraints & Adaptations

The target development environment is **not** a server. It's a developer workstation:

| Resource | Available | Used by |
|---|---|---|
| GPU VRAM | 4 GB (GTX 1650) | `nomic-embed-text` (274 MB), nothing else |
| System RAM | 8 GB | OS + Go gateway + Redis + Ollama CPU inference |
| Swap | 7.6 GB zram (CachyOS default) | Burst headroom for Gemma 3 4B model load |
| Disk | Assume SSD | Ollama model storage (~2.5 GB for gemma3:4b) |

### Consequences for the design

1. **Gemma 3 4B runs on CPU**, not GPU. Ollama is launched with `OLLAMA_NUM_GPU=0` for the inference model. Expected speed: 5–15 tokens/s.
2. **Embeddings stay on GPU.** `nomic-embed-text` is small (274 MB) and would otherwise waste the only GPU we have.
3. **Redis is capped at 2 GB** via `deploy.resources.limits.memory` in docker-compose, to prevent OOM-killing the gateway.
4. **Gemma 3 27B is NOT used.** The original brief mentions it, but 27B Q4 needs ~18 GB VRAM. We use `gemma3:4b` (Q4_0 quantization, ~2.5 GB on disk, ~4 GB loaded in RAM).
5. **Context window is reduced** to 2048 tokens (Ollama default is 4096) to keep memory pressure down.

### Production note (for the README)

> On a server with ≥12 GB VRAM (e.g., RTX 3060, A4000), the same `gemma3:4b` model would run on GPU at 30+ tokens/s. The codebase is identical — only `OLLAMA_NUM_GPU` changes. This makes the codebase hardware-adaptive without code changes.

---

## Tech Stack (Locked)

| Layer | Choice | Why |
|---|---|---|
| **Language** | Go 1.22+ | Goroutines, fasthttp performance, single static binary, mature ecosystem. |
| **HTTP Framework** | Fiber v2 (fasthttp) | 3x faster than `net/http` for proxy workloads. Aggressive buffer pooling. Critical detail: fasthttp does NOT use `net/http.Request` — Fiber re-exposes request/response through `*fiber.Ctx`. |
| **Cache Store** | Redis Stack 7.4 (RediSearch module) | Native HNSW vector search. Single process, easy ops. |
| **Embeddings** | `nomic-embed-text` via Ollama, 768 dim | Multilingual, fast on GPU, no Python dependency. |
| **Upstream LLM** | Groq (`llama-3.3-70b-versatile`) | Free tier, OpenAI-compatible API, sub-second inference. |
| **Local Fallback** | Gemma 3 4B (`gemma3:4b`) via Ollama, CPU | Fits hardware constraints. |
| **Circuit Breaker** | `github.com/sony/gobreaker` | Battle-tested, used in production at Sony. State machine + counters + ready-to-trip. |
| **Config** | Env vars + `.env.example` | 12-factor, no config files. |
| **Container** | Docker Compose | Local dev. Production deployment TBD. |
| **Tests** | Go stdlib `testing` + `httptest` | No external test framework needed. |
| **Benchmark** | `hey` (Go HTTP load gen) | Simple, ubiquitous. |

### Why Go + Fiber (not Python, not Node, not Rust)

The original brief calls out this explicitly: "Go & Fiber Stack ... para aprovechar el pool de memoria de fasthttp, permitiendo procesar miles de peticiones con un consumo de RAM despreciable en comparación con implementaciones en Python."

- **vs Python**: GIL limits concurrency; aiohttp doesn't pool buffers as aggressively; every request allocates bytes. A Python proxy handling 10K req/s is RAM-heavy.
- **vs Node**: Better than Python but still allocates per request. fasthttp is purpose-built for this.
- **vs Rust (e.g., hyper)**: Comparable performance, but development speed is much slower for an MVP. The agent chose Go for time-to-MVP without giving up too much perf.

### Why Groq (not OpenAI) for the upstream

- OpenAI removed its free tier in 2024.
- Groq offers a generous free tier with `llama-3.3-70b-versatile`.
- Groq's API is **OpenAI-compatible**, so the gateway's provider interface doesn't change.
- Latency is excellent (Groq's LPU inference is faster than most cloud GPU inference).

### Why Ollama (not llama.cpp, not vLLM) for the local fallback

- **vs llama.cpp server**: Ollama is a wrapper over llama.cpp with model management built in. One command (`ollama pull gemma3:4b`) downloads and configures. No compilation, no manual GGUF handling.
- **vs vLLM**: vLLM is great for batched throughput, but its setup is heavier (Python deps, separate config). Ollama is single-binary and good enough for a fallback that handles low traffic.
- **Same Ollama server** runs both the embedding model AND the fallback model. One process, one port (11434).

---

## API Key Strategy

The gateway uses **single API key with constant-time comparison**, read from `GATEWAY_API_KEY` env var.

### Properties

- **Single key**, shared across all clients/services. Sufficient for MVP.
- **`Authorization: Bearer <key>`** header, identical to OpenAI. Existing client libraries work.
- **Constant-time comparison** via `crypto/subtle.ConstantTimeCompare` — prevents timing attacks where an attacker deduces the key character-by-character.
- **Fail-closed**: if `GATEWAY_API_KEY` is not set, the gateway **refuses to start**. Better than starting open.
- **No rate limiting per key in MVP.** Will be added in Phase 11+ if needed; Redis already provides the primitives (`INCR` with TTL).

### Why not multi-tenant with multiple keys?

Multi-tenant adds significant complexity (key storage, per-key rate limit windows, admin endpoints) for no benefit at this stage. If the project ever needs to monetize, multi-tenant gets bolted on with Redis-backed key store.

### What gets implemented in which phase

- **Phase 8** (orchestrator): the `internal/auth` middleware that validates the bearer token.
- **Phase 9** (tests): unit test that:
  - rejects requests with no `Authorization` header
  - rejects requests with wrong key
  - accepts requests with correct key
  - refuses to start when `GATEWAY_API_KEY` is empty

---

## Phase 0 — Prerequisites (CURRENT PHASE — DO THIS FIRST)

This phase establishes the baseline environment. Nothing is built yet. We're verifying that the box can run what we're about to build.

### 0.1 — What this phase delivers

- A verified working environment (Go, Docker, GPU, Ollama, network).
- A cloned git repo at `/home/white/Projects/Go-LLM-Gateway`.
- A created folder skeleton for the Go project.
- An initialized `go.mod` with the correct module path.

### 0.2 — Concepts to learn (read these BEFORE running anything)

#### Concept 1: `fasthttp` vs `net/http`

Go's standard `net/http` allocates a new `http.Request` struct per request. Under 10K req/s that's a lot of garbage for the GC to chase.

`fasthttp` (which Fiber wraps) takes the opposite approach: it pre-allocates huge buffers and reuses them across requests via a `sync.Pool`. The trade-off is that the API is different from `net/http`:

- `c.Request().BodyStream()` instead of `r.Body`
- `c.Response().SetBodyStreamWriter(...)` for streaming responses
- No `context.Context` directly on the request; you use `c.Context()` (Fiber bridges it)

For our use case (a stateless proxy), this is fine. Just remember: **if you copy-paste a Stack Overflow snippet using `http.Request`, it will NOT compile in Fiber.** Look for Fiber-specific examples or use `c` (the `*fiber.Ctx`).

#### Concept 2: Embeddings as fixed-dimensional vectors

An "embedding" is an array of `float32` numbers (e.g., 768 of them for `nomic-embed-text`) that represents the *semantic meaning* of a text. Similar texts have similar vectors. We measure similarity with **cosine similarity**:

```
cos(A, B) = (A · B) / (|A| × |B|)
```

Range: `[-1, 1]`. For normalized vectors (which embedding models output), the range is `[0, 1]`. A threshold of `0.96` means "almost identical meaning."

We store these in Redis as binary blobs (`VECTOR HNSW 6 TYPE FLOAT32 DIM 768`) and use RediSearch's `KNN` operator to find the closest one.

#### Concept 3: Circuit Breaker pattern

A circuit breaker protects a downstream service from being hammered when it's failing. Three states:

- **closed** (normal): requests pass through. Count consecutive failures.
- **open** (tripped): requests fail fast without touching downstream. After a timeout, transition to half-open.
- **half-open** (probing): allow one request through. If it succeeds, go back to closed. If it fails, go back to open.

In our case, "the circuit" wraps the Groq provider. When Groq is down, every request would wait 2-5s timing out. With the breaker, requests fail fast and the fallback handles them in parallel.

#### Concept 4: Mixture of Experts (MoE)

Gemma 3 4B (and the original brief's 27B) uses MoE: the model has many "expert" sub-networks, and a router decides which experts to activate for each token. Only a fraction of the total parameters run per token.

Why this matters:
- The 27B model has 27B params total but activates only ~3.8B per token → can run on consumer hardware.
- We use the 4B variant (not 27B) due to VRAM constraints, but the MoE principle is the same.
- MoE inference is **inherently faster** than dense models of equivalent total size.

### 0.3 — Step-by-step commands

#### Step 0.3.1 — Verify Go

```bash
go version
```

**Expected**: `go version go1.22.0` or higher (we have 1.26.3, which is fine).

**If not installed**:
```bash
sudo pacman -S --needed go
```

**Why Go version matters**: 1.22 introduced range-over-int and improved `http.ServeMux` patterns. 1.21 introduced built-in `slices` and `maps` packages. 1.18+ has generics which we use in some interfaces.

#### Step 0.3.2 — Verify Docker + docker-compose

```bash
docker --version
docker compose version
```

**Expected**: Docker 20.10+, Compose v2 (`docker compose` not `docker-compose`).

**If not installed** (CachyOS / Arch):
```bash
sudo pacman -S --needed --noconfirm docker docker-compose
sudo systemctl enable --now docker.service
sudo systemctl enable --now containerd.service
sudo usermod -aG docker $USER
newgrp docker   # or log out and back in
```

**Why this matters**: Redis Stack is distributed as a Docker image with RediSearch pre-compiled into it. Building RediSearch from source is a 20+ minute affair — Docker sidesteps that.

#### Step 0.3.3 — Verify GPU

```bash
nvidia-smi
```

**Expected output**: A table showing the GPU name, driver version, and a `Memory-Usage` line showing `XXXMiB / 4096MiB` (for a 4GB card).

**Sanity check**: the driver version is ≥ 525 (for CUDA 12 support, which Ollama needs for GPU inference of the embedding model).

**If not detected**: install NVIDIA drivers. On CachyOS, `sudo pacman -S nvidia nvidia-utils` and reboot.

#### Step 0.3.4 — Verify swap

```bash
swapon --show
cat /proc/swaps
```

**Expected**: At least one swap device listed, ideally with ≥ 4 GB. On CachyOS, `zram` is the default and typically provides ~50% of RAM as compressed swap.

**If swap is missing or too small**:
```bash
# Option A: BTRFS swapfile (8GB)
sudo btrfs filesystem mkswapfile --size 8g /swap/swapfile
sudo swapon /swap/swapfile
echo '/swap/swapfile none swap defaults 0 0' | sudo tee -a /etc/fstab

# Option B: traditional swapfile (8GB)
sudo fallocate -l 8G /swapfile
sudo chmod 600 /swapfile
sudo mkswap /swapfile
sudo swapon /swapfile
echo '/swap/swapfile none swap defaults 0 0' | sudo tee -a /etc/fstab
```

**Why swap matters for inference**: when Ollama loads `gemma3:4b` (~4 GB), it pages in chunks. Without swap, if free RAM < model size, the kernel's OOM-killer will start killing processes (often the gateway itself, or Redis, or even your shell). Swap gives breathing room.

#### Step 0.3.5 — Install and verify Ollama

```bash
curl -fsSL https://ollama.com/install.sh | sh
ollama --version
sudo systemctl enable --now ollama.service
ollama --version
```

**Expected**: Ollama service running, accessible at `http://localhost:11434`.

**Sanity check**:
```bash
curl -sS http://localhost:11434/api/version
```
**Expected**: `{"version":"0.x.x"}`

**If Ollama doesn't start**: check `journalctl -u ollama.service -e --no-pager` for errors. Common issue: NVIDIA driver version mismatch.

#### Step 0.3.6 — Clone the repo and create folder structure

```bash
cd /home/white/Projects/Go-LLM-Gateway
ls -la
```

**Expected**: The repo is already cloned. You should see `.git/`, `README.md`, and the folders we just created (`cmd/`, `internal/`, `pkg/`, `configs/`, `scripts/`, `docs/`, `test/`).

If for some reason the repo is empty or missing, re-clone:
```bash
git clone https://github.com/Leito2/llm-edge-gateway.git .
```

The folder structure to be created (if not already present):

```bash
mkdir -p cmd/gateway
mkdir -p internal/{auth,breaker,cache,config,embedder,fallback,metrics,providers,proxy}
mkdir -p pkg/types
mkdir -p configs
mkdir -p scripts
mkdir -p docs
mkdir -p test
```

**Why these specific folders**:

- `cmd/gateway/` — only main package. Entry point.
- `internal/` — Go convention: anything in `internal/` is **not importable** by other modules. Perfect for proprietary business logic.
- `pkg/types/` — exposed to the world if someone imports the module as a library. Types are safe to expose.
- `configs/`, `scripts/`, `docs/`, `test/` — non-Go artifacts.

#### Step 0.3.7 — Initialize the Go module

```bash
go mod init github.com/Leito2/llm-edge-gateway
```

**Expected output**:
```
go: creating new go.mod: module github.com/Leito2/llm-edge-gateway
```

**Verify**:
```bash
cat go.mod
```

**Expected**:
```
module github.com/Leito2/llm-edge-gateway

go 1.22
```

(or whatever Go version is installed; we have 1.26.3)

**The module path** (`github.com/Leito2/llm-edge-gateway`) is the import path all Go files will use. Every internal import in the project looks like:

```go
import "github.com/Leito2/llm-edge-gateway/internal/cache"
```

If we wanted the module to be importable by other Go projects, the path would need to match a real Git URL. Since this is a closed-source project, the path is just a unique identifier — but matching the actual git URL keeps things clean.

#### Step 0.3.8 — Configure git identity (for commits)

```bash
git config user.name "Leandro Cataño Cardeño"
git config user.email "leandro.cc.pro@gmail.com"
```

**Why**: every commit we make needs an author. Use whatever name/email the user wants attached to their work.

#### Step 0.3.9 — Final smoke test

Run all verification commands in one shot:

```bash
echo "=== Go ==="; go version
echo "=== Docker ==="; docker --version; docker compose version
echo "=== GPU ==="; nvidia-smi | head -10
echo "=== Swap ==="; swapon --show
echo "=== Ollama ==="; ollama --version; curl -sS http://localhost:11434/api/version
echo "=== Git ==="; git status; git config user.name
echo "=== Module ==="; cat go.mod
```

**Expected**: every line produces output. No "command not found" errors.

### 0.4 — Phase 0 completion checklist

- [ ] `go version` returns 1.22+
- [ ] `docker --version` and `docker compose version` work without sudo
- [ ] `nvidia-smi` shows the GPU
- [ ] `swapon --show` shows ≥ 4 GB swap
- [ ] `ollama --version` works and the service is active
- [ ] `curl http://localhost:11434/api/version` returns a version string
- [ ] The repo is cloned with `README.md`, `.git/`
- [ ] The folder structure (`cmd/`, `internal/`, `pkg/`, `configs/`, `scripts/`, `docs/`, `test/`) exists
- [ ] `go.mod` exists with the correct module path
- [ ] `git config user.name` and `user.email` are set

**When all 10 items are checked, Phase 0 is complete. Move to Phase 1.**

---

## Phase 1 — Redis Stack + Vector Index (DRAFT)

> **Detailed implementation comes in the next iteration. This is the architectural plan.**

**Goal**: Stand up Redis with the RediSearch module and pre-create the vector index for cache entries.

**Files to create**:
- `docker-compose.yml` — single service running `redis/redis-stack-server:7.4.0-v3` with memory capped at 2GB and a volume for persistence.
- `scripts/init-redis.sh` — runs once on container startup, creates the `cache_idx` vector index using `FT.CREATE` with HNSW, FLOAT32, 768 dimensions, COSINE distance.

**Key decisions**:
- Use `redis-stack-server` (not `redis` + custom build) — pre-built module, no compilation.
- Cap Redis at 2GB to protect 8GB RAM.
- Vector schema: `embedding VECTOR HNSW 6 TYPE FLOAT32 DIM 768 DISTANCE_METRIC COSINE`.
- Sortable `created_at` field for future TTL/expiration.

**Verification**: `redis-cli FT._LIST` shows `cache_idx`; `redis-cli FT.INFO cache_idx` shows the schema with the VECTOR field.

---

## Phase 2 — Types & Config (DRAFT)

> **Detailed implementation comes in the next iteration. This is the architectural plan.**

**Goal**: Define all shared data structures and load configuration from env vars.

**Files to create**:
- `pkg/types/types.go` — `ChatRequest`, `ChatResponse`, `ChatMessage`, `Embedding`, `CachedEntry`, `ProviderResponse`.
- `internal/config/config.go` — `Config` struct with `Load()` function reading from env vars.
- `.env.example` — template with all required variables.
- `go.sum` — populated when we add dependencies in Phase 3.

**Type design considerations**:
- Use JSON struct tags compatible with OpenAI's wire format (so we can pass requests through unchanged).
- Use `*string` for optional fields like `temperature`, `top_p`.
- `Embedding` is `[]float32` — but for Redis storage we'll serialize it as a binary blob (`[]byte`) of little-endian float32s.

**Config**:
- Use a single `Config` struct with sub-structs (`UpstreamConfig`, `CacheConfig`, `BreakerConfig`, `LocalConfig`).
- Read with `os.Getenv` (no extra dep) or `github.com/kelseyhightower/envconfig` if we want defaults.
- Fail fast on missing required fields.

**Verification**: a `main.go` stub that loads config and prints it should run with `go run cmd/gateway/main.go`.

---

## Phase 3 — Embedder (DRAFT)

> **Detailed implementation comes in the next iteration. This is the architectural plan.**

**Goal**: HTTP client to Ollama's `/api/embeddings` endpoint, returning `[]float32`.

**Files to create**:
- `internal/embedder/embedder.go` — `Embedder` interface + `OllamaEmbedder` implementation.
- `internal/embedder/embedder_test.go` — unit test with `httptest` mocking Ollama.

**Interface**:
```go
type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
    EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
    Dimensions() int
}
```

**Ollama call**:
```bash
POST http://localhost:11434/api/embeddings
{"model": "nomic-embed-text", "prompt": "hello"}
```
Response: `{"embedding": [0.123, -0.456, ...]}` (768 floats).

**Why Ollama for embeddings, not a Go-native lib**:
- No CGo bindings to maintain.
- Same server that runs Gemma.
- Nomically slower (~3-5ms) than a C library but well within budget.

**Verification**: send a test request, assert result length == 768.

---

## Phase 4 — Semantic Cache (DRAFT)

> **Detailed implementation comes in the next iteration. This is the architectural plan.**

**Goal**: Get-or-set cache backed by Redis with HNSW vector search. Threshold-controlled hits.

**Files to create**:
- `internal/cache/semantic_cache.go` — `SemanticCache` struct with `Get`, `Set`, `Stats`.
- `internal/cache/semantic_cache_test.go` — tests with `miniredis` (in-memory Redis for testing) — but miniredis doesn't support RediSearch, so we'll use a real Redis in tests or test the math separately.

**Get logic**:
1. `FT.SEARCH cache_idx "*=>[KNN 1 @embedding $vec]" PARAMS 2 vec <blob> DIALECT 2`
2. If 0 results → return `nil, 0, nil` (miss).
3. If similarity (1 - distance) < 0.96 → return `nil, 0, nil` (miss).
4. Otherwise → load `response_json` from HASH, deserialize, return `(entry, similarity, nil)`.

**Set logic**:
1. Serialize response to JSON.
2. `HSET cache:<uuid> embedding <blob> response_json <json> created_at <unix> hits 0`.
3. Best-effort: don't fail the request if cache write fails.

**Key gotcha**: RediSearch's `KNN` returns results sorted by distance. We need the **distance → similarity** conversion: `similarity = 1 - distance` for COSINE.

**Verification**: integration test that sets an entry, queries with a near-identical text, gets a hit.

---

## Phase 5 — Groq Provider (DRAFT)

> **Detailed implementation comes in the next iteration. This is the architectural plan.**

**Goal**: HTTP client to Groq's `/v1/chat/completions` endpoint.

**Files to create**:
- `internal/providers/provider.go` — `Provider` interface.
- `internal/providers/groq.go` — `GroqProvider` implementing it.
- `internal/providers/groq_test.go` — mocked test.

**Interface**:
```go
type Provider interface {
    Name() string
    Chat(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error)
}
```

**Groq specifics**:
- Endpoint: `https://api.groq.com/openai/v1/chat/completions`.
- Auth: `Authorization: Bearer $GROQ_API_KEY`.
- Wire format: 100% OpenAI-compatible. We can serialize `types.ChatRequest` directly.
- Default model: `llama-3.3-70b-versatile`. Free tier.

**Why this is the easiest provider to integrate**:
- It's OpenAI-shaped, so we already have the types in `pkg/types`.
- No custom request/response transformation needed.
- Free tier means we can develop and test without spending money.

**Verification**: a unit test that mocks the Groq API and asserts the request body is shaped correctly.

---

## Phase 6 — Circuit Breaker (DRAFT)

> **Detailed implementation comes in the next iteration. This is the architectural plan.**

**Goal**: Wrap the Groq provider with `sony/gobreaker`, threshold 5s latency or 3 consecutive failures.

**Files to create**:
- `internal/breaker/breaker.go` — `Breaker` struct wrapping `gobreaker.CircuitBreaker`.
- `internal/breaker/breaker_test.go` — state transition tests.

**Configuration**:
- `MaxRequests` (half-open probe count): 1
- `Interval` (counter reset window, closed state): 60s
- `Timeout` (time before half-open from open): 30s
- `ReadyToTrip`: trip when `consecutive_failures >= 3` OR when `p95_latency > 5s` (custom predicate)
- `IsSuccessful`: any 2xx response is success; anything else is failure

**Custom latency check**:
- `gobreaker` doesn't natively support latency-based tripping, so we use the `OnStateChange` callback + a sliding window of latencies in our wrapper.
- Simpler alternative: trip on consecutive failures only, and let the *fallback path* itself detect slowness (Groq has its own timeouts, fallback kicks in via a context deadline).

**Verification**: test that 3 failures in a row → state = open → calls return `ErrOpenState` immediately.

---

## Phase 7 — Local Fallback (DRAFT)

> **Detailed implementation comes in the next iteration. This is the architectural plan.**

**Goal**: Local Ollama provider running Gemma 3 4B on CPU.

**Files to create**:
- `internal/fallback/ollama_fallback.go` — `OllamaLocal` implementing `Provider`.
- `scripts/pull-models.sh` — `ollama pull gemma3:4b` and `ollama pull nomic-embed-text`.

**Pre-flight check** (RUN BEFORE IMPLEMENTATION):
```bash
time ollama run gemma3:4b "Explica qué es un circuit breaker en 3 frases"
```
- If < 15s → proceed with `gemma3:4b`.
- If > 30s → switch to `llama3.2:3b` or `qwen2.5:1.5b`.

**Ollama configuration for CPU-only inference**:
```bash
# In ollama service env or systemd override:
OLLAMA_NUM_GPU=0
OLLAMA_NUM_CTX=2048
OLLAMA_KEEP_ALIVE=5m
```

**Endpoint**:
- `POST http://localhost:11434/api/chat` (chat completions)
- Body: `{"model": "gemma3:4b", "messages": [...], "stream": false}`
- Response: same shape as OpenAI.

**Verification**: smoke test above, plus an integration test in Phase 9.

---

## Phase 8 — Orchestrator + Main (DRAFT)

> **Detailed implementation comes in the next iteration. This is the architectural plan.**

**Goal**: Wire everything together. The Fiber HTTP server that orchestrates the request flow.

**Files to create**:
- `internal/proxy/proxy.go` — `HandleChat(c *fiber.Ctx)` function implementing the full pipeline.
- `internal/auth/auth.go` — `RequireAPIKey` middleware.
- `internal/metrics/metrics.go` — atomic counters for cache hits/misses.
- `cmd/gateway/main.go` — bootstrap, load config, create instances, mount routes.

**Route map**:
- `POST /v1/chat/completions` → `proxy.HandleChat` (auth required)
- `GET /health` → `proxy.HandleHealth` (no auth)
- `GET /stats` → `proxy.HandleStats` (no auth)

**Middleware stack** (in order):
1. `recover` — catches panics, returns 500
2. `logger` — request log
3. `auth.RequireAPIKey` — only on `/v1/*` routes

**HandleChat pipeline**:
```
1. Parse body → types.ChatRequest (Fiber's c.Body() is []byte)
2. Extract last user message (or all messages concatenated for embedding)
3. embedder.Embed(ctx, query) → []float32
4. cache.Get(ctx, vec) → hit?
   yes → return response with X-Cache-Status: HIT
5. circuit.Execute(func() (*types.ChatResponse, error) { return groq.Chat(ctx, req) })
   if error or open → fallback.OllamaLocal.Chat(ctx, req)
6. cache.Set(ctx, query, vec, response) // best-effort, async
7. Return response with X-Cache-Status: MISS or MISS-FALLBACK
```

**Auth implementation**:
```go
func RequireAPIKey(expected string) fiber.Handler {
    return func(c *fiber.Ctx) error {
        auth := c.Get("Authorization")
        if !strings.HasPrefix(auth, "Bearer ") {
            return c.Status(401).JSON(fiber.Map{"error": "missing bearer token"})
        }
        got := strings.TrimPrefix(auth, "Bearer ")
        if subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
            return c.Status(401).JSON(fiber.Map{"error": "invalid api key"})
        }
        return c.Next()
    }
}
```

**Verification**: end-to-end with curl, as outlined in README.

---

## Phase 9 — Tests, Benchmarks, Docs (DRAFT)

> **Detailed implementation comes in the next iteration. This is the architectural plan.**

**Goal**: Test coverage on critical paths, baseline benchmark numbers, polish docs.

**Tests to add**:
- `internal/cache/semantic_cache_test.go` — hit/miss/threshold logic
- `internal/breaker/breaker_test.go` — state transitions
- `internal/providers/groq_test.go` — HTTP request shape, error handling
- `internal/auth/auth_test.go` — valid/invalid/missing key
- `internal/proxy/proxy_test.go` — full pipeline with mocked deps

**Benchmark**:
```bash
# Install hey
go install github.com/rakyll/hey@latest

# Test cache hit latency
hey -n 1000 -c 50 -m POST -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"llama-3.3-70b-versatile","messages":[{"role":"user","content":"¿Qué es Go?"}]}' \
  http://localhost:8080/v1/chat/completions
```
Capture: p50, p95, p99 latency. Hit rate from `/stats`.

**Docs to update**:
- README with actual measured numbers (replace placeholders).
- Add `docs/ARCHITECTURE.md` with deeper design rationale.
- Add `docs/OPERATIONS.md` with deployment, scaling, monitoring.

---

## Phase 10 — Demo (DRAFT)

> **Detailed implementation comes in the next iteration. This is the architectural plan.**

**Goal**: Live demo of the circuit breaker.

**Demo script**:
1. Start gateway, Groq is healthy.
2. Send a request → see `X-Provider: groq, X-Cache-Status: MISS`.
3. Send same request again → see `X-Cache-Status: HIT`.
4. Stop the network to Groq (firewall rule or `iptables -A OUTPUT -p tcp --dport 443 -d groq.com -j DROP`).
5. Send a request → wait 5s → see `X-Provider: ollama-local, X-Cache-Status: MISS-FALLBACK`.
6. Restore network.
7. Send another request → see `X-Provider: groq` again (circuit closed after timeout).

**Optional**: record this as a screencast, include GIF in README.

---

## Phase 11 — Streaming SSE (POST-MVP, DRAFT)

> **Detailed implementation comes in the next iteration. This is the architectural plan.**

**Goal**: Add Server-Sent Events streaming for chat completions.

**Why post-MVP**:
- SSE handling in Fiber is non-trivial (`SetBodyStreamWriter`).
- Streaming the cache path is tricky (cached responses aren't streaming).
- Most demos can show value without streaming.

**Approach**:
- Detect `stream: true` in request.
- For cache HIT: synthesize a single SSE chunk from the cached response.
- For cache MISS: stream from upstream, tee into a goroutine that accumulates the full response for cache writeback.

**Verification**: `curl -N` shows chunks arriving.

---

## Conventions

### Code style

- **No comments unless absolutely necessary.** The code is self-documenting; function names and types should make intent clear.
- **Errors wrapped with `fmt.Errorf("...: %w", err)`** so the call chain is traceable.
- **Context propagated everywhere** — every I/O function takes `ctx context.Context` as the first arg.
- **Struct constructors return `(*T, error)`, never panic.**
- **No global state.** Everything is passed as a dependency.

### Naming

- Packages: lowercase, short (`cache`, `breaker`, `embedder`).
- Interfaces: noun describing capability (`Provider`, `Embedder`).
- Concrete types: noun + suffix (`GroqProvider`, `OllamaLocal`).
- Functions: verbs (`Get`, `Set`, `Embed`).
- Tests: `*_test.go` in the same package. Test functions: `TestThing_Scenario` (e.g., `TestSemanticCache_Hit`, `TestSemanticCache_Miss`).

### File organization

Each Go file should have ONE primary type or concept. If a file gets >300 lines, split it.

### Git commits

Commit at the end of each phase with a message like:

```
phase(N): <short description>
```

Examples:
- `phase(1): add redis stack docker-compose with vector index bootstrap`
- `phase(2): add shared types and env-based config loader`
- `phase(8): wire full proxy pipeline with auth and metrics`

---

## Troubleshooting

### "docker: command not found" after install

Group not picked up. Run `newgrp docker` or log out/in.

### Ollama slow on first request

The model is loaded into memory on first use. Subsequent requests are faster. This is expected.

### Redis FT.SEARCH returns 0 results

- Check the index exists: `FT._LIST`.
- Check the KNN syntax: must use `DIALECT 2` and the `@field` reference.
- Check the vector is 768 floats exactly.

### `subtle.ConstantTimeCompare` returns 0 even for correct key

The lengths must match. Pad or truncate before comparison. Standard Go pattern:

```go
if subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1 && len(got) == len(expected) {
    // OK
}
```

Or just compare the whole thing with `==` for non-secret comparisons, but for secrets always use constant-time.

### Gemma 3 4B is too slow on CPU

Switch to a smaller model: `ollama pull llama3.2:3b` or `qwen2.5:1.5b`. Update `OLLAMA_MODEL` in `.env`.

### OOM-killer killing the gateway

Swap is insufficient or Redis is over-consuming. Check:
- `free -h` — total swap should be ≥ model size (4 GB for gemma3:4b).
- `docker stats` — Redis should be capped at 2 GB.
- `dmesg | grep -i kill` — OOM events in the kernel log.

### Embedding dimensions mismatch

If you switch the embedding model, the vector dimension changes (nomic=768, MiniLM=384, bge-m3=1024). You MUST drop and recreate the Redis index:

```bash
docker exec -it gateway-redis redis-cli FT.DROPINDEX cache_idx
# then re-run init-redis.sh
```

---

**End of AGENTS.md**
