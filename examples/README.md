# 📡 LLM Edge Gateway — Client Examples

> Five ready-to-use client implementations for talking to the LLM Edge Gateway.
> Pick the one that matches your stack and copy-paste it into your project.

---

## 🎯 Project Overview

**LLM Edge Gateway** is a low-latency, high-concurrency API Gateway engineered to optimize Large Language Model (LLM) orchestration. It sits between your application and any LLM provider (Groq, OpenAI, etc.) and adds three production-critical capabilities that bare API calls don't have:

1. **💰 Semantic caching** — embeds every query locally and serves repeated or similar queries in **<100 ms** at zero API cost, cutting operational spending by **30–40 %**.
2. **🛡️ Circuit-breaker failover** — when the upstream provider is slow, errors, or rate-limits, traffic is automatically rerouted to a **local Gemma 3 1B fallback** running via Ollama, guaranteeing **99.9 % uptime**.
3. **⚡ High throughput** — built on Go + Fiber/fasthttp with Goroutine-based concurrency, the gateway can process **10 K+ req/s** on a single core with negligible memory footprint.

### Why this exists

Commercial LLM APIs have two production-killing properties:
- **Unpredictable latency** — p99 can spike from 800 ms to 8 s+ with no warning.
- **Linear cost scaling** — every token is billed, even for semantically equivalent queries.

This gateway is the abstraction layer that makes those problems tractable.

### Tech stack

| Layer | Technology |
|---|---|
| **Language** | Go 1.22+ |
| **HTTP framework** | Fiber v2 (wraps `fasthttp`) |
| **Cache store** | Redis Stack 7.4 (RediSearch module for vector similarity) |
| **Embeddings** | `nomic-embed-text` via Ollama (768-dim, GPU-accelerated) |
| **Upstream LLM** | Groq (`llama-3.3-70b-versatile`, OpenAI-compatible) |
| **Local fallback** | Gemma 3 1B (`gemma3:1b`) via Ollama, CPU |
| **Circuit breaker** | `sony/gobreaker` |
| **Container** | Docker Compose for Redis Stack |

---

## 🔑 Key Concepts (read this first)

### 1. Non-streaming mode (default)

In non-streaming mode, the client sends the request, **waits**, and gets a single JSON response back. Simple, but the user sees nothing until the entire response is ready.

```
Client                Gateway                 Upstream
  | --POST /v1/chat----> |                       |
  |                      | --POST /chat--------> |
  |                      |                       | (generates...)
  |                      | <-----JSON resp------ |
  | <-----JSON resp------ |                       |
```

**When to use it**: scripts, batch jobs, CI pipelines, or anything where the user is OK waiting for the full answer.

### 2. Streaming mode (SSE)

In streaming mode, the client sets `"stream": true`. The gateway keeps the HTTP connection open and sends the response **token-by-token** as it's generated using **Server-Sent Events (SSE)**. The user sees text appear progressively, like ChatGPT's UI.

```
Client                Gateway                 Upstream
  | --POST stream=true-> |                       |
  |                      | --POST stream=true--> |
  |                      | <--data: {"delta":{"content":"Go "}}--|
  | <-data: ...----------|                       |
  |                      | <--data: {"delta":{"content":"is "}}-|
  | <-data: ...----------|                       |
  |                      | <--data: {"delta":{"content":"fast"}}-|
  | <-data: ...----------|                       |
  | <-data: [DONE]-------|                       |
```

**When to use it**: chat UIs, anything user-facing, mobile apps where perceived latency matters.

**SSE wire format**:
- Content-Type: `text/event-stream`
- Each chunk is one line `data: {json}\n\n`
- Stream ends with `data: [DONE]\n\n`
- Newline `\n\n` (double) is mandatory between events

### 3. The semantic cache

The gateway converts every query to a 768-dimensional vector using `nomic-embed-text`. It then looks up that vector in Redis with **cosine similarity ≥ 0.85**. If a near-identical query was answered before, you get the cached response in **<100 ms** without ever calling the LLM.

