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

: "${GATEWAY_API_KEY:?Set GATEWAY_API_KEY in your env}"
: "${GATEWAY_URL:=http://localhost:8080}"

PROMPT="${1:-What is the circuit breaker pattern? Answer in 1 short sentence.}"
MODEL="${MODEL:-gemma3:1b}"

curl -sS -X POST "$GATEWAY_URL/v1/chat/completions" \
  -H "Authorization: Bearer $GATEWAY_API_KEY" \
  -H "Content-Type: application/json" \
  -D /tmp/curl_headers.txt \
  -d "$(jq -n \
    --arg model "$MODEL" \
    --arg prompt "$PROMPT" \
    '{model: $model, messages: [{role: "user", content: $prompt}]}')" \
  | jq -r '"Response: " + .choices[0].message.content'

echo
echo "--- Headers ---"
grep -iE "^(X-Cache|X-Provider|X-Latency|Content-Type):" /tmp/curl_headers.txt || true
rm -f /tmp/curl_headers.txt
