# LLM Edge Gateway — Client Examples

This directory contains ready-to-use client examples that talk to the gateway.
Pick the one that matches your stack.

## Prerequisites

1. The gateway must be running. See [README.md](../README.md) → "Quick Start".
2. Set `GATEWAY_API_KEY` in your shell to whatever you configured in `.env`.

```bash
# Default values used by all examples:
export GATEWAY_API_KEY=demo-key-1234567890
export GATEWAY_URL=http://localhost:8080  # optional, this is the default
```

## The five clients

| # | File | Stack | Deps | Streaming | Notes |
|---|------|-------|------|-----------|-------|
| 1 | [`curl_nonstream.sh`](curl_nonstream.sh) | curl | none | ❌ | Simplest possible. Good for CI/scripts. |
| 2 | [`curl_stream.sh`](curl_stream.sh) | curl | none | ✅ | Uses `curl -N` for SSE chunks. |
| 3 | [`python_client.py`](python_client.py) | Python 3 | **stdlib only** (no pip) | ✅ | Works without `pip install openai`. |
| 4 | [`node_client.js`](node_client.js) | Node 18+ | **stdlib only** (no npm) | ✅ | Works without `npm install openai`. |
| 4b | [`node_sdk_example.js`](node_sdk_example.js) | Node 18+ | `npm i openai` | ✅ | Real SDK usage. Drop-in compatible. |
| 5 | [`../cmd/client-example/`](../cmd/client-example/) | Go 1.22+ | `github.com/sashabaranov/go-openai` | ✅ | Real SDK usage from a Go program. |

## Quick reference (the 2 most common patterns)

### Pattern 1 — `curl` non-streaming (wait for full response)

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $GATEWAY_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemma3:1b",
    "messages": [{"role": "user", "content": "What is Go?"}]
  }'
```

### Pattern 2 — `curl` streaming (chunks arrive over time)

```bash
curl -N -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $GATEWAY_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemma3:1b",
    "stream": true,
    "messages": [{"role": "user", "content": "What is Go?"}]
  }'
```

## What you get back

### Non-streaming response

```
HTTP/1.1 200 OK
Content-Type: application/json
X-Cache-Status: HIT          # or MISS, MISS-FALLBACK
X-Provider: ollama-local     # or groq
X-Cache-Similarity: 1.0000   # only on HIT, cosine similarity

{
  "id": "ollama-...",
  "object": "chat.completion",
  "created": 1700000000,
  "model": "gemma3:1b",
  "choices": [
    {
      "index": 0,
      "message": {"role": "assistant", "content": "Go is a..."},
      "finish_reason": "stop"
    }
  ],
  "usage": {"prompt_tokens": 5, "completion_tokens": 10, "total_tokens": 15}
}
```

### Streaming response

```
HTTP/1.1 200 OK
Content-Type: text/event-stream
Transfer-Encoding: chunked
X-Cache-Status: HIT
X-Provider: ollama-local
X-Cache-Similarity: 1.0000
X-Latency-Ms: 47            # time to first chunk

data: {"choices":[{"delta":{"content":"Go "},"index":0}],...}

data: {"choices":[{"delta":{"content":"is "},"index":0}],...}

data: {"choices":[{"delta":{"content":"a "},"index":0}],...}

...

data: [DONE]
```

## Verifying it's working

After running any example, you can inspect gateway metrics:

```bash
# Health
curl http://localhost:8080/health
# → {"breaker_state":"closed","status":"ok","uptime_seconds":8642}

# Stats
curl http://localhost:8080/stats
# → {
#     "cache": {"hits": 1247, "misses": 318, "hit_rate": 0.797, "size": 1565},
#     "breaker": {"name": "groq", "state": "closed", "failures": 0, "successes": 1247, "rejects": 0},
#     "metrics": {...}
#   }
```

## Drop-in compatibility with OpenAI SDKs

All the official OpenAI SDKs (Python, Node, Go, Java, Ruby, .NET, etc.) work
unchanged — just point `baseURL` at the gateway:

| SDK | baseURL setting |
|-----|-----------------|
| Python (`openai.OpenAI`) | `OpenAI(base_url="http://localhost:8080/v1", api_key=GATEWAY_API_KEY)` |
| Node (`new OpenAI`) | `new OpenAI({ baseURL: "http://localhost:8080/v1", apiKey: GATEWAY_API_KEY })` |
| Go (`openai.DefaultConfig`) | `cfg.BaseURL = "http://localhost:8080/v1"` |
| Java | `OpenAIClient.builder().baseUrl("http://localhost:8080/v1")...` |
| curl | `-H "Authorization: Bearer $GATEWAY_API_KEY"` |

## Performance expectations (measured on GTX 1650 + 8GB RAM + zram)

| Operation | Latency |
|-----------|---------|
| Cache HIT (same / similar query) | **50–90 ms** |
| Cache MISS → Groq (network) | ~800 ms p50 |
| Cache MISS → Ollama local (gemma3:1b) | 1.2–14 s (1st call = model load) |

**Speedup: 50–200×** when the cache hits.
