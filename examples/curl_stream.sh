#!/usr/bin/env bash
# examples/curl_stream.sh
# Streaming chat completion call. Chunks arrive over time and print as
# they come (thanks to `curl -N` for no-buffering).
#
# Usage:
#     GATEWAY_API_KEY=demo-key-1234567890 ./examples/curl_stream.sh
#     GATEWAY_API_KEY=demo-key-1234567890 ./examples/curl_stream.sh "Tell me a joke"
#
# Required: curl, jq

set -euo pipefail

: "${GATEWAY_API_KEY:?Set GATEWAY_API_KEY in your env}"
: "${GATEWAY_URL:=http://localhost:8080}"

PROMPT="${1:-What is the circuit breaker pattern? Answer in 1 short sentence.}"
MODEL="${MODEL:-gemma3:1b}"

echo "--- Streaming output (Ctrl-C to abort) ---"
curl -N -sS -X POST "$GATEWAY_URL/v1/chat/completions" \
  -H "Authorization: Bearer $GATEWAY_API_KEY" \
  -H "Content-Type: application/json" \
  -D /tmp/curl_headers.txt \
  -d "$(jq -n \
    --arg model "$MODEL" \
    --arg prompt "$PROMPT" \
    '{model: $model, stream: true, messages: [{role: "user", content: $prompt}]}')" \
  | sed -n 's/^data: //p' \
  | while IFS= read -r line; do
      [ "$line" = "[DONE]" ] && { echo; echo "--- [DONE] ---"; break; }
      [ -z "$line" ] && continue
      content=$(echo "$line" | jq -r '.choices[0].delta.content // empty' 2>/dev/null)
      [ -n "$content" ] && printf '%s' "$content"
    done

echo
echo "--- Headers ---"
grep -iE "^(X-Cache|X-Provider|X-Latency|Content-Type|Transfer-Encoding):" /tmp/curl_headers.txt || true
rm -f /tmp/curl_headers.txt
