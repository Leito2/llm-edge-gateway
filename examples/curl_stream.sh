#!/usr/bin/env bash
# examples/curl_stream.sh
# Streaming chat completion via Server-Sent Events (SSE).
# Chunks arrive over time and print as they come (curl -N for no-buffering).
#
# Usage:
#     GATEWAY_API_KEY=demo-key-1234567890 ./examples/curl_stream.sh
#     GATEWAY_API_KEY=demo-key-1234567890 ./examples/curl_stream.sh "Tell me a joke"
#     MODEL=groq GATEWAY_API_KEY=demo-key-1234567890 ./examples/curl_stream.sh "Hi"
#
# Required: curl, jq

set -euo pipefail

: "${GATEWAY_API_KEY:?Set GATEWAY_API_KEY in your env (must match .env)}"
: "${GATEWAY_URL:=http://localhost:8080}"

PROMPT="${1:-What is the circuit breaker pattern? Answer in 1 short sentence.}"
MODEL="${MODEL:-gemma3:1b}"

echo "--- Streaming output (Ctrl-C to abort) ---"
echo

# SSE parser using a temp file (works in any bash >= 3)
RAW=$(mktemp)
trap "rm -f $RAW /tmp/curl_headers.txt" EXIT

HTTP_CODE=$(curl -N -sS -o "$RAW" -D /tmp/curl_headers.txt \
  -w "%{http_code}" \
  -X POST "$GATEWAY_URL/v1/chat/completions" \
  -H "Authorization: Bearer $GATEWAY_API_KEY" \
  -H "Content-Type: application/json" \
  -d "$(jq -n \
    --arg model "$MODEL" \
    --arg prompt "$PROMPT" \
    '{model: $model, stream: true, messages: [{role: "user", content: $prompt}]}')" 2>/dev/null) || true

if [ "$HTTP_CODE" != "200" ]; then
  echo "[ERROR] HTTP $HTTP_CODE"
  echo "Body:"
  cat "$RAW"
  echo
  echo "Headers:"
  grep -iE "^HTTP|X-Cache|X-Provider" /tmp/curl_headers.txt || true
  exit 1
fi

# Parse SSE: only print "data: {json}" lines, skip comments, show errors clearly
python3 - "$RAW" <<'PYEOF' || exit 0
import json, sys

path = sys.argv[1]
err_chunks = []
content_parts = []
done = False

with open(path) as f:
    for raw_line in f:
        line = raw_line.rstrip("\r\n")
        if not line:
            continue
        if line.startswith(":"):
            # SSE comment, ignore
            continue
        if not line.startswith("data: "):
            continue
        payload = line[len("data: "):]
        if payload == "[DONE]":
            done = True
            break
        try:
            evt = json.loads(payload)
        except Exception:
            continue
        # Error event from gateway
        if "error" in evt:
            err = evt["error"]
            msg = err.get("message", str(err)) if isinstance(err, dict) else str(err)
            err_chunks.append(msg)
            continue
        # Standard content delta
        choices = evt.get("choices", [])
        if choices:
            delta = choices[0].get("delta", {})
            c = delta.get("content")
            if c:
                content_parts.append(c)
                print(c, end="", flush=True)

if err_chunks:
    print()
    print()
    print("[ERROR chunks received from gateway]:")
    for e in err_chunks:
        print(f"  - {e}")
    sys.exit(2)
elif not content_parts and not done:
    print()
    print()
    print("[ERROR] stream ended without any content or [DONE] event")
    sys.exit(3)
else:
    print()
PYEOF

echo
echo "--- [DONE] ---"
echo
echo "--- Response headers ---"
grep -iE "^(X-Cache|X-Provider|X-Latency|X-Cache-Similarity|Content-Type|Transfer-Encoding):" /tmp/curl_headers.txt || true