| Operation | Latency | Cost |
|---|---|---|
| Cache **HIT** (same / similar query) | **50–90 ms** | $0 |
| Cache MISS → local fallback (gemma3:1b) | 1.2–14 s (first call = model load) | $0 |
| Cache MISS → Groq upstream | ~800 ms p50 | Free tier |

**Speedup**: 50–200× when the cache hits.

### 4. The circuit breaker

A 3-state state machine around the upstream provider. After 3 consecutive failures or 30 s of latency > 5 s, the breaker **opens** and sends subsequent traffic directly to the fallback without retrying the upstream. After 30 s, it goes **half-open** and lets one request through as a probe. If it succeeds, the breaker **closes** again and normal traffic resumes.

### 5. Response headers you'll see

| Header | Meaning |
|---|---|
| `X-Cache-Status` | `HIT` / `MISS` / `MISS-FALLBACK` |
| `X-Provider` | `groq` (upstream) or `ollama-local` (fallback) |
| `X-Cache-Similarity` | Cosine similarity (only on HIT, e.g. `0.8929`) |
| `X-Latency-Ms` | Time to first byte (only on streams) |

---

## 🚀 How to start the gateway

### Prerequisites (one-time setup)

```bash
# 1. Go 1.22+ (you already have this if you cloned the repo)
go version

# 2. Docker (for Redis Stack)
sudo pacman -S --needed --noconfirm docker docker-compose
sudo systemctl enable --now docker
sudo usermod -aG docker $USER   # log out / in after this

# 3. Ollama (for embeddings + fallback LLM)
curl -fsSL https://ollama.com/install.sh | sh
sudo systemctl enable --now ollama.service

# 4. Pull the two models the gateway needs (~1 GB total)
ollama pull nomic-embed-text   # 274 MB, runs on GPU
ollama pull gemma3:1b          # 800 MB, runs on CPU
```

### Per-session startup (every time you want to use the gateway)

```bash
# 1. Start Redis Stack with the vector index
cd /path/to/llm-edge-gateway
docker compose up -d
docker exec gateway-redis sh /docker-entrypoint-initdb.d/init.sh

# 2. Configure environment
cp .env.example .env
# Edit .env and set:
#   GATEWAY_API_KEY=demo-key-1234567890    # the bearer token clients must send
#   GROQ_API_KEY=gsk_xxx                   # your Groq key (free at groq.com)
#   GATEWAY_PORT=8080

# 3. Build and run
go build -o gateway ./cmd/gateway/
./gateway
# You should see:
#   [main] redis OK at localhost:6379
#   [main] embedder OK (nomic-embed-text, 768 dims)
#   [main] cache OK (threshold=0.85, ttl=168h0m0s)
#   [main] circuit breaker OK (threshold=3, timeout=30s)
#   [main] starting gateway on :8080
```

### Verify it's running

```bash
# Health check (no auth required)
curl http://localhost:8080/health
# → {"breaker_state":"closed","status":"ok","uptime_seconds":42}

# Stats (no auth required)
curl http://localhost:8080/stats
# → {"cache":{"hits":0,"misses":0,"hit_rate":0,"size":0},
#    "breaker":{"name":"groq","state":"closed","failures":0,...},
#    "metrics":{...}}
```

**The gateway is now live at `http://localhost:8080`. Any OpenAI-compatible client can talk to it.**

---

## 🧪 The two best demos (start here)

If you only have 5 minutes, run these two. They cover 95 % of what you'll ever need.

### Demo 1 — `curl_stream.sh` (streaming, the killer feature)

This is what real users hit. Open a terminal and run:

```bash
GATEWAY_API_KEY=demo-key-1234567890 ./examples/curl_stream.sh "Tell me a joke"
```

You will see text appear character by character, exactly like ChatGPT. Headers, `X-Cache-Status`, and the raw SSE stream are all visible.

**What it does step by step**:

