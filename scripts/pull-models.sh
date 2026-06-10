#!/bin/sh
# pull-models.sh
# Downloads the Ollama models used by the gateway.
# Run once after installing Ollama.
#
# Models:
#   - nomic-embed-text  -> embedding model, 768 dims, ~274 MB, runs on GPU
#   - gemma3:4b         -> local fallback LLM, ~2.5 GB on disk, ~4 GB RAM, runs on CPU
#
# Hardware note: with 4GB VRAM, embeddings go to GPU and gemma3:4b goes to CPU
# (set OLLAMA_NUM_GPU=0 in the Ollama service to enforce this).

set -e

echo "[pull-models] Pulling nomic-embed-text (embeddings, GPU)..."
ollama pull nomic-embed-text

echo "[pull-models] Pulling gemma3:4b (fallback, CPU)..."
ollama pull gemma3:4b

echo "[pull-models] Verifying installed models..."
ollama list

echo "[pull-models] Done."
