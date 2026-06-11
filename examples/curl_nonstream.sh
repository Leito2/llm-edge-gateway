#!/usr/bin/env bash
# examples/curl_nonstream.sh
# Non-streaming chat completion call. Waits for the full response.
#
# Usage:
#     GATEWAY_API_KEY=demo-key-1234567890 ./examples/curl_nonstream.sh
#     GATEWAY_API_KEY=demo-key-1234567890 ./examples/curl_nonstream.sh "What is Go?"
#
# Required: curl, jq

set -euo pipefail

: "${GATEWAY_API_KEY:?Set GATEWAY_API_KEY in your env (must match .env)}"
: "${GATEWAY_URL:=http://localhost:8080}"

PROMPT="${1:-What is the circuit breaker pattern? Answer in 1 short sentence.}"
MODEL="${MODEL:-gemma3:1b}"

RESP=$(mktemp)
trap "rm -f $RESP /tmp/curl_headers.txt" EXIT

HTTP_CODE=$(curl -sS -o "$RESP" -D /tmp/curl_headers.txt \
  -w "%{http_code}" \
  -X POST "$GATEWAY_URL/v1/chat/completions" \
  -H "Authorization: Bearer $GATEWAY_API_KEY" \
  -H "Content-Type: application/json" \
  -d "$(jq -n \
    --arg model "$MODEL" \
    --arg prompt "$PROMPT" \
    '{model: $model, messages: [{role: "user", content: $prompt}]}')" 2>/dev/null) || true

if [ "$HTTP_CODE" != "200" ]; then
  echo "[ERROR] HTTP $HTTP_CODE"
  echo "Body:"
  cat "$RESP" | jq . 2>/dev/null || cat "$RESP"
  echo
  echo "Headers:"
  grep -iE "^HTTP|X-Cache|X-Provider" /tmp/curl_headers.txt || true
  exit 1
fi

# On success, extract the content
CONTENT=$(jq -r '.choices[0].message.content // empty' "$RESP" 2>/dev/null)
if [ -z "$CONTENT" ]; then
  echo "[ERROR] Empty response body"
  cat "$RESP"
  exit 2
fi
echo "Response: $CONTENT"

echo
echo "--- Response headers ---"
grep -iE "^(X-Cache|X-Provider|X-Latency|X-Cache-Similarity|Content-Type):" /tmp/curl_headers.txt || true