1. POSTs a request to `/v1/chat/completions` with `"stream": true`
2. The gateway hits its semantic cache — first run = MISS (slow), second run = HIT (fast)
3. For cache MISS: gateway calls the provider (Groq or local Ollama fallback) and streams chunks back
4. For cache HIT: gateway tokenizes the cached response and emits it in 20 ms chunks (simulated streaming)
5. Closes with `data: [DONE]`

**Expected output**:

```
--- Streaming output (Ctrl-C to abort) ---
Why don't scientists trust atoms? Because they make up everything!
--- [DONE] ---

--- Headers ---
Content-Type: text/event-stream
X-Provider: ollama-local
Transfer-Encoding: chunked
```

**Time the first chunk vs the full response**:

- Cache HIT, 1st call: **~50 ms** to first chunk
- Cache HIT, full response: **~500 ms** (25 chunks × 20 ms simulated delay)
- Cache MISS, full response: **1.2–14 s** (real model generation)

### Demo 2 — `python_client.py` (Python, both modes)

If you're building in Python, this is your reference implementation. Uses **only the standard library** (no `pip install` needed) and demonstrates both non-streaming and streaming:

```bash
# Non-streaming (one JSON response, wait for completion)
python3 examples/python_client.py non-stream

# Streaming (SSE, token-by-token)
python3 examples/python_client.py stream
```

The script mimics what the official `openai` SDK does internally, so porting to the real SDK is trivial — see [`node_sdk_example.js`](node_sdk_example.js) and [`../cmd/client-example/`](../cmd/client-example/) for SDK versions.

**Key lines**:

```python
# Non-streaming
req = urllib.request.Request(GATEWAY_URL, data=body, headers={...}, method="POST")
with urllib.request.urlopen(req, timeout=60) as resp:
    data = json.loads(resp.read())
    print(data["choices"][0]["message"]["content"])

# Streaming
for raw_line in resp:
    line = raw_line.decode("utf-8").rstrip()
    if line.startswith("data: "):
        payload = line[6:]
        if payload == "[DONE]":
            break
        chunk = json.loads(payload)
        print(chunk["choices"][0]["delta"]["content"], end="", flush=True)
```

---

## 📁 All five clients (reference)

| # | File | Stack | Deps | Streaming | Use it when... |
|---|------|-------|------|-----------|-----------------|
| 1 | [`curl_nonstream.sh`](curl_nonstream.sh) | bash + curl + jq | none | ❌ | Scripting, CI, one-off curls. |
| **2** | **[`curl_stream.sh`](curl_stream.sh)** | **bash + curl + jq** | **none** | **✅** | **Best for quick demos, see "the streaming in action".** |
| **3** | **[`python_client.py`](python_client.py)** | **Python 3** | **stdlib only** | **✅** | **Best Python reference, no pip required.** |
| 4 | [`node_client.js`](node_client.js) | Node 18+ | stdlib only | ✅ | Quick Node demo, no npm install. |
| 4b | [`node_sdk_example.js`](node_sdk_example.js) | Node 18+ | `npm i openai` | ✅ | Real-world Node SDK usage. |
| 5 | [`../cmd/client-example/`](../cmd/client-example/) | Go 1.22+ | `go-openai` SDK | ✅ | Calling the gateway from another Go program. |

---

## 🔌 Drop-in compatibility with any OpenAI SDK

The gateway is **100 % OpenAI-compatible**. Just point your SDK at it:

| SDK | One-line change |
|---|---|
| Python `openai` | `OpenAI(base_url="http://localhost:8080/v1", api_key="...")` |
| Node `openai` | `new OpenAI({baseURL: "http://localhost:8080/v1", apiKey: "..."})` |
| Go `openai-go` | `cfg.BaseURL = "http://localhost:8080/v1"` |
| Java `openai-java` | `OpenAIClient.builder().baseUrl("http://localhost:8080/v1")...` |
| curl / httpie | `Authorization: Bearer $GATEWAY_API_KEY` |

