#!/usr/bin/env python3
"""
Demo 3: Cliente Python puro (sin dependencias externas).
Hace exactamente lo que haría el SDK openai, pero usando solo stdlib.

Uso:
    python3 examples/python_client.py non-stream
    python3 examples/python_client.py stream
"""

import json
import sys
import time
import urllib.request
import urllib.error

GATEWAY_URL = "http://localhost:8080/v1/chat/completions"
GATEWAY_KEY = "demo-key-1234567890"  # Reemplazar con tu GATEWAY_API_KEY
MODEL = "gemma3:1b"
USER_MESSAGE = "¿Qué es un circuit breaker en 1 frase?"


def chat_non_streaming():
    """Igual a openai.chat.completions.create(stream=False)."""
    body = json.dumps({
        "model": MODEL,
        "messages": [{"role": "user", "content": USER_MESSAGE}],
    }).encode()

    req = urllib.request.Request(
        GATEWAY_URL,
        data=body,
        headers={
            "Content-Type": "application/json",
            "Authorization": f"Bearer {GATEWAY_KEY}",
        },
        method="POST",
    )

    start = time.time()
    with urllib.request.urlopen(req, timeout=60) as resp:
        elapsed = (time.time() - start) * 1000
        print(f"[non-stream] HTTP {resp.status} in {elapsed:.0f}ms")
        print(f"  X-Cache-Status: {resp.headers.get('X-Cache-Status')}")
        print(f"  X-Provider:     {resp.headers.get('X-Provider')}")
        print(f"  X-Cache-Sim:    {resp.headers.get('X-Cache-Similarity')}")
        data = json.loads(resp.read())
        print(f"  Response: {data['choices'][0]['message']['content']}")


def chat_streaming():
    """Igual a openai.chat.completions.create(stream=True) con iteración de chunks."""
    body = json.dumps({
        "model": MODEL,
        "stream": True,
        "messages": [{"role": "user", "content": USER_MESSAGE}],
    }).encode()

    req = urllib.request.Request(
        GATEWAY_URL,
        data=body,
        headers={
            "Content-Type": "application/json",
            "Authorization": f"Bearer {GATEWAY_KEY}",
        },
        method="POST",
    )

    start = time.time()
    first_chunk_ms = None
    full_content = ""

    with urllib.request.urlopen(req, timeout=60) as resp:
        print(f"[stream] HTTP {resp.status}, Content-Type: {resp.headers.get('Content-Type')}")
        print(f"  X-Cache-Status: {resp.headers.get('X-Cache-Status')}")
        print(f"  X-Provider:     {resp.headers.get('X-Provider')}")
        print(f"  X-Cache-Sim:    {resp.headers.get('X-Cache-Similarity')}")
        print()
        print("--- Streaming output ---")

        for raw_line in resp:
            line = raw_line.decode("utf-8").rstrip("\r\n")
            if not line:
                continue
            if line.startswith("data: "):
                payload = line[len("data: "):]
                if first_chunk_ms is None:
                    first_chunk_ms = (time.time() - start) * 1000
                if payload == "[DONE]":
                    print("\n--- [DONE] ---")
                    break
                chunk = json.loads(payload)
                delta = chunk["choices"][0]["delta"].get("content", "")
                full_content += delta
                print(delta, end="", flush=True)
            elif line.startswith(":"):
                # SSE comment, ignorar
                continue

    total_ms = (time.time() - start) * 1000
    print(f"\n[stream] First chunk: {first_chunk_ms:.0f}ms, total: {total_ms:.0f}ms")
    print(f"[stream] Full content: {full_content}")


def main():
    mode = sys.argv[1] if len(sys.argv) > 1 else "non-stream"
    if mode == "non-stream":
        chat_non_streaming()
    elif mode == "stream":
        chat_streaming()
    else:
        print(f"Unknown mode: {mode}. Use 'non-stream' or 'stream'.")
        sys.exit(1)


if __name__ == "__main__":
    main()