**Everything else stays the same** — `model`, `messages`, `temperature`, `stream`, etc. are all supported because the gateway passes them through untouched.

---

## 🧠 How to test all the features in 3 minutes

```bash
# (1) MISS (1st call) — should be slow
time curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer demo-key-1234567890" \
  -H "Content-Type: application/json" \
  -d '{"model":"gemma3:1b","messages":[{"role":"user","content":"What is Go?"}]}'
# → ~10 s, X-Cache-Status: MISS-FALLBACK

# (2) HIT (2nd call, same query) — should be fast
time curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer demo-key-1234567890" \
  -H "Content-Type: application/json" \
  -d '{"model":"gemma3:1b","messages":[{"role":"user","content":"What is Go?"}]}'
# → ~50 ms, X-Cache-Status: HIT, X-Cache-Similarity: 1.0000

# (3) HIT streaming — first chunk in ~50 ms
curl -N -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer demo-key-1234567890" \
  -H "Content-Type: application/json" \
  -d '{"model":"gemma3:1b","stream":true,"messages":[{"role":"user","content":"What is Go?"}]}'

# (4) Force a fallback path (set a bad upstream URL and watch the circuit open)
GATEWAY_API_KEY=demo-key-1234567890 \
GROQ_BASE_URL=http://localhost:1 \
GATEWAY_PORT=8081 \
./gateway &
# Send 3 requests, watch X-Cache-Status: MISS-FALLBACK after circuit opens
```

---

## 📊 Real measured latencies (on the project's reference hardware)

| Operation | Measured latency | Notes |
|---|---|---|
| **Cache HIT, non-streaming** | **34 ms** | X-Cache-Status: HIT, X-Cache-Similarity: 1.0000 |
| **Cache HIT, streaming** | **50 ms to first chunk, 500 ms total** | 25 chunks × 20 ms simulated |
| Cache MISS, non-streaming (1st call) | 4.3 s | Includes model load + token generation |
| Cache MISS, non-streaming (subsequent) | 1.2–2 s | Model already in memory |
| Cache MISS, streaming, Groq upstream | ~800 ms p50 | Depends on Groq's network |
| Cache MISS, streaming, Ollama local | 1.2–14 s | gemma3:1b on CPU is ~65 tok/s |

Reference hardware: NVIDIA GTX 1650 (4 GB VRAM) · 8 GB RAM · 7.6 GB zram (CachyOS).

---

## 🆘 Troubleshooting

### "Connection refused" on port 8080
The gateway isn't running. Start it with `./gateway` and watch the logs.

### "401 Unauthorized"
The `GATEWAY_API_KEY` in the gateway's `.env` doesn't match the `Authorization: Bearer` header you're sending. Both must be identical.

### Cache HIT but content is wrong
The cache might have a stale entry. Lower the threshold in `.env` to `CACHE_SIMILARITY_THRESHOLD=0.95` for stricter matching, or flush Redis with `docker exec gateway-redis redis-cli FLUSHDB` (warning: also drops the index, re-run `init-redis.sh` after).

### Streaming chunks are tiny or missing
Some HTTP intermediaries (nginx, cloudflare) buffer SSE by default. Add `X-Accel-Buffering: no` to your response headers (already done by the gateway) and configure your proxy to not buffer.

### Performance: cache hit but still slow
Check that the `X-Cache-Status` is actually `HIT` and not `MISS-FALLBACK`. If you see the latter, the upstream is being called every time and the fallback is responding — your cache is empty or the threshold is too low.

---

## 📚 Where to go next

- **Gateway architecture and design rationale**: see [`../README.md`](../README.md)
- **Full build plan and phase-by-phase decisions**: see [`../AGENTS.md`](../AGENTS.md)
- **Performance benchmarks**: see [`../README.md`](../README.md#-measured-performance)
- **How the circuit breaker works**: see [`../AGENTS.md`](../AGENTS.md#phase-6--circuit-breaker-draft)
